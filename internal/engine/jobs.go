package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"crypto/rand"
	"encoding/hex"
	"errors"
	randv2 "math/rand/v2"
	"sort"
	"sync"
	"time"
)

type Jobs struct {
	runner *Runner
	state  *state.Store
	clock  core.Clock
	step   sync.Mutex
	wake   chan struct{}
}

func NewJobs(r *Runner, s *state.Store, c core.Clock) *Jobs {
	return &Jobs{runner: r, state: s, clock: c, wake: make(chan struct{}, 1)}
}
func randomID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("system random unavailable")
	}
	return hex.EncodeToString(b[:])
}
func active(status string) bool {
	return status == "queued" || status == "running" || status == "retry"
}
func Retryable(err error) bool {
	switch core.ErrorKind(err) {
	case "network_error", "rate_limited", "upstream_error", "result_unknown":
		return true
	}
	return false
}
func RetryDelay(attempt int, retryAfter, jitter time.Duration) time.Duration {
	base := 30 * time.Second
	if attempt >= 2 {
		base = 120 * time.Second
	}
	d := base + jitter
	if retryAfter > d {
		d = retryAfter
	}
	return d
}
func (j *Jobs) Submit(ctx context.Context, ids []string, trigger string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	jobID := randomID()
	now := j.clock.Now().UTC()
	err := j.state.Update(func(s *state.Snapshot) error {
		job := state.Job{ID: jobID, Trigger: trigger, Status: "queued", CreatedAt: now, WorkIDs: []string{}}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			a, ok := s.Accounts[id]
			if !ok || s.DeletePending[id] || a.Status != "ready" {
				continue
			}
			workID := ""
			for k, w := range s.WorkItems {
				if w.AccountID == id && active(w.Status) {
					workID = k
					break
				}
			}
			if workID == "" {
				workID = randomID()
				s.WorkItems[workID] = state.WorkItem{ID: workID, AccountID: id, CreatedAt: now, Status: "queued", NextAttemptAt: now, Generation: a.LastGeneration}
			}
			job.WorkIDs = append(job.WorkIDs, workID)
		}
		if len(job.WorkIDs) == 0 {
			return core.Error("no_runnable_accounts")
		}
		s.Jobs[jobID] = job
		return nil
	})
	if err != nil {
		return "", err
	}
	select {
	case j.wake <- struct{}{}:
	default:
	}
	return jobID, nil
}
func (j *Jobs) Recover() error {
	return j.state.Update(func(s *state.Snapshot) error {
		for id, w := range s.WorkItems {
			if w.Status == "running" {
				w.Status = "retry"
				if w.NextAttemptAt.IsZero() {
					w.NextAttemptAt = j.clock.Now().UTC()
				}
				s.WorkItems[id] = w
			}
		}
		return nil
	})
}
func (j *Jobs) Step(ctx context.Context) error {
	j.step.Lock()
	defer j.step.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	now := j.clock.Now()
	snapshot := j.state.Read()
	ids := []string{}
	for id, w := range snapshot.WorkItems {
		if active(w.Status) && w.Status != "running" && !w.NextAttemptAt.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, k int) bool {
		return snapshot.WorkItems[ids[i]].NextAttemptAt.Before(snapshot.WorkItems[ids[k]].NextAttemptAt)
	})
	if len(ids) == 0 {
		return nil
	}
	id := ids[0]
	var work state.WorkItem
	run := false
	queryOnly := false
	if e := j.state.Update(func(s *state.Snapshot) error {
		w := s.WorkItems[id]
		a := s.Accounts[w.AccountID]
		if !active(w.Status) {
			return nil
		}
		if s.DeletePending[w.AccountID] || a.Status != "ready" || a.LastGeneration != w.Generation {
			w.Status = "canceled"
			s.WorkItems[id] = w
			updateJobs(s)
			return nil
		}
		if w.Attempt >= 3 && !w.NeedsReconcile {
			w.Status = "failed"
			s.WorkItems[id] = w
			updateJobs(s)
			return nil
		}
		queryOnly = w.Attempt >= 3
		w.Status = "running"
		if !queryOnly {
			w.Attempt++
		}
		s.WorkItems[id] = w
		work = w
		run = true
		return nil
	}); e != nil {
		return e
	}
	if !run {
		return nil
	}
	result, err := j.runner.attempt(ctx, work.AccountID, queryOnly, work.ID)
	saveErr := j.state.Update(func(s *state.Snapshot) error {
		w := s.WorkItems[id]
		if w.Status == "canceled" {
			updateJobs(s)
			return nil
		}
		w.Result = &result
		w.NeedsReconcile = result.NeedsReconcile
		switch {
		case err == nil:
			w.Status = "completed"
		case ctx.Err() != nil:
			w.Status = "retry"
			w.NextAttemptAt = j.clock.Now().UTC()
		case Retryable(err) && w.Attempt < 3:
			var api *core.APIError
			var after time.Duration
			if errors.As(err, &api) {
				after = api.RetryAfter
			}
			w.Status = "retry"
			w.NextAttemptAt = j.clock.Now().Add(RetryDelay(w.Attempt, after, time.Duration(randv2.IntN(11))*time.Second)).UTC()
		default:
			w.Status = "failed"
		}
		s.WorkItems[id] = w
		updateJobs(s)
		return nil
	})
	if saveErr != nil {
		return saveErr
	}
	return nil
}
func updateJobs(s *state.Snapshot) {
	s.UpdateJobs()
}
func (j *Jobs) Run(ctx context.Context) error {
	if e := j.Recover(); e != nil {
		return e
	}
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := j.Step(ctx); e != nil {
			return e
		}
		if e := j.clock.Sleep(ctx, time.Duration(1+randv2.IntN(3))*time.Second); e != nil {
			return e
		}
	}
}

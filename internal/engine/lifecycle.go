package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"errors"
	randv2 "math/rand/v2"
	"sync"
	"time"
)

type Scheduler struct {
	repo       *credential.Store
	state      *state.Store
	jobs       *Jobs
	clock      core.Clock
	randomIntn func(int) int
	mu         sync.Mutex
	cancel     context.CancelFunc
	done       chan struct{}
	settings   config.Settings
	Reconcile  func(context.Context) error
}

func NewScheduler(r *credential.Store, s *state.Store, j *Jobs, c core.Clock, rng func(int) int) *Scheduler {
	if rng == nil {
		rng = randv2.IntN
	}
	return &Scheduler{repo: r, state: s, jobs: j, clock: c, randomIntn: rng}
}
func (s *Scheduler) stop(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		s.cancel = nil
		s.done = nil
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Scheduler) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop(ctx)
}
func (s *Scheduler) Reconfigure(ctx context.Context, settings config.Settings) error {
	if e := config.Validate(settings); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.stop(ctx); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	previous := s.settings
	s.settings = settings
	if previous.CheckinAt != "" && (previous.CheckinAt != settings.CheckinAt || previous.JitterMinutes != settings.JitterMinutes || previous.Timezone != settings.Timezone) {
		if e := s.state.Update(func(v *state.Snapshot) error {
			for id, entry := range v.Schedules {
				if entry.WorkID == "" {
					delete(v.Schedules, id)
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	if !settings.Enabled {
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	done := s.done
	go func() {
		defer close(done)
		defer cancel()
		errCh := make(chan error, 1)
		var wg sync.WaitGroup
		defer func() { cancel(); wg.Wait() }()
		workerStarted := false
		startup := true
		lastReconcile := time.Time{}
		for {
			if runCtx.Err() != nil {
				return
			}
			if s.Reconcile != nil && (lastReconcile.IsZero() || s.clock.Now().Sub(lastReconcile) >= time.Minute) {
				if e := s.Reconcile(runCtx); e != nil && runCtx.Err() == nil {
					if core.ErrorKind(e) == "host_unavailable" {
						if s.clock.Sleep(runCtx, time.Second) != nil {
							return
						}
						continue
					}
					s.fail(e)
					cancel()
					return
				}
				lastReconcile = s.clock.Now()
			}
			if !workerStarted {
				workerStarted = true
				wg.Add(1)
				go func() { defer wg.Done(); errCh <- s.jobs.Run(runCtx) }()
			}
			if e := s.Tick(runCtx, settings, startup); e != nil {
				if runCtx.Err() == nil {
					s.fail(e)
				}
				cancel()
				return
			}
			startup = false
			select {
			case e := <-errCh:
				if e != nil && !errors.Is(e, context.Canceled) {
					s.fail(e)
				}
				cancel()
				return
			default:
			}
			if e := s.clock.Sleep(runCtx, time.Second); e != nil {
				return
			}
		}
	}()
	return nil
}
func (s *Scheduler) fail(e error) {
	_ = s.state.Update(func(v *state.Snapshot) error {
		v.Error = core.ErrorKind(e)
		if v.Error == "unknown_error" {
			v.Error = "engine_error"
		}
		return nil
	})
}

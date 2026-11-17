package account

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
)

const healthCacheAge = 5 * time.Minute

const (
	healthBucketSeconds = 1800
	healthBucketCount   = 48
	healthMaxPages      = 20
)

type healthRefresh struct {
	mu      sync.Mutex
	root    context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
	error   string
}

// StartHealth binds background refreshes to the current plugin lifetime.
func (s *Service) StartHealth(root context.Context) {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	if s.health.root != nil && s.health.root.Err() == nil {
		return
	}
	if root == nil {
		root = context.Background()
	}
	s.health.root, s.health.cancel = context.WithCancel(root)
	s.health.error = ""
}

func (s *Service) ShutdownHealth(ctx context.Context) error {
	s.health.mu.Lock()
	if s.health.cancel != nil {
		s.health.cancel()
	}
	done := s.health.done
	s.health.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// HealthStatus only reads counters; polling must never query the sites.
func (s *Service) HealthStatus() core.HealthSummary {
	snap := s.state.Read()
	out := core.HealthSummary{Accounts: map[string]core.AccountHealth{}}
	for id, a := range snap.Health {
		rec, exists := snap.Accounts[id]
		if !exists || rec.Status == "deleted" || snap.DeletePending[id] {
			continue
		}
		if !a.SyncedAt.IsZero() && time.Since(a.SyncedAt) >= healthCacheAge {
			a.Stale = true
		}
		out.Accounts[id] = a
	}
	s.health.mu.Lock()
	out.Refreshing, out.Error = s.health.running, s.health.error
	s.health.mu.Unlock()
	return out
}

// RefreshHealth starts at most one worker and returns without waiting for logs.
// Failed attempts are cached too, so polling cannot hammer an unavailable site.
func (s *Service) RefreshHealth(ctx context.Context, force bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	if s.health.root == nil || s.health.root.Err() != nil {
		return false, core.Error("disabled")
	}
	if s.health.running {
		return false, nil
	}
	snap := s.state.Read()
	ids := make([]string, 0, len(snap.Accounts))
	for id, a := range snap.Accounts {
		if a.Status == "deleted" || snap.DeletePending[id] {
			continue
		}
		cached := snap.Health[id]
		if !force && !cached.CheckedAt.IsZero() && time.Since(cached.CheckedAt) < healthCacheAge {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return false, nil
	}
	// If a round reaches its time budget, the next round must start with the
	// accounts it did not reach, rather than retrying the same early hashes.
	sort.Slice(ids, func(i, j int) bool {
		a, b := snap.Health[ids[i]].CheckedAt, snap.Health[ids[j]].CheckedAt
		if !a.Equal(b) {
			return a.Before(b)
		}
		return ids[i] < ids[j]
	})
	s.health.running, s.health.error = true, ""
	s.health.done = make(chan struct{})
	root, done := s.health.root, s.health.done
	go s.refreshHealth(root, done, snap, ids)
	return true, nil
}

func (s *Service) refreshHealth(root context.Context, done chan struct{}, snap state.Snapshot, ids []string) {
	defer func() {
		s.health.mu.Lock()
		s.health.running = false
		close(done)
		s.health.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(root, 120*time.Second)
	defer cancel()
	end := time.Now().Unix()
	start := end - healthBucketCount*healthBucketSeconds
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		rec := snap.Accounts[id]
		accountCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		out, generation, err := s.collectHealth(accountCtx, rec, start, end)
		stop()
		if ctx.Err() != nil {
			return
		}
		if core.ErrorKind(err) == "health_invalidated" {
			continue
		}
		if err = s.saveHealth(ctx, rec, generation, out); err != nil {
			if core.ErrorKind(err) == "health_invalidated" {
				continue
			}
			s.health.mu.Lock()
			s.health.error = healthError(err)
			s.health.mu.Unlock()
			return
		}
	}
}

func healthError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "health_timeout"
	}
	return core.ErrorKind(err)
}

func (s *Service) collectHealth(ctx context.Context, rec state.AccountRecord, start, end int64) (core.AccountHealth, uint64, error) {
	out := core.AccountHealth{ID: rec.ID, Start: start, End: end, Complete: true, Buckets: make([]core.HealthBucket, healthBucketCount)}
	for i := range out.Buckets {
		out.Buckets[i].Start = start + int64(i)*healthBucketSeconds
	}
	generation := rec.LastGeneration
	hasPage := false
	for _, kind := range []int{2, 5} {
		fetched := int64(0)
		for p := 1; p <= healthMaxPages; p++ {
			pageCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			var page newapi.HealthLogPage
			err := s.broker.WithAccount(pageCtx, rec.ID, func(use credential.UseCredential) error {
				current := s.state.Read()
				a, exists := current.Accounts[rec.ID]
				if !exists || current.DeletePending[rec.ID] || a.Status == "deleted" || a.LastGeneration != generation || a.Site != rec.Site || a.UpstreamUserID != rec.UpstreamUserID {
					return core.Error("health_invalidated")
				}
				return use(func(a core.Account, bearer string) error {
					current := s.state.Read().Accounts[rec.ID]
					if a.Site != rec.Site || a.UpstreamUserID != rec.UpstreamUserID || current.LastGeneration != a.Credential.Generation {
						return core.Error("health_invalidated")
					}
					// A routine Broker refresh can advance the credential generation
					// inside this gate; a rebind between pages is still rejected.
					generation = a.Credential.Generation
					var err error
					page, err = s.client.HealthLogs(pageCtx, a.Site, bearer, a.UpstreamUserID, kind, p, start, end)
					return err
				})
			})
			cancel()
			if core.ErrorKind(err) == "health_invalidated" {
				return out, generation, err
			}
			if err != nil {
				out.Complete = false
				if out.Error == "" {
					out.Error = healthError(err)
				}
				break
			}
			hasPage = true
			for _, ts := range page.Timestamps {
				b := &out.Buckets[(ts-start)/healthBucketSeconds]
				if kind == 2 {
					b.Success++
					out.Success++
				} else {
					b.Failed++
					out.Failed++
				}
			}
			fetched += int64(len(page.Timestamps))
			if page.TotalKnown && fetched >= page.Total {
				if fetched > page.Total {
					out.Complete, out.Error = false, "unsupported_site"
				}
				break
			}
			if len(page.Timestamps) == 0 || (!page.TotalKnown && len(page.Timestamps) < 100) {
				if page.TotalKnown && fetched < page.Total {
					out.Complete, out.Error = false, "unsupported_site"
				}
				break
			}
			if p == healthMaxPages {
				out.Complete, out.Truncated = false, true
				if out.Error == "" {
					out.Error = "health_truncated"
				}
			}
		}
	}
	out.CheckedAt = time.Now().UTC()
	if hasPage {
		out.SyncedAt = out.CheckedAt
	}
	return out, generation, nil
}

func (s *Service) saveHealth(ctx context.Context, rec state.AccountRecord, generation uint64, out core.AccountHealth) error {
	return s.gate.Do(ctx, func() error {
		return s.state.Update(func(v *state.Snapshot) error {
			current, exists := v.Accounts[rec.ID]
			if !exists || v.DeletePending[rec.ID] || current.Status == "deleted" || current.LastGeneration != generation || current.Site != rec.Site || current.UpstreamUserID != rec.UpstreamUserID {
				return core.Error("health_invalidated")
			}
			previous := v.Health[rec.ID]
			if !previous.SyncedAt.IsZero() && (out.SyncedAt.IsZero() || (!out.Complete && previous.Complete)) {
				previous.Stale, previous.CheckedAt, previous.Error = true, out.CheckedAt, out.Error
				v.Health[rec.ID] = previous
			} else {
				v.Health[rec.ID] = out
			}
			return nil
		})
	})
}

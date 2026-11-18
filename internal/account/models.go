package account

import (
	"context"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
)

// modelsCacheAge is how long a fetched model list stays fresh. Model
// availability changes far less often than request logs, so it is not
// re-read on every health cycle; failed attempts retry on the health
// cadence instead.
const modelsCacheAge = 12 * time.Hour

// ModelsStatus only reads the cache; polling must never query the sites.
func (s *Service) ModelsStatus() core.ModelsSummary {
	out := core.ModelsSummary{Accounts: map[string]core.AccountModels{}}
	if s.health.root == nil || s.health.root.Err() != nil {
		return out
	}
	snap := s.state.Read()
	for id, m := range snap.Models {
		rec, exists := snap.Accounts[id]
		if !exists || rec.Status == "deleted" || snap.DeletePending[id] {
			continue
		}
		if !m.SyncedAt.IsZero() && time.Since(m.SyncedAt) >= modelsCacheAge {
			m.Stale = true
		}
		out.Accounts[id] = m
	}
	s.health.mu.Lock()
	out.Refreshing = s.health.running
	s.health.mu.Unlock()
	return out
}

// modelsCacheDue reports whether the account's model list needs a read in
// this round: never attempted, past the attempt cooldown, and either never
// succeeded or the last good list is older than modelsCacheAge.
func modelsCacheDue(snap state.Snapshot, id string) bool {
	cached := snap.Models[id]
	if cached.CheckedAt.IsZero() {
		return true
	}
	if time.Since(cached.CheckedAt) < healthCacheAge {
		return false
	}
	return cached.SyncedAt.IsZero() || time.Since(cached.SyncedAt) >= modelsCacheAge
}

// collectModels reads the account's available model names in one request.
// A failed read is a cached attempt, not a round-level abort.
func (s *Service) collectModels(ctx context.Context, rec state.AccountRecord) (core.AccountModels, uint64, error) {
	out := core.AccountModels{ID: rec.ID}
	generation := rec.LastGeneration
	err := s.broker.WithAccount(ctx, rec.ID, func(use credential.UseCredential) error {
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
			generation = a.Credential.Generation
			names, err := s.client.UserModels(ctx, a.Site, bearer, a.UpstreamUserID)
			if err == nil {
				out.Models, out.Complete = names, true
			}
			return err
		})
	})
	out.CheckedAt = time.Now().UTC()
	if core.ErrorKind(err) == "health_invalidated" {
		return out, generation, err
	}
	if err != nil {
		out.Error = healthError(err)
		return out, generation, nil
	}
	out.SyncedAt = out.CheckedAt
	return out, generation, nil
}

// saveModels rechecks the account before writing; a failed attempt keeps the
// last good list, marked stale, so a rebound account cannot get old names back.
func (s *Service) saveModels(ctx context.Context, rec state.AccountRecord, generation uint64, out core.AccountModels) error {
	return s.gate.Do(ctx, func() error {
		return s.state.Update(func(v *state.Snapshot) error {
			current, exists := v.Accounts[rec.ID]
			if !exists || v.DeletePending[rec.ID] || current.Status == "deleted" || current.LastGeneration != generation || current.Site != rec.Site || current.UpstreamUserID != rec.UpstreamUserID {
				return core.Error("health_invalidated")
			}
			previous := v.Models[rec.ID]
			if out.SyncedAt.IsZero() && !previous.SyncedAt.IsZero() {
				previous.Stale, previous.CheckedAt, previous.Error = true, out.CheckedAt, out.Error
				v.Models[rec.ID] = previous
			} else {
				v.Models[rec.ID] = out
			}
			return nil
		})
	})
}

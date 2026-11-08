package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
)

type Runner struct {
	broker *credential.Broker
	client *newapi.Client
	state  *state.Store
	clock  core.Clock
}

func NewRunner(b *credential.Broker, c *newapi.Client, s *state.Store, clock core.Clock) *Runner {
	return &Runner{b, c, s, clock}
}
func (r *Runner) Attempt(ctx context.Context, id string) (core.AttemptResult, error) {
	return r.attempt(ctx, id, false, "")
}
func (r *Runner) attempt(ctx context.Context, id string, queryOnly bool, workID string) (result core.AttemptResult, err error) {
	result.ObservedAt = r.clock.Now().UTC()
	err = r.broker.WithAccount(ctx, id, func(use credential.UseCredential) error {
		if workID != "" {
			snapshot := r.state.Read()
			work, ok := snapshot.WorkItems[workID]
			if !ok || work.Status != "running" || snapshot.DeletePending[id] || work.Generation != snapshot.Accounts[id].LastGeneration {
				return core.Error("canceled")
			}
		}
		attemptErr := use(func(a core.Account, bearer string) error {
			info, e := r.client.Probe(ctx, a.Site)
			if e != nil {
				return e
			}
			if info.TurnstileCheck == nil {
				return core.Error("unsupported_site")
			}
			if *info.TurnstileCheck {
				return core.Error("turnstile_blocked")
			}
			stats, e := r.client.CheckinStatus(ctx, a.Site, bearer, "")
			if e != nil {
				return e
			}
			synced := r.clock.Now().UTC()
			if e = r.state.Update(func(s *state.Snapshot) error {
				ar := s.Accounts[id]
				ar.TotalQuota = stats.TotalQuota
				ar.TotalCheckins = stats.TotalCheckins
				ar.StatsSyncedAt = synced
				ar.StatsStale = false
				s.Accounts[id] = ar
				return nil
			}); e != nil {
				return e
			}
			result.StatsSyncedAt = synced
			if stats.CheckedInToday {
				result.Status = "already"
				return nil
			}
			if queryOnly {
				return core.Error("retry_exhausted")
			}
			if e = r.state.Update(func(s *state.Snapshot) error {
				for k, w := range s.WorkItems {
					if w.AccountID == id && w.Status == "running" {
						w.NeedsReconcile = true
						s.WorkItems[k] = w
					}
				}
				ar := s.Accounts[id]
				ar.LastResult = &core.AttemptResult{Status: "result_unknown", ObservedAt: r.clock.Now().UTC(), NeedsReconcile: true}
				s.Accounts[id] = ar
				return nil
			}); e != nil {
				return e
			}
			result.NeedsReconcile = true
			checkin, e := r.client.Checkin(ctx, a.Site, bearer)
			if e != nil {
				k := core.ErrorKind(e)
				if k == "already" {
					stats, e = r.client.CheckinStatus(ctx, a.Site, bearer, "")
					if e == nil && stats.CheckedInToday {
						result.Status = "already"
						result.NeedsReconcile = false
						return nil
					}
				}
				if k == "network_error" || k == "upstream_error" || k == "canceled" || k == "unsupported_site" {
					return core.Error("result_unknown")
				}
				result.NeedsReconcile = false
				return e
			}
			result.Status = "success"
			result.ServerDate = checkin.Date
			result.QuotaAwarded = checkin.QuotaAwarded
			result.NeedsReconcile = false
			final, statErr := r.client.CheckinStatus(ctx, a.Site, bearer, "")
			return r.state.Update(func(s *state.Snapshot) error {
				ar := s.Accounts[id]
				ar.WritePermissionVerified = true
				if statErr != nil {
					ar.StatsStale = true
				} else {
					ar.TotalQuota = final.TotalQuota
					ar.TotalCheckins = final.TotalCheckins
					ar.StatsSyncedAt = r.clock.Now().UTC()
					ar.StatsStale = false
					result.StatsSyncedAt = ar.StatsSyncedAt
				}
				s.Accounts[id] = ar
				return nil
			})
		})
		if attemptErr != nil {
			result.Status = core.ErrorKind(attemptErr)
		}
		if result.Status == "already" {
			result.NeedsReconcile = false
		}
		saveErr := r.state.Update(func(s *state.Snapshot) error {
			s.RecordResult(id, result)
			if attemptErr != nil && !Retryable(attemptErr) && result.Status != "unknown_error" && result.Status != "canceled" && result.Status != "retry_exhausted" {
				a := s.Accounts[id]
				a.Status = result.Status
				if result.Status == "permission_denied" {
					a.PermissionDenied = true
					a.WritePermissionVerified = false
				}
				s.Accounts[id] = a
			}
			return nil
		})
		if saveErr != nil {
			return saveErr
		}
		return attemptErr
	})
	return result, err
}

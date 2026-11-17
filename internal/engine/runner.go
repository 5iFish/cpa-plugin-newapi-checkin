package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"time"
)

type Runner struct {
	broker *credential.Broker
	client *newapi.Client
	state  *state.Store
	clock  core.Clock
	// Location supplies the schedule timezone for login-mode day keys; nil
	// falls back to the host local timezone.
	Location func() *time.Location
}

func NewRunner(b *credential.Broker, c *newapi.Client, s *state.Store, clock core.Clock) *Runner {
	return &Runner{broker: b, client: c, state: s, clock: clock}
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
		// loginAttempt persists its own terminal outcome (reward snapshot plus
		// attempt result) atomically; the generic path below must not record it
		// a second time.
		loginRecorded := false
		attemptErr := use(func(a core.Account, bearer string) error {
			if a.EffectiveCheckinMode() == core.CheckinModeLogin {
				var e error
				loginRecorded, e = r.loginAttempt(ctx, id, a, bearer, queryOnly, &result)
				return e
			}
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
			stats, e := r.client.CheckinStatus(ctx, a.Site, bearer, "", a.UpstreamUserID)
			if e != nil {
				return e
			}
			// The wallet balance rides along on the daily stats query; a
			// failure here must never fail the check-in itself.
			var balance *int64
			if idn, e2 := r.client.Self(ctx, a.Site, bearer, a.UpstreamUserID); e2 == nil && idn.ID == a.UpstreamUserID && idn.QuotaPresent {
				q := idn.Quota
				balance = &q
			}
			synced := r.clock.Now().UTC()
			if e = r.state.Update(func(s *state.Snapshot) error {
				ar := s.Accounts[id]
				ar.TotalQuota = stats.TotalQuota
				ar.TotalCheckins = stats.TotalCheckins
				ar.StatsSyncedAt = synced
				ar.StatsStale = false
				if info.QuotaPerUnit > 0 {
					ar.BalancePerUnit = info.QuotaPerUnit
				}
				if balance != nil {
					ar.BalanceQuota = balance
				}
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
			checkin, e := r.client.Checkin(ctx, a.Site, bearer, a.UpstreamUserID)
			if e != nil {
				k := core.ErrorKind(e)
				if k == "already" {
					stats, e = r.client.CheckinStatus(ctx, a.Site, bearer, "", a.UpstreamUserID)
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
			final, statErr := r.client.CheckinStatus(ctx, a.Site, bearer, "", a.UpstreamUserID)
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
		deferred := core.ErrorKind(attemptErr) == "login_deferred"
		if attemptErr != nil {
			result.Status = core.ErrorKind(attemptErr)
		}
		if deferred {
			// A cooldown deferral is not a completed business attempt and must
			// not replace the last result or pause the account.
			result.Status = core.RewardStatePendingLogin
		}
		if result.Status == "already" {
			result.NeedsReconcile = false
		}
		saveErr := r.state.Update(func(s *state.Snapshot) error {
			if !deferred && !loginRecorded {
				s.RecordResult(id, result)
			}
			if attemptErr != nil && !deferred && !Retryable(attemptErr) && result.Status != "unknown_error" && result.Status != "canceled" && result.Status != "retry_exhausted" {
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

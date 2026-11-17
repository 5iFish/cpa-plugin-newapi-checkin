package engine

import (
	"context"
	"strings"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
)

// minLoginInterval keeps a schedule or timezone change from replaying a login
// that may already have run, independently of the local day key.
const minLoginInterval = 20 * time.Hour

// loginAttempt executes the daily task for accounts whose site grants the
// reward on a fresh login. It never contacts check-in endpoints. Evidence from
// /api/user/self is evaluated before the chain so an unexplained balance rise
// is adopted first, and a manual login today suppresses an automatic one.
// The returned flag reports that the terminal outcome was already persisted
// (reward snapshot and attempt result together), so the generic runner must
// not record it a second time.
func (r *Runner) loginAttempt(ctx context.Context, id string, a core.Account, bearer string, queryOnly bool, result *core.AttemptResult) (bool, error) {
	info, e := r.client.Probe(ctx, a.Site)
	if e != nil {
		return false, e
	}
	if info.TurnstileCheck == nil {
		return false, core.Error("unsupported_site")
	}
	loc := time.Local
	if r.Location != nil {
		if l := r.Location(); l != nil {
			loc = l
		}
	}
	now := r.clock.Now().In(loc)
	dayKey := now.Format("2006-01-02")
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Unix()

	rec := r.state.Read().Accounts[id]
	identity, e := r.client.Self(ctx, a.Site, bearer, a.UpstreamUserID)
	evidenceBlocked := false
	if e != nil {
		if core.ErrorKind(e) == core.ErrorWafBlocked {
			// The site gates /api/user/self behind a bot check. Login endpoints
			// are unaffected, so a daily login can still run; only the reward
			// evidence cannot be read. Keep the stored snapshot untouched.
			evidenceBlocked = true
		} else {
			// An OAuth-bootstrapped session without a refresh cookie only carries
			// a short-lived access token; once it lapses between daily logins the
			// chain itself mints a fresh one, so fall through to the chain instead
			// of failing the day.
			tolerable := a.Credential.Kind == "session" && a.Credential.RefreshToken == "" && a.Credential.OAuthCookie != "" &&
				(core.ErrorKind(e) == "credential_expired" || core.ErrorKind(e) == "permission_denied")
			if !tolerable {
				return false, e
			}
			identity = core.Identity{}
		}
	} else {
		if identity.ID != a.UpstreamUserID {
			return false, core.Error("identity_mismatch")
		}
		if !identity.HasRewardEvidence() {
			return false, core.Error("unsupported_site")
		}
	}

	// Evaluate before any login attempt: this adopts an unattributed rise and
	// detects a login that already happened today (for example, manually).
	pre := rewardSnapshotOutcome(rec, dayKey)
	if !evidenceBlocked {
		pre = r.rewardEvidence(identity, core.RewardSnapshot{
			Total:     rec.RewardTotal,
			LastLogin: rec.RewardLastLogin,
			DayKey:    rec.RewardDayKey,
			State:     rec.RewardState,
			Baseline:  rec.RewardBaseline,
			Receipt:   rec.RewardReceipt,
		}, dayKey, dayStart, false)
		if e := r.persistReward(id, pre.Snapshot, balanceOf(identity, info)); e != nil {
			return false, e
		}
	}
	recent := rec.RewardAttemptAt > 0 && now.Unix()-rec.RewardAttemptAt < int64(minLoginInterval/time.Second)
	pause := rewardPause(rec)
	if pre.Status != core.RewardStatePendingLogin || queryOnly || pause != "" || rec.RewardAttemptDayKey == dayKey || recent || a.Credential.OAuthCookie == "" {
		if evidenceBlocked && pre.Status == core.RewardStatePendingLogin && pause == "" &&
			(queryOnly || rec.RewardAttemptDayKey == dayKey || a.Credential.OAuthCookie == "") {
			// Nothing can complete the day: the evidence endpoint is walled and
			// this run may not (or cannot) log in. Report the wall instead of
			// inventing a pending/completed state.
			return false, core.Error(core.ErrorWafBlocked)
		}
		result.Status = pre.Status
		result.QuotaAwarded = pre.QuotaAwarded
		if pre.Status == core.RewardStatePendingLogin && pause != "" {
			result.Status = pause
		}
		if pre.Status == core.RewardStatePendingLogin && pause == "" && !queryOnly && rec.RewardAttemptDayKey != dayKey && recent && a.Credential.OAuthCookie != "" {
			// The 20-hour guard only defers today's still-eligible attempt. Do
			// not consume the daily task; ask Jobs to retry when the interval
			// has elapsed, without spending a business attempt.
			wait := time.Duration(rec.RewardAttemptAt+int64(minLoginInterval/time.Second)-now.Unix()) * time.Second
			if wait < time.Minute {
				wait = time.Minute
			}
			return false, &core.APIError{Kind: "login_deferred", SafeMessage: "login_deferred", RetryAfter: wait}
		}
		return r.finishLogin(id, pre.Snapshot, pause, *result, balanceOf(identity, info))
	}

	marker := false
	hooks := &newapi.LoginHooks{BeforeCallback: func(context.Context) error {
		if e := r.markLoginAttempt(id, dayKey, now.Unix()); e != nil {
			return e
		}
		marker = true
		return nil
	}}
	chain, chainErr := r.client.RunLoginChain(ctx, a.Site, a.Credential.OAuthProvider, a.Credential.OAuthCookie, hooks)
	// The provider may rotate its session cookie during the chain, and the
	// callback may mint a fresh site session for OAuth-bootstrapped
	// credentials. Persist the newest values in the CPA credential file —
	// never in state — so the next daily login does not depend on yesterday's
	// expiry or replay an already invalidated provider session.
	rotatedProvider := chain.ProviderCookie
	providerChanged := rotatedProvider != "" && rotatedProvider != a.Credential.OAuthCookie && len(rotatedProvider) <= 4096 && !strings.ContainsAny(rotatedProvider, "\r\n")
	tokenChanged := chain.AccessToken != "" && chain.AccessToken != a.Credential.AccessToken
	if providerChanged || tokenChanged {
		if e := r.broker.SaveCredential(ctx, id, func(next *core.Account) {
			if providerChanged {
				next.Credential.OAuthCookie = rotatedProvider
			}
			if tokenChanged {
				next.Credential.AccessToken = chain.AccessToken
				next.Credential.AccessExpiresAt = chain.AccessExpiresAt
				if chain.SessionID != "" {
					next.Credential.SessionID = chain.SessionID
				}
				if chain.SessionExpires != 0 {
					next.Credential.SessionExpiresAt = chain.SessionExpires
				}
				if chain.RefreshCookie != "" {
					next.Credential.RefreshToken = chain.RefreshCookie
				}
			}
		}); e != nil {
			return false, e
		}
	}
	var receipt bool
	switch {
	case chainErr != nil:
		switch core.ErrorKind(chainErr) {
		case core.RewardStateCookieExpired, core.RewardStateNeedsAttention, core.RewardStateChallengeBlocked:
			return false, r.pauseLogin(id, pre.Snapshot, core.ErrorKind(chainErr), balanceOf(identity, info))
		case "canceled":
			return false, chainErr
		}
		if !marker {
			// The callback was never sent, so rebuilding the chain cannot
			// duplicate a login and a normal retry is safe.
			return false, chainErr
		}
		// The callback may have completed. Never replay it; inspect the
		// evidence below instead.
	case chain.UserID != a.UpstreamUserID:
		return false, r.pauseLogin(id, pre.Snapshot, core.RewardStateNeedsAttention, balanceOf(identity, info))
	default:
		receipt = chain.CheckedIn != nil && *chain.CheckedIn
	}
	if receipt {
		// Persist the callback receipt before any further query. If the query
		// fails, the receipt survives and later evidence can still attribute a
		// same-day quota rise instead of absorbing it into the baseline.
		if e := r.persistReceipt(id, dayKey); e != nil {
			return false, e
		}
	}

	// The evidence query right after the login uses the freshly minted token:
	// for bootstrapped credentials the pre-chain one may be minutes from
	// expiry (or already tolerated as expired above).
	postBearer := bearer
	if chain.AccessToken != "" {
		postBearer = chain.AccessToken
	}
	identity, e = r.client.Self(ctx, a.Site, postBearer, a.UpstreamUserID)
	if e != nil {
		if core.ErrorKind(e) == core.ErrorWafBlocked {
			// The chain just completed a login; only the amount is unreadable.
			result.Status = core.RewardStateUnknown
			result.QuotaAwarded = 0
			return r.finishUnverifiedLogin(id, dayKey, pre.Snapshot, receipt, *result)
		}
		return false, e
	}
	if identity.ID != a.UpstreamUserID {
		return false, core.Error("identity_mismatch")
	}
	if !identity.HasRewardEvidence() {
		return false, core.Error("unsupported_site")
	}
	post := r.rewardEvidence(identity, pre.Snapshot, dayKey, dayStart, receipt)
	result.Status = post.Status
	result.QuotaAwarded = post.QuotaAwarded
	return r.finishLogin(id, post.Snapshot, "", *result, balanceOf(identity, info))
}

// balanceSnapshot carries the wallet evidence observed during an attempt so
// every login persistence path stores the amount and its conversion ratio.
type balanceSnapshot struct {
	quota   *int64
	perUnit int64
}

func balanceOf(identity core.Identity, info core.SiteInfo) balanceSnapshot {
	b := balanceSnapshot{perUnit: info.QuotaPerUnit}
	if identity.QuotaPresent {
		q := identity.Quota
		b.quota = &q
	}
	return b
}

func (r *Runner) rewardEvidence(identity core.Identity, cur core.RewardSnapshot, dayKey string, dayStart int64, receipt bool) core.RewardOutcome {
	loginToday := identity.LoginEvidenceOn(dayKey, dayStart)
	return core.EvaluateReward(cur, core.RewardEvidence{
		Total:        identity.Quota + identity.UsedQuota,
		TotalSet:     true,
		LastLogin:    identity.LastLogin,
		LastLoginSet: true,
		LoginToday:   loginToday,
		Receipt:      receipt,
	}, dayKey)
}

// rewardPause reports a stored provider-session block. The account status is
// the durable signal; RewardState is checked only as a fallback for records
// written before the pause moved entirely into the status.
func rewardPause(rec state.AccountRecord) string {
	for _, v := range []string{rec.Status, rec.RewardState} {
		if v == core.RewardStateCookieExpired || v == core.RewardStateNeedsAttention || v == core.RewardStateChallengeBlocked {
			return v
		}
	}
	return ""
}

func (r *Runner) markLoginAttempt(id, dayKey string, at int64) error {
	return r.state.Update(func(s *state.Snapshot) error {
		a := s.Accounts[id]
		a.RewardAttemptDayKey = dayKey
		a.RewardAttemptAt = at
		s.Accounts[id] = a
		return nil
	})
}

func (r *Runner) persistReceipt(id, dayKey string) error {
	return r.state.Update(func(s *state.Snapshot) error {
		a := s.Accounts[id]
		a.RewardReceipt = true
		a.RewardDayKey = dayKey
		s.Accounts[id] = a
		return nil
	})
}

func (r *Runner) persistReward(id string, snap core.RewardSnapshot, bal balanceSnapshot) error {
	return r.state.Update(func(s *state.Snapshot) error {
		a := s.Accounts[id]
		a.SetReward(snap)
		if bal.quota != nil {
			a.BalanceQuota = bal.quota
		}
		if bal.perUnit > 0 {
			a.BalancePerUnit = bal.perUnit
		}
		s.Accounts[id] = a
		return nil
	})
}

// rewardSnapshotOutcome reports the day state already persisted when the
// evidence endpoint is unreadable (for example an Aliyun WAF captcha wall). It
// never invents new evidence: a day confirmed before the wall keeps its
// outcome, any other day is still pending.
func rewardSnapshotOutcome(rec state.AccountRecord, dayKey string) core.RewardOutcome {
	out := core.RewardOutcome{Snapshot: rec.Reward(), Status: core.RewardStatePendingLogin}
	if rec.RewardDayKey != dayKey {
		return out
	}
	switch rec.RewardState {
	case core.RewardStateSuccess:
		out.Status = "already"
	case core.RewardStateUnknown:
		out.Status = core.RewardStateUnknown
	}
	return out
}

// finishUnverifiedLogin records a login that completed while the evidence
// endpoint was walled. The successful callback is the proof that today's login
// happened; only the awarded amount is unknown. A stale evidence-wall status is
// cleared because the login itself proves the account works.
func (r *Runner) finishUnverifiedLogin(id, dayKey string, snap core.RewardSnapshot, receipt bool, result core.AttemptResult) (bool, error) {
	if snap.DayKey != dayKey || snap.State == "" || snap.State == core.RewardStatePendingLogin {
		snap.DayKey = dayKey
		snap.State = core.RewardStateUnknown
	}
	snap.Baseline = true
	if receipt {
		snap.Receipt = true
	}
	result.Status = core.RewardStateUnknown
	result.QuotaAwarded = 0
	if e := r.state.Update(func(s *state.Snapshot) error {
		a := s.Accounts[id]
		a.SetReward(snap)
		if a.Status == core.ErrorWafBlocked || a.Status == "unsupported_site" {
			a.Status = "ready"
		}
		s.Accounts[id] = a
		s.RecordResult(id, result)
		return nil
	}); e != nil {
		return false, e
	}
	return true, nil
}

// finishLogin persists the terminal reward outcome in one atomic update: the
// evidence snapshot, the attempt result (with its inferred amount), and the
// provider pause when present. Saving them together means a crash can never
// mark a day successful without its award amount, and a provider pause stays
// durable even if the generic runner never writes its final status.
func (r *Runner) finishLogin(id string, snap core.RewardSnapshot, pause string, result core.AttemptResult, bal balanceSnapshot) (bool, error) {
	if e := r.state.Update(func(s *state.Snapshot) error {
		a := s.Accounts[id]
		a.SetReward(snap)
		if bal.quota != nil {
			a.BalanceQuota = bal.quota
		}
		if bal.perUnit > 0 {
			a.BalancePerUnit = bal.perUnit
		}
		if pause != "" {
			a.Status = pause
		}
		s.Accounts[id] = a
		s.RecordResult(id, result)
		return nil
	}); e != nil {
		return false, e
	}
	return true, nil
}

// pauseLogin durably blocks third-party automatic login. The pause lives in
// the account status; the reward snapshot keeps the evidence outcome so a
// later detection can still confirm the same-day reward. Only a rebind clears
// it. The error surfaces the pause so the account stops retrying.
func (r *Runner) pauseLogin(id string, snap core.RewardSnapshot, name string, bal balanceSnapshot) error {
	_ = r.state.Update(func(s *state.Snapshot) error {
		a := s.Accounts[id]
		a.Status = name
		a.SetReward(snap)
		if bal.quota != nil {
			a.BalanceQuota = bal.quota
		}
		if bal.perUnit > 0 {
			a.BalancePerUnit = bal.perUnit
		}
		// Every pause path is a deterministic failure (expired session, consent
		// wall, identity mismatch): the attempt provably did not complete for
		// THIS account. Clear the same-day marker so a manual action (batch
		// check-in or rebind) may retry immediately. Genuinely uncertain
		// callback outcomes never pause and keep the marker guarded.
		a.RewardAttemptDayKey = ""
		a.RewardAttemptAt = 0
		s.Accounts[id] = a
		return nil
	})
	return core.Error(name)
}

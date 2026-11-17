package account

import (
	"context"
	"errors"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"sort"
	"strings"
	"time"
)

type BindInput struct {
	UpstreamUserID            int64  `json:"upstream_user_id"`
	Site                      string `json:"site"`
	Label                     string `json:"label"`
	Channel                   string `json:"channel"`
	Token                     string `json:"token"`
	Username                  string `json:"username"`
	Password                  string `json:"password"`
	RefreshCookie             string `json:"refresh_cookie"`
	AccountID                 string `json:"account_id"`
	DedicatedSessionConfirmed bool   `json:"dedicated_session_confirmed"`
	ExpiresAtHint             *int64 `json:"expires_at_hint"`
	CheckinMode               string `json:"checkin_mode"`
	OAuthProvider             string `json:"oauth_provider"`
	OAuthCookie               string `json:"oauth_cookie"`
}
type Service struct {
	repo   *credential.Store
	state  *state.Store
	client *newapi.Client
	broker *credential.Broker
	gate   *core.Gate
	health healthRefresh
	// Location supplies the schedule timezone for login-mode day keys; nil
	// falls back to the host local timezone.
	Location func() *time.Location
}

func NewService(r *credential.Store, s *state.Store, c *newapi.Client, b *credential.Broker, g *core.Gate) *Service {
	return &Service{repo: r, state: s, client: c, broker: b, gate: g}
}

// inheritedMode returns the mode a bind will use before the credential switch.
// It only looks at an explicit mode, the referenced account, or (for password
// binds) an existing login-mode account with the same site username.
func (s *Service) inheritedMode(site string, in BindInput, old map[string]core.Account) string {
	if in.CheckinMode != "" {
		return in.CheckinMode
	}
	lookup := func(id string) string {
		if a, ok := old[id]; ok {
			return a.EffectiveCheckinMode()
		}
		if record, ok := s.state.Read().Accounts[id]; ok {
			if record.CheckinMode == core.CheckinModeLogin {
				return core.CheckinModeLogin
			}
			return core.CheckinModeAPI
		}
		return ""
	}
	if in.AccountID != "" {
		if mode := lookup(in.AccountID); mode != "" {
			return mode
		}
	}
	if in.UpstreamUserID > 0 {
		if mode := lookup(core.AccountID(site, in.UpstreamUserID)); mode != "" {
			return mode
		}
	}
	if in.Channel == "password" && in.Username != "" {
		for _, a := range old {
			if a.Site == site && a.Username == in.Username && a.EffectiveCheckinMode() == core.CheckinModeLogin {
				return core.CheckinModeLogin
			}
		}
	}
	return ""
}
func (s *Service) Bind(ctx context.Context, in BindInput) (out core.AccountSummary, err error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	err = s.gate.Do(ctx, func() error {
		site, e := newapi.NormalizeSite(in.Site)
		if e != nil {
			return e
		}
		if len(in.Label) > 256 || in.UpstreamUserID < 0 {
			return core.Error("invalid_input")
		}
		if in.Channel == "refresh_cookie" && !in.DedicatedSessionConfirmed {
			return core.Error("invalid_input")
		}
		if !core.ValidCheckinMode(in.CheckinMode) {
			return core.Error("invalid_input")
		}
		// A cookie always travels with its provider. A provider without a
		// cookie is valid only as an explicit "keep the saved session" rebind
		// request; an empty provider selects the manual channel and drops it.
		if !core.ValidOAuthProvider(in.OAuthProvider) || (in.OAuthCookie != "" && in.OAuthProvider == "") {
			return core.Error("invalid_input")
		}
		if in.OAuthCookie != "" && (len(in.OAuthCookie) > 4096 || strings.ContainsAny(in.OAuthCookie, "\r\n") || !strings.Contains(in.OAuthCookie, "=")) {
			return core.Error("invalid_input")
		}
		// The third-party cookie only makes sense for a token binding in login
		// mode. An omitted mode is resolved below from the existing account.
		if in.OAuthProvider != "" && (!loginChannelOK(in.Channel) || (in.CheckinMode != "" && in.CheckinMode != core.CheckinModeLogin)) {
			return core.Error("invalid_input")
		}
		existing, e := s.repo.List(ctx)
		if e != nil {
			return e
		}
		old := map[string]core.Account{}
		revs := map[string]credential.Revision{}
		for _, a := range existing {
			loaded, r, e := s.repo.Load(ctx, a.ID)
			if e != nil {
				return e
			}
			old[a.ID] = loaded
			revs[a.ID] = r
		}
		if in.AccountID != "" {
			if _, ok := old[in.AccountID]; !ok {
				record, known := s.state.Read().Accounts[in.AccountID]
				if !known {
					return core.Error("credential_unreadable")
				}
				s.repo.Track(in.AccountID, record.PhysicalPath)
				missing, e := s.repo.Missing(ctx, in.AccountID)
				if e != nil {
					return e
				}
				if !missing {
					return core.Error("credential_unreadable")
				}
			}
		}
		// Resolve an omitted mode before the credential switch so an inherited
		// login mode rejects a password or refresh binding before side effects.
		mode := s.inheritedMode(site, in, old)
		if mode == core.CheckinModeLogin && !loginChannelOK(in.Channel) {
			return core.Error("invalid_input")
		}
		if in.OAuthProvider != "" && mode != "" && mode != core.CheckinModeLogin {
			return core.Error("invalid_input")
		}
		info, e := s.client.Probe(ctx, site)
		if e != nil {
			return e
		}
		c := core.Credential{Channel: in.Channel, Generation: 1}
		var identity core.Identity
		var sessionKey string
		switch in.Channel {
		case "token":
			if strings.TrimSpace(in.Token) == "" {
				// A rebind can keep the stored token: the console never sees the
				// secret, so an omitted token means "leave it unchanged". The
				// target account must be referenced explicitly.
				previous, ok := old[in.AccountID]
				if in.AccountID == "" || !ok || previous.Credential.Channel != "token" || previous.Credential.Token == "" {
					return core.Error("invalid_input")
				}
				in.Token = previous.Credential.Token
				if in.ExpiresAtHint == nil {
					in.ExpiresAtHint = previous.Credential.ExpiresAtHint
				}
			}
			c.Token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(in.Token), "Bearer "))
			if c.Token == "" || strings.ContainsAny(c.Token, " \t\r\n") || strings.Count(c.Token, ".") == 2 {
				return core.Error("invalid_input")
			}
			c.Kind = "legacy"
			if strings.HasPrefix(c.Token, "nap_") {
				c.Kind = "pat"
			}
			c.ExpiresAtHint = in.ExpiresAtHint
			if in.OAuthCookie != "" {
				c.OAuthProvider = in.OAuthProvider
				c.OAuthCookie = in.OAuthCookie
			}
		case "oauth":
			// Bootstrap channel: one replayed third-party login mints the site
			// session (access token + refresh cookie) directly from the OAuth
			// callback, for NewAPI-style sites without a PAT entry point.
			if in.OAuthProvider == "" || in.OAuthCookie == "" {
				return core.Error("invalid_input")
			}
			chain, e := s.client.RunLoginChain(ctx, site, in.OAuthProvider, in.OAuthCookie, nil)
			if e != nil {
				return e
			}
			if chain.AccessToken == "" {
				return core.Error("unsupported_site")
			}
			c.Kind = "session"
			c.AccessToken = chain.AccessToken
			c.AccessExpiresAt = chain.AccessExpiresAt
			c.RefreshToken = chain.RefreshCookie
			c.SessionID = chain.SessionID
			c.SessionExpiresAt = chain.SessionExpires
			if c.SessionExpiresAt == 0 {
				c.SessionExpiresAt = time.Now().Add(30 * 24 * time.Hour).Unix()
			}
			c.OAuthProvider = in.OAuthProvider
			c.OAuthCookie = in.OAuthCookie
			identity = core.Identity{ID: chain.UserID, Username: chain.Username}
		case "password", "refresh_cookie":
			var bundle core.SessionBundle
			if in.Channel == "password" {
				bundle, e = s.client.PasswordLogin(ctx, site, in.Username, in.Password)
			} else {
				refresh := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(in.RefreshCookie), "new_api_refresh="))
				sid, parseErr := newapi.SessionID(refresh)
				if parseErr != nil {
					return parseErr
				}
				sessionKey = site + "\n" + sid
				for _, a := range existing {
					if a.Site == site && a.Credential.SessionID == sid {
						return core.Error("session_already_managed")
					}
				}
				if known, ok := s.state.Read().KnownSessions[sessionKey]; ok && known.ExpiresAt.After(time.Now()) {
					return core.Error("session_already_managed")
				}
				if e = s.state.Update(func(v *state.Snapshot) error {
					v.KnownSessions[sessionKey] = state.KnownSession{Site: site, SessionID: sid, Status: "importing", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).UTC()}
					return nil
				}); e != nil {
					return e
				}
				bundle, e = s.client.Refresh(ctx, site, refresh)
				if e != nil {
					var api *core.APIError
					definite := errors.As(e, &api) && api.HTTPStatus >= 400 && api.HTTPStatus < 500 && api.Code != ""
					saveErr := s.state.Update(func(v *state.Snapshot) error {
						if definite {
							delete(v.KnownSessions, sessionKey)
						} else {
							k := v.KnownSessions[sessionKey]
							k.Status = "unknown"
							v.KnownSessions[sessionKey] = k
						}
						return nil
					})
					if saveErr != nil {
						return saveErr
					}
					if !definite {
						return core.Error("refresh_outcome_unknown")
					}
					return e
				}
			}
			if e != nil {
				return e
			}
			identity = bundle.Identity
			c.Kind = "session"
			c.AccessToken = bundle.AccessToken
			c.RefreshToken = bundle.RefreshToken
			c.SessionID = bundle.SessionID
			c.AccessExpiresAt = bundle.AccessExpiresAt
			c.SessionExpiresAt = bundle.SessionExpiresAt
			if in.OAuthCookie != "" {
				// A dedicated refresh session may also drive the third-party
				// automatic login for sites without a PAT entry point.
				c.OAuthProvider = in.OAuthProvider
				c.OAuthCookie = in.OAuthCookie
			}
			sessionKey = site + "\n" + c.SessionID
			// Preserve a consumed/new session even when later identity or storage validation fails.
			if e = s.state.Update(func(v *state.Snapshot) error {
				v.KnownSessions[sessionKey] = state.KnownSession{Site: site, SessionID: c.SessionID, Status: "unknown", ExpiresAt: time.Unix(c.SessionExpiresAt, 0).UTC()}
				return nil
			}); e != nil {
				return e
			}
		default:
			return core.Error("invalid_input")
		}
		bearer := c.Token
		if c.Kind == "session" {
			bearer = c.AccessToken
		}
		userID := in.UpstreamUserID
		if userID == 0 && identity.ID > 0 {
			userID = identity.ID
		}
		if userID == 0 && in.AccountID != "" {
			if previous, ok := old[in.AccountID]; ok {
				userID = previous.UpstreamUserID
			} else {
				userID = s.state.Read().Accounts[in.AccountID].UpstreamUserID
			}
		}
		verified, e := s.client.Self(ctx, site, bearer, userID)
		if e != nil {
			return e
		}
		if (identity.ID != 0 && identity.ID != verified.ID) || (userID != 0 && userID != verified.ID) {
			return core.Error("identity_mismatch")
		}
		identity = verified
		id := core.AccountID(site, identity.ID)
		if in.AccountID != "" && in.AccountID != id {
			return core.Error("identity_mismatch")
		}
		// A rebind without an explicit mode keeps the previously stored mode so a
		// partial re-submit can never silently switch an account back to api mode.
		// Identity-based rebinds (no account_id) are resolved by the verified ID.
		if mode == "" {
			if previous, ok := old[id]; ok {
				mode = previous.EffectiveCheckinMode()
			} else if record, ok := s.state.Read().Accounts[id]; ok && record.CheckinMode != "" {
				mode = record.CheckinMode
			}
		}
		if mode == "" {
			mode = core.CheckinModeAPI
		}
		if mode == core.CheckinModeLogin && !loginChannelOK(in.Channel) {
			return core.Error("invalid_input")
		}
		if in.OAuthProvider != "" && mode != core.CheckinModeLogin {
			return core.Error("invalid_input")
		}
		loginMode := mode == core.CheckinModeLogin
		if loginMode && !identity.HasRewardEvidence() {
			return core.Error("unsupported_site")
		}
		status := "ready"
		if info.TurnstileCheck == nil {
			status = "unsupported_site"
		} else if *info.TurnstileCheck && !loginMode {
			status = "turnstile_blocked"
		}
		var stats core.CheckinStatus
		statsOK := false
		if !loginMode {
			// Login-mode accounts never touch check-in endpoints; their baseline
			// evidence comes from /api/user/self alone.
			stats, e = s.client.CheckinStatus(ctx, site, bearer, "", identity.ID)
			statsOK = e == nil
			if e != nil {
				if core.ErrorKind(e) == "not_enabled" {
					status = "not_enabled"
				} else {
					return e
				}
			}
		}
		if previous, ok := old[id]; ok {
			c.Generation = previous.Credential.Generation + 1
		} else if record, ok := s.state.Read().Accounts[id]; ok && record.LastGeneration >= c.Generation {
			c.Generation = record.LastGeneration + 1
		}
		if loginMode && c.OAuthCookie == "" && in.OAuthProvider != "" {
			// The console cannot round-trip the stored third-party session, so a
			// rebind that keeps the same provider selected without pasting a
			// fresh cookie keeps the saved session. An empty provider is the
			// explicit manual channel: the saved session is dropped and the
			// account becomes detection-only with a browser login link.
			previous, ok := old[id]
			if !ok || previous.Credential.OAuthCookie == "" || previous.Credential.OAuthProvider != in.OAuthProvider {
				return core.Error("invalid_input")
			}
			c.OAuthProvider = previous.Credential.OAuthProvider
			c.OAuthCookie = previous.Credential.OAuthCookie
		}
		a := core.Account{ID: id, Site: site, UpstreamUserID: identity.ID, Username: identity.Username, Label: strings.TrimSpace(in.Label), CheckinMode: mode, Credential: c}
		if a.Label == "" {
			a.Label = a.Username
		}
		if e = s.repo.Save(ctx, a, revs[id]); e != nil {
			return e
		}
		_, revision, e := s.repo.Load(ctx, id)
		if e != nil {
			return core.Error("persistence_error")
		}
		record := state.Record(a, revision.PhysicalPath, status)
		if info.QuotaPerUnit > 0 {
			record.BalancePerUnit = info.QuotaPerUnit
		}
		if verified.QuotaPresent {
			// The wallet balance observed during validation travels with the
			// record so the console can show it before the first recheck.
			balance := verified.Quota
			record.BalanceQuota = &balance
		}
		if statsOK {
			record.TotalQuota = stats.TotalQuota
			record.TotalCheckins = stats.TotalCheckins
			record.StatsSyncedAt = time.Now().UTC()
		} else if !loginMode {
			record.StatsStale = true
		}
		if loginMode {
			// A rebind keeps the existing evidence: overwriting it here would
			// swallow a reward collected since the last detection and erase the
			// same-day login-attempt marker.
			prior := s.state.Read().Accounts[id]
			if prior.CheckinMode == core.CheckinModeLogin && prior.Reward().BaselineKnown() {
				snap := prior.Reward()
				snap.Baseline = snap.BaselineKnown()
				// A rebind is the explicit way to replace or re-validate the
				// provider session, so it clears an old pause while preserving
				// the same-day reward and attempt evidence.
				if snap.State == core.RewardStateCookieExpired || snap.State == core.RewardStateNeedsAttention || snap.State == core.RewardStateChallengeBlocked {
					snap.State = ""
				}
				record.SetReward(snap)
				// An explicit rebind is the operator asking for a fresh attempt:
				// drop the same-day attempt marker so the corrected credential
				// retries immediately. Reward evidence above (a same-day success
				// or receipt) is kept, so a completed day still reports "already"
				// and never logs in twice.
				record.RewardAttemptDayKey = ""
				record.RewardAttemptAt = 0
			} else {
				// A first bind (or a switch from api mode) starts with a baseline
				// and no day expectation, so binding after today's login never
				// claims a fresh day.
				record.RewardTotal = identity.Quota + identity.UsedQuota
				record.RewardLastLogin = identity.LastLogin
				record.RewardBaseline = true
			}
		}
		if e = s.state.Update(func(v *state.Snapshot) error {
			record.LastResult = v.Accounts[id].LastResult
			if !v.Accounts[id].CreatedAt.IsZero() {
				// A rebind keeps the original registration time for ordering.
				record.CreatedAt = v.Accounts[id].CreatedAt
			}
			v.Accounts[id] = record
			delete(v.Health, id)
			// Cached statistics capture the account label at aggregation time;
			// a rename must not leave the charts showing the previous name.
			for key, summary := range v.Stats {
				for i := range summary.Accounts {
					if summary.Accounts[i].ID == id {
						summary.Accounts[i].Label = record.Label
					}
				}
				v.Stats[key] = summary
			}
			delete(v.DeletePending, id)
			delete(v.RefreshPending, id)
			v.CancelWork(id)
			delete(v.Schedules, id)
			if sessionKey != "" {
				known := v.KnownSessions[sessionKey]
				known.Status = "bound"
				known.AccountID = id
				v.KnownSessions[sessionKey] = known
			}
			// A rebind starts with a fresh GET immediately, independent of the old retry chain.
			if _, wasBound := old[id]; (wasBound || in.AccountID != "") && status == "ready" {
				workID := "rebind-" + id + "-" + time.Now().UTC().Format("20060102T150405.000000000")
				v.WorkItems[workID] = state.WorkItem{ID: workID, AccountID: id, CreatedAt: time.Now().UTC(), Status: "queued", NextAttemptAt: time.Now().UTC(), Generation: c.Generation}
				v.Jobs[workID] = state.Job{ID: workID, Trigger: "rebind", Status: "queued", CreatedAt: time.Now().UTC(), WorkIDs: []string{workID}}
			}
			return nil
		}); e != nil {
			return e
		}
		s.broker.Forget(id)
		out = record.Summary()
		return nil
	})
	return out, err
}

// loginChannelOK reports which credential channels can drive login-reward
// mode: a site access token, or a dedicated refresh session that the broker
// exchanges for access tokens (for sites without a PAT entry point).
func loginChannelOK(channel string) bool {
	return channel == "token" || channel == "refresh_cookie" || channel == "oauth"
}

// providerPause reports a stored third-party login block. The account status is
// the primary signal; RewardState can retain the reason after a crash between
// the failed login chain and the attempt result being saved.
func providerPause(rec state.AccountRecord) string {
	for _, v := range []string{rec.Status, rec.RewardState} {
		if v == core.RewardStateCookieExpired || v == core.RewardStateNeedsAttention || v == core.RewardStateChallengeBlocked {
			return v
		}
	}
	return ""
}

// knownRewardToday reports whether the stored snapshot already carries an
// outcome for the current schedule-local day. It is used when the evidence
// endpoint is walled: a confirmed day must not be downgraded to a failure,
// while a day with no outcome still needs a real query.
func (s *Service) knownRewardToday(id string) bool {
	rec, ok := s.state.Read().Accounts[id]
	if !ok || (rec.RewardState != core.RewardStateSuccess && rec.RewardState != core.RewardStateUnknown) {
		return false
	}
	loc := time.Local
	if s.Location != nil {
		if l := s.Location(); l != nil {
			loc = l
		}
	}
	return rec.RewardDayKey == time.Now().In(loc).Format("2006-01-02")
}

func (s *Service) Recheck(ctx context.Context, id string) (out core.AccountSummary, err error) {
	err = s.broker.WithAccount(ctx, id, func(use credential.UseCredential) error {
		if s.state.Read().DeletePending[id] {
			return core.Error("delete_pending")
		}
		paused := providerPause(s.state.Read().Accounts[id])
		// Explicit recheck permits a newly validated external revision; ambiguous refresh never clears here.
		if err = func() error {
			if s.state.Read().Accounts[id].Status != "external_change" {
				return nil
			}
			a, _, e := s.repo.Load(ctx, id)
			if e != nil {
				return e
			}
			if a.Disabled {
				return core.Error("disabled")
			}
			return s.state.Update(func(v *state.Snapshot) error { r := v.Accounts[id]; r.Status = "ready"; v.Accounts[id] = r; return nil })
		}(); err != nil {
			return err
		}
		err = use(func(a core.Account, bearer string) error {
			info, e := s.client.Probe(ctx, a.Site)
			if e != nil {
				return e
			}
			if info.TurnstileCheck == nil {
				return core.Error("unsupported_site")
			}
			identity, e := s.client.Self(ctx, a.Site, bearer, a.UpstreamUserID)
			if e != nil {
				// A captcha wall in front of /api/user/self hides the evidence
				// endpoint, but a reward already observed for today stays
				// valid. Keep that day's state and record the wall as the
				// latest result instead of replacing a healthy account with a
				// failure status.
				if core.ErrorKind(e) == core.ErrorWafBlocked && a.EffectiveCheckinMode() == core.CheckinModeLogin && s.knownRewardToday(id) {
					return s.state.Update(func(v *state.Snapshot) error {
						r := v.Accounts[id]
						if paused != "" {
							r.Status = paused
						} else {
							r.Status = "ready"
						}
						v.Accounts[id] = r
						v.RecordResult(id, core.AttemptResult{Status: core.ErrorWafBlocked, ObservedAt: time.Now().UTC()})
						out = v.Accounts[id].Summary()
						return nil
					})
				}
				return e
			}
			if identity.ID != a.UpstreamUserID {
				return core.Error("identity_mismatch")
			}
			if a.EffectiveCheckinMode() == core.CheckinModeLogin {
				// Login-mode recheck refreshes reward evidence only; it never
				// contacts check-in endpoints and never triggers a login.
				if !identity.HasRewardEvidence() {
					return core.Error("unsupported_site")
				}
				loc := time.Local
				if s.Location != nil {
					if l := s.Location(); l != nil {
						loc = l
					}
				}
				now := time.Now().In(loc)
				dayKey := now.Format("2006-01-02")
				dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Unix()
				loginToday := identity.LastLoginPresent && identity.LastLogin >= dayStart && identity.LastLogin > 0
				return s.state.Update(func(v *state.Snapshot) error {
					r := v.Accounts[id]
					ev := core.EvaluateReward(r.Reward(), core.RewardEvidence{
						Total:        identity.Quota + identity.UsedQuota,
						TotalSet:     true,
						LastLogin:    identity.LastLogin,
						LastLoginSet: true,
						LoginToday:   loginToday,
					}, dayKey)
					r.SetReward(ev.Snapshot)
					if identity.QuotaPresent {
						balance := identity.Quota
						r.BalanceQuota = &balance
					}
					if info.QuotaPerUnit > 0 {
						r.BalancePerUnit = info.QuotaPerUnit
					}
					if a.Credential.OAuthProvider != "" {
						r.OAuthProvider = a.Credential.OAuthProvider
					}
					r.LastGeneration = a.Credential.Generation
					// A token-only query cannot prove an unchanged provider cookie
					// works. Keep the pause in the status until the credential is
					// rebound; the reward state keeps the evidence outcome (for
					// example a same-day success) so the two never overwrite
					// each other.
					if paused != "" {
						r.Status = paused
					} else {
						r.Status = "ready"
					}
					v.Accounts[id] = r
					// Manual detection is an execution result: persist it so a
					// later "already" check cannot erase the only success amount.
					v.RecordResult(id, core.AttemptResult{Status: ev.Status, QuotaAwarded: ev.QuotaAwarded, ObservedAt: time.Now().UTC()})
					out = v.Accounts[id].Summary()
					return nil
				})
			}
			status := "ready"
			if *info.TurnstileCheck {
				status = "turnstile_blocked"
			}
			stats, e := s.client.CheckinStatus(ctx, a.Site, bearer, "", a.UpstreamUserID)
			statsOK := e == nil
			if e != nil {
				if core.ErrorKind(e) != "not_enabled" {
					return e
				}
				status = "not_enabled"
			}
			return s.state.Update(func(v *state.Snapshot) error {
				r := v.Accounts[id]
				if r.Status == "permission_denied" {
					r.PermissionDenied = true
				}
				if r.PermissionDenied && status == "ready" {
					status = "permission_denied"
				}
				r.Status = status
				if identity.QuotaPresent {
					balance := identity.Quota
					r.BalanceQuota = &balance
				}
				if info.QuotaPerUnit > 0 {
					r.BalancePerUnit = info.QuotaPerUnit
				}
				if statsOK {
					r.TotalQuota = stats.TotalQuota
					r.TotalCheckins = stats.TotalCheckins
					r.StatsSyncedAt = time.Now().UTC()
				}
				r.StatsStale = !statsOK
				r.LastGeneration = a.Credential.Generation
				v.Accounts[id] = r
				out = r.Summary()
				return nil
			})
		})
		if err != nil {
			if e := s.state.Update(func(v *state.Snapshot) error {
				r, ok := v.Accounts[id]
				if ok {
					if r.Status == "permission_denied" || core.ErrorKind(err) == "permission_denied" {
						r.PermissionDenied = true
					}
					// A transient recheck failure must not erase an unverified
					// provider-session pause; otherwise a later successful token
					// query would resume automatic login with the same cookie.
					if paused == "" {
						r.Status = core.ErrorKind(err)
					}
					v.Accounts[id] = r
				}
				return nil
			}); e != nil {
				err = e
			}
		}
		return err
	})
	return out, err
}

// ProbeLoginProvider reports whether the automatic third-party login channel
// can leave this server at all: "ok", "blocked" (a human-verification wall
// such as Cloudflare that no server-side credential can pass), or
// "unreachable". It carries no cookie and completes no login.
func (s *Service) ProbeLoginProvider(ctx context.Context, provider string) (string, error) {
	return s.client.ProbeProvider(ctx, provider)
}

// Stats aggregates every bound account's site consumption logs for one window,
// converts each account's quota with its own per_unit, merges models and time
// buckets, and caches the merged payload under rangeKey. Accounts whose logs
// cannot be read are reported in Failed and skipped; the rest still aggregate.
func (s *Service) Stats(ctx context.Context, rangeKey string, start, end, bucketSeconds, bucketOffset int64, maxPages int) (core.StatsSummary, error) {
	snap := s.state.Read()
	ids := make([]string, 0, len(snap.Accounts))
	for id, a := range snap.Accounts {
		if a.Status == "deleted" || snap.DeletePending[id] {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := core.StatsSummary{Range: rangeKey, Start: start, End: end, SyncedAt: time.Now().UTC(), Failed: map[string]string{}}
	modelAgg := map[string]*core.StatsModel{}
	bucketAgg := map[int64]map[string]int64{}
	for _, id := range ids {
		rec := snap.Accounts[id]
		var site core.SiteUsageStats
		truncated := false
		e := s.broker.WithAccount(ctx, id, func(use credential.UseCredential) error {
			return use(func(a core.Account, bearer string) error {
				st, trunc, e := s.client.UsageStats(ctx, a.Site, bearer, a.UpstreamUserID, start, end, bucketSeconds, bucketOffset, maxPages)
				if e != nil {
					return e
				}
				site, truncated = st, trunc
				return nil
			})
		})
		if e != nil {
			out.Failed[id] = core.ErrorKind(e)
			continue
		}
		if truncated {
			out.Truncated = true
		}
		label := rec.Label
		if label == "" {
			label = rec.Username
		}
		acc := core.StatsAccount{ID: id, Label: label, Site: rec.Site, Calls: site.Totals.Calls,
			PromptTokens: site.Totals.PromptTokens, CompletionTokens: site.Totals.CompletionTokens,
			TotalTokens: site.Totals.TotalTokens, CacheTokens: site.Totals.CacheTokens,
			Quota: site.Totals.Quota, PerUnit: rec.BalancePerUnit}
		if rec.BalancePerUnit > 0 {
			acc.CostUSD = float64(acc.Quota) / float64(rec.BalancePerUnit)
		} else {
			// Without the site ratio the consumption cannot be priced; keep it
			// out of the USD total and tell the console the cost is partial.
			out.PartialCost = true
		}
		out.Accounts = append(out.Accounts, acc)
		out.Totals.Calls += acc.Calls
		out.Totals.PromptTokens += acc.PromptTokens
		out.Totals.CompletionTokens += acc.CompletionTokens
		out.Totals.TotalTokens += acc.TotalTokens
		out.Totals.CacheTokens += acc.CacheTokens
		out.Totals.Quota += acc.Quota
		out.Totals.CostUSD += acc.CostUSD
		for _, m := range site.Models {
			g := modelAgg[m.Model]
			if g == nil {
				g = &core.StatsModel{Model: m.Model}
				modelAgg[m.Model] = g
			}
			g.Calls += m.Calls
			g.PromptTokens += m.PromptTokens
			g.CompletionTokens += m.CompletionTokens
			g.TotalTokens += m.TotalTokens
			g.CacheTokens += m.CacheTokens
			g.Quota += m.Quota
			if rec.BalancePerUnit > 0 {
				g.CostUSD += float64(m.Quota) / float64(rec.BalancePerUnit)
			}
		}
		for _, b := range site.Series {
			bm := bucketAgg[b.Start]
			if bm == nil {
				bm = map[string]int64{}
				bucketAgg[b.Start] = bm
			}
			for _, v := range b.Tokens {
				bm[v.Model] += v.Tokens
			}
		}
	}
	out.Models = make([]core.StatsModel, 0, len(modelAgg))
	for _, g := range modelAgg {
		out.Models = append(out.Models, *g)
	}
	sort.Slice(out.Models, func(i, j int) bool {
		a, b := out.Models[i], out.Models[j]
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		return a.Model < b.Model
	})
	sort.Slice(out.Accounts, func(i, j int) bool {
		a, b := out.Accounts[i], out.Accounts[j]
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		return a.Label < b.Label
	})
	out.Series = make([]core.StatsBucket, 0, len(bucketAgg))
	for startAt, bm := range bucketAgg {
		tokens := make([]core.StatsModelTokens, 0, len(bm))
		for model, n := range bm {
			if n > 0 {
				tokens = append(tokens, core.StatsModelTokens{Model: model, Tokens: n})
			}
		}
		sort.Slice(tokens, func(i, j int) bool {
			if tokens[i].Tokens != tokens[j].Tokens {
				return tokens[i].Tokens > tokens[j].Tokens
			}
			return tokens[i].Model < tokens[j].Model
		})
		out.Series = append(out.Series, core.StatsBucket{Start: startAt, Tokens: tokens})
	}
	sort.Slice(out.Series, func(i, j int) bool { return out.Series[i].Start < out.Series[j].Start })
	if e := s.state.Update(func(v *state.Snapshot) error {
		if v.Stats == nil {
			v.Stats = map[string]core.StatsSummary{}
		}
		v.Stats[rangeKey] = out
		return nil
	}); e != nil {
		return core.StatsSummary{}, e
	}
	return out, nil
}

// RefreshBalance updates only the wallet balance evidence for one account. It
// is a read-only query: it never touches check-in state, reward evidence,
// history, or a stored provider pause, so it is safe to click at any time.
func (s *Service) RefreshBalance(ctx context.Context, id string) (out core.AccountSummary, err error) {
	err = s.broker.WithAccount(ctx, id, func(use credential.UseCredential) error {
		if s.state.Read().DeletePending[id] {
			return core.Error("delete_pending")
		}
		return use(func(a core.Account, bearer string) error {
			info, e := s.client.Probe(ctx, a.Site)
			if e != nil {
				return e
			}
			identity, e := s.client.Self(ctx, a.Site, bearer, a.UpstreamUserID)
			if e != nil {
				return e
			}
			if identity.ID != a.UpstreamUserID {
				return core.Error("identity_mismatch")
			}
			return s.state.Update(func(v *state.Snapshot) error {
				r := v.Accounts[id]
				if identity.QuotaPresent {
					balance := identity.Quota
					r.BalanceQuota = &balance
				}
				if info.QuotaPerUnit > 0 {
					r.BalancePerUnit = info.QuotaPerUnit
				}
				if a.Credential.OAuthProvider != "" {
					r.OAuthProvider = a.Credential.OAuthProvider
				}
				v.Accounts[id] = r
				out = v.Accounts[id].Summary()
				return nil
			})
		})
	})
	return out, err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.gate.Do(ctx, func() error {
		if _, ok := s.state.Read().Accounts[id]; !ok {
			return core.Error("invalid_input")
		}
		if e := s.state.Update(func(v *state.Snapshot) error {
			v.DeletePending[id] = true
			delete(v.Health, id)
			delete(v.RefreshPending, id)
			v.CancelWork(id)
			delete(v.Schedules, id)
			a := v.Accounts[id]
			a.Status = "delete_pending"
			v.Accounts[id] = a
			return nil
		}); e != nil {
			return e
		}
		s.broker.Forget(id)
		deleteErr := s.repo.Delete(ctx, id)
		if e := s.state.Update(func(v *state.Snapshot) error {
			a := v.Accounts[id]
			a.Status = "deleted"
			if deleteErr != nil {
				a.Status = "delete_failed"
			}
			v.Accounts[id] = a
			return nil
		}); e != nil {
			return e
		}
		return deleteErr
	})
}

// Reconcile discovers credentials without erasing histories during an empty host enumeration.
func (s *Service) Reconcile(ctx context.Context) error {
	return s.gate.Do(ctx, func() error {
		snapshot := s.state.Read()
		for id, a := range snapshot.Accounts {
			s.repo.Track(id, a.PhysicalPath)
		}
		accounts, e := s.repo.List(ctx)
		if e != nil {
			return e
		}
		return s.state.Update(func(v *state.Snapshot) error {
			for id, record := range v.Accounts {
				if v.DeletePending[id] {
					continue
				}
				a, _, err := s.repo.Load(ctx, id)
				if core.ErrorKind(err) == "host_unavailable" {
					return err
				}
				if err != nil {
					record.Status = "credential_unreadable"
					v.Accounts[id] = record
					continue
				}
				if a.Disabled {
					record.Status = "disabled"
					v.Accounts[id] = record
					continue
				}
				if a.Credential.Generation != record.LastGeneration {
					pending, refreshing := v.RefreshPending[id]
					// Recovery owns a generation already saved by our pending refresh.
					if !refreshing || a.Credential.Generation <= pending.OldGeneration {
						record.Status = "external_change"
						v.Accounts[id] = record
					}
				}
			}
			for _, a := range accounts {
				if v.DeletePending[a.ID] {
					continue
				}
				if _, ok := v.Accounts[a.ID]; ok {
					continue
				}
				_, rev, e := s.repo.Load(ctx, a.ID)
				if e != nil {
					continue
				}
				status := "ready"
				if a.Disabled {
					status = "disabled"
				}
				v.Accounts[a.ID] = state.Record(a, rev.PhysicalPath, status)
			}
			return nil
		})
	})
}

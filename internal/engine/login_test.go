package engine

import (
	"context"
	"encoding/json"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type loginFixture struct {
	runner *Runner
	state  *state.Store
	repo   *credential.Store
	clock  *fixedClock
	id     string
	site   *httptest.Server
	authz  *httptest.Server

	mu              sync.Mutex
	selfHits        int
	callbackHits    int
	authorizeHits   int
	quota, used     int64
	lastLogin       int64
	allowanceOnly   bool
	allowanceDate   string
	authorizeStatus int
	grantOnCallback bool
}

func (f *loginFixture) snapshot() (quota, used, lastLogin int64, selfHits, authorizeHits int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.quota, f.used, f.lastLogin, f.selfHits, f.authorizeHits
}

func newLoginFixture(t *testing.T, cookie string) *loginFixture {
	t.Helper()
	f := &loginFixture{clock: &fixedClock{now: time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)}, authorizeStatus: http.StatusFound}
	f.site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"github_oauth":true,"github_client_id":"cid1","quota_per_unit":500000}}`))
		case "/api/user/self":
			f.mu.Lock()
			f.selfHits++
			quota, used, lastLogin := f.quota, f.used, f.lastLogin
			allowanceOnly, allowanceDate := f.allowanceOnly, f.allowanceDate
			f.mu.Unlock()
			data := map[string]any{"id": 7, "username": "u", "quota": quota, "used_quota": used}
			if allowanceOnly {
				data["setting"] = `{"allowance_date":"` + allowanceDate + `"}`
			} else {
				data["last_login_time"] = lastLogin
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
		case "/api/oauth/state":
			_, _ = w.Write([]byte(`{"success":true,"data":"st-1"}`))
		case "/api/oauth/github":
			f.mu.Lock()
			f.callbackHits++
			if f.grantOnCallback {
				f.quota += 12500000
				f.lastLogin = f.clock.Now().Unix()
			}
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"username":"u","checked_in":true,"access_token":"access-minted","access_expires_at":1994802799,"session":{"sid":"sid-mint","expires_at":1997393899}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.site.Close)
	f.authz = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.authorizeHits++
		status := f.authorizeStatus
		f.mu.Unlock()
		if status != http.StatusFound {
			_, _ = w.Write([]byte(`<html><form><input type="password" name="password"></form></html>`))
			return
		}
		http.Redirect(w, r, f.site.URL+"/oauth/github?code=C&state=st-1", http.StatusFound)
	}))
	t.Cleanup(f.authz.Close)
	h := testutil.NewHost(t)
	f.repo = credential.NewStore(h)
	f.state, _ = state.Open(testutil.Dir(t))
	c := core.Credential{Kind: "pat", Channel: "token", Token: "nap_test-secret", Generation: 1}
	if cookie != "" {
		c.OAuthProvider = core.OAuthProviderGitHub
		c.OAuthCookie = cookie
	}
	a := core.Account{ID: core.AccountID(f.site.URL, 7), Site: f.site.URL, UpstreamUserID: 7, Username: "u", CheckinMode: core.CheckinModeLogin, Credential: c}
	if e := f.repo.Save(context.Background(), a, credential.Revision{}); e != nil {
		t.Fatal(e)
	}
	f.id = a.ID
	_ = f.state.Update(func(s *state.Snapshot) error {
		r := state.Record(a, "", "ready")
		r.RewardTotal = 62500000
		r.RewardLastLogin = time.Date(2026, 11, 14, 19, 0, 0, 0, time.Local).Unix()
		s.Accounts[a.ID] = r
		return nil
	})
	client := newapi.NewClient(f.site.Client())
	client.SetLoginEndpoints(func(string) newapi.LoginEndpoints {
		return newapi.LoginEndpoints{AuthorizeURL: f.authz.URL + "/login/oauth/authorize", CookieDomain: hostOf(f.authz.URL)}
	})
	gate := &core.Gate{}
	broker := credential.NewBroker(f.repo, f.state, client, gate, time.Now)
	f.runner = NewRunner(broker, client, f.state, f.clock)
	f.setTotals(62500000, 0)
	return f
}

func hostOf(raw string) string {
	u, _ := url.Parse(raw)
	return u.Hostname()
}

func (f *loginFixture) setTotals(quota, used int64) {
	f.mu.Lock()
	f.quota, f.used = quota, used
	f.mu.Unlock()
}

func (f *loginFixture) record() state.AccountRecord {
	return f.state.Read().Accounts[f.id]
}

// TestLoginDetectsDailyAllowanceWithoutLastLogin covers fork sites that never
// expose last_login_time: the daily allowance date in setting is the same-day
// reward signal, so an observed rise is attributed without replaying a login.
func TestLoginDetectsDailyAllowanceWithoutLastLogin(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=abc")
	f.mu.Lock()
	f.allowanceOnly = true
	f.allowanceDate = "2026-11-15"
	f.mu.Unlock()
	f.setTotals(75000000, 0)
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != "success" || r.QuotaAwarded != 12500000 {
		t.Fatal(r, e)
	}
	if _, _, _, _, authorizeHits := f.snapshot(); authorizeHits != 0 {
		t.Fatalf("a completed allowance day must not trigger a login, authorize=%d", authorizeHits)
	}
}

func TestLoginPendingWithoutCookie(t *testing.T) {
	f := newLoginFixture(t, "")
	f.mu.Lock()
	f.lastLogin = time.Date(2026, 11, 14, 0, 0, 0, 0, time.Local).Unix()
	f.mu.Unlock()
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != core.RewardStatePendingLogin {
		t.Fatal(r, e)
	}
	rec := f.record()
	if rec.RewardState != core.RewardStatePendingLogin || rec.RewardDayKey != "2026-11-15" || rec.Status != "ready" {
		t.Fatal(rec)
	}
	if rec.BalanceQuota == nil || *rec.BalanceQuota != 62500000 || rec.BalancePerUnit != 500000 {
		t.Fatal("wallet balance or conversion ratio was not persisted", rec.BalanceQuota, rec.BalancePerUnit)
	}
	_, _, _, self, authz := f.snapshot()
	if self != 1 || authz != 0 {
		t.Fatal("unexpected requests", self, authz)
	}
}

func TestLoginRewardArrivalAndSameDayDedup(t *testing.T) {
	f := newLoginFixture(t, "")
	f.setTotals(43750000, 20000000) // total 63,750,000 = baseline + 1,250,000
	f.mu.Lock()
	f.lastLogin = time.Date(2026, 11, 15, 8, 30, 0, 0, time.Local).Unix()
	f.mu.Unlock()
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != "success" || r.QuotaAwarded != 1250000 {
		t.Fatal(r, e)
	}
	rec := f.record()
	if rec.RewardState != core.RewardStateSuccess || rec.RewardTotal != 63750000 || rec.RewardDayKey != "2026-11-15" {
		t.Fatal(rec)
	}
	r, e = f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != "already" {
		t.Fatal(r, e)
	}
}

func TestLoginRewardUnknownWhenOnlyLoginTimeMoved(t *testing.T) {
	f := newLoginFixture(t, "")
	f.mu.Lock()
	f.lastLogin = time.Date(2026, 11, 15, 8, 0, 0, 0, time.Local).Unix()
	f.mu.Unlock()
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != core.RewardStateUnknown {
		t.Fatal(r, e)
	}
	if rec := f.record(); rec.RewardState != core.RewardStateUnknown {
		t.Fatal(rec)
	}
}

func TestLoginDayRolloverResetsExpectation(t *testing.T) {
	f := newLoginFixture(t, "")
	_ = f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[f.id]
		r.RewardDayKey = "2026-11-14"
		r.RewardState = core.RewardStateSuccess
		s.Accounts[f.id] = r
		return nil
	})
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != core.RewardStatePendingLogin {
		t.Fatal(r, e)
	}
	if rec := f.record(); rec.RewardDayKey != "2026-11-15" || rec.RewardState != core.RewardStatePendingLogin {
		t.Fatal(rec)
	}
}

func TestLoginChainRunsWhenCookiePresent(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=abc")
	f.grantOnCallback = true
	f.mu.Lock()
	f.lastLogin = time.Date(2026, 11, 14, 19, 0, 0, 0, time.Local).Unix()
	f.mu.Unlock()
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != "success" || r.QuotaAwarded != 12500000 {
		t.Fatal(r, e)
	}
	_, _, _, self, authz := f.snapshot()
	if authz != 1 || self != 2 || f.callbackHits != 1 {
		t.Fatal("chain did not run", authz, self, f.callbackHits)
	}
}

func TestLoginManualLoginTodaySkipsChain(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=abc")
	f.grantOnCallback = true
	f.mu.Lock()
	f.lastLogin = f.clock.Now().Unix()
	f.mu.Unlock()
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != core.RewardStateUnknown {
		t.Fatal(r, e)
	}
	_, _, _, self, authz := f.snapshot()
	if authz != 0 || self != 1 || f.callbackHits != 0 {
		t.Fatal("a manual login today must suppress the automatic chain", authz, self, f.callbackHits)
	}
}

func TestLoginChainCookieExpiredPausesAccount(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=stale")
	f.mu.Lock()
	f.authorizeStatus = http.StatusOK
	f.mu.Unlock()
	r, e := f.runner.Attempt(context.Background(), f.id)
	if core.ErrorKind(e) != core.RewardStateCookieExpired || r.Status != core.RewardStateCookieExpired {
		t.Fatal(r, e)
	}
	// The pause lives durably in the account status; the reward state keeps the
	// evidence outcome so a later detection can still confirm a same-day reward.
	if rec := f.record(); rec.Status != core.RewardStateCookieExpired || rec.RewardState != core.RewardStatePendingLogin {
		t.Fatal(rec)
	}
	_, _, _, self, authz := f.snapshot()
	if self != 1 || authz != 1 || f.callbackHits != 0 {
		t.Fatal("unexpected requests after cookie expiry", self, authz, f.callbackHits)
	}
	if Retryable(e) {
		t.Fatal("cookie expiry must not retry")
	}
}

func TestExpiredBootstrappedTokenStillLogsIn(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=tolerated")
	f.grantOnCallback = true
	saved, rev, err := f.repo.Load(context.Background(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	saved.Credential.Kind = "session"
	saved.Credential.Channel = "oauth"
	saved.Credential.Token = ""
	saved.Credential.AccessToken = "expired-old"
	saved.Credential.AccessExpiresAt = time.Now().Add(-time.Hour).Unix()
	saved.Credential.RefreshToken = ""
	saved.Credential.SessionID = "sid-old"
	saved.Credential.SessionExpiresAt = time.Now().Add(30 * 24 * time.Hour).Unix()
	if err = f.repo.Save(context.Background(), saved, rev); err != nil {
		t.Fatal(err)
	}
	original := f.site.Config.Handler
	f.site.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/self" && r.Header.Get("Authorization") == "Bearer expired-old" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"code":"AUTH_TOKEN_EXPIRED"}`))
			return
		}
		original.ServeHTTP(w, r)
	})
	r, err := f.runner.Attempt(context.Background(), f.id)
	if err != nil || r.Status != "success" || f.callbackHits != 1 {
		t.Fatalf("expired bootstrapped token blocked the login: status=%s callbacks=%d err=%v", r.Status, f.callbackHits, err)
	}
	after, _, err := f.repo.Load(context.Background(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Credential.AccessToken != "access-minted" || after.Credential.SessionID != "sid-mint" || after.Credential.SessionExpiresAt != 1997393899 {
		t.Fatalf("minted session not rotated into the credential: %+v", after.Credential)
	}
}

func TestPauseClearsSameDayMarkerForManualRetry(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=synthetic-mismatch")
	f.grantOnCallback = true
	original := f.site.Config.Handler
	f.site.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/oauth/github" {
			f.mu.Lock()
			f.callbackHits++
			f.mu.Unlock()
			// The callback logged into a different site account.
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":999,"username":"other","checked_in":true}}`))
			return
		}
		original.ServeHTTP(w, r)
	})
	first, err := f.runner.Attempt(context.Background(), f.id)
	if core.ErrorKind(err) != core.RewardStateNeedsAttention || first.Status != core.RewardStateNeedsAttention {
		t.Fatal(first, err)
	}
	crashed := f.record()
	if crashed.Status != core.RewardStateNeedsAttention || crashed.RewardAttemptDayKey != "" || crashed.RewardAttemptAt != 0 {
		t.Fatalf("a deterministic pause must clear the same-day marker: %+v", crashed)
	}
	// A rebind clears the pause; the same-day retry must run the chain again.
	if err := f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[f.id]
		r.Status = "ready"
		s.Accounts[f.id] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.site.Config.Handler = original
	second, err := f.runner.Attempt(context.Background(), f.id)
	if err != nil || second.Status != "success" || f.callbackHits != 2 {
		t.Fatalf("manual retry did not rerun the chain: status=%s callbacks=%d err=%v", second.Status, f.callbackHits, err)
	}
}

func TestLoginReceiptSurvivesPostQueryFailure(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=receipt")
	f.mu.Lock()
	f.lastLogin = f.clock.Now().Add(-24 * time.Hour).Unix()
	f.mu.Unlock()
	original := f.site.Config.Handler
	var callbackDone, failedPostQuery atomic.Bool
	f.site.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/oauth/github" {
			f.mu.Lock()
			f.quota += 12500000
			// The site has not refreshed last_login_time yet; checked_in is the
			// only signal until it does.
			f.mu.Unlock()
			callbackDone.Store(true)
		}
		if r.URL.Path == "/api/user/self" && callbackDone.Load() && failedPostQuery.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"success":false,"message":"temporary failure"}`))
			return
		}
		original.ServeHTTP(w, r)
	})
	first, e := f.runner.Attempt(context.Background(), f.id)
	if core.ErrorKind(e) != "upstream_error" {
		t.Fatalf("setup did not lose the post-login query: %s %v", first.Status, e)
	}
	if rec := f.record(); !rec.RewardReceipt {
		t.Fatal("the successful callback receipt was not persisted before the query")
	}
	second, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || second.Status != "success" || second.QuotaAwarded != 12500000 {
		t.Fatalf("receipt recovery failed: status=%s awarded=%d callbacks=%d error=%v", second.Status, second.QuotaAwarded, f.callbackHits, e)
	}
	if f.callbackHits != 1 {
		t.Fatal("receipt recovery must not replay OAuth", f.callbackHits)
	}
}

func TestLoginCooldownDefersInsteadOfCompletingDay(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=cooldown")
	f.grantOnCallback = true
	previous := time.Date(2026, 11, 14, 22, 0, 0, 0, time.Local)
	f.mu.Lock()
	f.lastLogin = previous.Unix()
	f.mu.Unlock()
	if e := f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[f.id]
		r.RewardDayKey = "2026-11-14"
		r.RewardState = core.RewardStateSuccess
		r.RewardAttemptDayKey = "2026-11-14"
		r.RewardAttemptAt = previous.Unix()
		s.Accounts[f.id] = r
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	jobs := NewJobs(f.runner, f.state, f.clock)
	scheduler := NewScheduler(f.repo, f.state, jobs, f.clock, func(int) int { return 0 })
	settings := config.Defaults()
	settings.CheckinAt, settings.JitterMinutes = "09:00", 0
	if e := scheduler.Tick(context.Background(), settings, false); e != nil {
		t.Fatal(e)
	}
	if e := jobs.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	if f.callbackHits != 0 {
		t.Fatal("cooldown should prohibit the morning callback")
	}
	morning := f.state.Read()
	entry := morning.Schedules[f.id]
	if w := morning.WorkItems[entry.WorkID]; w.Status != "retry" && w.Status != "queued" {
		t.Fatalf("a cooldown deferral consumed the daily task: work=%s result=%s", w.Status, w.Result.Status)
	}
	f.clock.now = time.Date(2026, 11, 15, 18, 1, 0, 0, time.Local)
	if e := scheduler.Tick(context.Background(), settings, false); e != nil {
		t.Fatal(e)
	}
	if e := jobs.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	if f.callbackHits != 1 || f.record().RewardState != core.RewardStateSuccess {
		t.Fatalf("the deferred login did not run after the interval: callbacks=%d reward=%s", f.callbackHits, f.record().RewardState)
	}
}

// wallSelf makes /api/user/self answer with an Aliyun WAF captcha page while
// every other endpoint keeps working, mirroring agentrouter.org.
func (f *loginFixture) wallSelf() {
	original := f.site.Config.Handler
	f.site.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/self" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><meta name="aliyun_waf_aa" content="x">`))
			return
		}
		original.ServeHTTP(w, r)
	})
}

func TestLoginEvidenceWalledStillLogsIn(t *testing.T) {
	f := newLoginFixture(t, "user_sessions=waf")
	f.grantOnCallback = true
	f.wallSelf()
	f.mu.Lock()
	f.lastLogin = time.Date(2026, 11, 14, 19, 0, 0, 0, time.Local).Unix()
	f.mu.Unlock()
	// The previous release left an unsupported_site status behind; a completed
	// login proves the account works and must clear it.
	_ = f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[f.id]
		r.Status = "unsupported_site"
		s.Accounts[f.id] = r
		return nil
	})
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != core.RewardStateUnknown {
		t.Fatal(r, e)
	}
	rec := f.record()
	if rec.Status != "ready" || rec.RewardState != core.RewardStateUnknown || rec.RewardDayKey != "2026-11-15" {
		t.Fatal(rec)
	}
	if rec.RewardTotal != 62500000 {
		t.Fatalf("the walled evidence endpoint clobbered the stored baseline: %d", rec.RewardTotal)
	}
	if f.callbackHits != 1 {
		t.Fatalf("the login chain did not run behind the wall: %d", f.callbackHits)
	}
}

func TestLoginEvidenceWalledWithoutLoginReportsWall(t *testing.T) {
	f := newLoginFixture(t, "")
	f.wallSelf()
	r, e := f.runner.Attempt(context.Background(), f.id)
	if core.ErrorKind(e) != core.ErrorWafBlocked || r.Status != core.ErrorWafBlocked {
		t.Fatal(r, e)
	}
	if rec := f.record(); rec.Status != core.ErrorWafBlocked {
		t.Fatal(rec)
	}
	_, _, _, _, authz := f.snapshot()
	if authz != 0 || f.callbackHits != 0 {
		t.Fatal("no login was possible, yet the chain ran", authz, f.callbackHits)
	}
}

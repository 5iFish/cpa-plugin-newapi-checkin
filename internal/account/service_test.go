package account

import (
	"context"
	"encoding/json"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/engine"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeleteIsIdempotent(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.Delete(context.Background(), a.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.s.Delete(context.Background(), a.ID); e != nil {
		t.Fatal("retry deletion should confirm absence", e)
	}
}
func TestCorruptCredentialVisibleOnRecovery(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(f.host.Dir, credential.Filename(a.ID))
	_ = os.WriteFile(p, []byte("broken"), 0600)
	if e = f.s.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
	if f.state.Read().Accounts[a.ID].Status != "credential_unreadable" {
		t.Fatal("corrupt credential still advertised ready")
	}
}

func TestStartupUnavailableHostDoesNotPauseAccounts(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	f.host.Unavailable = true
	e = f.s.Reconcile(context.Background())
	if core.ErrorKind(e) != "host_unavailable" {
		t.Fatalf("host availability conflated with broken credential: %v", e)
	}
	if f.state.Read().Accounts[a.ID].Status != "ready" {
		t.Fatal("startup erased account readiness")
	}
}
func TestBlockedSiteDoesNotClaimSyncedStats(t *testing.T) {
	f := fixture(t)
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"u"}}`))
		default:
			_, _ = w.Write([]byte(`{"success":false,"message":"Check-in feature is not enabled"}`))
		}
	})
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	if !a.StatsSyncedAt.IsZero() || !a.StatsStale {
		t.Fatal("failed stats fetch was presented as synchronized")
	}
}

type accountFixture struct {
	s                                   *Service
	client                              *newapi.Client
	repo                                *credential.Store
	state                               *state.Store
	host                                *testutil.Host
	server                              *httptest.Server
	refresh, posts, checkinHits         int
	turnstile, denyProfile, loseRefresh bool
	user                                int
	quota, used, lastLogin              int64
	logItems                            []any
}

func fixture(t *testing.T) *accountFixture {
	f := &accountFixture{user: 1}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/api/status":
			data = map[string]any{"turnstile_check": f.turnstile, "quota_per_unit": 500000, "github_oauth": true, "github_client_id": "cid1"}
		case "/api/user/self":
			if f.denyProfile {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED"}`))
				return
			}
			data = map[string]any{"id": f.user, "username": "user", "quota": f.quota, "used_quota": f.used, "last_login_time": f.lastLogin}
		case "/api/user/checkin":
			f.checkinHits++
			if r.Method == "POST" {
				f.posts++
			}
			data = map[string]any{"enabled": true, "stats": map[string]any{"checked_in_today": false, "total_quota": 12, "total_checkins": 2, "records": []any{}}}
		case "/api/log/self":
			items := f.logItems
			if items == nil {
				items = []any{}
			}
			data = map[string]any{"items": items, "total": len(items)}
		case "/api/user/login/encryption-key":
			data = map[string]any{"enabled": false}
		case "/api/user/login", "/api/user/auth/refresh":
			f.refresh++
			if f.loseRefresh {
				c, _, _ := w.(http.Hijacker).Hijack()
				_ = c.Close()
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "new_api_refresh", Value: "sid.new"})
			data = map[string]any{"access_token": "access-secret", "access_expires_at": time.Now().Add(15 * time.Minute).Unix(), "session": map[string]any{"sid": "sid", "expires_at": time.Now().Add(24 * time.Hour).Unix()}, "user": map[string]any{"id": f.user, "username": "user"}}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	t.Cleanup(f.server.Close)
	f.host = testutil.NewHost(t)
	f.repo = credential.NewStore(f.host)
	f.state, _ = state.Open(testutil.Dir(t))
	f.client = newapi.NewClient(f.server.Client())
	gate := &core.Gate{}
	broker := credential.NewBroker(f.repo, f.state, f.client, gate, time.Now)
	f.s = NewService(f.repo, f.state, f.client, broker, gate)
	return f
}
func (f *accountFixture) input() BindInput {
	return BindInput{Site: f.server.URL, Channel: "token", Token: "nap_test-secret", Label: "hello"}
}
func TestCookieRequiresDedicatedSessionConsent(t *testing.T) {
	f := fixture(t)
	_, e := f.s.Bind(context.Background(), BindInput{Site: f.server.URL, Channel: "refresh_cookie", RefreshCookie: "sid.old"})
	if core.ErrorKind(e) != "invalid_input" || f.refresh != 0 {
		t.Fatal(e, f.refresh)
	}
}
func TestSameUserRebindPreservesHistory(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	_ = f.state.Update(func(s *state.Snapshot) error {
		s.History = append(s.History, state.HistoryEntry{AccountID: a.ID, Result: core.AttemptResult{Status: "success", ObservedAt: time.Now()}})
		s.WorkItems["old"] = state.WorkItem{ID: "old", AccountID: a.ID, Status: "retry", Attempt: 2}
		return nil
	})
	in := f.input()
	in.AccountID = a.ID
	in.Label = "updated"
	b, e := f.s.Bind(context.Background(), in)
	if e != nil || b.ID != a.ID || len(f.state.Read().History) != 1 {
		t.Fatal(b, e)
	}
	saved, _, _ := f.repo.Load(context.Background(), a.ID)
	if saved.Credential.Generation != 2 || f.state.Read().WorkItems["old"].Status != "canceled" {
		t.Fatal("old work not invalidated")
	}
}
func TestRebindRejectsDifferentUser(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	in := f.input()
	in.AccountID = a.ID
	f.user = 2
	if _, e = f.s.Bind(context.Background(), in); core.ErrorKind(e) != "identity_mismatch" {
		t.Fatal(e)
	}
	saved, _, _ := f.repo.Load(context.Background(), a.ID)
	if saved.Credential.Generation != 1 {
		t.Fatal("overwritten")
	}
}
func TestLoginModeBindsWithoutCheckinEndpoints(t *testing.T) {
	f := fixture(t)
	f.quota, f.used, f.lastLogin = 44749966, 17750034, 1794654511
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	a, e := f.s.Bind(context.Background(), in)
	if e != nil || a.Status != "ready" || a.CheckinMode != core.CheckinModeLogin {
		t.Fatal(a, e)
	}
	if f.checkinHits != 0 {
		t.Fatal("login mode touched checkin endpoints")
	}
	r := f.state.Read().Accounts[a.ID]
	if r.CheckinMode != core.CheckinModeLogin || r.RewardTotal != 62500000 || r.RewardLastLogin != 1794654511 {
		t.Fatal(r.CheckinMode, r.RewardTotal, r.RewardLastLogin)
	}
}

func TestLoginModeRejectsNonTokenChannels(t *testing.T) {
	f := fixture(t)
	_, e := f.s.Bind(context.Background(), BindInput{Site: f.server.URL, Channel: "password", Username: "u", Password: "p", CheckinMode: core.CheckinModeLogin})
	if core.ErrorKind(e) != "invalid_input" || f.refresh != 0 || f.host.Saves != 0 {
		t.Fatal(e, f.refresh, f.host.Saves)
	}
}

func TestBindRejectsUnknownCheckinMode(t *testing.T) {
	f := fixture(t)
	in := f.input()
	in.CheckinMode = "oauth"
	if _, e := f.s.Bind(context.Background(), in); core.ErrorKind(e) != "invalid_input" || f.host.Saves != 0 {
		t.Fatal(e, f.host.Saves)
	}
}

func TestRebindKeepsLoginModeWhenOmitted(t *testing.T) {
	f := fixture(t)
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	a, e := f.s.Bind(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	again := f.input()
	again.AccountID = a.ID
	b, e := f.s.Bind(context.Background(), again)
	if e != nil || b.CheckinMode != core.CheckinModeLogin {
		t.Fatal(b, e)
	}
	saved, _, _ := f.repo.Load(context.Background(), a.ID)
	if saved.CheckinMode != core.CheckinModeLogin {
		t.Fatal(saved.CheckinMode)
	}
}

func TestRecheckRefreshesLoginSnapshotWithoutCheckin(t *testing.T) {
	f := fixture(t)
	f.quota, f.used = 44749966, 17750034
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	a, e := f.s.Bind(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	f.quota = 50000000
	f.lastLogin = time.Now().Add(time.Hour).Unix()
	out, e := f.s.Recheck(context.Background(), a.ID)
	if e != nil || out.Status != "ready" || f.checkinHits != 0 {
		t.Fatal(out, e, f.checkinHits)
	}
	r := f.state.Read().Accounts[a.ID]
	if r.RewardTotal != 67750034 || r.RewardLastLogin != f.lastLogin {
		t.Fatal(r.RewardTotal, r.RewardLastLogin)
	}
}

// wallSelf makes /api/user/self answer with an Aliyun WAF captcha page, like
// agentrouter.org does for server-side requests.
func (f *accountFixture) wallSelf() {
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/self" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><meta name="aliyun_waf_aa" content="x">`))
			return
		}
		original.ServeHTTP(w, r)
	})
}

func TestRecheckKeepsKnownRewardWhenEvidenceWalled(t *testing.T) {
	f := fixture(t)
	f.quota, f.used = 44749966, 17750034
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	a, e := f.s.Bind(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	// Confirm today's reward before the wall appears.
	f.quota, f.used = 80000000, 0
	f.lastLogin = time.Now().Add(time.Hour).Unix()
	if _, e = f.s.Recheck(context.Background(), a.ID); e != nil {
		t.Fatal(e)
	}
	f.wallSelf()
	_ = f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[a.ID]
		r.Status = "unsupported_site" // leftover sticky status
		s.Accounts[a.ID] = r
		return nil
	})
	out, e := f.s.Recheck(context.Background(), a.ID)
	if e != nil || out.Status != "ready" {
		t.Fatal(out, e)
	}
	rec := f.state.Read().Accounts[a.ID]
	if rec.Status != "ready" || rec.RewardState != core.RewardStateSuccess {
		t.Fatal(rec)
	}
	if rec.LastResult == nil || rec.LastResult.Status != core.ErrorWafBlocked {
		t.Fatalf("the wall was not recorded: %+v", rec.LastResult)
	}
}

func TestRecheckReportsWafWhenDayUnknown(t *testing.T) {
	f := fixture(t)
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	a, e := f.s.Bind(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	f.wallSelf()
	if _, e = f.s.Recheck(context.Background(), a.ID); core.ErrorKind(e) != core.ErrorWafBlocked {
		t.Fatal(e)
	}
	if rec := f.state.Read().Accounts[a.ID]; rec.Status != core.ErrorWafBlocked {
		t.Fatal(rec)
	}
}

func TestLoginModeBindsWithOAuthCookie(t *testing.T) {
	f := fixture(t)
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	in.OAuthProvider = core.OAuthProviderGitHub
	in.OAuthCookie = "user_sessions=abc; logged_in=1"
	a, e := f.s.Bind(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	saved, _, _ := f.repo.Load(context.Background(), a.ID)
	if saved.Credential.OAuthProvider != core.OAuthProviderGitHub || saved.Credential.OAuthCookie != "user_sessions=abc; logged_in=1" {
		t.Fatal(saved.Credential.OAuthProvider, "cookie missing")
	}
	b, _ := os.ReadFile(filepath.Join(f.host.Dir, credential.Filename(a.ID)))
	if !strings.Contains(string(b), "user_sessions=abc") {
		t.Fatal("cookie must live in the credential file")
	}
	// Switching the same account back to api mode must clear the stored cookie.
	in2 := f.input()
	in2.AccountID = a.ID
	in2.CheckinMode = core.CheckinModeAPI
	if _, e = f.s.Bind(context.Background(), in2); e != nil {
		t.Fatal(e)
	}
	saved, _, _ = f.repo.Load(context.Background(), a.ID)
	if saved.Credential.OAuthProvider != "" || saved.Credential.OAuthCookie != "" {
		t.Fatal("oauth cookie survived api rebind", saved.Credential.OAuthProvider)
	}
}

func TestLoginModeRejectsHalfCookieInput(t *testing.T) {
	f := fixture(t)
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	in.OAuthProvider = core.OAuthProviderGitHub
	if _, e := f.s.Bind(context.Background(), in); core.ErrorKind(e) != "invalid_input" || f.host.Saves != 0 {
		t.Fatal(e, f.host.Saves)
	}
	in2 := f.input()
	in2.CheckinMode = core.CheckinModeLogin
	in2.OAuthCookie = "user_sessions=abc"
	if _, e := f.s.Bind(context.Background(), in2); core.ErrorKind(e) != "invalid_input" || f.host.Saves != 0 {
		t.Fatal(e, f.host.Saves)
	}
}

func TestPasswordNeverPersists(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), BindInput{Site: f.server.URL, Channel: "password", Username: "user", Password: "plaintext-marker"})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(f.host.Dir, credential.Filename(a.ID)))
	if strings.Contains(string(b), "plaintext-marker") {
		t.Fatal("password persisted")
	}
}
func TestKnownSessionRejectedBeforeRefresh(t *testing.T) {
	f := fixture(t)
	in := BindInput{Site: f.server.URL, Channel: "refresh_cookie", RefreshCookie: "sid.old", DedicatedSessionConfirmed: true}
	if _, e := f.s.Bind(context.Background(), in); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Bind(context.Background(), in); core.ErrorKind(e) != "session_already_managed" || f.refresh != 1 {
		t.Fatal(e, f.refresh)
	}
}
func TestInterruptedImportCannotBeReused(t *testing.T) {
	f := fixture(t)
	_ = f.state.Update(func(s *state.Snapshot) error {
		s.KnownSessions[f.server.URL+"\nsid"] = state.KnownSession{Site: f.server.URL, SessionID: "sid", Status: "importing", ExpiresAt: time.Now().Add(time.Hour)}
		return nil
	})
	_, e := f.s.Bind(context.Background(), BindInput{Site: f.server.URL, Channel: "refresh_cookie", RefreshCookie: "sid.old", DedicatedSessionConfirmed: true})
	if core.ErrorKind(e) != "session_already_managed" || f.refresh != 0 {
		t.Fatal(e, f.refresh)
	}
}
func TestWalletOnlyTokenCannotValidateProfile(t *testing.T) {
	f := fixture(t)
	f.denyProfile = true
	if _, e := f.s.Bind(context.Background(), f.input()); core.ErrorKind(e) != "permission_denied" || f.host.Saves != 0 {
		t.Fatal(e)
	}
}
func TestWalletWriteRemainsUnverified(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil || a.WritePermissionVerified || f.posts != 0 {
		t.Fatal(a, e)
	}
}
func TestTurnstileAccountSavedButBlocked(t *testing.T) {
	f := fixture(t)
	f.turnstile = true
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil || a.Status != "turnstile_blocked" || f.host.Saves != 1 || f.posts != 0 {
		t.Fatal(a, e)
	}
}
func TestBindRefreshTimeoutReportsUnknown(t *testing.T) {
	f := fixture(t)
	f.loseRefresh = true
	in := BindInput{Site: f.server.URL, Channel: "refresh_cookie", RefreshCookie: "sid.old", DedicatedSessionConfirmed: true}
	_, e := f.s.Bind(context.Background(), in)
	if core.ErrorKind(e) != "refresh_outcome_unknown" {
		t.Fatal(e)
	}
	if _, e = f.s.Bind(context.Background(), in); core.ErrorKind(e) != "session_already_managed" || f.refresh != 1 {
		t.Fatal(e)
	}
}
func TestDeleteTombstoneSurvivesStaleHostList(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	f.host.Stale = true
	if e = f.s.Delete(context.Background(), a.ID); e != nil {
		t.Fatal(e)
	}
	if !f.state.Read().DeletePending[a.ID] {
		t.Fatal("no tombstone")
	}
	list, e := f.repo.List(context.Background())
	if e != nil || len(list) != 0 {
		t.Fatal(list, e)
	}
}

func TestDeleteVersusRebindSerialized(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	original := f.server.Config.Handler
	entered, release := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user/self" && blocked.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		original.ServeHTTP(w, r)
	})
	in := f.input()
	in.AccountID = a.ID
	bound, deleted := make(chan error, 1), make(chan error, 1)
	go func() { _, err := f.s.Bind(context.Background(), in); bound <- err }()
	<-entered
	go func() { deleted <- f.s.Delete(context.Background(), a.ID) }()
	select {
	case e := <-deleted:
		close(release)
		t.Fatal("delete bypassed the ongoing binding", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if e := <-bound; e != nil {
		t.Fatal(e)
	}
	if e := <-deleted; e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(f.host.Dir, credential.Filename(a.ID))); !os.IsNotExist(e) {
		t.Fatal("deleted credential still exists", e)
	}
	if e := f.s.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
	snapshot := f.state.Read()
	if !snapshot.DeletePending[a.ID] || snapshot.Accounts[a.ID].Status != "deleted" {
		t.Fatal("deleted account resurrected")
	}
	for _, w := range snapshot.WorkItems {
		if w.Status != "canceled" {
			t.Fatal("rebind work remained runnable", w)
		}
	}
}

func TestRecheckDoesNotReviveExpiredSession(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), BindInput{Site: f.server.URL, Channel: "password", Username: "user", Password: "synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	saved, rev, e := f.repo.Load(context.Background(), a.ID)
	if e != nil {
		t.Fatal(e)
	}
	saved.Credential.SessionExpiresAt = time.Now().Add(-time.Minute).Unix()
	if e = f.repo.Save(context.Background(), saved, rev); e != nil {
		t.Fatal(e)
	}
	before := f.refresh
	if _, e = f.s.Recheck(context.Background(), a.ID); core.ErrorKind(e) != "credential_expired" {
		t.Fatal(e)
	}
	if f.refresh != before || f.state.Read().Accounts[a.ID].Status != "credential_expired" {
		t.Fatal("expired session refreshed or resumed")
	}
}

func TestStartupEmptyListDoesNotEraseAccounts(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), f.input())
	if e != nil {
		t.Fatal(e)
	}
	f.host.Mu.Lock()
	f.host.Files = map[string]string{}
	f.host.Mu.Unlock()
	if e = f.s.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
	if r, ok := f.state.Read().Accounts[a.ID]; !ok || r.Status != "ready" {
		t.Fatal("empty host enumeration erased a physical account")
	}
}

func TestReconcileBeforeRefreshRecoveryAcceptsSavedGeneration(t *testing.T) {
	f := fixture(t)
	a, e := f.s.Bind(context.Background(), BindInput{Site: f.server.URL, Channel: "password", Username: "user", Password: "synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	saved, rev, e := f.repo.Load(context.Background(), a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.state.Update(func(v *state.Snapshot) error {
		v.RefreshPending[a.ID] = state.RefreshPending{AccountID: a.ID, OldGeneration: saved.Credential.Generation, StartedAt: time.Now()}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	saved.Credential.Generation++
	saved.Credential.AccessToken = "next-synthetic"
	saved.Credential.RefreshToken = "sid.next"
	if e = f.repo.Save(context.Background(), saved, rev); e != nil {
		t.Fatal(e)
	}
	// This is the application startup order after a crash between credential and state saves.
	if e = f.s.Reconcile(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = f.s.broker.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	snapshot := f.state.Read()
	if snapshot.Accounts[a.ID].Status != "ready" || snapshot.Accounts[a.ID].LastGeneration != 2 || len(snapshot.RefreshPending) != 0 {
		t.Fatal("completed refresh was mistaken for an external edit", snapshot.Accounts[a.ID])
	}
}

func TestRecheckFailureKeepsProviderPaused(t *testing.T) {
	for _, pause := range []string{core.RewardStateCookieExpired, core.RewardStateNeedsAttention, core.RewardStateChallengeBlocked} {
		t.Run(pause, func(t *testing.T) {
			f := fixture(t)
			f.quota = 62500000
			in := f.input()
			in.CheckinMode = core.CheckinModeLogin
			in.OAuthProvider, in.OAuthCookie = core.OAuthProviderGitHub, "user_sessions=paused"
			a, e := f.s.Bind(context.Background(), in)
			if e != nil {
				t.Fatal(e)
			}
			if e = f.state.Update(func(s *state.Snapshot) error {
				r := s.Accounts[a.ID]
				r.Status, r.RewardState = pause, pause
				s.Accounts[a.ID] = r
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			original := f.server.Config.Handler
			var failOnce atomic.Bool
			failOnce.Store(true)
			f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/user/self" && failOnce.Swap(false) {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"success":false,"message":"temporary failure"}`))
					return
				}
				original.ServeHTTP(w, r)
			})
			if _, e = f.s.Recheck(context.Background(), a.ID); core.ErrorKind(e) != "upstream_error" {
				t.Fatalf("setup failed to simulate a transient failure: %v", e)
			}
			out, e := f.s.Recheck(context.Background(), a.ID)
			if e != nil {
				t.Fatal(e)
			}
			runner := engine.NewRunner(f.s.broker, f.s.client, f.state, core.RealClock{})
			jobs := engine.NewJobs(runner, f.state, core.RealClock{})
			_, submitErr := jobs.Submit(context.Background(), []string{a.ID}, "manual")
			if out.Status == "ready" || submitErr == nil {
				t.Fatalf("a token-only recheck resumed the unchanged paused cookie: after=%s reward=%s submit_error=%v", out.Status, out.RewardState, submitErr)
			}
		})
	}
}

func TestRebindKeepsStoredOAuthSession(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	in.OAuthProvider, in.OAuthCookie = core.OAuthProviderLinuxDO, "user_sessions=keep"
	a, err := f.s.Bind(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out := f.state.Read().Accounts[a.ID]; out.OAuthProvider != core.OAuthProviderLinuxDO {
		t.Fatalf("record lost the provider: %+v", out)
	}
	rebind := f.input()
	rebind.AccountID = a.ID
	rebind.CheckinMode = core.CheckinModeLogin
	rebind.Token = "nap_second-token"
	rebind.OAuthProvider = core.OAuthProviderLinuxDO
	if _, err = f.s.Bind(context.Background(), rebind); err != nil {
		t.Fatal(err)
	}
	saved, _, err := f.repo.Load(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Credential.OAuthProvider != core.OAuthProviderLinuxDO || saved.Credential.OAuthCookie != "user_sessions=keep" || saved.Credential.Token != "nap_second-token" {
		t.Fatalf("rebind lost the stored session: %+v", saved.Credential)
	}
	rebind.Token = "nap_third-token"
	rebind.OAuthProvider, rebind.OAuthCookie = core.OAuthProviderLinuxDO, "user_sessions=new"
	if _, err = f.s.Bind(context.Background(), rebind); err != nil {
		t.Fatal(err)
	}
	saved, _, _ = f.repo.Load(context.Background(), a.ID)
	if saved.Credential.OAuthCookie != "user_sessions=new" || saved.Credential.Token != "nap_third-token" {
		t.Fatal("a fresh cookie must replace the stored one", saved.Credential)
	}
	// A provider without a stored session of the same kind cannot be carried
	// over: switching providers requires pasting that provider's cookie.
	rebind.OAuthProvider, rebind.OAuthCookie = core.OAuthProviderGitHub, ""
	if _, err = f.s.Bind(context.Background(), rebind); core.ErrorKind(err) != "invalid_input" {
		t.Fatalf("cross-provider carryover must fail: %v", err)
	}
}

func TestRebindManualDropsStoredOAuthSession(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	in.OAuthProvider, in.OAuthCookie = core.OAuthProviderLinuxDO, "user_sessions=old"
	a, err := f.s.Bind(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	// Selecting 手动 (no provider) on rebind must clear the stored session.
	rebind := f.input()
	rebind.AccountID = a.ID
	rebind.CheckinMode = core.CheckinModeLogin
	rebind.Token = "nap_second-token"
	if _, err = f.s.Bind(context.Background(), rebind); err != nil {
		t.Fatal(err)
	}
	saved, _, err := f.repo.Load(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Credential.OAuthProvider != "" || saved.Credential.OAuthCookie != "" {
		t.Fatalf("manual rebind kept the third-party session: %+v", saved.Credential)
	}
	if out := f.state.Read().Accounts[a.ID]; out.OAuthProvider != "" {
		t.Fatalf("record still advertises automatic login: %+v", out)
	}
	// After the drop, an auto rebind without a fresh cookie has nothing to keep.
	rebind.OAuthProvider = core.OAuthProviderLinuxDO
	if _, err = f.s.Bind(context.Background(), rebind); core.ErrorKind(err) != "invalid_input" {
		t.Fatalf("carryover without a stored session must fail: %v", err)
	}
}

func TestRebindOmittedTokenKeepsStoredCredential(t *testing.T) {
	f := fixture(t)
	in := f.input()
	hint := time.Now().Add(72 * time.Hour).Unix()
	in.ExpiresAtHint = &hint
	a, err := f.s.Bind(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	rebind := BindInput{Site: f.server.URL, AccountID: a.ID, Channel: "token", Label: "renamed"}
	out, err := f.s.Bind(context.Background(), rebind)
	if err != nil {
		t.Fatal(err)
	}
	saved, _, err := f.repo.Load(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Credential.Token != "nap_test-secret" || saved.Label != "renamed" || saved.Credential.Generation != 2 {
		t.Fatalf("omitted token must keep the stored credential: %+v", saved)
	}
	if saved.Credential.ExpiresAtHint == nil || *saved.Credential.ExpiresAtHint != hint {
		t.Fatal("expiry hint was not carried over", saved.Credential.ExpiresAtHint)
	}
	if out.Label != "renamed" {
		t.Fatal(out)
	}
	// An omitted token without an explicit account reference cannot resolve.
	if _, err = f.s.Bind(context.Background(), BindInput{Site: f.server.URL, Channel: "token"}); core.ErrorKind(err) != "invalid_input" {
		t.Fatal(err)
	}
}

func TestRebindWhilePausedClearsSameDayAttempt(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	in.OAuthProvider, in.OAuthCookie = core.OAuthProviderLinuxDO, "user_sessions=fixed"
	a, err := f.s.Bind(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")
	if err = f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[a.ID]
		r.Status = core.RewardStateNeedsAttention
		r.RewardAttemptDayKey = today
		r.RewardAttemptAt = time.Now().Unix()
		s.Accounts[a.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Rebind while paused (the operator replaced the credential).
	if err = f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[a.ID]
		r.RewardAttemptDayKey = today
		r.RewardAttemptAt = time.Now().Unix()
		s.Accounts[a.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Also not paused: any login-mode rebind resets the attempt marker.
	if err = f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[a.ID]
		r.Status = "ready"
		r.RewardAttemptDayKey = today
		r.RewardAttemptAt = time.Now().Unix()
		s.Accounts[a.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rebind := f.input()
	rebind.AccountID = a.ID
	rebind.CheckinMode = core.CheckinModeLogin
	rebind.OAuthProvider = core.OAuthProviderLinuxDO
	if _, err = f.s.Bind(context.Background(), rebind); err != nil {
		t.Fatal(err)
	}
	rec := f.state.Read().Accounts[a.ID]
	if rec.Status != "ready" || rec.RewardAttemptDayKey != "" || rec.RewardAttemptAt != 0 {
		t.Fatalf("a login-mode rebind must clear the same-day attempt: status=%s day=%q at=%d", rec.Status, rec.RewardAttemptDayKey, rec.RewardAttemptAt)
	}
	saved, _, err := f.repo.Load(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Credential.OAuthCookie != "user_sessions=fixed" {
		t.Fatal("the corrected session must be preserved", saved.Credential.OAuthCookie)
	}
}

func TestLoginModeAcceptsRefreshSessionChannel(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	in := BindInput{Site: f.server.URL, Channel: "refresh_cookie", RefreshCookie: "sid.dedicated", DedicatedSessionConfirmed: true, CheckinMode: core.CheckinModeLogin}
	in.OAuthProvider, in.OAuthCookie = core.OAuthProviderGitHub, "user_sessions=freegg"
	a, err := f.s.Bind(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	rec := f.state.Read().Accounts[a.ID]
	if rec.CheckinMode != core.CheckinModeLogin || rec.Status != "ready" || rec.RewardBaseline != true {
		t.Fatalf("refresh-session login bind failed: %+v", rec)
	}
	saved, _, err := f.repo.Load(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Credential.Kind != "session" || saved.Credential.OAuthCookie != "user_sessions=freegg" || saved.Credential.OAuthProvider != core.OAuthProviderGitHub {
		t.Fatalf("third-party session not stored on the refresh credential: %+v", saved.Credential)
	}
	// The password channel stays rejected for login mode.
	if _, err = f.s.Bind(context.Background(), BindInput{Site: f.server.URL, AccountID: a.ID, Channel: "password", Username: "user", Password: "synthetic"}); core.ErrorKind(err) != "invalid_input" {
		t.Fatal(err)
	}
}

func TestBindOAuthBootstrapChannel(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	authz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.server.URL+"/api/oauth/github?code=C&state=st-acc", http.StatusFound)
	}))
	defer authz.Close()
	authzURL, _ := url.Parse(authz.URL)
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/state":
			_, _ = w.Write([]byte(`{"success":true,"data":"st-acc"}`))
		case "/api/oauth/github":
			http.SetCookie(w, &http.Cookie{Name: "new_api_refresh", Value: "sid-refresh", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"id": f.user, "username": "user", "access_token": "access-boot",
				"access_expires_at": time.Now().Add(15 * time.Minute).Unix(),
				"session":           map[string]any{"sid": "sid-boot", "expires_at": time.Now().Add(30 * 24 * time.Hour).Unix()},
			}})
		default:
			original.ServeHTTP(w, r)
		}
	})
	f.client.SetLoginEndpoints(func(string) newapi.LoginEndpoints {
		return newapi.LoginEndpoints{AuthorizeURL: authz.URL + "/login/oauth/authorize", CookieDomain: authzURL.Hostname()}
	})
	in := BindInput{Site: f.server.URL, Channel: "oauth", CheckinMode: core.CheckinModeLogin, OAuthProvider: core.OAuthProviderGitHub, OAuthCookie: "user_sessions=boot"}
	a, err := f.s.Bind(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	rec := f.state.Read().Accounts[a.ID]
	if rec.CheckinMode != core.CheckinModeLogin || rec.Status != "ready" || rec.Kind != "session" {
		t.Fatalf("bootstrap bind failed: %+v", rec)
	}
	saved, _, err := f.repo.Load(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Credential.AccessToken != "access-boot" || saved.Credential.RefreshToken != "sid-refresh" || saved.Credential.SessionID != "sid-boot" || saved.Credential.OAuthCookie != "user_sessions=boot" {
		t.Fatalf("bootstrapped session not stored: %+v", saved.Credential)
	}
	// The channel requires the third-party session up front.
	if _, err = f.s.Bind(context.Background(), BindInput{Site: f.server.URL, Channel: "oauth"}); core.ErrorKind(err) != "invalid_input" {
		t.Fatal(err)
	}
}

func TestRefreshBalanceUpdatesWalletOnly(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	a, err := f.s.Bind(context.Background(), f.input())
	if err != nil {
		t.Fatal(err)
	}
	if err = f.state.Update(func(s *state.Snapshot) error {
		r := s.Accounts[a.ID]
		r.Status = core.RewardStateCookieExpired
		s.Accounts[a.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.quota += 5000000
	f.lastLogin = time.Now().Unix()
	out, err := f.s.RefreshBalance(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.BalanceQuota == nil || *out.BalanceQuota != 67500000 || out.BalancePerUnit != 500000 {
		t.Fatalf("balance not refreshed: %v %d", out.BalanceQuota, out.BalancePerUnit)
	}
	rec := f.state.Read().Accounts[a.ID]
	if rec.Status != core.RewardStateCookieExpired || rec.LastResult != nil || rec.RewardTotal != 0 || len(f.state.Read().History) != 0 {
		t.Fatal("balance refresh must not touch status, reward evidence, or history", rec.Status, rec.LastResult, rec.RewardTotal)
	}
}

func TestRecheckPersistsRewardHistory(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	in := f.input()
	in.CheckinMode = core.CheckinModeLogin
	a, e := f.s.Bind(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	f.quota += 12500000
	f.lastLogin = time.Now().Unix()
	first, e := f.s.Recheck(context.Background(), a.ID)
	if e != nil || first.LastResult == nil || first.LastResult.Status != "success" || first.LastResult.QuotaAwarded != 12500000 {
		t.Fatalf("first check did not observe the reward: %v %v", first.LastResult, e)
	}
	if first.BalanceQuota == nil || *first.BalanceQuota != 75000000 || first.BalancePerUnit != 500000 {
		t.Fatalf("recheck did not refresh the wallet balance: quota=%v per_unit=%d", first.BalanceQuota, first.BalancePerUnit)
	}
	if _, e = f.s.Recheck(context.Background(), a.ID); e != nil {
		t.Fatal(e)
	}
	for _, entry := range f.state.Read().History {
		if entry.AccountID == a.ID && entry.Result.Status == "success" && entry.Result.QuotaAwarded == 12500000 {
			return
		}
	}
	t.Fatal("a later already check erased the only success amount")
}

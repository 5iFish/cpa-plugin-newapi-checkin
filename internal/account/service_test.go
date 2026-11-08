package account

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	repo                                *credential.Store
	state                               *state.Store
	host                                *testutil.Host
	server                              *httptest.Server
	refresh, posts                      int
	turnstile, denyProfile, loseRefresh bool
	user                                int
}

func fixture(t *testing.T) *accountFixture {
	f := &accountFixture{user: 1}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/api/status":
			data = map[string]any{"turnstile_check": f.turnstile}
		case "/api/user/self":
			if f.denyProfile {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED"}`))
				return
			}
			data = map[string]any{"id": f.user, "username": "user"}
		case "/api/user/checkin":
			if r.Method == "POST" {
				f.posts++
			}
			data = map[string]any{"enabled": true, "stats": map[string]any{"checked_in_today": false, "total_quota": 12, "total_checkins": 2, "records": []any{}}}
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
	client := newapi.NewClient(f.server.Client())
	gate := &core.Gate{}
	broker := credential.NewBroker(f.repo, f.state, client, gate, time.Now)
	f.s = NewService(f.repo, f.state, client, broker, gate)
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

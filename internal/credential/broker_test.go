package credential

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshUpdatesPendingWorkGeneration(t *testing.T) {
	f := fixture(t)
	_ = f.state.Update(func(s *state.Snapshot) error {
		s.WorkItems["w"] = state.WorkItem{ID: "w", AccountID: f.id, Status: "running", Generation: 1}
		return nil
	})
	_ = f.broker.Use(context.Background(), f.id, func(core.Account, string) error { return core.Error("network_error") })
	if f.state.Read().WorkItems["w"].Generation != 2 {
		t.Fatal("refresh makes the account's own retry look stale")
	}
}

type brokerFixture struct {
	broker  *Broker
	repo    *Store
	state   *state.Store
	host    *testutil.Host
	id      string
	server  *httptest.Server
	refresh atomic.Int32
	lose    bool
}

func fixture(t *testing.T) *brokerFixture {
	f := &brokerFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.refresh.Add(1)
		if f.lose {
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				_ = conn.Close()
			}
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "new_api_refresh", Value: "sid.new"})
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"access_token": "new-access", "access_expires_at": time.Now().Add(15 * time.Minute).Unix(), "session": map[string]any{"sid": "sid", "expires_at": time.Now().Add(24 * time.Hour).Unix()}, "user": map[string]any{"id": 1, "username": "u"}}})
	}))
	t.Cleanup(f.server.Close)
	f.host = testutil.NewHost(t)
	f.repo = NewStore(f.host)
	f.state, _ = state.Open(testutil.Dir(t))
	a := sample()
	a.Site = f.server.URL
	a.ID = core.AccountID(a.Site, 1)
	a.Credential = core.Credential{Kind: "session", Channel: "password", AccessToken: "old-access", RefreshToken: "sid.old", AccessExpiresAt: time.Now().Add(-time.Minute).Unix(), SessionID: "sid", SessionExpiresAt: time.Now().Add(24 * time.Hour).Unix(), Generation: 1}
	if e := f.repo.Save(context.Background(), a, Revision{}); e != nil {
		t.Fatal(e)
	}
	f.id = a.ID
	_ = f.state.Update(func(s *state.Snapshot) error { s.Accounts[a.ID] = state.Record(a, "", "ready"); return nil })
	f.broker = NewBroker(f.repo, f.state, newapi.NewClient(f.server.Client()), &core.Gate{}, time.Now)
	return f
}
func TestConcurrentUseRefreshesOnce(t *testing.T) {
	f := fixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := f.broker.Use(context.Background(), f.id, func(a core.Account, b string) error {
				if b != "new-access" {
					t.Error("old access used")
				}
				return nil
			}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if f.refresh.Load() != 1 {
		t.Fatal(f.refresh.Load())
	}
	a, _, _ := f.repo.Load(context.Background(), f.id)
	if a.Credential.Generation != 2 || len(f.state.Read().RefreshPending) != 0 {
		t.Fatal(a.Credential.Generation)
	}
}
func TestRefreshSaveFailureBlocksAction(t *testing.T) {
	f := fixture(t)
	f.host.FailSave = true
	n := 0
	action := func(core.Account, string) error { n++; return nil }
	e := f.broker.Use(context.Background(), f.id, action)
	if core.ErrorKind(e) != "persistence_error" || n != 0 {
		t.Fatal(e, n)
	}
	f.host.FailSave = false
	if e = f.broker.Use(context.Background(), f.id, action); e != nil || n != 1 || f.refresh.Load() != 1 {
		t.Fatal(e, n, f.refresh.Load())
	}
}
func TestRestartPendingOldGenerationDoesNotRefresh(t *testing.T) {
	f := fixture(t)
	_ = f.state.Update(func(s *state.Snapshot) error {
		s.RefreshPending[f.id] = state.RefreshPending{AccountID: f.id, OldGeneration: 1}
		return nil
	})
	if e := f.broker.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	e := f.broker.Use(context.Background(), f.id, func(core.Account, string) error { t.Error("action ran"); return nil })
	if core.ErrorKind(e) != "refresh_outcome_unknown" || f.refresh.Load() != 0 {
		t.Fatal(e)
	}
}
func TestRestartSavedGenerationClearsPending(t *testing.T) {
	f := fixture(t)
	a, r, _ := f.repo.Load(context.Background(), f.id)
	a.Credential.Generation = 2
	a.Credential.AccessExpiresAt = time.Now().Add(10 * time.Minute).Unix()
	if e := f.repo.Save(context.Background(), a, r); e != nil {
		t.Fatal(e)
	}
	_ = f.state.Update(func(s *state.Snapshot) error {
		s.RefreshPending[f.id] = state.RefreshPending{AccountID: f.id, OldGeneration: 1}
		return nil
	})
	if e := f.broker.Recover(context.Background()); e != nil || len(f.state.Read().RefreshPending) != 0 {
		t.Fatal(e)
	}
}
func TestLostRefreshResponseRequiresRebind(t *testing.T) {
	f := fixture(t)
	f.lose = true
	for i := 0; i < 2; i++ {
		e := f.broker.Use(context.Background(), f.id, func(core.Account, string) error { t.Error("action ran"); return nil })
		if core.ErrorKind(e) != "refresh_outcome_unknown" {
			t.Fatal(e)
		}
	}
	if f.refresh.Load() != 1 {
		t.Fatal(f.refresh.Load())
	}
}
func Test403DoesNotRefresh(t *testing.T) {
	f := fixture(t)
	a, r, _ := f.repo.Load(context.Background(), f.id)
	a.Credential.AccessExpiresAt = time.Now().Add(10 * time.Minute).Unix()
	a.Credential.Generation++
	_ = f.repo.Save(context.Background(), a, r)
	e := f.broker.Use(context.Background(), f.id, func(core.Account, string) error { return &core.APIError{Kind: "permission_denied", HTTPStatus: 403} })
	if core.ErrorKind(e) != "permission_denied" || f.refresh.Load() != 0 {
		t.Fatal(e)
	}
}
func Test401RefreshOnce(t *testing.T) {
	f := fixture(t)
	a, r, _ := f.repo.Load(context.Background(), f.id)
	a.Credential.AccessExpiresAt = time.Now().Add(10 * time.Minute).Unix()
	a.Credential.Generation++
	_ = f.repo.Save(context.Background(), a, r)
	n := 0
	e := f.broker.Use(context.Background(), f.id, func(core.Account, string) error {
		n++
		return &core.APIError{Kind: "credential_expired", HTTPStatus: 401, Code: "AUTH_TOKEN_EXPIRED"}
	})
	if core.ErrorKind(e) != "credential_expired" || n != 2 || f.refresh.Load() != 1 {
		t.Fatal(e, n, f.refresh.Load())
	}
}

package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
)

func healthFixture(t *testing.T, logs http.HandlerFunc) (*Service, *state.Store, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"username":"user","quota":100,"used_quota":0,"last_login_time":1}}`))
		case "/api/log/self":
			logs(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	st, err := state.Open(testutil.Dir(t))
	if err != nil {
		t.Fatal(err)
	}
	gate := &core.Gate{}
	repo := credential.NewStore(testutil.NewHost(t))
	client := newapi.NewClient(server.Client())
	svc := NewService(repo, st, client, credential.NewBroker(repo, st, client, gate, time.Now), gate)
	svc.StartHealth(context.Background())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svc.ShutdownHealth(ctx); err != nil {
			t.Error(err)
		}
	})
	bound, err := svc.Bind(context.Background(), BindInput{Site: server.URL, Channel: "token", Token: "nap_health-secret", CheckinMode: core.CheckinModeLogin})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, bound.ID
}

func waitHealth(t *testing.T, svc *Service) core.HealthSummary {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out := svc.HealthStatus()
		if !out.Refreshing {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("health refresh did not finish")
	return core.HealthSummary{}
}

func TestHealthRefreshBucketsCacheAndSecrets(t *testing.T) {
	var hits atomic.Int32
	svc, st, id := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		start, _ := strconv.ParseInt(r.URL.Query().Get("start_timestamp"), 10, 64)
		end, _ := strconv.ParseInt(r.URL.Query().Get("end_timestamp"), 10, 64)
		if r.Header.Get("New-Api-User") != "7" || end-start != 86399 {
			t.Errorf("wrong account/window: %s %v", r.Header.Get("New-Api-User"), r.URL.Query())
		}
		if r.URL.Query().Get("type") == "2" {
			_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":[{"type":2,"created_at":%d,"content":"private-error-body","token_name":"private-token-name"},{"type":2,"created_at":%d},{"type":2,"created_at":%d}],"total":3}}`, start, start+1800, end)
		} else {
			_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":[{"type":5,"created_at":%d,"other":"secret-metadata"}],"total":1}}`, start+1800)
		}
	})
	started, err := svc.RefreshHealth(context.Background(), false)
	if err != nil || !started {
		t.Fatal(started, err)
	}
	out := waitHealth(t, svc)
	a := out.Accounts[id]
	if !a.Complete || a.Stale || a.Error != "" || a.Success != 3 || a.Failed != 1 || len(a.Buckets) != 48 || a.End-a.Start != 86400 {
		t.Fatalf("bad health summary: %+v", a)
	}
	if a.Buckets[0].Start != a.Start || a.Buckets[0].Success != 1 || a.Buckets[1].Success != 1 || a.Buckets[1].Failed != 1 || a.Buckets[47].Success != 1 || a.Buckets[2].Success != 0 || a.Buckets[2].Failed != 0 {
		t.Fatalf("wrong bucket boundaries: %+v", a.Buckets)
	}
	if a.SyncedAt.IsZero() || a.CheckedAt.IsZero() || hits.Load() != 2 {
		t.Fatal(a.SyncedAt, a.CheckedAt, hits.Load())
	}
	if started, err = svc.RefreshHealth(context.Background(), false); err != nil || started || hits.Load() != 2 {
		t.Fatalf("fresh cache refetched: started=%v hits=%d err=%v", started, hits.Load(), err)
	}
	cached := st.Read().Health[id]
	if cached.Success != 3 || cached.Failed != 1 {
		t.Fatalf("cache missing: %+v", cached)
	}
	encoded, _ := json.Marshal(out)
	for _, marker := range []string{"nap_health-secret", "private-error-body", "private-token-name", "secret-metadata"} {
		if strings.Contains(string(encoded), marker) {
			t.Fatalf("secret in health response: %s", marker)
		}
	}
}

func TestHealthRefreshPartialDoesNotClaimSuccess(t *testing.T) {
	svc, _, id := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") == "5" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED","message":"private-failure"}`))
			return
		}
		start, _ := strconv.ParseInt(r.URL.Query().Get("start_timestamp"), 10, 64)
		_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":[{"type":2,"created_at":%d}],"total":1}}`, start)
	})
	_, _ = svc.RefreshHealth(context.Background(), false)
	a := waitHealth(t, svc).Accounts[id]
	if a.Complete || a.Success != 1 || a.Error != "permission_denied" || a.CheckedAt.IsZero() {
		t.Fatalf("partial logs reported healthy: %+v", a)
	}
}

func TestHealthRefreshFailureRetainsPreviousWindow(t *testing.T) {
	var fail atomic.Bool
	svc, _, id := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"success":false,"message":"private-upstream-body"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
	})
	_, _ = svc.RefreshHealth(context.Background(), true)
	first := waitHealth(t, svc).Accounts[id]
	if !first.Complete || first.Success != 0 || first.Failed != 0 {
		t.Fatalf("valid idle window: %+v", first)
	}
	fail.Store(true)
	_, _ = svc.RefreshHealth(context.Background(), true)
	last := waitHealth(t, svc).Accounts[id]
	if !last.Stale || !last.Complete || last.Error == "" || last.Start != first.Start || !last.SyncedAt.Equal(first.SyncedAt) || last.CheckedAt.Before(first.CheckedAt) {
		t.Fatalf("previous window lost on error: first=%+v last=%+v", first, last)
	}
	if started, err := svc.RefreshHealth(context.Background(), false); err != nil || started {
		t.Fatalf("failed refresh bypassed retry cache: %v %v", started, err)
	}
}

func TestHealthRefreshIsAsyncAndDeduplicated(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	svc, _, _ := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
	})
	started, err := svc.RefreshHealth(context.Background(), true)
	if err != nil || !started {
		t.Fatal(started, err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh never entered log endpoint")
	}
	if !svc.HealthStatus().Refreshing {
		t.Fatal("background work not reported")
	}
	if started, err := svc.RefreshHealth(context.Background(), true); err != nil || started || hits.Load() != 1 {
		t.Fatalf("concurrent refresh duplicated: %v %v %d", started, err, hits.Load())
	}
	close(release)
	waitHealth(t, svc)
}

func TestHealthRefreshPageCapAndGateRelease(t *testing.T) {
	first, release := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	svc, _, id := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") == "5" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
			return
		}
		if hits.Add(1) == 1 {
			close(first)
			<-release
		}
		start, _ := strconv.ParseInt(r.URL.Query().Get("start_timestamp"), 10, 64)
		items := make([]map[string]int64, 100)
		for i := range items {
			items[i] = map[string]int64{"type": 2, "created_at": start}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"items": items, "total": 2001}})
	})
	_, _ = svc.RefreshHealth(context.Background(), true)
	<-first
	gateEntered := make(chan struct{})
	go func() {
		_ = svc.gate.Do(context.Background(), func() error { close(gateEntered); return nil })
	}()
	close(release)
	select {
	case <-gateEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("health collection held shared gate across log pages")
	}
	a := waitHealth(t, svc).Accounts[id]
	if a.Complete || !a.Truncated || a.Success != 2000 || hits.Load() != 20 || a.Error != "health_truncated" {
		t.Fatalf("page cap not reported: %+v hits=%d", a, hits.Load())
	}
}

func TestHealthCacheInvalidatedOnRebindAndDelete(t *testing.T) {
	svc, st, id := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
	})
	_, _ = svc.RefreshHealth(context.Background(), true)
	waitHealth(t, svc)
	a, _, err := svc.repo.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Bind(context.Background(), BindInput{AccountID: id, Site: a.Site, Channel: "token", Token: "nap_new-health", CheckinMode: core.CheckinModeLogin}); err != nil {
		t.Fatal(err)
	}
	if _, exists := st.Read().Health[id]; exists {
		t.Fatal("rebind retained previous health cache")
	}
	_, _ = svc.RefreshHealth(context.Background(), true)
	waitHealth(t, svc)
	if err = svc.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if len(svc.HealthStatus().Accounts) != 0 || len(st.Read().Health) != 0 {
		t.Fatal("deleted account retained health")
	}
}

func TestHealthShutdownCancelsRequestsAndCanRestart(t *testing.T) {
	entered := make(chan struct{}, 4)
	svc, st, _ := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
	})
	_, _ = svc.RefreshHealth(context.Background(), true)
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.ShutdownHealth(ctx); err != nil {
		t.Fatal(err)
	}
	if svc.HealthStatus().Refreshing || len(st.Read().Health) != 0 {
		t.Fatal("shutdown returned before health worker stopped")
	}
	if _, err := svc.RefreshHealth(context.Background(), true); core.ErrorKind(err) != "disabled" {
		t.Fatalf("shutdown allowed a new worker: %v", err)
	}
	svc.StartHealth(context.Background())
	if started, err := svc.RefreshHealth(context.Background(), true); err != nil || !started {
		t.Fatal(started, err)
	}
}

func TestHealthRefreshPrioritizesAccountsNotYetChecked(t *testing.T) {
	svc, st, firstID := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
	})
	// A missing credential is still checked and receives an error timestamp.
	// Never-checked accounts must precede retries regardless of hash ordering,
	// so slow early accounts cannot consume every round's total time budget.
	if err := st.Update(func(v *state.Snapshot) error {
		v.Accounts["zz-not-checked"] = state.AccountRecord{ID: "zz-not-checked", Status: "ready"}
		v.Health[firstID] = core.AccountHealth{ID: firstID, CheckedAt: time.Now().Add(-10 * time.Minute)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = svc.RefreshHealth(context.Background(), true)
	out := waitHealth(t, svc)
	unchecked, previous := out.Accounts["zz-not-checked"], out.Accounts[firstID]
	if unchecked.CheckedAt.IsZero() || !unchecked.CheckedAt.Before(previous.CheckedAt) {
		t.Fatalf("never-checked account ran after an older retry: new=%v retry=%v", unchecked.CheckedAt, previous.CheckedAt)
	}
}

func TestHealthRefreshIsolatesAccountLogFailures(t *testing.T) {
	svc, _, firstID := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED"}`))
	})
	secondSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":9,"username":"second","quota":100,"used_quota":0,"last_login_time":1}}`))
		case "/api/log/self":
			start, _ := strconv.ParseInt(r.URL.Query().Get("start_timestamp"), 10, 64)
			if r.Header.Get("New-Api-User") != "9" {
				t.Error("second site's logs used the first account's identity")
			}
			if r.URL.Query().Get("type") == "2" {
				_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":[{"type":2,"created_at":%d}],"total":1}}`, start)
			} else {
				_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(secondSite.Close)
	second, err := svc.Bind(context.Background(), BindInput{Site: secondSite.URL, Channel: "token", Token: "nap_second-health", CheckinMode: core.CheckinModeLogin})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = svc.RefreshHealth(context.Background(), true)
	out := waitHealth(t, svc)
	if len(out.Accounts) != 2 || out.Accounts[firstID].Complete || out.Accounts[firstID].Error != "permission_denied" || !out.Accounts[second.ID].Complete || out.Accounts[second.ID].Success != 1 {
		t.Fatalf("one account failure damaged another: %+v", out)
	}
}

func TestHealthRefreshCannotRestoreCacheAfterRebind(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var pages atomic.Int32
	svc, st, id := healthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if pages.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		start, _ := strconv.ParseInt(r.URL.Query().Get("start_timestamp"), 10, 64)
		items := make([]map[string]int64, 100)
		for i := range items {
			items[i] = map[string]int64{"type": 2, "created_at": start}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"items": items, "total": 101}})
	})
	_, _ = svc.RefreshHealth(context.Background(), true)
	<-entered
	a, _, err := svc.repo.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	bound := make(chan error, 1)
	go func() {
		_, err := svc.Bind(context.Background(), BindInput{AccountID: id, Site: a.Site, Channel: "token", Token: "nap_rebound-health", CheckinMode: core.CheckinModeLogin})
		bound <- err
	}()
	close(release)
	if err := <-bound; err != nil {
		t.Fatal(err)
	}
	waitHealth(t, svc)
	if _, exists := st.Read().Health[id]; exists {
		t.Fatal("in-flight old logs restored the rebound account's cache")
	}
}

func TestHealthCacheOldStateCompatibility(t *testing.T) {
	dir := testutil.Dir(t)
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"accounts":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(dir)
	if err != nil || st.Read().Health == nil {
		t.Fatal("old state cannot initialise optional health cache", err)
	}
}

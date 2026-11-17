package console

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/account"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/engine"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Handler, *state.Store) {
	s, e := state.Open(testutil.Dir(t))
	if e != nil {
		t.Fatal(e)
	}
	h := NewHandler(nil, engine.NewJobs(nil, s, core.RealClock{}), s, func(context.Context, config.Overrides) error { return nil })
	return h, s
}
func TestManagementRoutesExact(t *testing.T) {
	h, _ := fixture(t)
	for _, path := range []string{"/status", "/v0/management/plugins/newapi-checkin/other", "/v0/management/plugins/newapi-checkin/status/"} {
		r, _ := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: path})
		if r.StatusCode != 404 {
			t.Fatal(path, r.StatusCode)
		}
	}
}
func TestCheckinReturns202WithoutWaiting(t *testing.T) {
	h, s := fixture(t)
	_ = s.Update(func(v *state.Snapshot) error {
		v.Accounts["id"] = state.AccountRecord{ID: "id", Status: "ready", LastGeneration: 1}
		return nil
	})
	r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "POST", Path: "/v0/management/plugins/newapi-checkin/checkin", Body: []byte(`{"account_id":"id"}`)})
	if e != nil || r.StatusCode != 202 || !strings.Contains(string(r.Body), "job_id") {
		t.Fatal(r, e)
	}
}
func TestAccountBodyLimit(t *testing.T) {
	h, _ := fixture(t)
	r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "POST", Path: "/v0/management/plugins/newapi-checkin/accounts", Body: []byte(strings.Repeat("x", 65537))})
	if e != nil || r.StatusCode != 413 {
		t.Fatal(r, e)
	}
}
func TestStatusNeverContainsSecrets(t *testing.T) {
	h, s := fixture(t)
	_ = s.Update(func(v *state.Snapshot) error {
		v.Accounts["id"] = state.AccountRecord{ID: "id", PhysicalPath: "physical-secret-path", TokenHint: "…1234"}
		return nil
	})
	r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: "/v0/management/plugins/newapi-checkin/status"})
	if e != nil || strings.Contains(string(r.Body), "physical-secret-path") || r.Headers.Get("Cache-Control") != "no-store" {
		t.Fatal(e, string(r.Body))
	}
}
func TestStatusOrdersAccountsByCreation(t *testing.T) {
	h, s := fixture(t)
	_ = s.Update(func(v *state.Snapshot) error {
		v.Accounts["zz-newest"] = state.AccountRecord{ID: "zz-newest", CreatedAt: time.Now().Add(time.Hour)}
		v.Accounts["aa-middle"] = state.AccountRecord{ID: "aa-middle", CreatedAt: time.Now()}
		v.Accounts["mm-legacy"] = state.AccountRecord{ID: "mm-legacy"}
		return nil
	})
	r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: "/v0/management/plugins/newapi-checkin/status"})
	if e != nil {
		t.Fatal(e)
	}
	body := string(r.Body)
	legacy := strings.Index(body, "mm-legacy")
	middle := strings.Index(body, "aa-middle")
	newest := strings.Index(body, "zz-newest")
	if legacy == -1 || middle == -1 || newest == -1 || !(legacy < middle && middle < newest) {
		t.Fatalf("accounts not ordered by creation time: legacy=%d middle=%d newest=%d", legacy, middle, newest)
	}
}

func TestUpstream403IsBusinessFailure(t *testing.T) {
	r := BusinessError(&core.APIError{Kind: "permission_denied", HTTPStatus: 403, SafeMessage: "permission_denied"})
	if r.StatusCode == 401 || r.StatusCode == 403 || !strings.Contains(string(r.Body), "permission_denied") {
		t.Fatal(r)
	}
}
func TestSettingsNullClearsOverride(t *testing.T) {
	h, s := fixture(t)
	v := "12:00"
	_ = s.Update(func(s *state.Snapshot) error { s.SettingsOverrides.CheckinAt = &v; return nil })
	var got config.Overrides
	h.applySettings = func(ctx context.Context, o config.Overrides) error { got = o; return nil }
	r, _ := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "POST", Path: "/v0/management/plugins/newapi-checkin/settings", Body: []byte(`{"checkin_at":null}`)})
	if r.StatusCode != 200 || got.CheckinAt != nil {
		t.Fatal(r)
	}
}
func TestDeleteOnlyAcceptsID(t *testing.T) {
	h, _ := fixture(t)
	r, _ := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "DELETE", Path: "/v0/management/plugins/newapi-checkin/accounts", Query: url.Values{"path": {"C:/foreign.json"}}})
	if r.StatusCode != 400 {
		t.Fatal(r)
	}
}
func TestProbeRouteReportsProviderStatus(t *testing.T) {
	host := testutil.NewHost(t)
	st, e := state.Open(testutil.Dir(t))
	if e != nil {
		t.Fatal(e)
	}
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "blocked" {
			w.Header().Set("Cf-Mitigated-Header", "challenge")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.Redirect(w, r, "https://example.com/callback", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	client := newapi.NewClient(srv.Client())
	client.SetLoginEndpoints(func(string) newapi.LoginEndpoints {
		return newapi.LoginEndpoints{AuthorizeURL: srv.URL + "/oauth/authorize", CookieDomain: u.Hostname()}
	})
	gate := &core.Gate{}
	repo := credential.NewStore(host)
	svc := account.NewService(repo, st, client, credential.NewBroker(repo, st, client, gate, time.Now), gate)
	h := NewHandler(svc, nil, st, nil)
	probe := func(q url.Values) pluginapi.ManagementResponse {
		r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: Base + "/probe", Query: q})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	r := probe(url.Values{"provider": {"linuxdo"}})
	if r.StatusCode != 200 || !strings.Contains(string(r.Body), `"status":"ok"`) {
		t.Fatal(r.StatusCode, string(r.Body))
	}
	mode = "blocked"
	r = probe(url.Values{"provider": {"linuxdo"}})
	if r.StatusCode != 200 || !strings.Contains(string(r.Body), `"status":"blocked"`) {
		t.Fatal(r.StatusCode, string(r.Body))
	}
	for _, q := range []url.Values{{"provider": {"evil"}}, {"provider": {""}}, {"provider": {"github"}, "extra": {"x"}}, {}} {
		if r = probe(q); r.StatusCode != 400 {
			t.Fatalf("query %v accepted with %d", q, r.StatusCode)
		}
	}
}

func TestResourceContainsNoAccountData(t *testing.T) {
	r, ok := Resource("/v0/resource/plugins/newapi-checkin/console")
	if !ok || r.StatusCode != 200 {
		t.Fatal("missing resource")
	}
	var v any
	if json.Unmarshal(r.Body, &v) == nil {
		t.Fatal("dynamic account data in resource")
	}
}

func TestStorageFailureRemainsVisibleInStatus(t *testing.T) {
	dir := testutil.Dir(t)
	s, e := state.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Update(func(v *state.Snapshot) error {
		v.Accounts["retained"] = state.AccountRecord{ID: "retained", Status: "ready"}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "state.json")
	if e = os.Rename(path, filepath.Join(dir, "state.saved")); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e = s.Update(func(v *state.Snapshot) error { v.Error = "uncommitted"; return nil }); core.ErrorKind(e) != "storage_error" {
		t.Fatal(e)
	}
	h := NewHandler(nil, nil, s, nil)
	r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: Base + "/status"})
	if e != nil || r.StatusCode != 200 || !strings.Contains(string(r.Body), `"error":"storage_error"`) || !strings.Contains(string(r.Body), `"account_id":"retained"`) {
		t.Fatal("storage failure hidden by unchanged snapshot", e, string(r.Body))
	}
}

func TestUsageWindowRanges(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	for _, tc := range []struct {
		key   string
		hours int64
		valid bool
	}{
		{"24h", 24, true}, {"7d", 168, true}, {"30d", 720, true}, {"1y", 0, false}, {"", 0, false},
	} {
		start, end, ok := usageWindow(tc.key, now)
		if ok != tc.valid || (ok && (end != now.Unix() || end-start != tc.hours*3600)) {
			t.Fatalf("%q: start=%d end=%d ok=%v", tc.key, start, end, ok)
		}
	}
}

func TestBalanceRefreshesAccounts(t *testing.T) {
	host := testutil.NewHost(t)
	st, e := state.Open(testutil.Dir(t))
	if e != nil {
		t.Fatal(e)
	}
	quota := int64(100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = fmt.Fprintf(w, `{"success":true,"data":{"id":1,"username":"u","quota":%d,"used_quota":10,"last_login_time":1}}`, quota)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	gate := &core.Gate{}
	repo := credential.NewStore(host)
	client := newapi.NewClient(srv.Client())
	svc := account.NewService(repo, st, client, credential.NewBroker(repo, st, client, gate, time.Now), gate)
	h := NewHandler(svc, nil, st, nil)
	a, err := svc.Bind(context.Background(), account.BindInput{Site: srv.URL, Channel: "token", Token: "nap_t", CheckinMode: core.CheckinModeLogin})
	if err != nil {
		t.Fatal(err)
	}
	quota = 777
	r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "POST", Path: Base + "/balance", Body: []byte(`{}`)})
	if e != nil || r.StatusCode != 200 || !strings.Contains(string(r.Body), `"failed":{}`) {
		t.Fatal(e, r.StatusCode, string(r.Body))
	}
	if b := st.Read().Accounts[a.ID].BalanceQuota; b == nil || *b != 777 {
		t.Fatalf("balance not refreshed: %v", b)
	}
}

func TestStatsRouteValidation(t *testing.T) {
	h, _ := fixture(t)
	for _, q := range []url.Values{
		{}, {"range": {"1y"}}, {"range": {"7d"}, "extra": {"x"}}, {"range": {"7d"}, "refresh": {"2"}},
	} {
		r, _ := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: Base + "/stats", Query: q})
		if r.StatusCode != 400 {
			t.Fatalf("query %v accepted with %d", q, r.StatusCode)
		}
	}
}

func TestStatsRouteCacheFirst(t *testing.T) {
	host := testutil.NewHost(t)
	st, e := state.Open(testutil.Dir(t))
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"u","quota":100,"used_quota":10,"last_login_time":1}}`))
		case "/api/log/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"model_name":"m","prompt_tokens":3,"completion_tokens":1,"quota":500000,"created_at":1794816000,"other":"{\"cache_tokens\":2}"}],"total":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	gate := &core.Gate{}
	repo := credential.NewStore(host)
	client := newapi.NewClient(srv.Client())
	svc := account.NewService(repo, st, client, credential.NewBroker(repo, st, client, gate, time.Now), gate)
	h := NewHandler(svc, nil, st, nil)
	if _, err := svc.Bind(context.Background(), account.BindInput{Site: srv.URL, Channel: "token", Token: "nap_t", CheckinMode: core.CheckinModeLogin}); err != nil {
		t.Fatal(err)
	}
	get := func(q url.Values) string {
		r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: Base + "/stats", Query: q})
		if e != nil || r.StatusCode != 200 {
			t.Fatal(e, r.StatusCode, string(r.Body))
		}
		return string(r.Body)
	}
	body := get(url.Values{"range": {"24h"}})
	if !strings.Contains(body, `"from_cache":false`) || !strings.Contains(body, `"model":"m"`) || !strings.Contains(body, `"cache_tokens":2`) {
		t.Fatalf("live stats wrong: %s", body)
	}
	if body = get(url.Values{"range": {"24h"}}); !strings.Contains(body, `"from_cache":true`) {
		t.Fatalf("second call not served from cache: %s", body)
	}
	if body = get(url.Values{"range": {"24h"}, "refresh": {"1"}}); !strings.Contains(body, `"from_cache":false`) {
		t.Fatalf("refresh=1 not live: %s", body)
	}
}

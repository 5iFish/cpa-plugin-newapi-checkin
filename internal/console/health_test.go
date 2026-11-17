package console

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/account"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestHealthReadOnlyRoutesUseCache(t *testing.T) {
	h, st := fixture(t)
	if err := st.Update(func(v *state.Snapshot) error {
		v.Accounts["id"] = state.AccountRecord{ID: "id", Status: "ready"}
		v.Health["id"] = core.AccountHealth{ID: "id", Success: 3, Failed: 1, Complete: true, SyncedAt: time.Now(), CheckedAt: time.Now()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health", "/status"} {
		r, err := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "GET", Path: Base + path})
		if err != nil || r.StatusCode != 200 || !strings.Contains(string(r.Body), `"health":{"accounts":{"id":`) || !strings.Contains(string(r.Body), `"success":3`) || r.Headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("cached health absent from %s: %v %d %s", path, err, r.StatusCode, r.Body)
		}
	}
}

func TestHealthRouteRejectsUnexpectedQueryAndBody(t *testing.T) {
	h, _ := fixture(t)
	for _, req := range []pluginapi.ManagementRequest{
		{Method: "GET", Path: Base + "/health", Query: url.Values{"refresh": {"1"}}},
		{Method: "GET", Path: Base + "/health", Body: []byte(`{}`)},
		{Method: "POST", Path: Base + "/health", Query: url.Values{"site": {"https://other.test"}}, Body: []byte(`{}`)},
		{Method: "POST", Path: Base + "/health", Body: []byte(`{"refresh":1}`)},
		{Method: "POST", Path: Base + "/health", Body: []byte(`{"account_id":"other"}`)},
		{Method: "POST", Path: Base + "/health", Body: []byte(`{} {}`)},
	} {
		r, err := h.Handle(context.Background(), req)
		if err != nil || r.StatusCode != 400 {
			t.Fatalf("invalid health request accepted: %+v %v %d", req, err, r.StatusCode)
		}
	}
}

func TestHealthRefreshReturns202WithoutBlockingStatus(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"user","quota":100,"used_quota":1,"last_login_time":1}}`))
		case "/api/log/self":
			if hits.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	st, _ := state.Open(testutil.Dir(t))
	gate := &core.Gate{}
	repo := credential.NewStore(testutil.NewHost(t))
	client := newapi.NewClient(server.Client())
	svc := account.NewService(repo, st, client, credential.NewBroker(repo, st, client, gate, time.Now), gate)
	svc.StartHealth(context.Background())
	t.Cleanup(func() { _ = svc.ShutdownHealth(context.Background()) })
	if _, err := svc.Bind(context.Background(), account.BindInput{Site: server.URL, Channel: "token", Token: "nap_health-test", CheckinMode: core.CheckinModeLogin}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(svc, nil, st, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := h.Handle(ctx, pluginapi.ManagementRequest{Method: "POST", Path: Base + "/health", Body: []byte(`{"refresh":true}`)})
	if err != nil || r.StatusCode != 202 || !strings.Contains(string(r.Body), `"refreshing":true`) {
		t.Fatalf("health refresh blocked or did not queue: %v %d %s", err, r.StatusCode, r.Body)
	}
	<-entered
	for _, path := range []string{"/status", "/health"} {
		r, err = h.Handle(ctx, pluginapi.ManagementRequest{Method: "GET", Path: Base + path})
		if err != nil || r.StatusCode != 200 || hits.Load() != 1 {
			t.Fatalf("%s queried logs instead of cache: %v %d hits=%d", path, err, r.StatusCode, hits.Load())
		}
	}
	close(release)
	for deadline := time.Now().Add(2 * time.Second); svc.HealthStatus().Refreshing && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	r, err = h.Handle(ctx, pluginapi.ManagementRequest{Method: "POST", Path: Base + "/health", Body: []byte(`{}`)})
	if err != nil || r.StatusCode != 200 || hits.Load() != 2 {
		t.Fatalf("fresh cache unnecessarily refetched: %v %d hits=%d", err, r.StatusCode, hits.Load())
	}
	var body struct {
		Data struct {
			Health core.HealthSummary `json:"health"`
		} `json:"data"`
	}
	if json.Unmarshal(r.Body, &body) != nil || len(body.Data.Health.Accounts) != 1 {
		t.Fatal(fmt.Sprintf("invalid health payload: %s", r.Body))
	}
}

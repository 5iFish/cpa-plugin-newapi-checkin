package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/console"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func appManagement(t *testing.T, a *App, method, path string, body any) pluginapi.ManagementResponse {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(pluginapi.ManagementRequest{Method: method, Path: console.Base + path, Body: b})
	raw, err := a.Handle(context.Background(), "management.handle", req)
	if err != nil {
		t.Fatal(err)
	}
	var out pluginapi.ManagementResponse
	if json.Unmarshal(raw, &out) != nil {
		t.Fatal("invalid management response", string(raw))
	}
	return out
}

func TestHealthWorkerFollowsPluginLifetime(t *testing.T) {
	a := fixture(t)
	entered, canceled := make(chan struct{}, 4), make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"username":"user","quota":100,"used_quota":0,"last_login_time":1}}`))
		case "/api/log/self":
			entered <- struct{}{}
			<-r.Context().Done()
			canceled <- struct{}{}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	bound := appManagement(t, a, "POST", "/accounts", map[string]any{"site": server.URL, "channel": "token", "token": "nap_health-test", "checkin_mode": "login"})
	if bound.StatusCode != 200 && bound.StatusCode != 201 {
		t.Fatalf("could not bind account: %d %s", bound.StatusCode, bound.Body)
	}
	for i := 0; i < 2; i++ {
		response := appManagement(t, a, "POST", "/health", map[string]bool{"refresh": true})
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("plugin health worker did not start: %d %s", response.StatusCode, response.Body)
		}
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("health worker did not enter the request")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := a.Close(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("Close returned without canceling health HTTP work")
		}
		if len(a.state.Read().Health) != 0 {
			t.Fatal("canceled health work persisted a partial snapshot")
		}
		if i == 0 {
			raw, _ := json.Marshal(map[string]any{"config_yaml": []byte("enabled: false\n")})
			if _, err := a.Handle(context.Background(), "plugin.register", raw); err != nil {
				t.Fatal(err)
			}
		}
	}
}

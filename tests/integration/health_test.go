//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

func TestWorkflowHealthReadCacheAndRefresh(t *testing.T) {
	var logs atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"username":"health-user","quota":100,"used_quota":0,"last_login_time":1}}`))
		case "/api/user/checkin":
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":true,"stats":{"checked_in_today":true,"total_quota":0,"total_checkins":0,"records":[]}}}`))
		case "/api/log/self":
			logs.Add(1)
			if r.Header.Get("New-Api-User") != "7" || r.Header.Get("Authorization") != "Bearer nap_health-integration" {
				t.Error("health logs used the wrong account credential")
			}
			start, _ := strconv.ParseInt(r.URL.Query().Get("start_timestamp"), 10, 64)
			kind, _ := strconv.Atoi(r.URL.Query().Get("type"))
			if kind != 2 && kind != 5 {
				t.Errorf("unfiltered health log query: %v", r.URL.Query())
			}
			_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":[{"type":%d,"created_at":%d,"content":"private-health-error"}],"total":1}}`, kind, start+1800)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	h := newHost(t)
	code, body := h.call("POST", "/accounts", map[string]any{"site": server.URL, "channel": "token", "token": "nap_health-integration", "checkin_mode": "login"})
	if code != 200 {
		t.Fatalf("health account bind: %d %s", code, body)
	}
	var bound struct {
		Data core.AccountSummary `json:"data"`
	}
	if json.Unmarshal(body, &bound) != nil || bound.Data.ID == "" {
		t.Fatal("health account response missing identity")
	}
	code, body = h.call("GET", "/health", nil)
	if code != 200 || logs.Load() != 0 {
		t.Fatalf("reading health unexpectedly fetched site logs: %d %s hits=%d", code, body, logs.Load())
	}
	code, body = h.call("POST", "/health", map[string]bool{"refresh": true})
	if code != http.StatusAccepted {
		t.Fatalf("health refresh not queued: %d %s", code, body)
	}
	h.wait(10*time.Second, func() bool { return h.snapshot().Health[bound.Data.ID].Complete })
	a := h.snapshot().Health[bound.Data.ID]
	if a.Success != 1 || a.Failed != 1 || len(a.Buckets) != 48 || a.Buckets[1].Success != 1 || a.Buckets[1].Failed != 1 || logs.Load() != 2 {
		t.Fatalf("CPA health payload incorrect: %+v hits=%d", a, logs.Load())
	}
	for _, path := range []string{"/health", "/status"} {
		code, body = h.call("GET", path, nil)
		if code != 200 || logs.Load() != 2 || !bytes.Contains(body, []byte(`"health":`)) || bytes.Contains(body, []byte("nap_health-integration")) || bytes.Contains(body, []byte("private-health-error")) {
			t.Fatalf("unsafe or uncached %s response: %d %s hits=%d", path, code, body, logs.Load())
		}
	}
	res, err := http.Get(h.base + "/v0/management/plugins/newapi-checkin/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusForbidden {
		t.Fatalf("health route is not protected by CPA: %d", res.StatusCode)
	}
}

//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

func TestWorkflowUserID(t *testing.T) {
	s := site(t)
	original := s.server.Config.Handler
	s.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path == "/api/user/self" || r.URL.Path == "/api/user/checkin") && r.Header.Get("New-Api-User") != "1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"Unauthorized, New-Api-User header not provided"}`))
			return
		}
		original.ServeHTTP(w, r)
	})
	h := newHost(t)
	code, body := h.call("POST", "/accounts", map[string]any{
		"site": s.server.URL, "channel": "token", "token": "synthetic-legacy-token", "upstream_user_id": 1,
	})
	if code != 200 {
		t.Fatalf("binding with user ID: HTTP %d %s", code, body)
	}
	var response struct {
		Data core.AccountSummary `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Data.UpstreamUserID != 1 {
		t.Fatalf("verified identity missing: %s err=%v", body, err)
	}
	id := response.Data.ID
	h.restart()
	if code, body = h.call("POST", "/recheck", map[string]any{"account_id": id}); code != 200 {
		t.Fatalf("recheck after restart: HTTP %d %s", code, body)
	}
	h.submit(id)
	h.wait(15*time.Second, func() bool { r := h.snapshot().Accounts[id].LastResult; return r != nil && r.Status == "success" })
	if posts, _, _ := s.counts(); posts != 1 {
		t.Fatalf("expected one check-in, got %d", posts)
	}
	a := h.snapshot().Accounts[id]
	if a.UpstreamUserID != 1 || a.StatsStale || !a.WritePermissionVerified {
		t.Fatalf("saved account did not retain verified identity and statistics: %+v", a)
	}
}

package console

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/engine"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"encoding/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

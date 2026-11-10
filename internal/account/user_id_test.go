package account

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

func TestBindWithUpstreamUserIDAndRecheck(t *testing.T) {
	f := fixture(t)
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" && r.Header.Get("New-Api-User") != "1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"Unauthorized, New-Api-User header not provided"}`))
			return
		}
		original.ServeHTTP(w, r)
	})
	in := f.input()
	if err := json.Unmarshal([]byte(`{"upstream_user_id":1}`), &in); err != nil {
		t.Fatal(err)
	}
	a, err := f.s.Bind(context.Background(), in)
	if err != nil {
		t.Fatalf("bind with supplied user ID: %v", err)
	}
	saved, _, err := f.repo.Load(context.Background(), a.ID)
	if err != nil || saved.UpstreamUserID != 1 || f.posts != 0 {
		t.Fatalf("identity persistence or unexpected check-in: id=%d posts=%d err=%v", saved.UpstreamUserID, f.posts, err)
	}
	raw, _ := json.Marshal(a)
	var summary map[string]any
	_ = json.Unmarshal(raw, &summary)
	if summary["upstream_user_id"] != float64(1) {
		t.Fatal("rebind form cannot recover upstream user ID", string(raw))
	}
	if _, err = f.s.Recheck(context.Background(), a.ID); err != nil {
		t.Fatalf("recheck lost stored user ID: %v", err)
	}
	rebind := f.input()
	rebind.AccountID = a.ID
	if _, err = f.s.Bind(context.Background(), rebind); err != nil {
		t.Fatalf("rebind should reuse verified user ID: %v", err)
	}
}

func TestBindRejectsMismatchedUpstreamUserID(t *testing.T) {
	f := fixture(t)
	in := f.input()
	_ = json.Unmarshal([]byte(`{"upstream_user_id":99}`), &in)
	_, err := f.s.Bind(context.Background(), in)
	if core.ErrorKind(err) != "identity_mismatch" || f.host.Saves != 0 {
		t.Fatalf("wrong supplied identity must not be saved: %v saves=%d", err, f.host.Saves)
	}
}

func TestBindRejectsNegativeUpstreamUserID(t *testing.T) {
	f := fixture(t)
	in := f.input()
	_ = json.Unmarshal([]byte(`{"upstream_user_id":-1}`), &in)
	_, err := f.s.Bind(context.Background(), in)
	if core.ErrorKind(err) != "invalid_input" || f.host.Saves != 0 {
		t.Fatalf("invalid user ID accepted: %v saves=%d", err, f.host.Saves)
	}
}

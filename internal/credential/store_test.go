package credential

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func sample() core.Account {
	return core.Account{ID: core.AccountID("https://example.com", 1), Site: "https://example.com", UpstreamUserID: 1, Username: "u", Label: "l", Credential: core.Credential{Kind: "pat", Channel: "token", Token: "test-secret", Generation: 1}}
}
func saved(t *testing.T) (*Store, *testutil.Host, core.Account) {
	t.Helper()
	h := testutil.NewHost(t)
	s := NewStore(h)
	a := sample()
	if e := s.Save(context.Background(), a, Revision{}); e != nil {
		t.Fatal(e)
	}
	return s, h, a
}
func TestAuthSaveEnvelopeAndReadback(t *testing.T) {
	s, h, a := saved(t)
	got, r, e := s.Load(context.Background(), a.ID)
	if e != nil || got.Credential.Token != "test-secret" || r.Generation != 1 || !r.MustExist {
		t.Fatal(got, r, e)
	}
	if h.Saves != 1 || filepath.Base(r.PhysicalPath) != "newapi-checkin-"+a.ID+".json" {
		t.Fatal(r)
	}
	h.WriteThenFail = true
	a.Credential.Generation = 2
	if s.Save(context.Background(), a, r) == nil {
		t.Fatal("host failure reported success")
	}
}
func TestOwnerAndSchemaFilter(t *testing.T) {
	s, h, a := saved(t)
	p := filepath.Join(h.Dir, "newapi-checkin-"+a.ID+".json")
	raw, _ := os.ReadFile(p)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["plugin_owner"] = "foreign"
	raw, _ = json.Marshal(m)
	_ = os.WriteFile(p, raw, 0600)
	list, e := s.List(context.Background())
	if e != nil || len(list) != 0 {
		t.Fatal(list, e)
	}
}
func TestPreserveUnknownJSON(t *testing.T) {
	s, h, a := saved(t)
	p := filepath.Join(h.Dir, "newapi-checkin-"+a.ID+".json")
	b, _ := os.ReadFile(p)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	m["future"] = json.RawMessage(`{"large":9007199254740993}`)
	b, _ = json.Marshal(m)
	_ = os.WriteFile(p, b, 0600)
	_, rev, e := s.Load(context.Background(), a.ID)
	if e != nil {
		t.Fatal(e)
	}
	a.Credential.Generation++
	if e = s.Save(context.Background(), a, rev); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(p)
	_ = json.Unmarshal(b, &m)
	if string(m["future"]) != `{"large":9007199254740993}` {
		t.Fatal(string(b))
	}
}
func TestStableRebindID(t *testing.T) {
	a := sample()
	id := a.ID
	a.Label = "new"
	a.Credential.Token = "new-secret"
	if core.AccountID(a.Site, a.UpstreamUserID) != id {
		t.Fatal("identity changed")
	}
}
func TestExternalChangeBeforeSaveAborts(t *testing.T) {
	for _, mode := range []string{"delete", "disable", "modify"} {
		t.Run(mode, func(t *testing.T) {
			s, h, a := saved(t)
			_, r, e := s.Load(context.Background(), a.ID)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "delete":
				e = os.Remove(r.PhysicalPath)
			case "disable", "modify":
				b, _ := os.ReadFile(r.PhysicalPath)
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				if mode == "disable" {
					m["disabled"] = true
				} else {
					m["label"] = "outside"
				}
				b, _ = json.Marshal(m)
				e = os.WriteFile(r.PhysicalPath, b, 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			h.Saves = 0
			a.Credential.Generation++
			if e = s.Save(context.Background(), a, r); core.ErrorKind(e) != "external_change" || h.Saves != 0 {
				t.Fatal(e, h.Saves)
			}
		})
	}
}
func TestDeleteRejectsForeignFile(t *testing.T) {
	s, _, a := saved(t)
	_, r, _ := s.Load(context.Background(), a.ID)
	b, _ := os.ReadFile(r.PhysicalPath)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["plugin_owner"] = "foreign"
	b, _ = json.Marshal(m)
	_ = os.WriteFile(r.PhysicalPath, b, 0600)
	if s.Delete(context.Background(), a.ID) == nil {
		t.Fatal("foreign deleted")
	}
	if _, e := os.Stat(r.PhysicalPath); e != nil {
		t.Fatal(e)
	}
}

func TestDeleteRejectsSymlink(t *testing.T) {
	s, _, a := saved(t)
	_, revision, e := s.Load(context.Background(), a.ID)
	if e != nil {
		t.Fatal(e)
	}
	original := revision.PhysicalPath + ".original"
	if e = os.Rename(revision.PhysicalPath, original); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(original, revision.PhysicalPath); e != nil {
		t.Skipf("symlink creation unavailable on this platform: %v", e)
	}
	if e = s.Delete(context.Background(), a.ID); e == nil {
		t.Fatal("symlink was accepted for deletion")
	}
	if _, e = os.Lstat(revision.PhysicalPath); e != nil {
		t.Fatal("symlink was removed", e)
	}
	if _, e = os.Stat(original); e != nil {
		t.Fatal("symlink target was removed", e)
	}
}

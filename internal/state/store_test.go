package state

import (
	"bytes"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStateReplaceFailureKeepsOldData(t *testing.T) {
	dir := testutil.Dir(t)
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Update(func(v *Snapshot) error { v.Error = "first"; return nil }); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	s.replace = func(string, string) error { return errors.New("injected failure") }
	if s.Update(func(v *Snapshot) error { v.Error = "second"; return nil }) == nil {
		t.Fatal("save should fail")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if !bytes.Equal(before, after) || s.Read().Error != "first" {
		t.Fatal("old state lost")
	}
}
func TestStateReplacesExistingFile(t *testing.T) {
	s, e := Open(testutil.Dir(t))
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"first", "second"} {
		if e = s.Update(func(s *Snapshot) error { s.Error = v; return nil }); e != nil {
			t.Fatal(e)
		}
	}
	r, e := Open(s.dir)
	if e != nil || r.Read().Error != "second" {
		t.Fatal(e)
	}
}
func TestCorruptStateStopsTasks(t *testing.T) {
	dir := testutil.Dir(t)
	p := filepath.Join(dir, "state.json")
	_ = os.WriteFile(p, []byte("broken"), 0600)
	if _, e := Open(dir); e == nil {
		t.Fatal("corrupt file silently reset")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "broken" {
		t.Fatal("corrupt original overwritten")
	}
}
func TestStateContainsNoSecrets(t *testing.T) {
	s, e := Open(testutil.Dir(t))
	if e != nil {
		t.Fatal(e)
	}
	_ = s.Update(func(v *Snapshot) error {
		v.Accounts["id"] = AccountRecord{ID: "id", Site: "https://site.example", Status: "ready"}
		return nil
	})
	b, _ := os.ReadFile(filepath.Join(s.dir, "state.json"))
	for _, word := range []string{"access_token", "refresh_token", "password", "credential", "management_key"} {
		if bytes.Contains(b, []byte(`"`+word+`"`)) {
			t.Fatal(word)
		}
	}
	v := s.Read()
	delete(v.Accounts, "id")
	if len(s.Read().Accounts) != 1 {
		t.Fatal("mutable snapshot escaped")
	}
}

func TestCancelWorkSettlesParentJobs(t *testing.T) {
	s := Snapshot{}
	s.init()
	s.WorkItems["one"] = WorkItem{ID: "one", AccountID: "a", Status: "running"}
	s.WorkItems["two"] = WorkItem{ID: "two", AccountID: "b", Status: "queued"}
	s.Jobs["single"] = Job{ID: "single", Status: "running", WorkIDs: []string{"one"}}
	s.Jobs["batch"] = Job{ID: "batch", Status: "running", WorkIDs: []string{"one", "two"}}
	s.CancelWork("a")
	if s.Jobs["single"].Status != "failed" {
		t.Fatal("cancelled account left its job running forever")
	}
	if s.Jobs["batch"].Status != "running" {
		t.Fatal("other account still has active work")
	}
	s.CancelWork("b")
	if s.Jobs["batch"].Status != "failed" {
		t.Fatal("canceled batch never completed")
	}
}

package state

import (
	"bytes"
	"errors"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
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
func TestRecordCarriesCheckinModeAndRewardSnapshot(t *testing.T) {
	a := core.Account{ID: "x", Site: "https://example.com", UpstreamUserID: 1, Username: "u", CheckinMode: core.CheckinModeLogin, Credential: core.Credential{Kind: "pat", Channel: "token", Token: "tok", Generation: 1}}
	r := Record(a, "/p", "ready")
	if r.CheckinMode != core.CheckinModeLogin {
		t.Fatal(r.CheckinMode)
	}
	if s := r.Summary(); s.CheckinMode != core.CheckinModeLogin {
		t.Fatal(s.CheckinMode)
	}
	// An unset mode is normalized to the standard API mode so stored state never stays ambiguous.
	if m := Record(core.Account{Credential: core.Credential{Kind: "pat", Generation: 1}}, "/p", "ready").CheckinMode; m != core.CheckinModeAPI {
		t.Fatal(m)
	}
}

func TestRewardSnapshotPersistsThroughStore(t *testing.T) {
	s, e := Open(testutil.Dir(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Update(func(v *Snapshot) error {
		r := v.Accounts["id"]
		r.CheckinMode = core.CheckinModeLogin
		r.RewardTotal = 62500000
		r.RewardLastLogin = 1794654511
		r.RewardDayKey = "2026-11-15"
		r.RewardState = core.RewardStateSuccess
		r.RewardBaseline = true
		r.RewardReceipt = true
		r.RewardAttemptDayKey = "2026-11-15"
		r.RewardAttemptAt = 1794700000
		v.Accounts["id"] = r
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	got := s.Read().Accounts["id"]
	if got.RewardTotal != 62500000 || got.RewardLastLogin != 1794654511 || got.RewardDayKey != "2026-11-15" || got.RewardState != core.RewardStateSuccess {
		t.Fatal(got)
	}
	if !got.RewardBaseline || !got.RewardReceipt || got.RewardAttemptDayKey != "2026-11-15" || got.RewardAttemptAt != 1794700000 {
		t.Fatal("login attempt and baseline evidence missing", got)
	}
	if got.Summary().RewardState != core.RewardStateSuccess {
		t.Fatal("reward state missing from summary")
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
	for _, word := range []string{"access_token", "refresh_token", "password", "credential", "management_key", "oauth_cookie"} {
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

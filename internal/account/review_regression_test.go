package account

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestReviewRebindMissingCredentialFile(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()
	a, err := f.s.Bind(ctx, f.input())
	if err != nil {
		t.Fatal(err)
	}
	if err = f.state.Update(func(v *state.Snapshot) error {
		v.History = append(v.History, state.HistoryEntry{AccountID: a.ID, Result: core.AttemptResult{Status: "success", ObservedAt: time.Now()}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, revision, err := f.repo.Load(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(revision.PhysicalPath); err != nil {
		t.Fatal(err)
	}
	if err = f.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.AccountID = a.ID
	_, err = f.s.Bind(ctx, in)
	if err != nil {
		t.Fatalf("UI rebind cannot repair missing file; account status=%s error=%v", f.state.Read().Accounts[a.ID].Status, err)
	}
	rebound, _, err := f.repo.Load(ctx, a.ID)
	if err != nil || rebound.Credential.Generation != 2 || len(f.state.Read().History) != 1 {
		t.Fatal("repair lost identity generation or history", err)
	}
}

func TestReviewDeleteFailureKeepsAccountVisible(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()
	a, err := f.s.Bind(ctx, f.input())
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := f.repo.Load(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a credential file left incomplete by a failed host write.
	if err = os.WriteFile(revision.PhysicalPath, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	err = f.s.Delete(ctx, a.ID)
	if err == nil {
		t.Fatal("expected validation failure")
	}
	_, statErr := os.Stat(revision.PhysicalPath)
	got := f.state.Read().Accounts[a.ID].Status
	if got != "delete_failed" || !f.state.Read().DeletePending[a.ID] {
		t.Fatalf("failed deletion is filtered out of UI: status=%s file_exists=%v error=%v", got, statErr == nil, err)
	}
	if _, err = f.s.Recheck(ctx, a.ID); core.ErrorKind(err) != "delete_pending" {
		t.Fatal("recheck resumed a pending deletion", err)
	}
	if f.state.Read().Accounts[a.ID].Status != "delete_failed" {
		t.Fatal("retry diagnostic was hidden")
	}
	// Once the operator has removed the untrusted corrupt file, retry confirms absence.
	if err = os.Remove(revision.PhysicalPath); err != nil {
		t.Fatal(err)
	}
	if err = f.s.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if f.state.Read().Accounts[a.ID].Status != "deleted" {
		t.Fatal("deletion not finalized")
	}
}

func TestReviewRecheckDoesNotRestoreMissingWriteScope(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()
	a, err := f.s.Bind(ctx, f.input())
	if err != nil {
		t.Fatal(err)
	}
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/user/checkin" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED"}`))
			return
		}
		original.ServeHTTP(w, r)
	})
	_, err = f.s.client.Checkin(ctx, f.server.URL, "nap_test-secret", 1)
	if core.ErrorKind(err) != "permission_denied" {
		t.Fatal(err)
	}
	if err = f.state.Update(func(v *state.Snapshot) error {
		r := v.Accounts[a.ID]
		r.Status = "permission_denied"
		v.Accounts[a.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	checked, err := f.s.Recheck(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status == "ready" {
		t.Fatal("unchanged PAT without wallet:write is marked ready after read-only GET validation")
	}
	in := f.input()
	in.AccountID = a.ID
	in.Token = "nap_replacement"
	checked, err = f.s.Bind(ctx, in)
	if err != nil || checked.Status != "ready" || f.state.Read().Accounts[a.ID].PermissionDenied {
		t.Fatal("explicit rebind did not reset denied permission", err)
	}
}

func TestRebindDoesNotOverwriteCorruptCredential(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()
	a, err := f.s.Bind(ctx, f.input())
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := f.repo.Load(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(revision.PhysicalPath, []byte("untrusted-corrupt-content"), 0600); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.AccountID = a.ID
	if _, err = f.s.Bind(ctx, in); core.ErrorKind(err) != "credential_unreadable" {
		t.Fatal(err)
	}
	b, err := os.ReadFile(revision.PhysicalPath)
	if err != nil || string(b) != "untrusted-corrupt-content" {
		t.Fatal("corrupt file overwritten", err)
	}
}

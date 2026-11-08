package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatchRetriesArePerAccount(t *testing.T) {
	f := fixture(t)
	var badGets, goodPosts atomic.Int32
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false}}`))
			return
		}
		if r.Header.Get("Authorization") == "Bearer nap_test-secret" {
			badGets.Add(1)
			http.Error(w, "temporary failure", 503)
			return
		}
		var data any
		if r.Method == "POST" {
			goodPosts.Add(1)
			data = map[string]any{"quota_awarded": 3, "checkin_date": "2026-11-08"}
		} else {
			data = map[string]any{"enabled": true, "stats": map[string]any{"checked_in_today": goodPosts.Load() > 0, "total_quota": 90, "total_checkins": 4, "records": []any{}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	})
	a := core.Account{ID: core.AccountID(f.server.URL, 2), Site: f.server.URL, UpstreamUserID: 2, Username: "second", Credential: core.Credential{Kind: "pat", Token: "nap_other", Generation: 1}}
	if e := f.repo.Save(context.Background(), a, credential.Revision{}); e != nil {
		t.Fatal(e)
	}
	if e := f.state.Update(func(s *state.Snapshot) error { s.Accounts[a.ID] = state.Record(a, "", "ready"); return nil }); e != nil {
		t.Fatal(e)
	}
	clock := &fixedClock{now: time.Now().UTC()}
	j := NewJobs(f.runner, f.state, clock)
	job, e := j.Submit(context.Background(), []string{f.id, a.ID}, "manual")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 6; i++ {
		if e := j.Step(context.Background()); e != nil {
			t.Fatal(e)
		}
		clock.now = clock.now.Add(10 * time.Minute)
	}
	snapshot := f.state.Read()
	for _, w := range snapshot.WorkItems {
		if w.AccountID == a.ID && (w.Status != "completed" || w.Attempt != 1) {
			t.Fatal("healthy account retried", w)
		}
		if w.AccountID == f.id && (w.Status != "failed" || w.Attempt != 3) {
			t.Fatal("failed account budget not exhausted", w)
		}
	}
	if badGets.Load() != 3 || goodPosts.Load() != 1 || snapshot.Jobs[job].Status != "failed" {
		t.Fatal("wrong independent budget", badGets.Load(), goodPosts.Load())
	}
}

func TestCanceledWorkWaitingForGateDoesNotUseReboundCredential(t *testing.T) {
	f := fixture(t)
	j := NewJobs(f.runner, f.state, core.RealClock{})
	if _, e := j.Submit(context.Background(), []string{f.id}, "manual"); e != nil {
		t.Fatal(e)
	}
	locked, release, gateDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		gateDone <- f.gate.Do(context.Background(), func() error { close(locked); <-release; return nil })
	}()
	<-locked
	stepDone := make(chan error, 1)
	go func() { stepDone <- j.Step(context.Background()) }()
	deadline := time.Now().Add(2 * time.Second)
	running := false
	for time.Now().Before(deadline) {
		for _, w := range f.state.Read().WorkItems {
			running = w.Status == "running"
		}
		if running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !running {
		close(release)
		t.Fatal("work did not enter the gate queue")
	}
	// Rebinding owns this same gate and invalidates the previous work generation.
	err := f.state.Update(func(s *state.Snapshot) error {
		s.CancelWork(f.id)
		a := s.Accounts[f.id]
		a.LastGeneration++
		s.Accounts[f.id] = a
		return nil
	})
	close(release)
	if e := <-gateDone; e != nil {
		t.Fatal(e)
	}
	if e := <-stepDone; e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatal(err)
	}
	if f.posts.Load() != 0 || f.gets.Load() != 0 {
		t.Fatal("canceled old work contacted the site", f.posts.Load(), f.gets.Load())
	}
	if len(f.state.Read().History) != 0 {
		t.Fatal("canceled work overwrote account history")
	}
}

func TestCanceledPostReconcilesOnRestart(t *testing.T) {
	f := fixture(t)
	posted := make(chan struct{})
	var requests atomic.Int32
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false}}`))
			return
		}
		if r.Method == "POST" {
			_, _ = io.Copy(io.Discard, r.Body)
			requests.Add(1)
			close(posted)
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"enabled": true, "stats": map[string]any{"checked_in_today": requests.Load() > 0, "total_quota": 10, "total_checkins": 1, "records": []any{}}}})
	})
	j := NewJobs(f.runner, f.state, core.RealClock{})
	if _, e := j.Submit(context.Background(), []string{f.id}, "manual"); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- j.Step(ctx) }()
	select {
	case <-posted:
	case <-ctx.Done():
		t.Fatal("POST never reached server")
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	for _, w := range f.state.Read().WorkItems {
		if w.Status != "retry" || !w.NeedsReconcile || w.Attempt != 1 {
			t.Fatal("ambiguous POST not recoverable", w)
		}
	}
	restarted := NewJobs(f.runner, f.state, core.RealClock{})
	if e := restarted.Recover(); e != nil {
		t.Fatal(e)
	}
	if e := restarted.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	if requests.Load() != 1 || f.state.Read().Accounts[f.id].LastResult.Status != "already" {
		t.Fatal("canceled POST was replayed")
	}
}

func TestExternalCredentialDisableStopsRun(t *testing.T) {
	f := fixture(t)
	j := NewJobs(f.runner, f.state, core.RealClock{})
	if _, e := j.Submit(context.Background(), []string{f.id}, "manual"); e != nil {
		t.Fatal(e)
	}
	_, rev, e := f.repo.Load(context.Background(), f.id)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(rev.PhysicalPath)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	v["disabled"] = true
	b, _ = json.Marshal(v)
	if e = os.WriteFile(rev.PhysicalPath, b, 0600); e != nil {
		t.Fatal(e)
	}
	if e = j.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	if f.posts.Load() != 0 || f.gets.Load() != 0 || f.state.Read().Accounts[f.id].Status != "disabled" {
		t.Fatal("externally disabled credential was used")
	}
}

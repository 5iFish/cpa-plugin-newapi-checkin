package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureData struct {
	runner                             *Runner
	repo                               *credential.Store
	state                              *state.Store
	gate                               *core.Gate
	id                                 string
	posts, gets                        atomic.Int32
	checked, lose, statsFail, httpFail bool
	server                             *httptest.Server
}

func fixture(t *testing.T) *fixtureData {
	f := &fixtureData{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		if r.URL.Path == "/api/status" {
			data = map[string]any{"turnstile_check": false}
		} else if r.Method == "GET" {
			f.gets.Add(1)
			if f.statsFail && f.posts.Load() > 0 {
				http.Error(w, "failure", 500)
				return
			}
			data = map[string]any{"enabled": true, "stats": map[string]any{"checked_in_today": f.checked, "total_quota": 12345, "total_checkins": 99, "records": []any{}}}
		} else {
			f.posts.Add(1)
			if f.httpFail {
				_, _ = w.Write([]byte(`{"success":false,"message":"opaque failure"}`))
				return
			}
			f.checked = true
			if f.lose {
				c, _, _ := w.(http.Hijacker).Hijack()
				_ = c.Close()
				return
			}
			data = map[string]any{"quota_awarded": 7, "checkin_date": "2026-10-31"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	t.Cleanup(f.server.Close)
	h := testutil.NewHost(t)
	f.repo = credential.NewStore(h)
	f.state, _ = state.Open(testutil.Dir(t))
	a := core.Account{ID: core.AccountID(f.server.URL, 1), Site: f.server.URL, UpstreamUserID: 1, Username: "u", Credential: core.Credential{Kind: "pat", Channel: "token", Token: "nap_test-secret", Generation: 1}}
	if e := f.repo.Save(context.Background(), a, credential.Revision{}); e != nil {
		t.Fatal(e)
	}
	f.id = a.ID
	_ = f.state.Update(func(s *state.Snapshot) error { s.Accounts[a.ID] = state.Record(a, "", "ready"); return nil })
	client := newapi.NewClient(f.server.Client())
	f.gate = &core.Gate{}
	b := credential.NewBroker(f.repo, f.state, client, f.gate, time.Now)
	f.runner = NewRunner(b, client, f.state, core.RealClock{})
	return f
}
func TestAlreadySkipsPost(t *testing.T) {
	f := fixture(t)
	f.checked = true
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != "already" || f.posts.Load() != 0 || r.ServerDate != "" {
		t.Fatal(r, e)
	}
}
func TestLostCheckinResponseQueriesBeforeRetry(t *testing.T) {
	f := fixture(t)
	f.lose = true
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e == nil || !r.NeedsReconcile {
		t.Fatal(r, e)
	}
	r, e = f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.Status != "already" || f.posts.Load() != 1 || f.gets.Load() != 2 {
		t.Fatal(r, e, f.posts.Load())
	}
}
func TestStatsUsesServerTotalQuota(t *testing.T) {
	f := fixture(t)
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e != nil || r.ServerDate != "2026-10-31" || r.QuotaAwarded != 7 {
		t.Fatal(r, e)
	}
	if a := f.state.Read().Accounts[f.id]; a.TotalQuota != 12345 || a.TotalCheckins != 99 {
		t.Fatal(a)
	}
}
func TestStatsFailureKeepsStaleTimestamp(t *testing.T) {
	f := fixture(t)
	f.statsFail = true
	old := time.Now().Add(-time.Hour).UTC()
	_ = f.state.Update(func(s *state.Snapshot) error {
		a := s.Accounts[f.id]
		a.StatsSyncedAt = old
		s.Accounts[f.id] = a
		return nil
	})
	r, e := f.runner.Attempt(context.Background(), f.id)
	a := f.state.Read().Accounts[f.id]
	if e != nil || r.Status != "success" || !a.StatsStale {
		t.Fatal(r, e, a)
	}
	if a.TotalQuota != 12345 {
		t.Fatal("initial GET totals were not cached")
	}
}
func TestHTTP200FailureNotSuccess(t *testing.T) {
	f := fixture(t)
	f.httpFail = true
	r, e := f.runner.Attempt(context.Background(), f.id)
	if e == nil || r.Status == "success" {
		t.Fatal(r, e)
	}
}
func TestConcurrentManualAndScheduledCoalesce(t *testing.T) {
	f := fixture(t)
	j := NewJobs(f.runner, f.state, core.RealClock{})
	one, e := j.Submit(context.Background(), []string{f.id}, "manual")
	if e != nil {
		t.Fatal(e)
	}
	two, e := j.Submit(context.Background(), []string{f.id}, "scheduled")
	s := f.state.Read()
	if e != nil || one == two || len(s.WorkItems) != 1 || s.Jobs[one].WorkIDs[0] != s.Jobs[two].WorkIDs[0] {
		t.Fatal(s, e)
	}
}
func TestThreeAttemptBudget(t *testing.T) {
	if n := RetryDelay(1, 0, 0); n != 30*time.Second {
		t.Fatal(n)
	}
	if n := RetryDelay(2, 0, 0); n != 120*time.Second {
		t.Fatal(n)
	}
	if Retryable(core.Error("permission_denied")) || !Retryable(core.Error("result_unknown")) {
		t.Fatal("classification")
	}
}
func TestRetryAfterNotEarlier(t *testing.T) {
	if RetryDelay(1, 5*time.Minute, 0) < 5*time.Minute {
		t.Fatal("early retry")
	}
}
func TestFailureDoesNotLockRestOfDay(t *testing.T) {
	f := fixture(t)
	j := NewJobs(f.runner, f.state, core.RealClock{})
	_, _ = j.Submit(context.Background(), []string{f.id}, "manual")
	_ = f.state.Update(func(s *state.Snapshot) error {
		for id, w := range s.WorkItems {
			w.Status = "failed"
			w.Attempt = 3
			s.WorkItems[id] = w
		}
		return nil
	})
	_, e := j.Submit(context.Background(), []string{f.id}, "manual")
	if e != nil || len(f.state.Read().WorkItems) != 2 {
		t.Fatal(e)
	}
}
func TestDeleteStopsQueuedWork(t *testing.T) {
	f := fixture(t)
	j := NewJobs(f.runner, f.state, core.RealClock{})
	_, _ = j.Submit(context.Background(), []string{f.id}, "manual")
	_ = f.state.Update(func(s *state.Snapshot) error { s.DeletePending[f.id] = true; return nil })
	if e := j.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	if f.posts.Load() != 0 || f.gets.Load() != 0 {
		t.Fatal("deleted account contacted")
	}
}

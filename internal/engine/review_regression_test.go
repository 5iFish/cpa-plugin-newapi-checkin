package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"net/http"
	"testing"
	"time"
)

func TestReviewDailyScheduleTakesOverOldRetry(t *testing.T) {
	f := fixture(t)
	now := time.Date(2026, 11, 9, 9, 1, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	oldRetry := now.Add(30 * time.Second)
	if err := f.state.Update(func(s *state.Snapshot) error {
		s.WorkItems["old"] = state.WorkItem{ID: "old", AccountID: f.id, Status: "retry", Attempt: 2, NextAttemptAt: oldRetry, NeedsReconcile: true, Generation: 1}
		s.Schedules[f.id] = state.ScheduleEntry{AccountID: f.id, ScheduleDate: "2026-11-08", PlannedAt: now.Add(-24 * time.Hour), WorkID: "old"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	jobs := NewJobs(f.runner, f.state, clock)
	scheduler := NewScheduler(f.repo, f.state, jobs, clock, func(int) int { return 0 })
	settings := config.Defaults()
	settings.Timezone = "UTC"
	settings.JitterMinutes = 0
	if err := scheduler.Tick(context.Background(), settings, false); err != nil {
		t.Fatal(err)
	}
	queued := f.state.Read()
	next := queued.WorkItems[queued.Schedules[f.id].WorkID]
	if next.ID == "old" || next.Attempt != 0 || !next.NeedsReconcile || next.NextAttemptAt.Before(oldRetry) || queued.WorkItems["old"].Status != "canceled" {
		t.Fatal("daily takeover did not retain reconciliation and retry deadline", next)
	}
	if err := jobs.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.gets.Load() != 0 || f.posts.Load() != 0 {
		t.Fatal("new round ignored previous Retry-After")
	}
	// Keep Retry-After respected, then fail just the carried-over attempt.
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "temporary outage", 503) })
	clock.now = oldRetry
	if err := jobs.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.server.Config.Handler = original
	clock.now = now.Add(time.Hour)
	if err := scheduler.Tick(context.Background(), settings, false); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := f.state.Read()
	entry := snap.Schedules[f.id]
	work := snap.WorkItems[entry.WorkID]
	if entry.WorkID == "old" || work.Status != "completed" || work.Attempt != 2 || f.posts.Load() != 1 {
		t.Fatalf("today consumed yesterday's exhausted budget: date=%s work=%s attempt=%d status=%s; after site recovery GETs=%d POSTs=%d", entry.ScheduleDate, entry.WorkID, work.Attempt, work.Status, f.gets.Load(), f.posts.Load())
	}
}

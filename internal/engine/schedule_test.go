package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }
func (c *fixedClock) Sleep(ctx context.Context, d time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}
func TestDSTMissingAndRepeatedHour(t *testing.T) {
	s := config.Defaults()
	s.Timezone = "America/New_York"
	s.JitterMinutes = 0
	s.CheckinAt = "02:30"
	date, _ := time.Parse(time.RFC3339, "2026-03-08T12:00:00Z")
	got, e := PlanDaily("a", date, s, func(int) int { return 0 })
	if e != nil || got.UTC().Format(time.RFC3339) != "2026-03-08T07:00:00Z" {
		t.Fatal(got, e)
	}
	s.CheckinAt = "01:30"
	date, _ = time.Parse(time.RFC3339, "2026-11-01T12:00:00Z")
	got, e = PlanDaily("a", date, s, func(int) int { return 0 })
	if e != nil || got.UTC().Format(time.RFC3339) != "2026-11-01T05:30:00Z" {
		t.Fatal(got, e)
	}
}
func TestDailyJitterPersistedAcrossRestart(t *testing.T) {
	f := fixture(t)
	clock := &fixedClock{now: time.Date(2026, 11, 8, 1, 0, 0, 0, time.UTC)}
	jobs := NewJobs(f.runner, f.state, clock)
	s := NewScheduler(f.repo, f.state, jobs, clock, func(n int) int { return n - 1 })
	settings := config.Defaults()
	settings.Timezone = "UTC"
	if e := s.Tick(context.Background(), settings, true); e != nil {
		t.Fatal(e)
	}
	first := f.state.Read().Schedules[f.id].PlannedAt
	s2 := NewScheduler(f.repo, f.state, jobs, clock, func(int) int { return 0 })
	if e := s2.Tick(context.Background(), settings, true); e != nil {
		t.Fatal(e)
	}
	if !f.state.Read().Schedules[f.id].PlannedAt.Equal(first) {
		t.Fatal("resampled")
	}
}
func TestStartupCatchupWithinTwoToFiveMinutes(t *testing.T) {
	f := fixture(t)
	clock := &fixedClock{now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	s := NewScheduler(f.repo, f.state, NewJobs(f.runner, f.state, clock), clock, func(int) int { return 0 })
	settings := config.Defaults()
	settings.Timezone = "UTC"
	if e := s.Tick(context.Background(), settings, true); e != nil {
		t.Fatal(e)
	}
	d := f.state.Read().Schedules[f.id].PlannedAt.Sub(clock.now)
	if d < 2*time.Minute || d > 5*time.Minute {
		t.Fatal(d)
	}
}
func TestRetryBudgetSurvivesRestart(t *testing.T) {
	f := fixture(t)
	_ = f.state.Update(func(s *state.Snapshot) error {
		s.WorkItems["w"] = state.WorkItem{ID: "w", AccountID: f.id, Status: "running", Attempt: 2, NeedsReconcile: true, NextAttemptAt: time.Now().Add(time.Hour)}
		return nil
	})
	j := NewJobs(f.runner, f.state, core.RealClock{})
	if e := j.Recover(); e != nil {
		t.Fatal(e)
	}
	w := f.state.Read().WorkItems["w"]
	if w.Attempt != 2 || !w.NeedsReconcile || w.Status != "retry" {
		t.Fatal(w)
	}
}
func TestShutdownCancelsHTTPAndTimers(t *testing.T) {
	f := fixture(t)
	clock := &fixedClock{now: time.Date(2026, 11, 8, 1, 0, 0, 0, time.UTC)}
	s := NewScheduler(f.repo, f.state, NewJobs(f.runner, f.state, clock), clock, func(int) int { return 0 })
	settings := config.Defaults()
	settings.Timezone = "UTC"
	if e := s.Reconfigure(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	if e := s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
}

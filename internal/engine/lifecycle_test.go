package engine

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type drainingClock struct {
	active, maximum   atomic.Int32
	entered, canceled chan struct{}
	release           chan struct{}
}

func (c *drainingClock) Now() time.Time { return time.Date(2026, 11, 8, 1, 0, 0, 0, time.UTC) }
func (c *drainingClock) Sleep(ctx context.Context, _ time.Duration) error {
	n := c.active.Add(1)
	for old := c.maximum.Load(); n > old && !c.maximum.CompareAndSwap(old, n); old = c.maximum.Load() {
	}
	defer c.active.Add(-1)
	c.entered <- struct{}{}
	<-ctx.Done()
	c.canceled <- struct{}{}
	<-c.release
	return ctx.Err()
}

func TestReconfigureWaitsForOldWorker(t *testing.T) {
	f := fixture(t)
	clock := &drainingClock{entered: make(chan struct{}, 10), canceled: make(chan struct{}, 10), release: make(chan struct{})}
	s := NewScheduler(f.repo, f.state, NewJobs(f.runner, f.state, clock), clock, func(int) int { return 0 })
	settings := config.Defaults()
	settings.Timezone = "UTC"
	if e := s.Reconfigure(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-clock.entered:
		case <-ctx.Done():
			close(clock.release)
			t.Fatal("workers did not start")
		}
	}
	done := make(chan error, 1)
	settings.CheckinAt = "10:00"
	go func() { done <- s.Reconfigure(ctx, settings) }()
	select {
	case <-clock.canceled:
	case <-ctx.Done():
		close(clock.release)
		t.Fatal("old workers were not canceled")
	}
	select {
	case e := <-done:
		close(clock.release)
		t.Fatal("reconfigure returned before worker drained", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(clock.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-clock.entered:
		case <-ctx.Done():
			t.Fatal("replacement workers did not start")
		}
	}
	if clock.maximum.Load() > 2 {
		t.Fatal("overlapping scheduler generations", clock.maximum.Load())
	}
	if e := s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	if clock.active.Load() != 0 {
		t.Fatal("workers still running after shutdown")
	}
}

func TestShutdownCancelsInFlightHTTP(t *testing.T) {
	f := fixture(t)
	entered, canceled := make(chan struct{}), make(chan struct{})
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	})
	j := NewJobs(f.runner, f.state, core.RealClock{})
	if _, e := j.Submit(context.Background(), []string{f.id}, "manual"); e != nil {
		t.Fatal(e)
	}
	s := NewScheduler(f.repo, f.state, j, core.RealClock{}, func(int) int { return 0 })
	settings := config.Defaults()
	settings.Timezone = "UTC"
	if e := s.Reconfigure(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("HTTP operation never began")
	}
	if e := s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("HTTP request survived scheduler shutdown")
	}
}

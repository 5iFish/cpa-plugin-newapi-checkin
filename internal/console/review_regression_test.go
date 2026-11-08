package console

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"sync"
	"testing"
	"time"
)

func TestConcurrentSettingsPatchesKeepBothFields(t *testing.T) {
	h, s := fixture(t)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var mu sync.Mutex
	h.applySettings = func(ctx context.Context, o config.Overrides) error {
		entered <- struct{}{}
		<-release
		mu.Lock()
		defer mu.Unlock()
		return s.Update(func(v *state.Snapshot) error { v.SettingsOverrides = o; return nil })
	}
	done := make(chan int, 2)
	send := func(body string) {
		r, e := h.Handle(context.Background(), pluginapi.ManagementRequest{Method: "POST", Path: Base + "/settings", Body: []byte(body)})
		if e != nil {
			done <- 0
		} else {
			done <- r.StatusCode
		}
	}
	go send(`{"checkin_at":"11:30"}`)
	<-entered
	go send(`{"jitter_minutes":7}`)
	// Correct serialization leaves request two waiting before its read/merge.
	select {
	case <-entered:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		if code := <-done; code != 200 {
			t.Fatal(code)
		}
	}
	o := s.Read().SettingsOverrides
	if o.CheckinAt == nil || *o.CheckinAt != "11:30" || o.JitterMinutes == nil || *o.JitterMinutes != 7 {
		t.Fatal("a successful field patch was lost", o)
	}
}

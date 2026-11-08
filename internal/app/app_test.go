package app

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/abi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/testutil"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T) *App {
	t.Setenv("NEWAPI_CHECKIN_PLUGIN_HOME", testutil.Dir(t))
	a := New(testutil.NewHost(t))
	raw, _ := json.Marshal(map[string]any{"config_yaml": []byte("enabled: false\n")})
	if _, e := a.Handle(context.Background(), "plugin.register", raw); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	return a
}
func TestApplicationOnlyManagement(t *testing.T) {
	a := fixture(t)
	b, e := a.Handle(context.Background(), "management.register", json.RawMessage(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	var routes abi.Routes
	if json.Unmarshal(b, &routes) != nil || len(routes.Routes) != 6 {
		t.Fatal(string(b))
	}
}
func TestSettingsApplyFailureRollsBack(t *testing.T) {
	a := fixture(t)
	before := a.state.Read().SettingsOverrides
	a.reconfigure = func(context.Context, config.Settings) error { return errors.New("injected") }
	v := "12:00"
	if a.applySettings(context.Background(), config.Overrides{CheckinAt: &v}) == nil {
		t.Fatal("reconfigure failure ignored")
	}
	after := a.state.Read().SettingsOverrides
	if before.CheckinAt != after.CheckinAt {
		t.Fatal("override persisted on failure")
	}
}
func TestSettingsRollbackFailureVisible(t *testing.T) {
	a := fixture(t)
	a.reconfigure = func(context.Context, config.Settings) error { return errors.New("injected") }
	v := "12:00"
	_ = a.applySettings(context.Background(), config.Overrides{CheckinAt: &v})
	if a.state.Read().Error != "engine_error" {
		t.Fatal("rollback failure invisible")
	}
}

func TestShutdownDoesNotRestartSchedulerDuringSettingsRollback(t *testing.T) {
	a := fixture(t)
	entered := make(chan struct{})
	var restores atomic.Int32
	a.reconfigure = func(ctx context.Context, _ config.Settings) error {
		if ctx == a.root {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		restores.Add(1)
		return nil
	}
	v := "12:00"
	finished := make(chan error, 1)
	go func() { finished <- a.applySettings(a.root, config.Overrides{CheckinAt: &v}) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := a.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if e := <-finished; e == nil {
		t.Fatal("canceled configuration reported success")
	}
	if restores.Load() != 0 {
		t.Fatal("settings rollback restarted a scheduler after shutdown", restores.Load())
	}
}

func TestSettingsRollbackPersistenceFailureVisible(t *testing.T) {
	a := fixture(t)
	path := filepath.Join(os.Getenv("NEWAPI_CHECKIN_PLUGIN_HOME"), "state.json")
	var calls int
	a.reconfigure = func(context.Context, config.Settings) error {
		calls++
		if calls == 1 {
			if e := os.Rename(path, path+".retained"); e != nil {
				t.Fatal(e)
			}
			if e := os.Mkdir(path, 0700); e != nil {
				t.Fatal(e)
			}
			return errors.New("configuration application failed")
		}
		return nil
	}
	v := "12:00"
	e := a.applySettings(context.Background(), config.Overrides{CheckinAt: &v})
	if core.ErrorKind(e) != "storage_error" || a.state.Diagnostic() != "storage_error" || calls != 2 {
		t.Fatal("failed rollback persistence was hidden", e, calls)
	}
}

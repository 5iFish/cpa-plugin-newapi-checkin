package app

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/abi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/account"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/console"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/credential"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/engine"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/newapi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"encoding/json"
	"errors"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"sync"
	"time"
)

type App struct {
	host                abi.HostCaller
	mu                  sync.RWMutex
	settingsMu          sync.Mutex
	root                context.Context
	cancel              context.CancelFunc
	closed              bool
	inflight            sync.WaitGroup
	state               *state.Store
	handler             *console.Handler
	scheduler           *engine.Scheduler
	defaults, effective config.Settings
	initError           error
	reconfigure         func(context.Context, config.Settings) error
}

func New(host abi.HostCaller) *App {
	ctx, c := context.WithCancel(context.Background())
	return &App{host: host, root: ctx, cancel: c, defaults: config.Defaults(), effective: config.Defaults()}
}
func (a *App) init() error {
	s, e := state.Open("")
	if e != nil {
		a.initError = e
		return e
	}
	a.state = s
	repo := credential.NewStore(a.host)
	gate := &core.Gate{}
	client := newapi.NewClient(nil)
	broker := credential.NewBroker(repo, s, client, gate, time.Now)
	accounts := account.NewService(repo, s, client, broker, gate)
	clock := core.RealClock{}
	runner := engine.NewRunner(broker, client, s, clock)
	jobs := engine.NewJobs(runner, s, clock)
	scheduler := engine.NewScheduler(repo, s, jobs, clock, nil)
	recovered := false
	scheduler.Reconcile = func(ctx context.Context) error {
		if e := accounts.Reconcile(ctx); e != nil {
			return e
		}
		if !recovered {
			if e := broker.Recover(ctx); e != nil {
				return e
			}
			recovered = true
		}
		return nil
	}
	a.scheduler = scheduler
	a.reconfigure = scheduler.Reconfigure
	a.handler = console.NewHandler(accounts, jobs, s, a.applySettings)
	a.handler.Settings = func() config.Settings { a.mu.RLock(); defer a.mu.RUnlock(); return a.effective }
	return nil
}
func (a *App) Handle(ctx context.Context, method string, body json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		raw, e := abi.DecodeConfig(body)
		if e != nil {
			return nil, e
		}
		defaults, e := config.Parse(raw)
		if e != nil {
			return nil, e
		}
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
		a.mu.Lock()
		if a.state == nil && a.initError == nil {
			_ = a.init()
		}
		a.defaults = defaults
		if a.closed {
			a.root, a.cancel = context.WithCancel(context.Background())
			a.closed = false
		}
		if a.state == nil {
			a.mu.Unlock()
			return abi.Result(abi.Registration())
		}
		effective, e := config.Merge(defaults, a.state.Read().SettingsOverrides)
		a.mu.Unlock()
		if e != nil {
			return nil, e
		}
		if e = a.reconfigure(ctx, effective); e != nil {
			return nil, e
		}
		a.mu.Lock()
		a.effective = effective
		a.mu.Unlock()
		return abi.Result(abi.Registration())
	case "management.register":
		return abi.Result(abi.ManagementRegistration())
	case "plugin.quiesce", "plugin.shutdown":
		if e := a.Close(ctx); e != nil {
			return nil, e
		}
		return abi.Result(map[string]any{})
	case "management.handle":
		var req pluginapi.ManagementRequest
		if json.Unmarshal(body, &req) != nil {
			return nil, core.Error("invalid_input")
		}
		if r, ok := console.Resource(req.Path); ok {
			return abi.Result(r)
		}
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			return abi.Result(console.BusinessError(core.Error("disabled")))
		}
		if a.handler == nil {
			e := a.initError
			if e == nil {
				e = core.Error("storage_error")
			}
			a.mu.Unlock()
			return abi.Result(console.BusinessError(e))
		}
		handler := a.handler
		root := a.root
		a.inflight.Add(1)
		a.mu.Unlock()
		defer a.inflight.Done()
		callCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(root, cancel)
		defer stop()
		defer cancel()
		r, e := handler.Handle(callCtx, req)
		if e != nil {
			return nil, e
		}
		return abi.Result(r)
	default:
		return nil, errors.New("unsupported plugin method")
	}
}
func (a *App) applySettings(ctx context.Context, overrides config.Overrides) error {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	a.mu.RLock()
	defaults, oldEffective := a.defaults, a.effective
	a.mu.RUnlock()
	next, e := config.Merge(defaults, overrides)
	if e != nil {
		return core.Error("invalid_input")
	}
	old := a.state.Read().SettingsOverrides
	if e = a.state.Update(func(s *state.Snapshot) error { s.SettingsOverrides = overrides; return nil }); e != nil {
		return e
	}
	if e = a.reconfigure(ctx, next); e != nil {
		rollbackErr := a.state.Update(func(s *state.Snapshot) error { s.SettingsOverrides = old; return nil })
		a.mu.RLock()
		closed := a.closed
		a.mu.RUnlock()
		if closed {
			if rollbackErr != nil {
				return core.Error("storage_error")
			}
			return core.Error("disabled")
		}
		restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		restoreErr := a.reconfigure(restoreCtx, oldEffective)
		if rollbackErr != nil {
			return core.Error("storage_error")
		}
		if restoreErr != nil {
			_ = a.state.Update(func(s *state.Snapshot) error { s.Error = "engine_error"; return nil })
			return core.Error("engine_error")
		}
		return core.Error("engine_error")
	}
	a.mu.Lock()
	a.effective = next
	a.mu.Unlock()
	return nil
}
func (a *App) Close(ctx context.Context) error {
	a.mu.Lock()
	a.closed = true
	a.cancel()
	scheduler := a.scheduler
	a.mu.Unlock()
	// Stop the workers before releasing the instance callback in main.
	// An in-flight settings rollback must finish before the final shutdown.
	a.settingsMu.Lock()
	if scheduler != nil {
		if e := scheduler.Shutdown(ctx); e != nil {
			a.settingsMu.Unlock()
			return e
		}
	}
	a.settingsMu.Unlock()
	done := make(chan struct{})
	go func() { a.inflight.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

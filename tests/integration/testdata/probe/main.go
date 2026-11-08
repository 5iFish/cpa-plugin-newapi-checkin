// Package probe is built only by the abiprobe integration build tag.
package probe

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/abi"
	"encoding/json"
	"errors"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"os"
	"sync"
	"time"
)

type Probe struct {
	host   abi.HostCaller
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	wg     sync.WaitGroup
}

func New(h abi.HostCaller) *Probe {
	ctx, c := context.WithCancel(context.Background())
	return &Probe{host: h, ctx: ctx, cancel: c}
}
func (p *Probe) Close(context.Context) error { p.cancel(); p.wg.Wait(); return nil }
func (p *Probe) Handle(ctx context.Context, m string, b json.RawMessage) (json.RawMessage, error) {
	switch m {
	case "plugin.register":
		p.once.Do(func() { p.wg.Add(1); go p.run() })
		return abi.Result(abi.Registration())
	case "plugin.reconfigure":
		return abi.Result(abi.Registration())
	case "management.register":
		return abi.Result(abi.ManagementRegistration())
	case "plugin.quiesce", "plugin.shutdown":
		_ = p.Close(ctx)
		return abi.Result(map[string]any{})
	default:
		return nil, errors.New("probe has no management handler")
	}
}
func (p *Probe) run() {
	defer p.wg.Done()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-p.ctx.Done():
		return
	}
	var saved pluginapi.HostAuthSaveResponse
	err := p.host.Call(p.ctx, "host.auth.save", pluginapi.HostAuthSaveRequest{Name: "newapi-checkin-probe.json", JSON: json.RawMessage(`{"type":"newapi-checkin","plugin_owner":"newapi-checkin","probe":true}`)}, &saved)
	if err == nil {
		var list struct {
			Files []pluginapi.HostAuthFileEntry `json:"files"`
		}
		err = p.host.Call(p.ctx, "host.auth.list", map[string]any{}, &list)
		found := false
		if err == nil {
			for _, f := range list.Files {
				if f.Name == saved.Name {
					var got pluginapi.HostAuthGetResponse
					err = p.host.Call(p.ctx, "host.auth.get", pluginapi.HostAuthGetRequest{AuthIndex: f.AuthIndex}, &got)
					if err == nil {
						var v map[string]any
						err = json.Unmarshal(got.JSON, &v)
						found = v["probe"] == true && got.Path == saved.Path
					}
					break
				}
			}
		}
		if err == nil && !found {
			err = errors.New("background auth roundtrip mismatch")
		}
	}
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	if path := os.Getenv("NEWAPI_CHECKIN_PROBE_RESULT"); path != "" {
		_ = os.WriteFile(path, []byte(result), 0600)
	}
}

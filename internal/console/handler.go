package console

import (
	"bytes"
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/account"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/engine"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"encoding/json"
	"errors"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"io"
	"net/http"
	"sort"
	"sync"
)

const Base = "/v0/management/plugins/newapi-checkin"

type Handler struct {
	accounts      *account.Service
	jobs          *engine.Jobs
	state         *state.Store
	applySettings func(context.Context, config.Overrides) error
	Settings      func() config.Settings
	settingsMu    sync.Mutex
}

func NewHandler(a *account.Service, j *engine.Jobs, s *state.Store, apply func(context.Context, config.Overrides) error) *Handler {
	return &Handler{accounts: a, jobs: j, state: s, applySettings: apply, Settings: config.Defaults}
}
func response(status int, data any) pluginapi.ManagementResponse {
	b, _ := json.Marshal(data)
	return pluginapi.ManagementResponse{StatusCode: status, Headers: http.Header{"Content-Type": {"application/json; charset=utf-8"}, "Cache-Control": {"no-store"}}, Body: b}
}
func Success(status int, data any) pluginapi.ManagementResponse {
	return response(status, map[string]any{"success": true, "data": data})
}
func BusinessError(err error) pluginapi.ManagementResponse {
	var api *core.APIError
	if !errors.As(err, &api) {
		api = core.Error("unknown_error")
	}
	status := 422
	if api.Kind == "invalid_input" {
		status = 400
	}
	if api.Kind == "storage_error" || api.Kind == "engine_error" {
		status = 503
	}
	return response(status, map[string]any{"success": false, "error": api})
}
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return core.Error("invalid_input")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return core.Error("invalid_input")
	}
	return nil
}
func (h *Handler) Handle(ctx context.Context, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	if len(req.Body) > 64<<10 {
		return response(413, map[string]any{"success": false, "error": core.Error("body_too_large")}), nil
	}
	var result any
	var err error
	status := 200
	switch req.Method + " " + req.Path {
	case "GET " + Base + "/status":
		s := h.state.Read()
		if diagnostic := h.state.Diagnostic(); diagnostic != "" {
			s.Error = diagnostic
		}
		accounts := make([]core.AccountSummary, 0, len(s.Accounts))
		for _, a := range s.Accounts {
			accounts = append(accounts, a.Summary())
		}
		sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
		result = map[string]any{"accounts": accounts, "jobs": s.Jobs, "work_items": s.WorkItems, "history": s.History, "schedules": s.Schedules, "settings": h.Settings(), "overrides": s.SettingsOverrides, "error": s.Error}
	case "POST " + Base + "/accounts":
		var in account.BindInput
		if err = decode(req.Body, &in); err == nil {
			result, err = h.accounts.Bind(ctx, in)
		}
	case "DELETE " + Base + "/accounts":
		id := req.Query.Get("id")
		if len(req.Query) != 1 || id == "" || len(req.Body) > 0 {
			err = core.Error("invalid_input")
		} else {
			err = h.accounts.Delete(ctx, id)
			result = map[string]any{"deleted": id}
		}
	case "POST " + Base + "/recheck":
		var in struct {
			ID string `json:"account_id"`
		}
		if err = decode(req.Body, &in); err == nil {
			if in.ID == "" {
				err = core.Error("invalid_input")
			} else {
				result, err = h.accounts.Recheck(ctx, in.ID)
			}
		}
	case "POST " + Base + "/checkin":
		var in struct {
			ID string `json:"account_id"`
		}
		if err = decode(req.Body, &in); err == nil {
			ids := []string{in.ID}
			if in.ID == "" {
				ids = nil
				for id := range h.state.Read().Accounts {
					ids = append(ids, id)
				}
			}
			var jobID string
			jobID, err = h.jobs.Submit(ctx, ids, "manual")
			result = map[string]string{"job_id": jobID}
			status = 202
		}
	case "POST " + Base + "/settings":
		h.settingsMu.Lock()
		defer h.settingsMu.Unlock()
		var patch map[string]json.RawMessage
		if err = decode(req.Body, &patch); err == nil {
			overrides := h.state.Read().SettingsOverrides
			for k, v := range patch {
				switch k {
				case "checkin_at":
					err = json.Unmarshal(v, &overrides.CheckinAt)
				case "jitter_minutes":
					err = json.Unmarshal(v, &overrides.JitterMinutes)
				case "timezone":
					err = json.Unmarshal(v, &overrides.Timezone)
				default:
					err = core.Error("invalid_input")
				}
				if err != nil {
					break
				}
			}
			if err == nil {
				err = h.applySettings(ctx, overrides)
			}
			result = map[string]any{"settings": h.Settings(), "overrides": overrides}
		}
	default:
		return response(404, map[string]any{"success": false, "error": core.Error("not_found")}), nil
	}
	if err != nil {
		return BusinessError(err), nil
	}
	return Success(status, result), nil
}

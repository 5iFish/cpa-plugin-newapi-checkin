package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/account"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/engine"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

const Base = "/v0/management/plugins/newapi-checkin"

// Page caps bound how many 100-entry log pages one statistics window reads.
const (
	stats24hMaxPages = 20
	stats30dMaxPages = 100
)

// usageWindow maps a console range key to a rolling unix window.
func usageWindow(key string, now time.Time) (start, end int64, ok bool) {
	var d time.Duration
	switch key {
	case "24h":
		d = 24 * time.Hour
	case "7d":
		d = 7 * 24 * time.Hour
	case "30d":
		d = 30 * 24 * time.Hour
	default:
		return 0, 0, false
	}
	end = now.Unix()
	return end - int64(d/time.Second), end, true
}

// statsRange maps a console range key to the statistics window and bucket
// layout: 24h uses hourly buckets, 7d/30d daily ones; bucket starts align to
// the schedule timezone via offset.
func statsRange(key string, now time.Time, loc *time.Location) (start, end, bucketSeconds, offset int64, ok bool) {
	start, end, ok = usageWindow(key, now)
	if !ok {
		return 0, 0, 0, 0, false
	}
	bucketSeconds = 86400
	if key == "24h" {
		bucketSeconds = 3600
	}
	if loc == nil {
		loc = time.Local
	}
	_, off := now.In(loc).Zone()
	return start, end, bucketSeconds, int64(off), true
}

// statsPages bounds the log pages read for one statistics window.
func statsPages(key string) int {
	switch key {
	case "24h":
		return stats24hMaxPages
	case "7d":
		return 60
	default:
		return stats30dMaxPages
	}
}

// statsQuery validates the console statistics query and reports whether a
// forced refresh was requested.
func statsQuery(q url.Values) (key string, refresh, ok bool) {
	if len(q) < 1 || len(q) > 2 {
		return "", false, false
	}
	for k := range q {
		if k != "range" && k != "refresh" {
			return "", false, false
		}
	}
	if q.Get("range") == "" {
		return "", false, false
	}
	switch q.Get("refresh") {
	case "":
		return q.Get("range"), false, true
	case "1":
		return q.Get("range"), true, true
	}
	return "", false, false
}

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
		sort.Slice(accounts, func(i, j int) bool {
			// Registration order: legacy records without a timestamp first,
			// then oldest bind wins; the ID breaks exact ties.
			a, b := accounts[i], accounts[j]
			switch {
			case a.CreatedAt.IsZero() && !b.CreatedAt.IsZero():
				return true
			case !a.CreatedAt.IsZero() && b.CreatedAt.IsZero():
				return false
			case !a.CreatedAt.Equal(b.CreatedAt):
				return a.CreatedAt.Before(b.CreatedAt)
			default:
				return a.ID < b.ID
			}
		})
		result = map[string]any{"accounts": accounts, "jobs": s.Jobs, "work_items": s.WorkItems, "history": s.History, "schedules": s.Schedules, "settings": h.Settings(), "overrides": s.SettingsOverrides, "error": s.Error, "health": h.healthStatus()}
	case "GET " + Base + "/health":
		if len(req.Query) != 0 || len(req.Body) != 0 {
			err = core.Error("invalid_input")
			break
		}
		result = map[string]any{"health": h.healthStatus()}
	case "POST " + Base + "/health":
		var in struct {
			Refresh bool `json:"refresh"`
		}
		if len(req.Query) != 0 {
			err = core.Error("invalid_input")
			break
		}
		if err = decode(req.Body, &in); err != nil {
			break
		}
		if h.accounts == nil {
			err = core.Error("disabled")
			break
		}
		var started bool
		started, err = h.accounts.RefreshHealth(ctx, in.Refresh)
		if err == nil {
			health := h.healthStatus()
			if started || health.Refreshing {
				status = http.StatusAccepted
			}
			result = map[string]any{"health": health}
		}
	case "GET " + Base + "/probe":
		provider := req.Query.Get("provider")
		if len(req.Query) != 1 || provider == "" || !core.ValidOAuthProvider(provider) {
			err = core.Error("invalid_input")
			break
		}
		// The probe feeds a bind-dialog hint, so it must answer quickly even
		// when the provider endpoint hangs.
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var status string
		status, err = h.accounts.ProbeLoginProvider(pctx, provider)
		cancel()
		if err == nil {
			result = map[string]any{"provider": provider, "status": status}
		}
	case "GET " + Base + "/stats":
		key, refresh, ok := statsQuery(req.Query)
		now := time.Now()
		loc := time.Local
		if tz := h.Settings().Timezone; tz != "" {
			if l, e := time.LoadLocation(tz); e == nil {
				loc = l
			}
		}
		start, end, bucket, offset, windowOK := statsRange(key, now, loc)
		if !ok || !windowOK {
			err = core.Error("invalid_input")
			break
		}
		if !refresh {
			if cached, hit := h.state.Read().Stats[key]; hit {
				result = map[string]any{"stats": cached, "from_cache": true}
				break
			}
		}
		// A 30-day window walks up to the page cap across every account, so
		// the live path needs more room than the balance refresh.
		pctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		var summary core.StatsSummary
		summary, err = h.accounts.Stats(pctx, key, start, end, bucket, offset, statsPages(key))
		cancel()
		if err == nil {
			result = map[string]any{"stats": summary, "from_cache": false}
		}
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
	case "POST " + Base + "/balance":
		var in struct {
			ID string `json:"account_id"`
		}
		if err = decode(req.Body, &in); err == nil {
			if in.ID != "" {
				result, err = h.accounts.RefreshBalance(ctx, in.ID)
				break
			}
			ids := make([]string, 0, len(h.state.Read().Accounts))
			for id, a := range h.state.Read().Accounts {
				if a.Status != "deleted" && !h.state.Read().DeletePending[id] {
					ids = append(ids, id)
				}
			}
			sort.Strings(ids)
			summaries := make([]core.AccountSummary, 0, len(ids))
			failed := map[string]string{}
			var firstErr string
			for _, id := range ids {
				summary, e := h.accounts.RefreshBalance(ctx, id)
				if e != nil {
					failed[id] = core.ErrorKind(e)
					if firstErr == "" {
						firstErr = failed[id]
					}
					continue
				}
				summaries = append(summaries, summary)
			}
			if len(ids) > 0 && len(summaries) == 0 && firstErr != "" {
				err = core.Error(firstErr)
			} else {
				result = map[string]any{"accounts": summaries, "failed": failed}
			}
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

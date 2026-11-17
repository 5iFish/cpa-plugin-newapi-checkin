package account

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
)

func TestStatsMergesAccountsAndConvertsCost(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	if _, err := f.s.Bind(context.Background(), f.input()); err != nil {
		t.Fatal(err)
	}
	f.logItems = []any{
		map[string]any{"model_name": "m1", "prompt_tokens": 100, "completion_tokens": 10, "quota": 500000, "created_at": 1794816000, "use_time": 2, "other": `{"cache_tokens":40}`},
	}
	deny := false
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"quota_per_unit":200000}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":2,"username":"u2","quota":100,"used_quota":10,"last_login_time":1}}`))
		case "/api/user/checkin":
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":true,"stats":{"checked_in_today":true,"total_quota":0,"total_checkins":0,"records":[]}}}`))
		case "/api/log/self":
			if deny {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED"}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"model_name":"m1","prompt_tokens":50,"completion_tokens":5,"quota":200000,"created_at":1794816001}],"total":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv2.Close)
	second, err := f.s.Bind(context.Background(), BindInput{Site: srv2.URL, Channel: "token", Token: "nap_two"})
	if err != nil {
		t.Fatal(err)
	}
	start, end := int64(1794816000), int64(1794820000)
	out, err := f.s.Stats(context.Background(), "7d", start, end, 86400, 28800, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Accounts) != 2 || out.Failed == nil || len(out.Failed) != 0 {
		t.Fatalf("accounts/failed wrong: %+v", out)
	}
	var m1 *core.StatsModel
	for i := range out.Models {
		if out.Models[i].Model == "m1" {
			m1 = &out.Models[i]
		}
	}
	if m1 == nil || m1.Calls != 2 || m1.TotalTokens != 165 || m1.CacheTokens != 40 {
		t.Fatalf("model merge wrong: %+v", out.Models)
	}
	// 成本 = 500000/500000 + 200000/200000 = 2.0 美元
	if m1.CostUSD < 1.99 || m1.CostUSD > 2.01 || out.Totals.CostUSD < 1.99 || out.Totals.CostUSD > 2.01 || out.PartialCost {
		t.Fatalf("cost conversion wrong: model=%+v totals=%+v partial=%v", m1, out.Totals, out.PartialCost)
	}
	if cached := f.state.Read().Stats["7d"]; len(cached.Models) == 0 || cached.Models[0].Model != "m1" {
		t.Fatalf("stats cache not written: %+v", cached)
	}
	// 一个账号失败只进 failed，另一个仍聚合
	deny = true
	out, err = f.s.Stats(context.Background(), "7d", start, end, 86400, 28800, 5)
	if err != nil || len(out.Failed) != 1 || len(out.Accounts) != 1 {
		t.Fatalf("failure isolation wrong: err=%v failed=%v accounts=%d", err, out.Failed, len(out.Accounts))
	}
	// 无 per_unit 的账号成本不计入并标 partial
	deny = false
	if err = f.state.Update(func(v *state.Snapshot) error {
		r := v.Accounts[second.ID]
		r.BalancePerUnit = 0
		v.Accounts[second.ID] = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out, err = f.s.Stats(context.Background(), "7d", start, end, 86400, 28800, 5)
	if err != nil || !out.PartialCost || out.Totals.CostUSD < 0.99 || out.Totals.CostUSD > 1.01 || len(out.Accounts) != 2 {
		t.Fatalf("partial cost wrong: err=%v partial=%v total=%v", err, out.PartialCost, out.Totals.CostUSD)
	}
	for _, acc := range out.Accounts {
		if acc.ID == second.ID && (acc.PerUnit != 0 || acc.CostUSD != 0) {
			t.Fatalf("zero per_unit account counted cost: %+v", acc)
		}
	}
}

func TestRenameUpdatesCachedStatsLabel(t *testing.T) {
	f := fixture(t)
	f.quota = 62500000
	in := f.input()
	bound, err := f.s.Bind(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	start, end := int64(1794816000), int64(1794820000)
	if _, err = f.s.Stats(context.Background(), "7d", start, end, 86400, 28800, 5); err != nil {
		t.Fatal(err)
	}
	if cached := f.state.Read().Stats["7d"]; len(cached.Accounts) != 1 || cached.Accounts[0].Label != "hello" {
		t.Fatalf("stats cache not primed: %+v", cached.Accounts)
	}
	rename := f.input()
	rename.AccountID = bound.ID
	rename.Label = "重命名后"
	if _, err = f.s.Bind(context.Background(), rename); err != nil {
		t.Fatal(err)
	}
	if cached := f.state.Read().Stats["7d"]; len(cached.Accounts) != 1 || cached.Accounts[0].Label != "重命名后" {
		t.Fatalf("rename did not update the cached stats label: %+v", cached.Accounts)
	}
}

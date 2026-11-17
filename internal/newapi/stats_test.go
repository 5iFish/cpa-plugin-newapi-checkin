package newapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func statsSite(t *testing.T, items []map[string]any, withTotal bool, seen *[]string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/log/self" || r.Header.Get("New-Api-User") != "7" {
			http.NotFound(w, r)
			return
		}
		*seen = append(*seen, r.URL.RawQuery)
		if withTotal {
			_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":%s,"total":%d}}`, mustJSON(items), len(items))
			return
		}
		_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":%s}}`, mustJSON(items))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestUsageStatsAggregatesCacheAndBuckets(t *testing.T) {
	seen := []string{}
	t1 := int64(1794812400 + 10*3600 + 5*60) // :05:00 in some hour, +08:00 aligned
	t2 := t1 + 3600
	items := []map[string]any{
		{"model_name": "m1", "prompt_tokens": 100, "completion_tokens": 10, "quota": 50, "created_at": t1, "use_time": 3, "other": `{"cache_tokens":40}`},
		{"model_name": "m1", "prompt_tokens": 50, "completion_tokens": 5, "quota": 25, "created_at": t2, "use_time": 2, "other": `{"cache_tokens":10,"cache_ratio":0.2}`},
		{"model_name": "m2", "prompt_tokens": 10, "completion_tokens": 1, "quota": 5, "created_at": t2, "other": `not-json`},
	}
	site := statsSite(t, items, true, &seen)
	got, truncated, err := NewClient(site.Client()).UsageStats(context.Background(), site.URL, "tok", 7, t1-60, t2+60, 3600, 28800, 10)
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	if len(got.Models) != 2 || got.Models[0].Model != "m1" || got.Models[0].Calls != 2 || got.Models[0].TotalTokens != 165 ||
		got.Models[0].CacheTokens != 50 || got.Models[0].UseSeconds != 5 || got.Models[0].Quota != 75 {
		t.Fatalf("model aggregate wrong: %+v", got.Models)
	}
	if got.Models[1].CacheTokens != 0 || got.Models[1].TotalTokens != 11 {
		t.Fatalf("invalid other must count zero cache: %+v", got.Models[1])
	}
	if got.Totals.Calls != 3 || got.Totals.PromptTokens != 160 || got.Totals.CompletionTokens != 16 || got.Totals.TotalTokens != 176 || got.Totals.CacheTokens != 50 || got.Totals.Quota != 80 {
		t.Fatalf("totals wrong: %+v", got.Totals)
	}
	if len(got.Series) != 2 {
		t.Fatalf("series wrong: %+v", got.Series)
	}
	if got.Series[0].Start > t1 || t1-got.Series[0].Start >= 3600 || (got.Series[0].Start+28800)%3600 != 0 {
		t.Fatalf("bucket not aligned to offset: start=%d t1=%d", got.Series[0].Start, t1)
	}
	if len(got.Series[0].Tokens) != 1 || got.Series[0].Tokens[0].Model != "m1" || got.Series[0].Tokens[0].Tokens != 110 {
		t.Fatalf("bucket one wrong: %+v", got.Series[0])
	}
	sum := int64(0)
	for _, v := range got.Series[1].Tokens {
		sum += v.Tokens
	}
	if sum != 66 || got.Series[1].Tokens[0].Model != "m1" {
		t.Fatalf("bucket two wrong: %+v", got.Series[1])
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "type=2") || !strings.Contains(seen[0], "page_size=100") ||
		!strings.Contains(seen[0], fmt.Sprintf("start_timestamp=%d", t1-60)) || !strings.Contains(seen[0], fmt.Sprintf("end_timestamp=%d", t2+60)) {
		t.Fatalf("query wrong: %v", seen)
	}
}

func TestUsageStatsBucketOffsetAndTruncation(t *testing.T) {
	full := make([]map[string]any, 100)
	for i := range full {
		full[i] = map[string]any{"model_name": "m", "prompt_tokens": 1, "completion_tokens": 0, "quota": 1, "created_at": 1794816000}
	}
	seen := []string{}
	site := statsSite(t, full, true, &seen)
	// total 已知且恰好读完时不截断
	if _, truncated, err := NewClient(site.Client()).UsageStats(context.Background(), site.URL, "tok", 7, 0, 1, 86400, 28800, 1); err != nil || truncated {
		t.Fatalf("exact read marked truncated: %v %v", err, truncated)
	}
	// total 缺失且触顶 → 截断
	seen = nil
	site2 := statsSite(t, full, false, &seen)
	_, truncated, err := NewClient(site2.Client()).UsageStats(context.Background(), site2.URL, "tok", 7, 0, 1, 86400, 28800, 1)
	if err != nil || !truncated {
		t.Fatalf("unknown total at cap must truncate: %v %v", err, truncated)
	}
}

func TestUsageStatsDailyBucketWithOffset(t *testing.T) {
	// 当地 00:10 的记录应落在当地日桶起点：offset=28800、bucket=86400。
	seen := []string{}
	localMidnight := int64(1794812400) // 07:00 UTC == 15:00 +08:00；再加 8h 得到当地人日界前的点
	items := []map[string]any{
		{"model_name": "m", "prompt_tokens": 7, "completion_tokens": 0, "quota": 1, "created_at": localMidnight},
	}
	site := statsSite(t, items, true, &seen)
	got, _, err := NewClient(site.Client()).UsageStats(context.Background(), site.URL, "tok", 7, 0, localMidnight+1, 86400, 28800, 5)
	if err != nil || len(got.Series) != 1 {
		t.Fatal(err, got.Series)
	}
	if (got.Series[0].Start+28800)%86400 != 0 || got.Series[0].Start > localMidnight || localMidnight-got.Series[0].Start >= 86400 {
		t.Fatalf("daily bucket misaligned: start=%d t=%d", got.Series[0].Start, localMidnight)
	}
}

func TestUsageStatsEmptyWindow(t *testing.T) {
	seen := []string{}
	site := statsSite(t, nil, true, &seen)
	got, truncated, err := NewClient(site.Client()).UsageStats(context.Background(), site.URL, "tok", 7, 0, 1, 3600, 28800, 5)
	if err != nil || truncated || len(got.Models) != 0 || len(got.Series) != 0 || got.Totals.TotalTokens != 0 {
		t.Fatalf("empty window wrong: err=%v truncated=%v got=%+v", err, truncated, got)
	}
}

package newapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

// UsageStats aggregates one site's consumption logs (type=2) within
// [start, end]: per-model totals (tokens, cache hits, latency, raw quota) and
// a per-bucket model token series for the stacked chart. Bucket starts are
// aligned to the caller's timezone offset:
//
//	bucket = floor((t+offset)/size)*size - offset
//
// maxPages bounds the number of 100-entry pages; the returned flag reports
// that the site had more entries than the cap could read.
func (c *Client) UsageStats(ctx context.Context, site, bearer string, userID, start, end, bucketSeconds, bucketOffset int64, maxPages int) (core.SiteUsageStats, bool, error) {
	if bucketSeconds <= 0 {
		bucketSeconds = 86400
	}
	if maxPages < 1 {
		maxPages = 1
	}
	models := map[string]*core.ModelUsage{}
	buckets := map[int64]map[string]int64{}
	var totals core.StatsTotals
	fetched := 0
	truncated := false
	for p := 1; ; p++ {
		raw, _, e := c.request(ctx, site, usageLogPath(p, start, end), "GET", bearer, nil, http.Header{"New-Api-User": {strconv.FormatInt(userID, 10)}})
		if e != nil {
			return core.SiteUsageStats{}, false, e
		}
		var page usagePage
		if json.Unmarshal(raw, &page) != nil {
			return core.SiteUsageStats{}, false, core.Error("unsupported_site")
		}
		for _, it := range page.Items {
			model := it.ModelName
			if model == "" {
				model = "未知模型"
			}
			m := models[model]
			if m == nil {
				m = &core.ModelUsage{Model: model}
				models[model] = m
			}
			m.Calls++
			m.PromptTokens += it.PromptTokens
			m.CompletionTokens += it.CompletionTokens
			m.Quota += it.Quota
			m.UseSeconds += it.UseTime
			totals.Calls++
			totals.PromptTokens += it.PromptTokens
			totals.CompletionTokens += it.CompletionTokens
			totals.Quota += it.Quota
			// The per-entry billing detail is a JSON string. Cache hits matter
			// for the cache-rate metric; a missing or malformed value is not an
			// error, it just counts as zero.
			if it.Other != "" {
				var extra struct {
					CacheTokens int64 `json:"cache_tokens"`
				}
				if json.Unmarshal([]byte(it.Other), &extra) == nil && extra.CacheTokens > 0 {
					m.CacheTokens += extra.CacheTokens
					totals.CacheTokens += extra.CacheTokens
				}
			}
			if it.CreatedAt > 0 {
				b := (it.CreatedAt+bucketOffset)/bucketSeconds*bucketSeconds - bucketOffset
				bm := buckets[b]
				if bm == nil {
					bm = map[string]int64{}
					buckets[b] = bm
				}
				bm[model] += it.PromptTokens + it.CompletionTokens
			}
		}
		fetched += len(page.Items)
		if len(page.Items) == 0 {
			break
		}
		if page.Total > 0 && fetched >= int(page.Total) {
			break
		}
		if p >= maxPages {
			truncated = page.Total == 0 || fetched < int(page.Total)
			break
		}
		if len(page.Items) < usagePageSize {
			break
		}
	}
	out := core.SiteUsageStats{Totals: totals}
	out.Totals.TotalTokens = totals.PromptTokens + totals.CompletionTokens
	out.Models = make([]core.ModelUsage, 0, len(models))
	for _, m := range models {
		m.TotalTokens = m.PromptTokens + m.CompletionTokens
		out.Models = append(out.Models, *m)
	}
	sort.Slice(out.Models, func(i, j int) bool {
		a, b := out.Models[i], out.Models[j]
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		if a.Quota != b.Quota {
			return a.Quota > b.Quota
		}
		return a.Model < b.Model
	})
	out.Series = make([]core.StatsBucket, 0, len(buckets))
	for startAt, bm := range buckets {
		tokens := make([]core.StatsModelTokens, 0, len(bm))
		for model, n := range bm {
			if n > 0 {
				tokens = append(tokens, core.StatsModelTokens{Model: model, Tokens: n})
			}
		}
		sort.Slice(tokens, func(i, j int) bool {
			if tokens[i].Tokens != tokens[j].Tokens {
				return tokens[i].Tokens > tokens[j].Tokens
			}
			return tokens[i].Model < tokens[j].Model
		})
		out.Series = append(out.Series, core.StatsBucket{Start: startAt, Tokens: tokens})
	}
	sort.Slice(out.Series, func(i, j int) bool { return out.Series[i].Start < out.Series[j].Start })
	return out, truncated, nil
}

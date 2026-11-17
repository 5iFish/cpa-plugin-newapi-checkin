package newapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

// HealthLogPage deliberately keeps only timestamps. Request bodies, error
// messages, token names and addresses must not enter the health cache.
type HealthLogPage struct {
	Timestamps []int64
	Total      int64
	TotalKnown bool
}

// HealthLogs reads one account's consumption (2) or error (5) log page inside
// [start, end). A site that ignores the filters is not a source of health data.
func (c *Client) HealthLogs(ctx context.Context, site, bearer string, userID int64, logType, page int, start, end int64) (HealthLogPage, error) {
	if (logType != 2 && logType != 5) || page < 1 || start <= 0 || end <= start {
		return HealthLogPage{}, core.Error("invalid_input")
	}
	path := fmt.Sprintf("/api/log/self?p=%d&page_size=%d&type=%d&start_timestamp=%d&end_timestamp=%d", page, usagePageSize, logType, start, end-1)
	raw, _, err := c.request(ctx, site, path, "GET", bearer, nil, userHeaders(userID))
	if err != nil {
		return HealthLogPage{}, err
	}
	var wire struct {
		Items []struct {
			Type      *int   `json:"type"`
			CreatedAt *int64 `json:"created_at"`
		} `json:"items"`
		Total json.RawMessage `json:"total"`
	}
	if json.Unmarshal(raw, &wire) != nil || wire.Items == nil || len(wire.Items) > usagePageSize {
		return HealthLogPage{}, core.Error("unsupported_site")
	}
	out := HealthLogPage{Timestamps: make([]int64, 0, len(wire.Items))}
	if len(wire.Total) > 0 {
		if string(wire.Total) == "null" || json.Unmarshal(wire.Total, &out.Total) != nil || out.Total < int64(len(wire.Items)) || out.Total < 0 {
			return HealthLogPage{}, core.Error("unsupported_site")
		}
		out.TotalKnown = true
		if len(wire.Items) == 0 && out.Total > int64(page-1)*usagePageSize {
			return HealthLogPage{}, core.Error("unsupported_site")
		}
	}
	for _, it := range wire.Items {
		if it.Type == nil || *it.Type != logType || it.CreatedAt == nil || *it.CreatedAt < start || *it.CreatedAt >= end {
			return HealthLogPage{}, core.Error("unsupported_site")
		}
		out.Timestamps = append(out.Timestamps, *it.CreatedAt)
	}
	return out, nil
}

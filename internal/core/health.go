package core

import "time"

// HealthBucket counts site log records, not uptime or client request outcomes.
type HealthBucket struct {
	Start   int64 `json:"start"`
	Success int64 `json:"success"`
	Failed  int64 `json:"failed"`
}

// AccountHealth holds aggregates only. A stale result keeps its original window
// and SyncedAt; CheckedAt and Error describe the most recent refresh attempt.
type AccountHealth struct {
	ID        string         `json:"account_id"`
	Start     int64          `json:"start"`
	End       int64          `json:"end"`
	Buckets   []HealthBucket `json:"buckets"`
	Success   int64          `json:"success"`
	Failed    int64          `json:"failed"`
	Complete  bool           `json:"complete"`
	Truncated bool           `json:"truncated"`
	Stale     bool           `json:"stale"`
	SyncedAt  time.Time      `json:"synced_at"`
	CheckedAt time.Time      `json:"checked_at"`
	Error     string         `json:"error,omitempty"`
}

type HealthSummary struct {
	Accounts   map[string]AccountHealth `json:"accounts"`
	Refreshing bool                     `json:"refreshing"`
	Error      string                   `json:"error,omitempty"`
}

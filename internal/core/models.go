package core

import "time"

// AccountModels caches the model names the account's user group may call.
// Only names and timestamps are kept; a stale entry preserves the last good
// list while CheckedAt and Error describe the most recent attempt.
type AccountModels struct {
	ID        string    `json:"account_id"`
	Models    []string  `json:"models"`
	Complete  bool      `json:"complete"`
	Stale     bool      `json:"stale"`
	SyncedAt  time.Time `json:"synced_at"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

type ModelsSummary struct {
	Accounts   map[string]AccountModels `json:"accounts"`
	Refreshing bool                     `json:"refreshing"`
	Error      string                   `json:"error,omitempty"`
}

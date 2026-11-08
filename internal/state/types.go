package state

import (
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/config"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"time"
)

type AccountRecord struct {
	ID                      string              `json:"account_id"`
	Site                    string              `json:"site"`
	UpstreamUserID          int64               `json:"upstream_user_id"`
	Username                string              `json:"username"`
	Label                   string              `json:"label"`
	Kind                    string              `json:"kind"`
	Channel                 string              `json:"channel"`
	Status                  string              `json:"status"`
	PhysicalPath            string              `json:"physical_path"`
	LastGeneration          uint64              `json:"last_generation"`
	ExpiresAt               *int64              `json:"expires_at,omitempty"`
	TokenHint               string              `json:"token_hint,omitempty"`
	WritePermissionVerified bool                `json:"write_permission_verified"`
	PermissionDenied        bool                `json:"permission_denied,omitempty"`
	TotalQuota              int64               `json:"total_quota"`
	TotalCheckins           int64               `json:"total_checkins"`
	StatsSyncedAt           time.Time           `json:"stats_synced_at"`
	StatsStale              bool                `json:"stats_stale"`
	LastResult              *core.AttemptResult `json:"last_result,omitempty"`
}

func (a AccountRecord) Summary() core.AccountSummary {
	return core.AccountSummary{ID: a.ID, Site: a.Site, Username: a.Username, Label: a.Label, Kind: a.Kind, Channel: a.Channel, Status: a.Status, ExpiresAt: a.ExpiresAt, TokenHint: a.TokenHint, WritePermissionVerified: a.WritePermissionVerified, TotalQuota: a.TotalQuota, TotalCheckins: a.TotalCheckins, StatsSyncedAt: a.StatsSyncedAt, StatsStale: a.StatsStale, LastResult: a.LastResult}
}
func Record(a core.Account, path, status string) AccountRecord {
	r := AccountRecord{ID: a.ID, Site: a.Site, UpstreamUserID: a.UpstreamUserID, Username: a.Username, Label: a.Label, Kind: a.Credential.Kind, Channel: a.Credential.Channel, Status: status, PhysicalPath: path, LastGeneration: a.Credential.Generation, ExpiresAt: a.Credential.ExpiresAtHint}
	if a.Credential.Kind == "session" {
		expiry := a.Credential.SessionExpiresAt
		r.ExpiresAt = &expiry
	}
	if n := len(a.Credential.Token); n > 4 {
		r.TokenHint = "…" + a.Credential.Token[n-4:]
	}
	return r
}

type RefreshPending struct {
	AccountID     string    `json:"account_id"`
	OldGeneration uint64    `json:"old_generation"`
	StartedAt     time.Time `json:"started_at"`
}
type KnownSession struct {
	Site      string    `json:"site"`
	SessionID string    `json:"session_id"`
	AccountID string    `json:"account_id"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Job struct {
	ID        string    `json:"id"`
	Trigger   string    `json:"trigger"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	WorkIDs   []string  `json:"work_ids"`
}
type WorkItem struct {
	ID             string              `json:"id"`
	AccountID      string              `json:"account_id"`
	CreatedAt      time.Time           `json:"created_at"`
	Status         string              `json:"status"`
	Attempt        int                 `json:"attempt"`
	NextAttemptAt  time.Time           `json:"next_attempt_at"`
	NeedsReconcile bool                `json:"needs_reconcile"`
	Result         *core.AttemptResult `json:"result,omitempty"`
	Generation     uint64              `json:"generation"`
}
type ScheduleEntry struct {
	AccountID    string    `json:"account_id"`
	ScheduleDate string    `json:"schedule_date"`
	PlannedAt    time.Time `json:"planned_at"`
	WorkID       string    `json:"work_id"`
}
type HistoryEntry struct {
	AccountID string             `json:"account_id"`
	Result    core.AttemptResult `json:"result"`
}
type Snapshot struct {
	SchemaVersion     int                       `json:"schema_version"`
	SettingsOverrides config.Overrides          `json:"settings_overrides"`
	Accounts          map[string]AccountRecord  `json:"accounts"`
	Schedules         map[string]ScheduleEntry  `json:"schedules"`
	Jobs              map[string]Job            `json:"jobs"`
	WorkItems         map[string]WorkItem       `json:"work_items"`
	History           []HistoryEntry            `json:"history"`
	RefreshPending    map[string]RefreshPending `json:"refresh_pending"`
	KnownSessions     map[string]KnownSession   `json:"known_sessions"`
	DeletePending     map[string]bool           `json:"delete_pending"`
	Error             string                    `json:"error,omitempty"`
}

func (s *Snapshot) init() {
	if s.Accounts == nil {
		s.Accounts = map[string]AccountRecord{}
	}
	if s.Schedules == nil {
		s.Schedules = map[string]ScheduleEntry{}
	}
	if s.Jobs == nil {
		s.Jobs = map[string]Job{}
	}
	if s.WorkItems == nil {
		s.WorkItems = map[string]WorkItem{}
	}
	if s.RefreshPending == nil {
		s.RefreshPending = map[string]RefreshPending{}
	}
	if s.KnownSessions == nil {
		s.KnownSessions = map[string]KnownSession{}
	}
	if s.DeletePending == nil {
		s.DeletePending = map[string]bool{}
	}
	if s.History == nil {
		s.History = []HistoryEntry{}
	}
}
func (s *Snapshot) CancelWork(id string) {
	for k, w := range s.WorkItems {
		if w.AccountID == id && (w.Status == "queued" || w.Status == "running" || w.Status == "retry") {
			w.Status = "canceled"
			s.WorkItems[k] = w
		}
	}
	s.UpdateJobs()
}

func (s *Snapshot) UpdateJobs() {
	for id, job := range s.Jobs {
		running, failed := false, false
		for _, wid := range job.WorkIDs {
			w, exists := s.WorkItems[wid]
			running = running || w.Status == "queued" || w.Status == "running" || w.Status == "retry"
			failed = failed || !exists || w.Status == "failed" || w.Status == "canceled"
		}
		switch {
		case running:
			job.Status = "running"
		case failed:
			job.Status = "failed"
		default:
			job.Status = "completed"
		}
		s.Jobs[id] = job
	}
}
func (s *Snapshot) RecordResult(id string, r core.AttemptResult) {
	cutoff := r.ObservedAt.AddDate(0, 0, -30)
	h := make([]HistoryEntry, 0, len(s.History)+1)
	merged := false
	for _, v := range s.History {
		if v.Result.ObservedAt.Before(cutoff) {
			continue
		}
		if r.ServerDate != "" && v.AccountID == id && v.Result.ServerDate == r.ServerDate && (r.Status == "success" || r.Status == "already") && (v.Result.Status == "success" || v.Result.Status == "already") {
			if v.Result.Status == "success" && r.Status == "already" {
				h = append(h, v)
			} else {
				h = append(h, HistoryEntry{id, r})
			}
			merged = true
			continue
		}
		h = append(h, v)
	}
	if !merged {
		h = append(h, HistoryEntry{id, r})
	}
	s.History = h
	a := s.Accounts[id]
	a.LastResult = &r
	s.Accounts[id] = a
}

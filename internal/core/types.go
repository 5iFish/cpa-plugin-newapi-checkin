package core

import (
	"context"
	"errors"
	"time"
)

type Credential struct {
	Kind             string `json:"kind"`
	Channel          string `json:"channel"`
	Token            string `json:"token,omitempty"`
	AccessToken      string `json:"access_token,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	AccessExpiresAt  int64  `json:"access_expires_at,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	SessionExpiresAt int64  `json:"session_expires_at,omitempty"`
	ExpiresAtHint    *int64 `json:"expires_at_hint,omitempty"`
	Generation       uint64 `json:"generation"`
}
type Account struct {
	ID             string     `json:"account_id"`
	Site           string     `json:"site"`
	UpstreamUserID int64      `json:"upstream_user_id"`
	Username       string     `json:"username"`
	Label          string     `json:"label"`
	Disabled       bool       `json:"disabled,omitempty"`
	Credential     Credential `json:"credential"`
}
type Identity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}
type SiteInfo struct {
	TurnstileCheck *bool `json:"turnstile_check"`
}
type CheckinRecord struct {
	Date         string `json:"checkin_date"`
	QuotaAwarded int64  `json:"quota_awarded"`
}
type CheckinStatus struct {
	CheckedInToday bool            `json:"checked_in_today"`
	Records        []CheckinRecord `json:"records"`
	TotalQuota     int64           `json:"total_quota"`
	TotalCheckins  int64           `json:"total_checkins"`
}
type AttemptResult struct {
	Status         string    `json:"status"`
	ServerDate     string    `json:"server_date,omitempty"`
	QuotaAwarded   int64     `json:"quota_awarded"`
	ObservedAt     time.Time `json:"observed_at"`
	StatsSyncedAt  time.Time `json:"stats_synced_at"`
	NeedsReconcile bool      `json:"needs_reconcile"`
}
type SessionBundle struct {
	AccessToken, RefreshToken string
	AccessExpiresAt           int64
	SessionID                 string
	SessionExpiresAt          int64
	Identity                  Identity
}
type AccountSummary struct {
	ID                      string         `json:"account_id"`
	Site                    string         `json:"site"`
	Username                string         `json:"username"`
	Label                   string         `json:"label"`
	Kind                    string         `json:"kind"`
	Channel                 string         `json:"channel"`
	Status                  string         `json:"status"`
	ExpiresAt               *int64         `json:"expires_at,omitempty"`
	TokenHint               string         `json:"token_hint,omitempty"`
	WritePermissionVerified bool           `json:"write_permission_verified"`
	TotalQuota              int64          `json:"total_quota"`
	TotalCheckins           int64          `json:"total_checkins"`
	StatsSyncedAt           time.Time      `json:"stats_synced_at"`
	StatsStale              bool           `json:"stats_stale"`
	LastResult              *AttemptResult `json:"last_result,omitempty"`
}
type APIError struct {
	Kind        string        `json:"kind"`
	Code        string        `json:"code,omitempty"`
	MessageKey  string        `json:"message_key,omitempty"`
	SafeMessage string        `json:"message"`
	HTTPStatus  int           `json:"-"`
	RetryAfter  time.Duration `json:"-"`
}

func (e *APIError) Error() string { return e.Kind + ": " + e.SafeMessage }
func Error(kind string) *APIError { return &APIError{Kind: kind, SafeMessage: kind} }
func ErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var e *APIError
	if errors.As(err, &e) {
		return e.Kind
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	return "unknown_error"
}

type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }
func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

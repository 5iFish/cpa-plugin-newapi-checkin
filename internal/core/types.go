package core

import (
	"context"
	"encoding/json"
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
	// Third-party login state for login-mode accounts: a raw Cookie header that
	// is only ever sent to the provider's own domain. Secret; never summarized.
	OAuthProvider string `json:"oauth_provider,omitempty"`
	OAuthCookie   string `json:"oauth_cookie,omitempty"`
	Generation    uint64 `json:"generation"`
}

// OAuth providers whose session cookie can drive a fresh site login.
const (
	OAuthProviderGitHub  = "github"
	OAuthProviderLinuxDO = "linuxdo"
)

func ValidOAuthProvider(p string) bool {
	return p == "" || p == OAuthProviderGitHub || p == OAuthProviderLinuxDO
}

type Account struct {
	ID             string     `json:"account_id"`
	Site           string     `json:"site"`
	UpstreamUserID int64      `json:"upstream_user_id"`
	Username       string     `json:"username"`
	Label          string     `json:"label"`
	CheckinMode    string     `json:"checkin_mode,omitempty"`
	Disabled       bool       `json:"disabled,omitempty"`
	Credential     Credential `json:"credential"`
}

// Checkin modes: the standard check-in API, or sites that grant the daily
// reward on a fresh login (reward detected via /api/user/self evidence).
const (
	CheckinModeAPI   = "api"
	CheckinModeLogin = "login"
)

// Reward states for login-mode accounts.
const (
	RewardStatePendingLogin   = "pending_login"
	RewardStateSuccess        = "success"
	RewardStateUnknown        = "reward_unknown"
	RewardStateCookieExpired  = "cookie_expired"
	RewardStateNeedsAttention = "needs_attention"
	// RewardStateChallengeBlocked marks a Cloudflare-style human-verification
	// wall against this server: no stored credential can pass it.
	RewardStateChallengeBlocked = "challenge_blocked"
	// ErrorWafBlocked marks a bot-verification page (for example Aliyun WAF
	// in front of /api/user/self) returned instead of the API's JSON.
	ErrorWafBlocked = "waf_blocked"
)

// EffectiveCheckinMode normalizes unset or unknown values to the standard API
// mode so a corrupted value never silently switches a bound account.
func (a Account) EffectiveCheckinMode() string {
	if a.CheckinMode == CheckinModeLogin {
		return CheckinModeLogin
	}
	return CheckinModeAPI
}
func ValidCheckinMode(mode string) bool {
	return mode == "" || mode == CheckinModeAPI || mode == CheckinModeLogin
}

type Identity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	// Reward evidence from /api/user/self for login-mode accounts. Presence is
	// tracked separately because zero is a valid balance and login timestamp.
	Quota            int64 `json:"quota"`
	UsedQuota        int64 `json:"used_quota"`
	LastLogin        int64 `json:"last_login_time"`
	QuotaPresent     bool  `json:"-"`
	UsedQuotaPresent bool  `json:"-"`
	LastLoginPresent bool  `json:"-"`
	// Some NewAPI forks replace the login timestamp with a daily allowance:
	// the first signed-in visit of each calendar day credits a fixed quota and
	// records that date in setting.allowance_date. It is the same-day reward
	// signal that last_login_time provides elsewhere.
	AllowanceDate    string `json:"-"`
	AllowancePresent bool   `json:"-"`
}

func (i *Identity) UnmarshalJSON(b []byte) error {
	var v struct {
		ID        int64           `json:"id"`
		Username  string          `json:"username"`
		Quota     *int64          `json:"quota"`
		UsedQuota *int64          `json:"used_quota"`
		LastLogin *int64          `json:"last_login_time"`
		Setting   json.RawMessage `json:"setting"`
	}
	if e := json.Unmarshal(b, &v); e != nil {
		return e
	}
	i.ID, i.Username = v.ID, v.Username
	i.Quota, i.QuotaPresent = 0, v.Quota != nil
	if v.Quota != nil {
		i.Quota = *v.Quota
	}
	i.UsedQuota, i.UsedQuotaPresent = 0, v.UsedQuota != nil
	if v.UsedQuota != nil {
		i.UsedQuota = *v.UsedQuota
	}
	i.LastLogin, i.LastLoginPresent = 0, v.LastLogin != nil
	if v.LastLogin != nil {
		i.LastLogin = *v.LastLogin
	}
	// setting is an opaque JSON document (a JSON string in current NewAPI);
	// accept either form. A malformed value only means this site exposes no
	// allowance, never that the whole self lookup failed.
	if len(v.Setting) > 0 && string(v.Setting) != "null" {
		raw := v.Setting
		var wrapped string
		if json.Unmarshal(raw, &wrapped) == nil && wrapped != "" {
			raw = []byte(wrapped)
		}
		var s struct {
			AllowanceDate string `json:"allowance_date"`
		}
		if json.Unmarshal(raw, &s) == nil && s.AllowanceDate != "" {
			i.AllowanceDate, i.AllowancePresent = s.AllowanceDate, true
		}
	}
	return nil
}

// HasRewardEvidence reports whether /api/user/self exposed every field login
// mode needs; absence is different from a zero value. A daily-allowance date
// substitutes for the missing login timestamp.
func (i Identity) HasRewardEvidence() bool {
	return i.QuotaPresent && i.UsedQuotaPresent && (i.LastLoginPresent || i.AllowancePresent)
}

// LoginEvidenceOn reports whether the self snapshot carries a same-day reward
// signal: either a login timestamp inside the given local day, or a daily
// allowance dated with the local day key.
func (i Identity) LoginEvidenceOn(dayKey string, dayStart int64) bool {
	if i.AllowancePresent && i.AllowanceDate == dayKey {
		return true
	}
	return i.LastLoginPresent && i.LastLogin >= dayStart && i.LastLogin > 0
}

type SiteInfo struct {
	TurnstileCheck *bool `json:"turnstile_check"`
	// QuotaPerUnit is the site's currency conversion (quota per 1 USD); zero
	// when the site does not publish it.
	QuotaPerUnit int64 `json:"quota_per_unit"`
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

// ModelUsage is one model's aggregated consumption within a usage window. The
// numbers come from the site's own consumption logs (/api/log/self, type=2);
// no request details, token names or addresses are kept.
type ModelUsage struct {
	Model            string `json:"model"`
	Calls            int64  `json:"calls"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	CacheTokens      int64  `json:"cache_tokens"`
	UseSeconds       int64  `json:"use_seconds"`
	Quota            int64  `json:"quota"`
}

// StatsTotals is one window's consumption over every bound account. Quota is
// the raw mixed-site total for reference only; CostUSD is the sum of each
// account's quota converted with its own quota_per_unit.
type StatsTotals struct {
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	Quota            int64   `json:"quota"`
	CostUSD          float64 `json:"cost_usd"`
}

// StatsAccount is one bound account's consumption in the window. PerUnit is
// the site ratio used to convert Quota into CostUSD; zero means the site has
// not published a ratio and the cost is excluded.
type StatsAccount struct {
	ID               string  `json:"account_id"`
	Label            string  `json:"label"`
	Site             string  `json:"site"`
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	Quota            int64   `json:"quota"`
	PerUnit          int64   `json:"per_unit"`
	CostUSD          float64 `json:"cost_usd"`
}

// StatsModel is one model aggregated across every bound account.
type StatsModel struct {
	Model            string  `json:"model"`
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CacheTokens      int64   `json:"cache_tokens"`
	Quota            int64   `json:"quota"`
	CostUSD          float64 `json:"cost_usd"`
}

// StatsModelTokens is one model's tokens inside one time bucket.
type StatsModelTokens struct {
	Model  string `json:"model"`
	Tokens int64  `json:"tokens"`
}

// StatsBucket is one time bucket of the stacked chart; bucket starts are
// aligned to the schedule timezone (see UsageStats).
type StatsBucket struct {
	Start  int64              `json:"start"`
	Tokens []StatsModelTokens `json:"tokens"`
}

// StatsSummary is the console statistics payload. It holds aggregates only:
// no request details, addresses or token names.
type StatsSummary struct {
	Range string `json:"range"`
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	// SyncedAt is when the live aggregation finished; cached payloads keep the
	// original timestamp.
	SyncedAt time.Time `json:"synced_at"`
	// Truncated reports at least one account hit the page cap.
	Truncated bool `json:"truncated"`
	// PartialCost reports at least one account has no quota_per_unit, so its
	// consumption cannot be converted to USD and is excluded from costs.
	PartialCost bool              `json:"partial_cost"`
	Totals      StatsTotals       `json:"totals"`
	Accounts    []StatsAccount    `json:"accounts"`
	Models      []StatsModel      `json:"models"`
	Series      []StatsBucket     `json:"series"`
	Failed      map[string]string `json:"failed"`
}

// SiteUsageStats is one site's aggregation before cross-account merging;
// quota stays raw and the account layer converts it with per_unit.
type SiteUsageStats struct {
	Models []ModelUsage
	Series []StatsBucket
	Totals StatsTotals
}

type AccountSummary struct {
	UpstreamUserID int64  `json:"upstream_user_id"`
	ID             string `json:"account_id"`
	Site           string `json:"site"`
	Username       string `json:"username"`
	Label          string `json:"label"`
	Kind           string `json:"kind"`
	Channel        string `json:"channel"`
	// OAuthProvider reports the stored third-party login provider (github or
	// linuxdo) for login-mode accounts; empty when no session is imported.
	OAuthProvider string `json:"oauth_provider,omitempty"`
	CheckinMode   string `json:"checkin_mode"`
	RewardState   string `json:"reward_state,omitempty"`
	RewardTotal   int64  `json:"reward_total,omitempty"`
	// BalanceQuota is the site wallet balance from /api/user/self; nil when it
	// has never been observed (older records, or a site that omits quota).
	BalanceQuota *int64 `json:"balance_quota,omitempty"`
	// BalancePerUnit is the site's quota-per-USD ratio used to render the
	// wallet balance the way the site's own wallet page does.
	BalancePerUnit int64  `json:"balance_per_unit,omitempty"`
	Status         string `json:"status"`
	// CreatedAt is the first successful bind time; the console sorts by it.
	CreatedAt               time.Time      `json:"created_at,omitempty"`
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

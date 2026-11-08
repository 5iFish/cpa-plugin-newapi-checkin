package newapi

import (
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func classify(r *http.Response, v *wireEnvelope) *core.APIError {
	kind := "unknown_error"
	code := strings.ToUpper(v.Code)
	switch code {
	case "ACCESS_TOKEN_SCOPE_DENIED", "INSUFFICIENT_SCOPE", "ACCESS_TOKEN_ROUTE_UNDECLARED":
		kind = "permission_denied"
	case "ACCESS_TOKEN_LEGACY_RETIRED":
		kind = "legacy_retired"
	case "AUTH_ACCESS_TOKEN_EXPIRED", "ACCESS_TOKEN_EXPIRED", "AUTH_TOKEN_EXPIRED":
		kind = "credential_expired"
	case "AUTH_ORIGIN_FORBIDDEN":
		kind = "origin_forbidden"
	default:
		text := v.MessageKey
		if text == "" {
			text = v.Message
		}
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "turnstile"):
			kind = "turnstile_blocked"
		case lower == "check-in feature is not enabled" || text == "签到功能未开启":
			kind = "not_enabled"
		case strings.Contains(lower, "already checked") || text == "今日已签到":
			kind = "already"
		case r.StatusCode == 401:
			kind = "credential_expired"
		case r.StatusCode == 403:
			kind = "permission_denied"
		case r.StatusCode == 429:
			kind = "rate_limited"
		case r.StatusCode >= 500:
			kind = "upstream_error"
		case r.StatusCode >= 300 && r.StatusCode < 400 || r.StatusCode == 404:
			kind = "unsupported_site"
		}
	}
	e := core.Error(kind)
	e.HTTPStatus = r.StatusCode
	// Only known protocol codes are exposed, never arbitrary upstream text.
	switch code {
	case "ACCESS_TOKEN_SCOPE_DENIED", "INSUFFICIENT_SCOPE", "ACCESS_TOKEN_ROUTE_UNDECLARED", "ACCESS_TOKEN_LEGACY_RETIRED", "AUTH_ACCESS_TOKEN_EXPIRED", "ACCESS_TOKEN_EXPIRED", "AUTH_TOKEN_EXPIRED", "AUTH_ORIGIN_FORBIDDEN", "AUTH_SESSION_REVOKED", "AUTH_UNAUTHORIZED", "AUTH_REFRESH_RACE", "AUTH_SESSION_MISMATCH", "AUTH_SESSION_ISSUANCE_LIMIT", "AUTH_SESSION_LIMIT":
		e.Code = code
	}
	if n, err := strconv.Atoi(r.Header.Get("Retry-After")); err == nil && n > 0 {
		e.RetryAfter = time.Duration(n) * time.Second
	} else if d, err := http.ParseTime(r.Header.Get("Retry-After")); err == nil && d.After(time.Now()) {
		e.RetryAfter = time.Until(d)
	}
	return e
}

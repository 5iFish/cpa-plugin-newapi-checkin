package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnknownUpstreamCodeIsNotEchoed(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"success":false,"code":"SECRET_TOKEN_MARKER"}`))
	}))
	defer s.Close()
	_, e := NewClient(s.Client()).Self(context.Background(), s.URL, "", 0)
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), "SECRET_TOKEN_MARKER") {
		t.Fatal("arbitrary upstream code escaped into public error")
	}
}

func kind(t *testing.T, err error, want string) {
	t.Helper()
	var e *core.APIError
	if !errors.As(err, &e) || e.Kind != want {
		t.Fatalf("got %v want %s", err, want)
	}
}
func TestCheckinContract(t *testing.T) {
	var calls []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing bearer")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":12,"username":"hello"}}`))
		case "GET /api/user/checkin":
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabled":true,"stats":{"checked_in_today":false,"total_quota":100,"total_checkins":3,"records":[]}}}`))
		case "POST /api/user/checkin":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_awarded":10,"checkin_date":"2026-11-08"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := NewClient(s.Client())
	ctx := context.Background()
	if id, e := c.Self(ctx, s.URL, "test-secret", 0); e != nil || id.ID != 12 {
		t.Fatal(id, e)
	}
	if st, e := c.CheckinStatus(ctx, s.URL, "test-secret", "", 0); e != nil || st.TotalQuota != 100 || st.CheckedInToday {
		t.Fatal(st, e)
	}
	if r, e := c.Checkin(ctx, s.URL, "test-secret", 0); e != nil || r.Date != "2026-11-08" || r.QuotaAwarded != 10 {
		t.Fatal(r, e)
	}
	if strings.Contains(strings.Join(calls, ","), "?") {
		t.Fatal(calls)
	}
}
func TestSelfParsesRewardFields(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":814507,"username":"u","quota":44749966,"used_quota":17750034,"last_login_time":1794654511}}`))
	}))
	defer s.Close()
	id, e := NewClient(s.Client()).Self(context.Background(), s.URL, "test-secret", 0)
	if e != nil || id.ID != 814507 {
		t.Fatal(id, e)
	}
	if id.Quota != 44749966 || id.UsedQuota != 17750034 || id.LastLogin != 1794654511 {
		t.Fatal(id)
	}
}

func TestSelfWithoutRewardFieldsStillValidIdentity(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"u"}}`))
	}))
	defer s.Close()
	id, e := NewClient(s.Client()).Self(context.Background(), s.URL, "test-secret", 0)
	if e != nil || id.Quota != 0 || id.UsedQuota != 0 || id.LastLogin != 0 {
		t.Fatal(id, e)
	}
}

func TestScopeErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"success":false,"code":"insufficient_scope","message":"test-secret"}`))
	}))
	defer s.Close()
	_, e := NewClient(s.Client()).Self(context.Background(), s.URL, "test-secret", 0)
	kind(t, e, "permission_denied")
	if strings.Contains(e.Error(), "test-secret") {
		t.Fatal("secret leaked")
	}
}
func TestSuccessFalseWithHTTP200(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"unrecognized test-secret"}`))
	}))
	defer s.Close()
	_, e := NewClient(s.Client()).Self(context.Background(), s.URL, "", 0)
	kind(t, e, "unknown_error")
}
func TestHTMLResponse(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>login</html>`)) }))
	defer s.Close()
	_, e := NewClient(s.Client()).Self(context.Background(), s.URL, "", 0)
	kind(t, e, "unsupported_site")
}
func TestBotChallengePageIsReportedAsWafBlocked(t *testing.T) {
	// Aliyun WAF answers /api/user/self with HTTP 200 and its captcha loader;
	// that is a wall in front of the endpoint, not a site without the API.
	pages := map[string]string{
		"aliyun":     `<!doctype html><meta name="aliyun_waf_aa" content="x"><script src="https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js"></script>`,
		"cloudflare": `<html><script src="/cdn-cgi/challenge-platform/h/b/orchestrate/chl_page/v1"></script></html>`,
	}
	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(page))
			}))
			defer s.Close()
			_, e := NewClient(s.Client()).Self(context.Background(), s.URL, "", 0)
			kind(t, e, "waf_blocked")
		})
	}
}
func TestMissingUserIDIsActionable(t *testing.T) {
	// Real NewAPI/AgentRouter wording: the header is missing (English or
	// Chinese) or does not match the token's owner. All of them must guide the
	// user to the numeric ID instead of claiming the credential expired.
	for name, body := range map[string]string{
		"english missing":  `{"success":false,"message":"Unauthorized, New-Api-User header not provided"}`,
		"chinese missing":  `{"success":false,"message":"无权进行此操作，未提供 New-Api-User"}`,
		"chinese mismatch": `{"success":false,"message":"无权进行此操作，New-Api-User 与登录用户不匹配"}`,
		"english mismatch": `{"success":false,"message":"unauthorized: New-Api-User does not match the logged-in user"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(body))
			}))
			defer s.Close()
			_, err := NewClient(s.Client()).Self(context.Background(), s.URL, "valid-token", 0)
			kind(t, err, "user_id_required")
		})
	}
}
func TestMissingRequiredFields(t *testing.T) {
	for _, body := range []string{`{"success":true,"data":{"stats":{"total_quota":0,"total_checkins":0}}}`, `{"success":true,"data":{"stats":{"checked_in_today":false}}}`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer s.Close()
			_, e := NewClient(s.Client()).CheckinStatus(context.Background(), s.URL, "", "", 0)
			kind(t, e, "unsupported_site")
		})
	}
}
func TestNormalizeSiteIdentity(t *testing.T) {
	a, e := NormalizeSite(" HTTPS://EXAMPLE.COM:443/Sub/ ")
	if e != nil || a != "https://example.com/Sub" {
		t.Fatal(a, e)
	}
	b, _ := NormalizeSite("https://example.com/Sub")
	if core.AccountID(a, 12) != core.AccountID(b, 12) {
		t.Fatal("unstable identity")
	}
	for _, x := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?q=x", "https://example.com/#x", "file:///tmp/a"} {
		if _, e := NormalizeSite(x); e == nil {
			t.Fatal(x)
		}
	}
}
func TestRedirectDoesNotLeakBearer(t *testing.T) {
	n := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n++ }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer s.Close()
	_, e := NewClient(s.Client()).Self(context.Background(), s.URL, "secret", 0)
	kind(t, e, "unsupported_site")
	if n != 0 {
		t.Fatal("redirect followed")
	}
}
func TestResponseLimit(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", (2<<20)+1))) }))
	defer s.Close()
	_, e := NewClient(s.Client()).Self(context.Background(), s.URL, "", 0)
	kind(t, e, "unsupported_site")
}

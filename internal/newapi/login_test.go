package newapi

import (
	"context"
	"encoding/json"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeOAuthPair builds a fake site plus a fake third-party authorize endpoint
// and replays the verified AgentRouter wire contract between them. newStyle
// switches the site to the current NewAPI contract: POST /api/oauth/state
// returns a flow token and the legacy GET form is rejected.
type fakeOAuthPair struct {
	site, provider *httptest.Server
	stateQueries   int
	postQueries    int
	newStyle       bool
	callbackHits   int
	providerCookie string
}

func newFakeOAuthPair(t *testing.T) *fakeOAuthPair {
	t.Helper()
	p := &fakeOAuthPair{}
	p.site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"turnstile_check":false,"github_oauth":true,"github_client_id":"cid123"}}`))
		case "/api/oauth/state":
			if r.Method == http.MethodPost {
				p.postQueries++
				if !p.newStyle {
					http.NotFound(w, r)
					return
				}
				var body struct {
					Provider string `json:"provider"`
					Intent   string `json:"intent"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Provider != "github" || body.Intent != "login" {
					t.Error("new-style state body mismatch", body)
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"success":false,"message":"Invalid parameters"}`))
					return
				}
				_, _ = w.Write([]byte(`{"success":true,"data":{"flow_token":"st-9f","expires_at":1794802799}}`))
				return
			}
			p.stateQueries++
			if p.newStyle {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"success":false,"message":"Unknown OAuth provider"}`))
				return
			}
			if r.URL.Query().Get("mode") != "login" {
				t.Error("state request missing mode=login")
			}
			_, _ = w.Write([]byte(`{"success":true,"data":"st-9f"}`))
		case "/api/oauth/github":
			p.callbackHits++
			if r.URL.Query().Get("state") != "st-9f" || r.URL.Query().Get("code") != "CODE1" {
				t.Error("callback query mismatch", r.URL.RawQuery)
			}
			if strings.Contains(r.Header.Get("Cookie"), "user_sessions") {
				t.Error("third-party cookie leaked to the site")
			}
			http.SetCookie(w, &http.Cookie{Name: "new_api_refresh", Value: "sid-refresh", Path: "/"})
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":814507,"username":"u","checked_in":true,"access_token":"access-minted","access_expires_at":1794802799,"session":{"sid":"sid-mint","expires_at":1797393899}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.site.Close)
	p.provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/authorize" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Cookie") != p.providerCookie {
			t.Errorf("authorize cookie = %q want %q", r.Header.Get("Cookie"), p.providerCookie)
		}
		if r.URL.Query().Get("client_id") != "cid123" || r.URL.Query().Get("state") != "st-9f" {
			t.Error("authorize query mismatch", r.URL.RawQuery)
		}
		http.Redirect(w, r, p.site.URL+"/oauth/github?code=CODE1&state=st-9f", http.StatusFound)
	}))
	t.Cleanup(p.provider.Close)
	return p
}

func fakeChainTargets(f *fakeOAuthPair) loginTargets {
	providerURL, _ := url.Parse(f.provider.URL)
	siteURL, _ := url.Parse(f.site.URL)
	return loginTargets{
		authorizeURL: providerURL.String() + "/login/oauth/authorize",
		cookieDomain: providerURL.Hostname(),
		callbackHost: siteURL.Host,
		providerName: "github",
	}
}

func TestLoginChainReplaySendsCookieOnlyToProvider(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=abc; logged_in=1"
	res, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	if e != nil || res.UserID != 814507 || res.Username != "u" || res.CheckedIn == nil || !*res.CheckedIn {
		t.Fatal(res, e)
	}
	if f.stateQueries != 1 || f.callbackHits != 1 {
		t.Fatal(f.stateQueries, f.callbackHits)
	}
}

// TestLoginChainSupportsNewAPIStateFlow covers the current NewAPI wire
// contract: the state comes from POST /api/oauth/state as a flow token, and
// the callback path is still /api/oauth/<provider>.
func TestLoginChainSupportsNewAPIStateFlow(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.newStyle = true
	f.providerCookie = "user_sessions=abc"
	res, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	if e != nil || res.UserID != 814507 || res.AccessToken != "access-minted" {
		t.Fatal(res, e)
	}
	if f.postQueries != 1 || f.stateQueries != 0 || f.callbackHits != 1 {
		t.Fatalf("post=%d get=%d callback=%d", f.postQueries, f.stateQueries, f.callbackHits)
	}
}

func TestLoginChainKeepsPerLoginSiteSession(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=abc"
	original := f.site.Config.Handler
	f.site.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/state":
			http.SetCookie(w, &http.Cookie{Name: "oauth_session", Value: "state-bound", Path: "/", HttpOnly: true})
		case "/api/oauth/github":
			cookie, err := r.Cookie("oauth_session")
			if err != nil || cookie.Value != "state-bound" {
				t.Error("callback missing the site session cookie created by /api/oauth/state")
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		original.ServeHTTP(w, r)
	})
	res, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	if e != nil || res.UserID != 814507 {
		t.Fatal("a session-bound OAuth login must complete", res, e)
	}
}

func TestLoginChainRunsHookBeforeCallback(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=abc"
	calls := 0
	hooks := &LoginHooks{BeforeCallback: func(context.Context) error {
		calls++
		if f.callbackHits != 0 {
			t.Error("hook ran after the callback")
		}
		return nil
	}}
	if _, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), hooks); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	blocked := &LoginHooks{BeforeCallback: func(context.Context) error { return core.Error("persistence_error") }}
	if _, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), blocked); core.ErrorKind(e) != "persistence_error" || f.callbackHits != 1 {
		t.Fatal("a rejected marker must stop the callback", e, f.callbackHits)
	}
}

func TestLoginChainCookieExpiredOnLoginPage(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=stale"
	f.provider.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><form><input type="password" name="password"></form></html>`))
	})
	_, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	kind(t, e, "cookie_expired")
	if f.callbackHits != 0 {
		t.Fatal("callback reached with expired cookie")
	}
}

func TestLoginChainCloudflareChallengeBlocked(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=valid"
	f.provider.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "cloudflare")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Just a moment...</title></head></html>`))
	})
	_, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	kind(t, e, core.RewardStateChallengeBlocked)
	if f.callbackHits != 0 {
		t.Fatal("callback reached through a challenge wall")
	}
}

func TestLoginChainConsentPageNeedsAttention(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=valid"
	f.provider.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><form action="/oauth2/authorize"><button>Authorize</button></form></html>`))
	})
	_, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	kind(t, e, "needs_attention")
	if f.callbackHits != 0 {
		t.Fatal("callback reached from a consent page")
	}
}

func TestLoginChainRejectsForeignRedirect(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=abc"
	f.provider.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/oauth/github?code=X&state=st-9f", http.StatusFound)
	})
	_, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	kind(t, e, "needs_attention")
	if f.callbackHits != 0 {
		t.Fatal("callback reached from foreign redirect")
	}
}

func TestLoginChainRejectsStateMismatch(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=abc"
	f.provider.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.site.URL+"/oauth/github?code=CODE1&state=tampered", http.StatusFound)
	})
	_, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	kind(t, e, "needs_attention")
}

func TestLoginChainMapsCallbackDenial(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=abc"
	f.site.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"github_oauth":true,"github_client_id":"cid123"}}`))
		case "/api/oauth/state":
			_, _ = w.Write([]byte(`{"success":true,"data":"st-9f"}`))
		default:
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"success":false,"message":"WAF blocked"}`))
		}
	})
	_, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	kind(t, e, "needs_attention")
}

func TestProductionLoginTargets(t *testing.T) {
	github := productionTargets(core.OAuthProviderGitHub)
	if github.authorizeURL != "https://github.com/login/oauth/authorize" || github.cookieDomain != "github.com" || github.providerName != "github" {
		t.Fatal(github)
	}
	linuxdo := productionTargets(core.OAuthProviderLinuxDO)
	if linuxdo.authorizeURL != "https://connect.linux.do/oauth2/authorize" || linuxdo.cookieDomain != "connect.linux.do" || linuxdo.providerName != "linuxdo" {
		t.Fatal(linuxdo)
	}
	if q := oauthAuthorizeQuery("github", "cid", "st 1"); q != "client_id=cid&state=st+1" {
		t.Fatal(q)
	}
	if q := oauthAuthorizeQuery("linuxdo", "cid", "s"); q != "client_id=cid&response_type=code&state=s" {
		t.Fatal(q)
	}
}

func TestLoginChainProviderCookieRotationUsesJar(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=original"
	cookieCount := 0
	f.provider.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/authorize":
			http.SetCookie(w, &http.Cookie{Name: "user_sessions", Value: "rotated", Path: "/", HttpOnly: true})
			http.Redirect(w, r, "/continue", http.StatusFound)
		case "/continue":
			cookieCount = len(r.Cookies())
			cookie, err := r.Cookie("user_sessions")
			if err != nil || cookie.Value != "rotated" {
				_, _ = w.Write([]byte(`<html><input type="password"></html>`))
				return
			}
			http.Redirect(w, r, f.site.URL+"/oauth/github?code=CODE1&state=st-9f", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
	res, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	if e != nil || res.UserID != 814507 || cookieCount != 1 {
		t.Fatalf("provider Set-Cookie must replace the imported value: status=%s cookies=%d callback_hits=%d", core.ErrorKind(e), cookieCount, f.callbackHits)
	}
}

func TestLoginChainCapturesSiteSession(t *testing.T) {
	f := newFakeOAuthPair(t)
	f.providerCookie = "user_sessions=abc"
	res, e := NewClient(f.site.Client()).loginChain(context.Background(), f.site.URL, f.providerCookie, "cid123", fakeChainTargets(f), nil)
	if e != nil {
		t.Fatal(e)
	}
	if res.AccessToken != "access-minted" || res.AccessExpiresAt != 1794802799 || res.SessionID != "sid-mint" || res.SessionExpires != 1797393899 || res.RefreshCookie != "sid-refresh" {
		t.Fatalf("callback session not captured: %+v", res)
	}
}

func TestOAuthClientIDFromStatus(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"github_oauth":true,"github_client_id":"cid123","linuxdo_oauth":false}}`))
	}))
	defer s.Close()
	c := NewClient(s.Client())
	id, e := c.oauthClientID(context.Background(), s.URL, core.OAuthProviderGitHub)
	if e != nil || id != "cid123" {
		t.Fatal(id, e)
	}
	_, e = c.oauthClientID(context.Background(), s.URL, core.OAuthProviderLinuxDO)
	kind(t, e, "not_enabled")
}

func TestProbeProviderClassifiesReachability(t *testing.T) {
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "challenge":
			w.Header().Set("Cf-Mitigated-Header", "challenge")
			w.WriteHeader(http.StatusForbidden)
		case "cfmitigated":
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusOK)
		case "cloudflare403":
			w.Header().Set("Server", "cloudflare")
			w.WriteHeader(http.StatusForbidden)
		case "page":
			_, _ = w.Write([]byte("<html>Just a moment...</html>"))
		default:
			http.Redirect(w, r, "https://example.com/callback", http.StatusFound)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c := NewClient(srv.Client())
	c.SetLoginEndpoints(func(string) LoginEndpoints {
		return LoginEndpoints{AuthorizeURL: srv.URL + "/oauth/authorize", CookieDomain: u.Hostname()}
	})
	for _, tc := range []struct{ mode, want string }{{"ok", "ok"}, {"challenge", "blocked"}, {"cfmitigated", "blocked"}, {"cloudflare403", "blocked"}, {"page", "blocked"}} {
		mode = tc.mode
		got, err := c.ProbeProvider(context.Background(), core.OAuthProviderLinuxDO)
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %q err %v want %q", tc.mode, got, err, tc.want)
		}
	}
	dead := NewClient(srv.Client())
	dead.SetLoginEndpoints(func(string) LoginEndpoints {
		return LoginEndpoints{AuthorizeURL: "http://127.0.0.1:1/oauth/authorize", CookieDomain: "127.0.0.1"}
	})
	if got, err := dead.ProbeProvider(context.Background(), core.OAuthProviderGitHub); err != nil || got != "unreachable" {
		t.Fatalf("closed endpoint: got %q err %v", got, err)
	}
	if _, err := c.ProbeProvider(context.Background(), "evil"); core.ErrorKind(err) != "invalid_input" {
		t.Fatalf("invalid provider accepted: %v", err)
	}
}

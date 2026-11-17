package newapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
)

// LoginChainResult carries the evidence of one fresh OAuth login replay. The
// identity comes from the site's own callback response; CheckedIn is the
// fork-specific check-in receipt and stays nil when the site does not send it.
type LoginChainResult struct {
	UserID    int64
	Username  string
	CheckedIn *bool
	// ProviderCookie is the provider jar rendered as a Cookie request header
	// after the chain, including any Set-Cookie rotation observed on the way.
	// It stays empty when the jar holds nothing for the authorize host.
	ProviderCookie string
	// Site session minted by the callback. NewAPI-style forks return the
	// access token (and its expiry) in the body plus, on most deployments, a
	// new_api_refresh cookie; together they bootstrap or rotate a site
	// credential on sites without a PAT entry point.
	AccessToken     string
	AccessExpiresAt int64
	SessionID       string
	SessionExpires  int64
	RefreshCookie   string
}

// LoginHooks lets the caller persist a login-attempt marker immediately before
// the callback request. That request is the first step that can complete a
// login; if it returns an uncertain result the caller must not replay the chain.
type LoginHooks struct {
	BeforeCallback func(context.Context) error
}

// loginTargets pins every network destination of the login chain. The stored
// third-party cookie is only ever attached to requests whose host equals
// cookieDomain; state and callback calls never see it.
type loginTargets struct {
	authorizeURL string
	cookieDomain string
	callbackHost string
	providerName string
}

func productionTargets(provider string) loginTargets {
	if provider == core.OAuthProviderLinuxDO {
		return loginTargets{authorizeURL: "https://connect.linux.do/oauth2/authorize", cookieDomain: "connect.linux.do", providerName: "linuxdo"}
	}
	return loginTargets{authorizeURL: "https://github.com/login/oauth/authorize", cookieDomain: "github.com", providerName: "github"}
}

// LoginEndpoints lets deployments and tests redirect the third-party authorize
// endpoint. The cookie domain travels with it because the stored cookie is only
// ever sent to the authorize host.
type LoginEndpoints struct{ AuthorizeURL, CookieDomain string }

// ProbeProvider checks whether this server can reach the provider's OAuth
// authorize endpoint at all — before an operator invests in importing a
// session cookie. It carries no cookie and completes no login: "ok" means the
// channel is usable, "blocked" a human-verification wall (Cloudflare) that no
// server-side credential can ever satisfy, "unreachable" a transport failure.
func (c *Client) ProbeProvider(ctx context.Context, provider string) (string, error) {
	if provider == "" || !core.ValidOAuthProvider(provider) {
		return "", core.Error("invalid_input")
	}
	t := productionTargets(provider)
	if c.loginEndpoints != nil {
		if e := c.loginEndpoints(provider); e.AuthorizeURL != "" && e.CookieDomain != "" {
			t.authorizeURL = e.AuthorizeURL
		}
	}
	hc := *c.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, t.authorizeURL, nil)
	if e != nil {
		return "", core.Error("invalid_input")
	}
	req.Header.Set("Accept", "text/html")
	res, e := hc.Do(req)
	if e != nil {
		return "unreachable", nil
	}
	defer func() { _ = res.Body.Close() }()
	if cloudflareChallenge(res) {
		return "blocked", nil
	}
	if res.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 128<<10))
		lower := strings.ToLower(string(body))
		if strings.Contains(lower, "cdn-cgi/challenge-platform") || strings.Contains(lower, "just a moment") {
			return "blocked", nil
		}
	}
	return "ok", nil
}

// splitJar keeps the site session and provider cookies in separate stores.
// Cookies are keyed by the exact site host (including port) or the provider
// domain, so a loopback test or a misconfigured deployment cannot leak one
// side's cookies to the other.
type splitJar struct {
	site           http.CookieJar
	provider       http.CookieJar
	siteHost       string
	providerDomain string
}

func (j *splitJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	switch {
	case u.Host == j.siteHost:
		j.site.SetCookies(u, cookies)
	case u.Hostname() == j.providerDomain:
		j.provider.SetCookies(u, cookies)
	}
}

func (j *splitJar) Cookies(u *url.URL) []*http.Cookie {
	switch {
	case u.Host == j.siteHost:
		return j.site.Cookies(u)
	case u.Hostname() == j.providerDomain:
		return j.provider.Cookies(u)
	}
	return nil
}

// SetLoginEndpoints installs an endpoint resolver; nil restores production.
func (c *Client) SetLoginEndpoints(fn func(provider string) LoginEndpoints) {
	c.loginEndpoints = fn
}

func oauthAuthorizeQuery(provider, clientID, state string) string {
	v := url.Values{}
	v.Set("client_id", clientID)
	if provider == core.OAuthProviderLinuxDO {
		v.Set("response_type", "code")
	}
	v.Set("state", state)
	return v.Encode()
}

func (c *Client) oauthClientID(ctx context.Context, site, provider string) (string, error) {
	b, _, e := c.request(ctx, site, "/api/status", "GET", "", nil, nil)
	if e != nil {
		return "", e
	}
	var v struct {
		GithubOAuth     bool   `json:"github_oauth"`
		LinuxdoOAuth    bool   `json:"linuxdo_oauth"`
		GithubClientID  string `json:"github_client_id"`
		LinuxdoClientID string `json:"linuxdo_client_id"`
	}
	if json.Unmarshal(b, &v) != nil {
		return "", core.Error("unsupported_site")
	}
	if provider == core.OAuthProviderGitHub {
		if !v.GithubOAuth || v.GithubClientID == "" {
			return "", core.Error("not_enabled")
		}
		return v.GithubClientID, nil
	}
	if !v.LinuxdoOAuth || v.LinuxdoClientID == "" {
		return "", core.Error("not_enabled")
	}
	return v.LinuxdoClientID, nil
}

// oauthState mints the CSRF state for one authorization redirect. Current
// NewAPI releases create a server-side auth flow with POST /api/oauth/state
// and return its flow token; AgentRouter-style forks predate that flow and
// return a bare state string from GET /api/oauth/state?mode=login. Try the
// current contract first and fall back to the legacy one when the endpoint
// does not exist or does not answer with a flow token.
func (c *Client) oauthState(ctx context.Context, site, provider string) (string, error) {
	b, _, e := c.request(ctx, site, "/api/oauth/state", "POST", "", map[string]string{
		"provider": provider,
		"intent":   "login",
	}, nil)
	if e == nil {
		var v struct {
			FlowToken string `json:"flow_token"`
		}
		if json.Unmarshal(b, &v) == nil && v.FlowToken != "" {
			return v.FlowToken, nil
		}
	} else if kind := core.ErrorKind(e); kind == "network_error" || kind == "rate_limited" || kind == core.RewardStateChallengeBlocked || kind == core.ErrorWafBlocked {
		// A transport or anti-bot failure is not evidence of an old API; retrying
		// with the legacy shape would only hide the real cause.
		return "", e
	}
	b, _, e = c.request(ctx, site, "/api/oauth/state?mode=login", "GET", "", nil, nil)
	if e != nil {
		return "", e
	}
	var state string
	if json.Unmarshal(b, &state) != nil || state == "" {
		return "", core.Error("unsupported_site")
	}
	return state, nil
}

// RunLoginChain replays one fresh site login with the stored third-party
// session cookie. It never writes anything to the site; the only mutation is
// the login itself, which is the reward trigger being requested.
func (c *Client) RunLoginChain(ctx context.Context, site, provider, cookie string, hooks *LoginHooks) (LoginChainResult, error) {
	normalized, e := NormalizeSite(site)
	if e != nil {
		return LoginChainResult{}, e
	}
	if cookie == "" || !core.ValidOAuthProvider(provider) || provider == "" {
		return LoginChainResult{}, core.Error("invalid_input")
	}
	clientID, e := c.oauthClientID(ctx, site, provider)
	if e != nil {
		return LoginChainResult{}, e
	}
	t := productionTargets(provider)
	if c.loginEndpoints != nil {
		if e := c.loginEndpoints(provider); e.AuthorizeURL != "" && e.CookieDomain != "" {
			t.authorizeURL = e.AuthorizeURL
			t.cookieDomain = e.CookieDomain
		}
	}
	siteURL, _ := url.Parse(normalized)
	t.callbackHost = siteURL.Host
	return c.loginChain(ctx, site, cookie, clientID, t, hooks)
}

func (c *Client) loginChain(ctx context.Context, site, cookie, clientID string, t loginTargets, hooks *LoginHooks) (res LoginChainResult, err error) {
	// A fresh jar per chain gives each login its own anonymous site session.
	// It is never shared with the normal API client or another account. The
	// imported third-party cookie only enters the provider jar, whose cookies
	// are only sent to the provider hostname by splitJar.
	siteJar, _ := cookiejar.New(nil)
	providerJar, _ := cookiejar.New(nil)
	// Whatever the outcome, surface the newest provider session so the caller
	// can persist a rotation instead of replaying an invalidated cookie.
	defer func() { res.ProviderCookie = providerCookieHeader(providerJar, t.authorizeURL) }()
	if e := importCookieHeader(providerJar, t.authorizeURL, cookie); e != nil {
		return LoginChainResult{}, e
	}
	hc := *c.http
	hc.Jar = &splitJar{site: siteJar, provider: providerJar, siteHost: t.callbackHost, providerDomain: t.cookieDomain}
	loginClient := &Client{http: &hc}

	state, e := loginClient.oauthState(ctx, site, t.providerName)
	if e != nil {
		return LoginChainResult{}, e
	}
	callback, e := loginClient.oauthAuthorize(ctx, t.authorizeURL+"?"+oauthAuthorizeQuery(t.providerName, clientID, state), t, state)
	if e != nil {
		return LoginChainResult{}, e
	}
	q := callback.Query()
	code := q.Get("code")
	if code == "" || q.Get("state") != state {
		return LoginChainResult{}, core.Error("needs_attention")
	}
	// The callback is the first request that can complete a login and grant the
	// reward. Persist evidence before sending it so an uncertain reply is never
	// resolved by replaying the whole chain.
	if hooks != nil && hooks.BeforeCallback != nil {
		if e := hooks.BeforeCallback(ctx); e != nil {
			return LoginChainResult{}, e
		}
	}
	res, e = loginClient.oauthCallback(ctx, site, t.providerName, code, state)
	if e != nil {
		return res, e
	}
	return res, nil
}

// oauthCallback completes one site OAuth login by replaying the callback URL
// through the given client, whose jar carries the site session that requested
// the state.
func (c *Client) oauthCallback(ctx context.Context, site, provider, code, state string) (LoginChainResult, error) {
	path := "/api/oauth/" + provider + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	b, cbRes, e := c.request(ctx, site, path, "GET", "", nil, nil)
	if e != nil {
		if core.ErrorKind(e) == "permission_denied" {
			return LoginChainResult{}, core.Error("needs_attention")
		}
		return LoginChainResult{}, e
	}
	refreshCookie := ""
	for _, sc := range cbRes.Header.Values("Set-Cookie") {
		if ck, err := (&http.Request{Header: http.Header{"Cookie": {sc}}}).Cookie("new_api_refresh"); err == nil {
			refreshCookie = ck.Value
			break
		}
	}
	var v struct {
		ID              int64  `json:"id"`
		Username        string `json:"username"`
		CheckedIn       *bool  `json:"checked_in"`
		AccessToken     string `json:"access_token"`
		AccessExpiresAt int64  `json:"access_expires_at"`
		Session         *struct {
			SID       string `json:"sid"`
			ExpiresAt int64  `json:"expires_at"`
		} `json:"session"`
		User *struct {
			ID        int64  `json:"id"`
			Username  string `json:"username"`
			CheckedIn *bool  `json:"checked_in"`
		} `json:"user"`
	}
	if json.Unmarshal(b, &v) != nil {
		return LoginChainResult{}, core.Error("unsupported_site")
	}
	res := LoginChainResult{UserID: v.ID, Username: v.Username, CheckedIn: v.CheckedIn,
		AccessToken: v.AccessToken, AccessExpiresAt: v.AccessExpiresAt, RefreshCookie: refreshCookie}
	if v.Session != nil {
		res.SessionID = v.Session.SID
		res.SessionExpires = v.Session.ExpiresAt
	}
	if v.User != nil {
		if res.UserID == 0 {
			res.UserID = v.User.ID
		}
		if res.Username == "" {
			res.Username = v.User.Username
		}
		if res.CheckedIn == nil {
			res.CheckedIn = v.User.CheckedIn
		}
	}
	if res.UserID <= 0 {
		return LoginChainResult{}, core.Error("unsupported_site")
	}
	return res, nil
}

// providerCookieHeader renders the provider jar as a Cookie request header for
// the authorize host, so a rotated session can be persisted back into the CPA
// credential file in the same format the user imported.
func providerCookieHeader(jar http.CookieJar, rawURL string) string {
	u, e := url.Parse(rawURL)
	if e != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range jar.Cookies(u) {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(c.Name)
		b.WriteString("=")
		b.WriteString(c.Value)
	}
	return b.String()
}

// importCookieHeader loads the user-supplied Cookie request header into the
// isolated provider jar once. Later redirects therefore see any Set-Cookie
// rotation from the provider instead of a stale header being re-applied.
func importCookieHeader(jar http.CookieJar, rawURL, header string) error {
	u, e := url.Parse(rawURL)
	if e != nil || u.Hostname() == "" {
		return core.Error("invalid_input")
	}
	var cookies []*http.Cookie
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return core.Error("invalid_input")
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: strings.TrimSpace(value), Path: "/"})
	}
	if len(cookies) == 0 {
		return core.Error("invalid_input")
	}
	jar.SetCookies(u, cookies)
	return nil
}

// oauthAuthorize walks the provider-side redirects manually. The stored session
// only earns a redirect when the provider still considers it signed in; a 200
// page is either a login form (expired cookie) or a consent/challenge page that
// needs a human. Only a redirect back to the site host with a matching state is
// accepted; anything else is refused rather than followed.
func (c *Client) oauthAuthorize(ctx context.Context, startURL string, t loginTargets, state string) (*url.URL, error) {
	current := startURL
	for hop := 0; hop < 5; hop++ {
		u, e := url.Parse(current)
		if e != nil || (u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback())) {
			return nil, core.Error("needs_attention")
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if e != nil {
			return nil, core.Error("invalid_input")
		}
		req.Header.Set("Accept", "text/html,application/json")
		res, e := c.http.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, core.Error("network_error")
		}
		var body []byte
		if res.StatusCode == http.StatusOK {
			body, _ = io.ReadAll(io.LimitReader(res.Body, 128<<10))
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, ResponseLimit))
		}
		_ = res.Body.Close()
		if cloudflareChallenge(res) {
			// The provider's CDN is challenging this server's IP. The clearance
			// a human earns is bound to their browser fingerprint and address,
			// so no server-side credential or retry can ever satisfy it.
			return nil, core.Error(core.RewardStateChallengeBlocked)
		}
		switch {
		case res.StatusCode == http.StatusOK:
			lower := strings.ToLower(string(body))
			if strings.Contains(lower, "cdn-cgi/challenge-platform") || strings.Contains(lower, "just a moment") {
				return nil, core.Error(core.RewardStateChallengeBlocked)
			}
			if isProviderLoginPage(u, body) {
				return nil, core.Error("cookie_expired")
			}
			// A consent, authorization, or challenge page means the session is
			// valid but the chain cannot be completed unattended.
			return nil, core.Error("needs_attention")
		case res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnauthorized:
			return nil, core.Error("needs_attention")
		case res.StatusCode/100 != 3:
			return nil, core.Error("upstream_error")
		}
		location := res.Header.Get("Location")
		next, e := u.Parse(location)
		if e != nil || location == "" || next.Host == "" {
			return nil, core.Error("needs_attention")
		}
		if next.Host == t.callbackHost {
			if next.Query().Get("state") != state {
				return nil, core.Error("needs_attention")
			}
			return next, nil
		}
		if next.Host != u.Host {
			return nil, core.Error("needs_attention")
		}
		current = next.String()
	}
	return nil, core.Error("needs_attention")
}

// cloudflareChallenge reports a human-verification wall served by the
// provider's CDN. Definitive via the cf-mitigated header (observed live as
// "cf-mitigated"; some stacks append "-header"); otherwise a cloudflare-fronted
// 403 to a headless request is the challenge page.
func cloudflareChallenge(res *http.Response) bool {
	if res.Header.Get("Cf-Mitigated") == "challenge" || res.Header.Get("Cf-Mitigated-Header") == "challenge" {
		return true
	}
	return res.Header.Get("Server") == "cloudflare" && res.StatusCode == http.StatusForbidden
}

// isProviderLoginPage separates a signed-out login form from a signed-in
// consent page. The path check catches redirects to /login; the password-field
// check catches providers that render the login form in place. A page that is
// neither is reported as requiring human attention, never as an expired cookie.
func isProviderLoginPage(u *url.URL, body []byte) bool {
	path := strings.ToLower(u.Path)
	if !strings.Contains(path, "/oauth") {
		for _, marker := range []string{"/login", "/signin", "/sign-in", "/session"} {
			if strings.Contains(path, marker) {
				return true
			}
		}
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{`type="password"`, `type='password'`, `name="password"`, `name='password'`, `id="password"`, `id='password'`} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

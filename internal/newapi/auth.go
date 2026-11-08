package newapi

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func SessionID(refresh string) (string, error) {
	parts := strings.Split(refresh, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || len(refresh) > 4096 || strings.ContainsAny(refresh, ";\r\n \t\"\\") {
		return "", core.Error("invalid_input")
	}
	return parts[0], nil
}
func (c *Client) PasswordLogin(ctx context.Context, site, username, password string) (core.SessionBundle, error) {
	var zero core.SessionBundle
	if username == "" || password == "" {
		return zero, core.Error("invalid_input")
	}
	b, _, e := c.request(ctx, site, "/api/user/login/encryption-key", "GET", "", nil, nil)
	if e != nil {
		return zero, e
	}
	var key struct {
		Enabled   *bool  `json:"enabled"`
		KID       string `json:"kid"`
		PublicKey string `json:"public_key"`
	}
	if json.Unmarshal(b, &key) != nil || key.Enabled == nil {
		return zero, core.Error("unsupported_site")
	}
	fields := map[string]string{"username": username}
	if *key.Enabled {
		encrypted, e := EncryptPassword(key.PublicKey, key.KID, password, rand.Reader)
		if e != nil {
			return zero, e
		}
		fields["password_encrypted"] = encrypted
		fields["encryption_key_id"] = key.KID
	} else {
		fields["password"] = password
	}
	b, res, e := c.request(ctx, site, "/api/user/login", "POST", "", fields, nil)
	if e != nil {
		return zero, e
	}
	return sessionBundle(b, res)
}
func (c *Client) Refresh(ctx context.Context, site, refreshToken string) (core.SessionBundle, error) {
	if _, e := SessionID(refreshToken); e != nil {
		return core.SessionBundle{}, e
	}
	normalized, e := NormalizeSite(site)
	if e != nil {
		return core.SessionBundle{}, e
	}
	u, _ := url.Parse(normalized)
	cookie := (&http.Cookie{Name: "new_api_refresh", Value: refreshToken}).String()
	b, res, e := c.request(ctx, normalized, "/api/user/auth/refresh", "POST", "", map[string]any{}, http.Header{"Origin": {u.Scheme + "://" + u.Host}, "Cookie": {cookie}})
	if e != nil {
		return core.SessionBundle{}, e
	}
	return sessionBundle(b, res)
}
func sessionBundle(b []byte, res *http.Response) (core.SessionBundle, error) {
	var v struct {
		Verify  bool   `json:"require_verification"`
		Access  string `json:"access_token"`
		Expires int64  `json:"access_expires_at"`
		Session struct {
			SID     string `json:"sid"`
			Expires int64  `json:"expires_at"`
		} `json:"session"`
		User core.Identity `json:"user"`
	}
	if json.Unmarshal(b, &v) != nil {
		return core.SessionBundle{}, core.Error("unsupported_site")
	}
	if v.Verify {
		return core.SessionBundle{}, core.Error("login_verification_required")
	}
	refresh := ""
	if res != nil {
		for _, cookie := range res.Cookies() {
			if cookie.Name == "new_api_refresh" && cookie.MaxAge >= 0 {
				refresh = cookie.Value
			}
		}
	}
	sid, e := SessionID(refresh)
	if e != nil || sid != v.Session.SID || v.Access == "" || v.Expires <= time.Now().Unix() || v.Session.Expires <= time.Now().Unix() || v.User.ID <= 0 || v.User.Username == "" {
		return core.SessionBundle{}, core.Error("unsupported_site")
	}
	return core.SessionBundle{AccessToken: v.Access, RefreshToken: refresh, AccessExpiresAt: v.Expires, SessionID: v.Session.SID, SessionExpiresAt: v.Session.Expires, Identity: v.User}, nil
}

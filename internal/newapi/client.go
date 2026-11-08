package newapi

import (
	"bytes"
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ResponseLimit = 2 << 20

type Client struct{ http *http.Client }

func NewClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.Timeout = 20 * time.Second
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &copy}
}
func NormalizeSite(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", core.Error("invalid_input")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	ip := net.ParseIP(host)
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || ip != nil && ip.IsLoopback())) {
		return "", core.Error("invalid_input")
	}
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	return u.String(), nil
}

type wireEnvelope struct {
	Success    *bool           `json:"success"`
	Code       string          `json:"code"`
	MessageKey string          `json:"message_key"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
}

func (c *Client) request(ctx context.Context, site, path, method, bearer string, body any, headers http.Header) (json.RawMessage, *http.Response, error) {
	normalized, e := NormalizeSite(site)
	if e != nil {
		return nil, nil, e
	}
	var buf []byte
	if body != nil {
		buf, e = json.Marshal(body)
		if e != nil {
			return nil, nil, core.Error("invalid_input")
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, normalized+path, bytes.NewReader(buf))
	if e != nil {
		return nil, nil, core.Error("invalid_input")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range headers {
		req.Header[k] = v
	}
	res, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, core.Error("network_error")
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, ResponseLimit+1))
	if e != nil {
		return nil, res, core.Error("network_error")
	}
	if len(raw) > ResponseLimit {
		return nil, res, core.Error("unsupported_site")
	}
	var envelope wireEnvelope
	decodeErr := json.Unmarshal(raw, &envelope)
	if res.StatusCode >= 300 || (decodeErr == nil && envelope.Success != nil && !*envelope.Success) {
		return nil, res, classify(res, &envelope)
	}
	if decodeErr != nil || envelope.Success == nil || envelope.Data == nil || string(envelope.Data) == "null" {
		return nil, res, core.Error("unsupported_site")
	}
	return envelope.Data, res, nil
}
func (c *Client) Probe(ctx context.Context, site string) (core.SiteInfo, error) {
	b, _, e := c.request(ctx, site, "/api/status", "GET", "", nil, nil)
	var v core.SiteInfo
	if e == nil && json.Unmarshal(b, &v) != nil {
		e = core.Error("unsupported_site")
	}
	return v, e
}
func (c *Client) Self(ctx context.Context, site, bearer string) (core.Identity, error) {
	b, _, e := c.request(ctx, site, "/api/user/self", "GET", bearer, nil, nil)
	var v core.Identity
	if e == nil {
		if json.Unmarshal(b, &v) != nil || v.ID <= 0 || v.Username == "" {
			e = core.Error("unsupported_site")
		}
	}
	return v, e
}
func (c *Client) CheckinStatus(ctx context.Context, site, bearer, month string) (core.CheckinStatus, error) {
	path := "/api/user/checkin"
	if month != "" {
		if _, e := time.Parse("2006-01", month); e != nil {
			return core.CheckinStatus{}, core.Error("invalid_input")
		}
		path += "?month=" + url.QueryEscape(month)
	}
	b, _, e := c.request(ctx, site, path, "GET", bearer, nil, nil)
	if e != nil {
		return core.CheckinStatus{}, e
	}
	var v struct {
		Enabled *bool `json:"enabled"`
		Stats   *struct {
			Checked *bool                `json:"checked_in_today"`
			Total   *int64               `json:"total_quota"`
			Count   *int64               `json:"total_checkins"`
			Records []core.CheckinRecord `json:"records"`
		} `json:"stats"`
	}
	if json.Unmarshal(b, &v) != nil || v.Enabled == nil || v.Stats == nil || v.Stats.Checked == nil || v.Stats.Total == nil || v.Stats.Count == nil {
		return core.CheckinStatus{}, core.Error("unsupported_site")
	}
	if !*v.Enabled {
		return core.CheckinStatus{}, core.Error("not_enabled")
	}
	return core.CheckinStatus{CheckedInToday: *v.Stats.Checked, TotalQuota: *v.Stats.Total, TotalCheckins: *v.Stats.Count, Records: v.Stats.Records}, nil
}
func (c *Client) Checkin(ctx context.Context, site, bearer string) (core.CheckinRecord, error) {
	b, _, e := c.request(ctx, site, "/api/user/checkin", "POST", bearer, map[string]any{}, nil)
	var v struct {
		Date  string `json:"checkin_date"`
		Quota *int64 `json:"quota_awarded"`
	}
	if e != nil {
		return core.CheckinRecord{}, e
	}
	if json.Unmarshal(b, &v) != nil || v.Quota == nil {
		return core.CheckinRecord{}, core.Error("unsupported_site")
	}
	if _, e = time.Parse("2006-01-02", v.Date); e != nil {
		return core.CheckinRecord{}, core.Error("unsupported_site")
	}
	return core.CheckinRecord{Date: v.Date, QuotaAwarded: *v.Quota}, nil
}

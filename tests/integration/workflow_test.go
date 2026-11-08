//go:build integration

package integration

import (
	"bytes"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/state"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var buildOnce sync.Once
var productPath string
var productErr error

func product(t *testing.T) string {
	t.Helper()
	if os.Getenv("CPA_TEST_BIN") == "" {
		t.Skip("CPA_TEST_BIN required")
	}
	buildOnce.Do(func() {
		if artifact := os.Getenv("CPA_TEST_PLUGIN"); artifact != "" {
			productPath, productErr = filepath.Abs(artifact)
			return
		}
		dir, e := os.MkdirTemp("", "newapi-product-")
		if e != nil {
			productErr = e
			return
		}
		ext := ".so"
		if runtime.GOOS == "windows" {
			ext = ".dll"
		}
		productPath = filepath.Join(dir, "newapi-checkin"+ext)
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", productPath, ".")
		cmd.Dir = filepath.Join("..", "..")
		if out, e := cmd.CombinedOutput(); e != nil {
			productErr = fmt.Errorf("build product: %v %s", e, out)
		}
	})
	if productErr != nil {
		t.Fatal(productErr)
	}
	return productPath
}

type mockSite struct {
	server                *httptest.Server
	mu                    sync.Mutex
	checked               bool
	posts, gets, refresh  int
	lostPost, lostRefresh bool
	shortAccess           bool
	expiry                int64
}

func site(t *testing.T) *mockSite {
	s := &mockSite{expiry: time.Now().Add(24 * time.Hour).Unix()}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var data any
		switch r.URL.Path {
		case "/api/status":
			data = map[string]any{"turnstile_check": false}
		case "/api/user/self":
			data = map[string]any{"id": 1, "username": "contract-user"}
		case "/api/user/login/encryption-key":
			data = map[string]any{"enabled": false}
		case "/api/user/login", "/api/user/auth/refresh":
			if r.URL.Path == "/api/user/auth/refresh" {
				s.refresh++
				if s.lostRefresh {
					c, _, _ := w.(http.Hijacker).Hijack()
					_ = c.Close()
					return
				}
			}
			lifetime := 15 * time.Minute
			if s.shortAccess {
				lifetime = 30 * time.Second
			}
			http.SetCookie(w, &http.Cookie{Name: "new_api_refresh", Value: "integration-session.rotated"})
			data = map[string]any{"access_token": "synthetic-access", "access_expires_at": time.Now().Add(lifetime).Unix(), "session": map[string]any{"sid": "integration-session", "expires_at": s.expiry}, "user": map[string]any{"id": 1, "username": "contract-user"}}
		case "/api/user/checkin":
			if r.Method == "GET" {
				s.gets++
				data = map[string]any{"enabled": true, "stats": map[string]any{"checked_in_today": s.checked, "total_quota": 4200, "total_checkins": 8, "records": []any{}}}
			} else {
				s.posts++
				s.checked = true
				if s.lostPost {
					c, _, _ := w.(http.Hijacker).Hijack()
					_ = c.Close()
					return
				}
				data = map[string]any{"quota_awarded": 200, "checkin_date": "2026-11-08"}
			}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	t.Cleanup(s.server.Close)
	return s
}
func (s *mockSite) counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.posts, s.gets, s.refresh
}

type hostFixture struct {
	t                             *testing.T
	dir, config, auth, home, base string
	port                          int
	cmd                           *exec.Cmd
	log                           *os.File
}

func newHost(t *testing.T) *hostFixture {
	t.Helper()
	bin := product(t)
	dir, e := os.MkdirTemp("", "newapi-workflow-")
	if e != nil {
		t.Fatal(e)
	}
	h := &hostFixture{t: t, dir: dir, auth: filepath.Join(dir, "auth"), home: filepath.Join(dir, "state"), config: filepath.Join(dir, "config.yaml")}
	t.Log("retained artifacts:", dir)
	_ = os.MkdirAll(h.auth, 0700)
	_ = os.MkdirAll(h.home, 0700)
	plugins := filepath.Join(dir, "plugins")
	_ = os.MkdirAll(plugins, 0700)
	b, e := os.ReadFile(bin)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(plugins, filepath.Base(bin)), b, 0600); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	h.port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	h.base = fmt.Sprintf("http://127.0.0.1:%d", h.port)
	h.configure(true)
	t.Cleanup(h.stop)
	h.start()
	return h
}
func (h *hostFixture) configure(enabled bool) {
	h.t.Helper()
	text := fmt.Sprintf("server:\n  host: 127.0.0.1\n  port: %d\nmanagement:\n  secret-key: workflow-test-key\n  disable-control-panel: true\noauth:\n  auth-dir: %q\nplugins:\n  enabled: true\n  dir: %q\n  configs:\n    newapi-checkin:\n      enabled: %t\n      checkin-at: '23:59'\n      jitter-minutes: 0\n", h.port, filepath.ToSlash(h.auth), filepath.ToSlash(filepath.Join(h.dir, "plugins")), enabled)
	if e := os.WriteFile(h.config, []byte(text), 0600); e != nil {
		h.t.Fatal(e)
	}
}
func (h *hostFixture) start() {
	h.t.Helper()
	log, e := os.OpenFile(filepath.Join(h.dir, "host.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		h.t.Fatal(e)
	}
	h.log = log
	h.cmd = exec.Command(os.Getenv("CPA_TEST_BIN"), "-config", h.config)
	h.cmd.Dir = h.dir
	h.cmd.Env = append(os.Environ(), "NEWAPI_CHECKIN_PLUGIN_HOME="+h.home)
	h.cmd.Stdout = log
	h.cmd.Stderr = log
	if e = h.cmd.Start(); e != nil {
		h.t.Fatal(e)
	}
	h.wait(20*time.Second, func() bool { code, _ := h.call("GET", "/status", nil); return code == 200 })
}
func (h *hostFixture) stop() {
	if h.cmd != nil {
		_ = h.cmd.Process.Kill()
		_ = h.cmd.Wait()
		h.cmd = nil
	}
	if h.log != nil {
		_ = h.log.Close()
		h.log = nil
	}
}
func (h *hostFixture) restart() { h.stop(); h.start() }
func (h *hostFixture) call(method, path string, value any) (int, []byte) {
	var b []byte
	if value != nil {
		b, _ = json.Marshal(value)
	}
	r, e := http.NewRequest(method, h.base+"/v0/management/plugins/newapi-checkin"+path, bytes.NewReader(b))
	if e != nil {
		h.t.Fatal(e)
	}
	r.Header.Set("Authorization", "Bearer workflow-test-key")
	r.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 65 * time.Second}
	res, e := client.Do(r)
	if e != nil {
		return 0, nil
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, body
}
func (h *hostFixture) wait(timeout time.Duration, fn func() bool) {
	h.t.Helper()
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("condition timed out; inspect %s", h.dir)
}
func (h *hostFixture) bind(s *mockSite, channel string) string {
	h.t.Helper()
	body := map[string]any{"site": s.server.URL, "channel": channel, "token": "nap_contract-synthetic", "username": "contract-user", "password": "synthetic-password"}
	code, b := h.call("POST", "/accounts", body)
	if code != 200 {
		h.t.Fatalf("bind HTTP %d %s", code, b)
	}
	var response struct {
		Data core.AccountSummary `json:"data"`
	}
	_ = json.Unmarshal(b, &response)
	if response.Data.ID == "" {
		h.t.Fatal("missing account id")
	}
	return response.Data.ID
}
func (h *hostFixture) submit(id string) {
	h.t.Helper()
	code, b := h.call("POST", "/checkin", map[string]any{"account_id": id})
	if code != 202 {
		h.t.Fatalf("submit HTTP %d %s", code, b)
	}
}
func (h *hostFixture) snapshot() state.Snapshot {
	b, _ := os.ReadFile(filepath.Join(h.home, "state.json"))
	var s state.Snapshot
	_ = json.Unmarshal(b, &s)
	return s
}
func TestWorkflowPAT(t *testing.T) {
	s := site(t)
	h := newHost(t)
	id := h.bind(s, "token")
	h.submit(id)
	h.wait(15*time.Second, func() bool { r := h.snapshot().Accounts[id].LastResult; return r != nil && r.Status == "success" })
	a := h.snapshot().Accounts[id]
	if a.TotalQuota != 4200 || a.TotalCheckins != 8 {
		t.Fatal(a)
	}
	code, body := h.call("GET", "/status", nil)
	if code != 200 || bytes.Contains(body, []byte("nap_contract-synthetic")) || bytes.Contains(body, []byte(h.auth)) {
		t.Fatal("status leaks private data")
	}
	res, e := http.Get(h.base + "/v0/resource/plugins/newapi-checkin/console")
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	res, e = http.Get(h.base + "/v0/management/plugins/newapi-checkin/status")
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 401 && res.StatusCode != 403 {
		t.Fatal("management route unprotected", res.StatusCode)
	}
}
func TestAuthStoreRestartAndDelete(t *testing.T) {
	s := site(t)
	h := newHost(t)
	id := h.bind(s, "token")
	h.restart()
	code, b := h.call("GET", "/status", nil)
	if code != 200 || !bytes.Contains(b, []byte(id)) {
		t.Fatal("account did not recover")
	}
	code, b = h.call("DELETE", "/accounts?id="+id, nil)
	if code != 200 {
		t.Fatal(code, string(b))
	}
	h.restart()
	if !h.snapshot().DeletePending[id] {
		t.Fatal("tombstone disappeared")
	}
	if _, e := os.Stat(filepath.Join(h.auth, "newapi-checkin-"+id+".json")); !os.IsNotExist(e) {
		t.Fatal("credential resurrected", e)
	}
}
func TestWorkflowSessionExpiry(t *testing.T) {
	s := site(t)
	h := newHost(t)
	id := h.bind(s, "password")
	h.stop()
	path := filepath.Join(h.auth, "newapi-checkin-"+id+".json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var physical map[string]any
	_ = json.Unmarshal(b, &physical)
	physical["credential"].(map[string]any)["session_expires_at"] = time.Now().Add(-time.Minute).Unix()
	b, _ = json.Marshal(physical)
	_ = os.WriteFile(path, b, 0600)
	h.start()
	h.submit(id)
	h.wait(15*time.Second, func() bool { return h.snapshot().Accounts[id].Status == "credential_expired" })
	posts, _, refresh := s.counts()
	if posts != 0 || refresh != 0 {
		t.Fatal("expired session used")
	}
}
func TestRestartAfterUncertainRefresh(t *testing.T) {
	s := site(t)
	s.shortAccess = true
	s.lostRefresh = true
	h := newHost(t)
	id := h.bind(s, "password")
	h.submit(id)
	h.wait(15*time.Second, func() bool { return h.snapshot().Accounts[id].Status == "refresh_outcome_unknown" })
	h.restart()
	h.wait(5*time.Second, func() bool { return h.snapshot().Accounts[id].Status == "refresh_outcome_unknown" })
	posts, _, refresh := s.counts()
	if posts != 0 || refresh != 1 {
		t.Fatal("old refresh replayed", posts, refresh)
	}
}
func TestLostPostThenRestart(t *testing.T) {
	s := site(t)
	s.lostPost = true
	h := newHost(t)
	id := h.bind(s, "token")
	h.submit(id)
	h.wait(15*time.Second, func() bool {
		for _, w := range h.snapshot().WorkItems {
			if w.AccountID == id && w.Status == "retry" && w.NeedsReconcile {
				return true
			}
		}
		return false
	})
	h.restart()
	h.wait(50*time.Second, func() bool { r := h.snapshot().Accounts[id].LastResult; return r != nil && r.Status == "already" })
	posts, _, _ := s.counts()
	if posts != 1 {
		t.Fatal("duplicate POST", posts)
	}
}
func TestQuiesceStopsRequests(t *testing.T) {
	s := site(t)
	h := newHost(t)
	id := h.bind(s, "token")
	h.configure(false)
	h.wait(15*time.Second, func() bool { code, _ := h.call("GET", "/status", nil); return code == 404 })
	before, gets, refresh := s.counts()
	time.Sleep(3200 * time.Millisecond)
	after, gets2, refresh2 := s.counts()
	if before != after || gets != gets2 || refresh != refresh2 {
		t.Fatal("requests after quiesce", id)
	}
}
func TestProductDoesNotEmbedTestCredentials(t *testing.T) {
	b, e := os.ReadFile(product(t))
	if e != nil {
		t.Fatal(e)
	}
	for _, marker := range []string{"workflow-test-key", "nap_contract-synthetic", "synthetic-password", "integration-probe-key", "NEWAPI_CHECKIN_PROBE_RESULT"} {
		if strings.Contains(string(b), marker) {
			t.Fatal("test secret embedded in product", marker)
		}
	}
}

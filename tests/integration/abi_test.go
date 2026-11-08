//go:build integration

package integration

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestABI(t *testing.T) {
	host := os.Getenv("CPA_TEST_BIN")
	if host == "" {
		t.Skip("CPA_TEST_BIN is required for actual CPA loading")
	}
	dir, err := os.MkdirTemp("", "newapi-cpa-abi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("artifacts retained:", dir)
	plugins := filepath.Join(dir, "plugins")
	auth := filepath.Join(dir, "auth")
	_ = os.MkdirAll(plugins, 0700)
	_ = os.MkdirAll(auth, 0700)
	ext := ".so"
	if runtime.GOOS == "windows" {
		ext = ".dll"
	}
	build := exec.Command("go", "build", "-tags=abiprobe", "-buildmode=c-shared", "-o", filepath.Join(plugins, "newapi-checkin"+ext), ".")
	build.Dir = filepath.Join("..", "..")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("probe build: %v\n%s", e, out)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	config := fmt.Sprintf("server:\n  host: 127.0.0.1\n  port: %d\nmanagement:\n  secret-key: integration-probe-key\n  disable-control-panel: true\noauth:\n  auth-dir: %q\nplugins:\n  enabled: true\n  dir: %q\n  configs:\n    newapi-checkin:\n      enabled: true\n", port, filepath.ToSlash(auth), filepath.ToSlash(plugins))
	cfg := filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(cfg, []byte(config), 0600)
	log, e := os.Create(filepath.Join(dir, "host.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	result := filepath.Join(dir, "result.txt")
	cmd := exec.Command(host, "-config", cfg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NEWAPI_CHECKIN_PROBE_RESULT="+result)
	cmd.Stdout = log
	cmd.Stderr = log
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if b, e := os.ReadFile(result); e == nil {
			if string(b) != "ok" {
				t.Fatalf("background callback: %s; see %s", b, dir)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no background probe result, inspect %s", dir)
}

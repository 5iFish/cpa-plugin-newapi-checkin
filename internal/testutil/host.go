package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func Dir(t testing.TB) string {
	t.Helper()
	d, e := os.MkdirTemp("", "newapi-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Log("retained test directory:", d)
	return d
}

type Host struct {
	Mu            sync.Mutex
	Dir           string
	Saves         int
	FailSave      bool
	WriteThenFail bool
	Stale         bool
	Unavailable   bool
	Files         map[string]string
}

func NewHost(t testing.TB) *Host { return &Host{Dir: Dir(t), Files: map[string]string{}} }
func (h *Host) Call(ctx context.Context, method string, request, response any) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	b, _ := json.Marshal(request)
	var in struct {
		Name  string          `json:"name"`
		JSON  json.RawMessage `json:"json"`
		Index string          `json:"auth_index"`
	}
	_ = json.Unmarshal(b, &in)
	var out any
	switch method {
	case "host.auth.save":
		h.Saves++
		if h.FailSave && !h.WriteThenFail {
			return fmt.Errorf("save rejected")
		}
		path := filepath.Join(h.Dir, in.Name)
		if len(in.JSON) == 0 {
			return fmt.Errorf("missing raw JSON")
		}
		if e := os.WriteFile(path, in.JSON, 0600); e != nil {
			return e
		}
		h.Files[in.Name] = path
		if h.WriteThenFail {
			return fmt.Errorf("saved but host failed")
		}
		out = map[string]any{"name": in.Name, "path": path}
	case "host.auth.list":
		if h.Unavailable {
			return fmt.Errorf("host not initialized yet")
		}
		files := []map[string]any{}
		for name, path := range h.Files {
			if _, e := os.Stat(path); e == nil || h.Stale {
				files = append(files, map[string]any{"name": name, "path": path, "auth_index": name})
			}
		}
		out = map[string]any{"files": files}
	case "host.auth.get":
		path, ok := h.Files[in.Index]
		if !ok {
			return os.ErrNotExist
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		out = map[string]any{"name": in.Index, "path": path, "json": json.RawMessage(raw)}
	default:
		return fmt.Errorf("unexpected host method %s", method)
	}
	if response == nil {
		return nil
	}
	b, _ = json.Marshal(out)
	return json.Unmarshal(b, response)
}

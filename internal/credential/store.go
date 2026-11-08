package credential

import (
	"context"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/abi"
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

type Revision struct {
	MustExist    bool
	PhysicalPath string
	SHA256       string
	Generation   uint64
}
type Store struct {
	host  abi.HostCaller
	mu    sync.Mutex
	paths map[string]string
}

func NewStore(h abi.HostCaller) *Store { return &Store{host: h, paths: map[string]string{}} }
func Filename(id string) string        { return "newapi-checkin-" + id + ".json" }
func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 32 && strings.ToLower(id) == id
}
func (s *Store) Track(id, path string) {
	if validID(id) && path != "" {
		s.mu.Lock()
		s.paths[id] = path
		s.mu.Unlock()
	}
}
func (s *Store) path(id string) string { s.mu.Lock(); defer s.mu.Unlock(); return s.paths[id] }
func (s *Store) entries(ctx context.Context) ([]pluginapi.HostAuthFileEntry, error) {
	var r struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if e := s.host.Call(ctx, "host.auth.list", map[string]any{}, &r); e != nil {
		return nil, core.Error("host_unavailable")
	}
	return r.Files, nil
}
func read(path, id string) (core.Account, Revision, []byte, error) {
	fail := func() (core.Account, Revision, []byte, error) {
		return core.Account{}, Revision{}, nil, core.Error("credential_unreadable")
	}
	if !validID(id) || path == "" {
		return fail()
	}
	p, e := filepath.Abs(filepath.Clean(path))
	if e != nil || filepath.Base(p) != Filename(id) {
		return fail()
	}
	info, e := os.Lstat(p)
	if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 2<<20 {
		return fail()
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return fail()
	}
	a, e := decode(raw, id)
	if e != nil {
		return fail()
	}
	h := sha256.Sum256(raw)
	return a, Revision{MustExist: true, PhysicalPath: p, SHA256: hex.EncodeToString(h[:]), Generation: a.Credential.Generation}, raw, nil
}
func decode(raw []byte, id string) (core.Account, error) {
	var a core.Account
	var m struct {
		Type   string `json:"type"`
		Owner  string `json:"plugin_owner"`
		Schema int    `json:"schema_version"`
	}
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(raw, &m) != nil || m.Type != abi.Name || m.Owner != abi.Name || m.Schema != 1 || a.ID != id || a.ID != core.AccountID(a.Site, a.UpstreamUserID) || a.UpstreamUserID <= 0 || a.Credential.Generation == 0 {
		return a, core.Error("credential_unreadable")
	}
	c := a.Credential
	if c.Kind == "session" {
		if c.AccessToken == "" || c.RefreshToken == "" || c.SessionID == "" || c.AccessExpiresAt <= 0 || c.SessionExpiresAt <= 0 {
			return a, core.Error("credential_unreadable")
		}
	} else if (c.Kind != "pat" && c.Kind != "legacy") || c.Token == "" {
		return a, core.Error("credential_unreadable")
	}
	return a, nil
}
func (s *Store) List(ctx context.Context) ([]core.Account, error) {
	entries, e := s.entries(ctx)
	if e != nil {
		return nil, e
	}
	accounts := []core.Account{}
	for _, f := range entries {
		if !strings.HasPrefix(f.Name, "newapi-checkin-") || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(f.Name, "newapi-checkin-"), ".json")
		if !validID(id) {
			continue
		}
		s.Track(id, f.Path)
		var got pluginapi.HostAuthGetResponse
		if f.AuthIndex != "" {
			if s.host.Call(ctx, "host.auth.get", pluginapi.HostAuthGetRequest{AuthIndex: f.AuthIndex}, &got) != nil {
				continue
			}
			if got.Path != "" {
				s.Track(id, got.Path)
			}
		}
		a, _, _, err := read(s.path(id), id)
		if err != nil {
			continue
		}
		a.Disabled = a.Disabled || f.Disabled
		accounts = append(accounts, a)
	}
	return accounts, nil
}
func (s *Store) Load(ctx context.Context, id string) (core.Account, Revision, error) {
	if !validID(id) {
		return core.Account{}, Revision{}, core.Error("invalid_input")
	}
	entries, e := s.entries(ctx)
	if e != nil {
		return core.Account{}, Revision{}, e
	}
	disabled := false
	for _, f := range entries {
		if f.Name == Filename(id) {
			s.Track(id, f.Path)
			disabled = f.Disabled
			break
		}
	}
	a, r, _, e := read(s.path(id), id)
	a.Disabled = a.Disabled || disabled
	return a, r, e
}
func (s *Store) Check(ctx context.Context, id string, r Revision) error {
	a, current, e := s.Load(ctx, id)
	if e != nil || a.Disabled || current != r {
		return core.Error("external_change")
	}
	return nil
}

// Missing permits an explicit rebind to recreate only a known absent file.
// An unreadable, foreign or corrupt existing file must never be overwritten.
func (s *Store) Missing(ctx context.Context, id string) (bool, error) {
	if !validID(id) {
		return false, core.Error("invalid_input")
	}
	_, _, e := s.Load(ctx, id)
	if e == nil {
		return false, nil
	}
	if core.ErrorKind(e) == "host_unavailable" {
		return false, e
	}
	p := s.path(id)
	if p == "" || filepath.Base(p) != Filename(id) {
		return false, core.Error("credential_unreadable")
	}
	_, e = os.Lstat(p)
	if errors.Is(e, os.ErrNotExist) {
		return true, nil
	}
	if e != nil {
		return false, core.Error("credential_unreadable")
	}
	return false, nil
}
func encode(a core.Account, old []byte) ([]byte, error) {
	m := map[string]json.RawMessage{}
	if len(old) > 0 && json.Unmarshal(old, &m) != nil {
		return nil, core.Error("credential_unreadable")
	}
	known, _ := json.Marshal(a)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(known, &fields)
	// Preserve unknown fields at both the document and credential levels.
	prior := map[string]json.RawMessage{}
	_ = json.Unmarshal(m["credential"], &prior)
	for _, k := range []string{"kind", "channel", "token", "access_token", "refresh_token", "access_expires_at", "session_id", "session_expires_at", "expires_at_hint", "generation"} {
		delete(prior, k)
	}
	var cred map[string]json.RawMessage
	_ = json.Unmarshal(fields["credential"], &cred)
	for k, v := range cred {
		prior[k] = v
	}
	fields["credential"], _ = json.Marshal(prior)
	for k, v := range fields {
		m[k] = v
	}
	if !a.Disabled {
		delete(m, "disabled")
	}
	m["type"] = json.RawMessage(`"newapi-checkin"`)
	m["plugin_owner"] = m["type"]
	m["schema_version"] = json.RawMessage(`1`)
	return json.Marshal(m)
}
func (s *Store) Save(ctx context.Context, a core.Account, expected Revision) error {
	if !validID(a.ID) || core.AccountID(a.Site, a.UpstreamUserID) != a.ID {
		return core.Error("invalid_input")
	}
	var old []byte
	if expected.MustExist {
		current, rev, raw, e := read(expected.PhysicalPath, a.ID)
		// A retry after a host write failure may confirm the already complete target generation.
		if e == nil && current.Credential.Generation > expected.Generation && reflect.DeepEqual(current, a) {
			s.Track(a.ID, rev.PhysicalPath)
			return nil
		}
		if e != nil || rev != expected || current.Disabled {
			return core.Error("external_change")
		}
		old = raw
		if e = s.Check(ctx, a.ID, expected); e != nil {
			return e
		}
	} else {
		entries, e := s.entries(ctx)
		if e != nil {
			return e
		}
		for _, f := range entries {
			if f.Name == Filename(a.ID) {
				s.Track(a.ID, f.Path)
			}
		}
		if p := s.path(a.ID); p != "" {
			if _, e := os.Lstat(p); !errors.Is(e, os.ErrNotExist) {
				return core.Error("external_change")
			}
		}
	}
	raw, e := encode(a, old)
	if e != nil {
		return e
	}
	if _, e = decode(raw, a.ID); e != nil {
		return e
	}
	var saved pluginapi.HostAuthSaveResponse
	if e = s.host.Call(ctx, "host.auth.save", pluginapi.HostAuthSaveRequest{Name: Filename(a.ID), JSON: raw}, &saved); e != nil {
		return core.Error("persistence_error")
	}
	if saved.Name != Filename(a.ID) {
		return core.Error("persistence_error")
	}
	got, r, _, e := read(saved.Path, a.ID)
	if e != nil || !reflect.DeepEqual(got, a) {
		return core.Error("persistence_error")
	}
	s.Track(a.ID, r.PhysicalPath)
	return nil
}
func (s *Store) Delete(ctx context.Context, id string) error {
	if validID(id) {
		p := s.path(id)
		if p != "" && filepath.Base(p) == Filename(id) {
			if _, e := os.Lstat(p); errors.Is(e, os.ErrNotExist) {
				return nil
			}
		}
	}
	_, r, e := s.Load(ctx, id)
	if e != nil {
		return e
	}
	if _, _, _, e = read(r.PhysicalPath, id); e != nil {
		return e
	}
	// One explicitly validated regular file; never accept a caller-supplied path.
	if e = os.Remove(r.PhysicalPath); e != nil {
		return core.Error("persistence_error")
	}
	return nil
}

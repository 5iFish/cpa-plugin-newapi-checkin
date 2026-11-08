package state

import (
	"github.com/5ifish/cpa-plugin-newapi-checkin/internal/core"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu         sync.RWMutex
	dir        string
	snapshot   Snapshot
	replace    func(string, string) error
	diagnostic string
}

func Open(dir string) (*Store, error) {
	if dir == "" {
		dir = os.Getenv("NEWAPI_CHECKIN_PLUGIN_HOME")
	}
	if dir == "" {
		h, e := os.UserHomeDir()
		if e != nil {
			return nil, core.Error("storage_error")
		}
		dir = filepath.Join(h, ".newapi-checkin-plugin")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, core.Error("storage_error")
	}
	s := &Store{dir: dir, replace: replaceFile}
	b, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, core.Error("storage_error")
	}
	if e == nil {
		if json.Unmarshal(b, &s.snapshot) != nil || s.snapshot.SchemaVersion != 1 {
			return nil, core.Error("storage_error")
		}
	} else {
		s.snapshot.SchemaVersion = 1
	}
	s.snapshot.init()
	return s, nil
}
func clone(v Snapshot) Snapshot {
	b, _ := json.Marshal(v)
	var r Snapshot
	_ = json.Unmarshal(b, &r)
	r.init()
	return r
}
func (s *Store) Read() Snapshot { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.snapshot) }

// Diagnostic is independent of the last successfully persisted snapshot.
func (s *Store) Diagnostic() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.diagnostic }

func (s *Store) Update(fn func(*Snapshot) error) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if core.ErrorKind(err) == "storage_error" {
			s.diagnostic = "storage_error"
		}
	}()
	next := clone(s.snapshot)
	if e := fn(&next); e != nil {
		return e
	}
	next.SchemaVersion = 1
	b, e := json.MarshalIndent(next, "", "  ")
	if e != nil {
		return core.Error("storage_error")
	}
	file, e := os.CreateTemp(s.dir, ".state-*.tmp")
	if e != nil {
		return core.Error("storage_error")
	}
	name := file.Name()
	defer os.Remove(name)
	if e = file.Chmod(0600); e == nil {
		_, e = file.Write(b)
	}
	if e == nil {
		e = file.Sync()
	}
	closeErr := file.Close()
	if e != nil || closeErr != nil {
		return core.Error("storage_error")
	}
	if e = s.replace(name, filepath.Join(s.dir, "state.json")); e != nil {
		return core.Error("storage_error")
	}
	s.snapshot = next
	return nil
}

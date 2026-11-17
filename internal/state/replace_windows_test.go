package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReplaceFileWaitsForReader(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "next.json"), filepath.Join(dir, "state.json")
	if err := os.WriteFile(from, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	// Windows readers opened by os.ReadFile do not share delete access. Hold
	// that same kind of handle long enough to overlap an atomic replacement.
	reader, err := os.Open(to)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(50 * time.Millisecond)
		_ = reader.Close()
	}()
	err = replaceFile(from, to)
	<-released
	if err != nil {
		t.Fatalf("temporary reader must not fail the state write: %v", err)
	}
	if b, err := os.ReadFile(to); err != nil || string(b) != "new" {
		t.Fatalf("replacement was not saved: %q, %v", b, err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("replacement source remains: %v", err)
	}
}

func TestReplaceFilePersistentLockPreservesFiles(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "next.json"), filepath.Join(dir, "state.json")
	if err := os.WriteFile(from, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(to)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	start := time.Now()
	if err := replaceFile(from, to); err == nil {
		t.Fatal("replacement must fail while a reader keeps delete access locked")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("persistent lock was not bounded: %v", elapsed)
	}
	for path, want := range map[string]string{from: "new", to: "old"} {
		if b, err := os.ReadFile(path); err != nil || string(b) != want {
			t.Fatalf("failed replacement changed %s: %q, %v", path, b, err)
		}
	}
}

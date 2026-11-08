//go:build !windows

package state

import (
	"os"
	"path/filepath"
)

func replaceFile(from, to string) error {
	if e := os.Rename(from, to); e != nil {
		return e
	}
	if f, e := os.Open(filepath.Dir(to)); e == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	return nil
}

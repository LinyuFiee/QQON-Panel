//go:build linux || windows

// Package lock provides a process-lifetime lock for the panel's private data directory.
package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Acquire takes a nonblocking exclusive OS lock in an existing private directory.
// The caller must retain and call release after all users of the directory stop.
// The OS also releases the lock when the process exits, including abnormal exits.
// Existing file contents are ignored; neither Acquire nor release removes or
// truncates .panel.lock, so later callers continue to lock the same file.
func Acquire(dataDir string) (release func(), err error) {
	path := filepath.Join(dataDir, ".panel.lock")
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect data directory lock: %w", err)
	}
	if err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("data directory lock must be a regular file, not a symbolic link")
	}

	file, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("open data directory lock: %w", err)
	}
	defer func() {
		if err != nil {
			_ = file.Close()
		}
	}()
	info, err = file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect open data directory lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("data directory lock must be a regular file")
	}
	if err := lockFile(file); err != nil {
		return nil, fmt.Errorf("acquire data directory lock (another instance may be running): %w", err)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			unlockFile(file)
			_ = file.Close()
		})
	}, nil
}

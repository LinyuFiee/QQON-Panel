//go:build linux

package lock

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestAcquireRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, ".panel.lock"), 0600); err != nil {
		t.Fatal(err)
	}
	if release, err := Acquire(dir); err == nil {
		release()
		t.Fatal("accepted a FIFO as the lock file")
	}
}

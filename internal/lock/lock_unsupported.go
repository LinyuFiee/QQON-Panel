//go:build !linux && !windows

package lock

import (
	"fmt"
	"runtime"
)

// Acquire is unsupported on platforms other than Linux and Windows.
func Acquire(dataDir string) (release func(), err error) {
	return nil, fmt.Errorf("data directory locking is unsupported on %s", runtime.GOOS)
}

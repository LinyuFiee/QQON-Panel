//go:build linux

package lock

import (
	"os"
	"syscall"
)

func openLockFile(path string) (*os.File, error) {
	// NOFOLLOW rejects a replaced symlink; NONBLOCK prevents a replaced FIFO
	// from hanging startup before Acquire checks the opened file's type.
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
}

func lockFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != syscall.EINTR {
			return err
		}
	}
}

func unlockFile(file *os.File) {
	for syscall.Flock(int(file.Fd()), syscall.LOCK_UN) == syscall.EINTR {
	}
}

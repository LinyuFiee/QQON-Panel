//go:build !windows

package jobrunner

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func checkOwner(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 || !directory && stat.Nlink != 1 {
		return errors.New("jobs: private state must be owned by the panel identity and have no other access or hard links")
	}
	return nil
}

func replaceState(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

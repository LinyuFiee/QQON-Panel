//go:build !windows

package platform

import (
	"errors"
	"os"
)

// PreparePrivateDirectory preserves the existing Unix owner-only directory mode.
func PreparePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("platform: private data path must be a real directory")
	}
	return os.Chmod(path, 0700)
}

// CheckPrivateFile rejects links and group/world-readable or writable secrets.
func CheckPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("platform: private file must be regular and owner-only")
	}
	return nil
}

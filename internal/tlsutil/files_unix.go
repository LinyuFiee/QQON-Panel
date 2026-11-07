//go:build !windows

package tlsutil

import (
	"errors"
	"os"
	"syscall"
)

func validNativePath(string) bool     { return true }
func safeNativeComponent(string) bool { return true }

func createPrivateDirectory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil {
		return errors.New("无法创建全新的私有证书目录，已有目标不会被覆盖")
	}
	return nil
}

func openReadFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func checkReadableFile(_ *os.File, info os.FileInfo, private bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return errors.New("证书或私钥不能是硬链接")
	}
	// A certificate is public; a key may be group-readable for the service.
	// Neither may be writable by a group/world, and keys may not be world-readable.
	if info.Mode().Perm()&0022 != 0 || (private && info.Mode().Perm()&0007 != 0) {
		return errors.New("证书或私钥的访问权限过宽")
	}
	return nil
}

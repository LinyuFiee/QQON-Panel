//go:build !windows

package runlog

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func checkDirectory(path string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (current == path && info.Mode().Perm()&0077 != 0) {
				return errors.New("运行日志目录包含链接或权限不够私有")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("无法检查运行日志目录")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func checkFileLinks(_ string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return errors.New("运行日志不能是硬链接")
	}
	return nil
}

//go:build !linux && !windows

package machine

import "os"

func safeFilesSupported() bool                 { return false }
func isReparseOrSymlink(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
func openRootItem(*os.Root, string, bool) (*os.File, os.FileInfo, error) {
	return nil, nil, errUnsafeFile
}

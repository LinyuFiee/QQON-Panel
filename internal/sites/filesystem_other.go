//go:build !linux && !windows

package sites

import "os"

func nativeConnectionRefused(error) bool { return false }

func checkInfo(os.FileInfo, bool, bool) error               { return errUnsafe }
func checkOpenFile(*os.File, os.FileInfo, bool, bool) error { return errUnsafe }
func openNative(*os.Root, string, bool, bool) (*os.File, os.FileInfo, error) {
	return nil, nil, errUnsafe
}
func renameNative(*os.Root, string, string, bool) error { return errUnsafe }
func syncRoot(*os.Root) error                           { return errUnsafe }

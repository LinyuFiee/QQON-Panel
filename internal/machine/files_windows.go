//go:build windows

package machine

import (
	"os"
	"strings"
	"syscall"
)

func safeFilesSupported() bool { return true }

func isReparseOrSymlink(info os.FileInfo) bool {
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return true
	}
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return false
}

func openRootItem(root *os.Root, name string, directory bool) (*os.File, os.FileInfo, error) {
	// Refuse all reparse points, including directory junctions which are not
	// always represented by ModeSymlink. This check is defense in depth; Root's
	// handle-relative traversal, not this check, enforces race-safe containment.
	prefix := ""
	for _, part := range strings.Split(name, "/") {
		if prefix == "" {
			prefix = part
		} else {
			prefix += "/" + part
		}
		info, err := root.Lstat(prefix)
		if err != nil {
			return nil, nil, err
		}
		if isReparseOrSymlink(info) {
			return nil, nil, errUnsafeFile
		}
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	handle := syscall.Handle(file.Fd())
	kind, kindErr := syscall.GetFileType(handle)
	var attrs syscall.ByHandleFileInformation
	attrErr := syscall.GetFileInformationByHandle(handle, &attrs)
	if kindErr != nil || kind != syscall.FILE_TYPE_DISK || attrErr != nil || attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		isReparseOrSymlink(info) || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		_ = file.Close()
		return nil, nil, errUnsafeFile
	}
	return file, info, nil
}

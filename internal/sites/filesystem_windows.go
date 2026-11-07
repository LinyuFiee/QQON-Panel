//go:build windows

package sites

import (
	"errors"
	"os"
	"syscall"
)

func nativeConnectionRefused(err error) bool { return errors.Is(err, syscall.Errno(10061)) }

// Production hosting is disabled on Windows; these handle checks also protect
// the package's Windows temp-directory tests from links and special files.
func checkInfo(info os.FileInfo, directory, strict bool) error {
	if info == nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return errUnsafe
	}
	if a, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && a.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errUnsafe
	}
	return nil
}

func checkOpenFile(file *os.File, info os.FileInfo, directory, strict bool) error {
	if err := checkInfo(info, directory, strict); err != nil {
		return err
	}
	h := syscall.Handle(file.Fd())
	kind, err := syscall.GetFileType(h)
	var attrs syscall.ByHandleFileInformation
	if err != nil || kind != syscall.FILE_TYPE_DISK || syscall.GetFileInformationByHandle(h, &attrs) != nil || attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!directory && attrs.NumberOfLinks != 1) {
		return errUnsafe
	}
	return nil
}

func openNative(root *os.Root, name string, directory, strict bool) (*os.File, os.FileInfo, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if checkInfo(before, directory, strict) != nil {
		return nil, nil, errUnsafe
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	after, afterErr := root.Lstat(name)
	if err != nil || afterErr != nil || checkOpenFile(file, info, directory, strict) != nil || checkInfo(after, directory, strict) != nil || !os.SameFile(before, info) || !os.SameFile(after, info) {
		file.Close()
		return nil, nil, errUnsafe
	}
	return file, info, nil
}

func renameNative(root *os.Root, old, next string, noReplace bool) error {
	if noReplace {
		if _, err := root.Lstat(next); !os.IsNotExist(err) {
			return errConflict
		}
	}
	return root.Rename(old, next)
}

// Directory fsync is unsupported on Windows. Only mocks use this adapter.
func syncRoot(root *os.Root) error { return nil }

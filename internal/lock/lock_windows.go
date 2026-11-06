//go:build windows

package lock

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32.NewProc("LockFileEx")
	unlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func openLockFile(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Open the reparse point itself, never its target. Do not share DELETE:
	// while held, the lock file must not be renamed or replaced.
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &info); err != nil {
		_ = syscall.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&(syscall.FILE_ATTRIBUTE_REPARSE_POINT|syscall.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = syscall.CloseHandle(handle)
		return nil, errors.New("data directory lock must be a regular file, not a reparse point")
	}
	kind, err := syscall.GetFileType(handle)
	if err != nil || kind != syscall.FILE_TYPE_DISK {
		_ = syscall.CloseHandle(handle)
		if err != nil {
			return nil, err
		}
		return nil, errors.New("data directory lock must be a regular disk file")
	}
	return os.NewFile(uintptr(handle), path), nil
}

func lockFile(file *os.File) error {
	if err := lockFileEx.Find(); err != nil {
		return err
	}
	if err := unlockFileEx.Find(); err != nil {
		return err
	}
	const flags = 0x00000001 | 0x00000002 // FAIL_IMMEDIATELY | EXCLUSIVE_LOCK
	var overlapped syscall.Overlapped
	// A byte-range lock may extend past EOF, so an empty file works too.
	ok, _, err := lockFileEx.Call(file.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		return err
	}
	return nil
}

func unlockFile(file *os.File) {
	var overlapped syscall.Overlapped
	_, _, _ = unlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
}

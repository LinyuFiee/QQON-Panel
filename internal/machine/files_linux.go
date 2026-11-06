//go:build linux

package machine

import (
	"os"
	"strconv"
	"syscall"
)

func safeFilesSupported() bool { return true }

func isReparseOrSymlink(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

// A normal stat-then-open can race with a FIFO/device substitution. O_PATH pins
// the contained inode without opening a device or blocking on a FIFO. Only an
// already pinned regular file/directory is reopened through our own proc fd.
// Root itself performs race-safe containment for every path component.
func openRootItem(root *os.Root, name string, directory bool) (*os.File, os.FileInfo, error) {
	const oPath = 0x200000 // Linux O_PATH; the same value on supported Linux architectures.
	pinned, err := root.OpenFile(name, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	defer pinned.Close()
	info, err := pinned.Stat()
	if err != nil {
		return nil, nil, err
	}
	if isReparseOrSymlink(info) || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return nil, nil, errUnsafeFile
	}
	var fs syscall.Statfs_t
	if err := syscall.Fstatfs(int(pinned.Fd()), &fs); err != nil {
		return nil, nil, err
	}
	// proc/sysfs and similar kernel control files can report a regular mode.
	// They are not ordinary text files and must never be previewed.
	switch uint64(fs.Type) {
	case 0x9fa0, 0x62656572, 0x64626720, 0x74726163, 0x27e0eb, 0x63677270, 0x73636673, 0xcafe4a11:
		return nil, nil, errUnsafeFile
	}
	flags := os.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NONBLOCK
	if directory {
		flags |= syscall.O_DIRECTORY
	}
	file, err := os.OpenFile("/proc/self/fd/"+strconv.FormatUint(uint64(pinned.Fd()), 10), flags, 0)
	if err != nil {
		return nil, nil, err
	}
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) || (directory && !actual.IsDir()) || (!directory && !actual.Mode().IsRegular()) {
		_ = file.Close()
		return nil, nil, errUnsafeFile
	}
	return file, actual, nil
}

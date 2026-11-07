//go:build linux

package sites

import (
	"errors"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func nativeConnectionRefused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

func checkInfo(info os.FileInfo, directory, strict bool) error {
	if info == nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return errUnsafe
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (!directory && stat.Nlink != 1) {
		return errUnsafe
	}
	if strict && (stat.Uid != 0 || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0) {
		return errUnsafe
	}
	return nil
}

func checkOpenFile(file *os.File, info os.FileInfo, directory, strict bool) error {
	if err := checkInfo(info, directory, strict); err != nil {
		return err
	}
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &fs); err != nil {
		return err
	}
	switch uint64(fs.Type) {
	case 0x9fa0, 0x62656572, 0x64626720, 0x74726163, 0x27e0eb, 0x63677270, 0x73636673, 0xcafe4a11:
		return errUnsafe
	}
	return nil
}

func openNative(root *os.Root, name string, directory, strict bool) (*os.File, os.FileInfo, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if err := checkInfo(before, directory, strict); err != nil {
		return nil, nil, err
	}
	// O_PATH never opens a swapped device or blocks on a FIFO. O_NOFOLLOW pins
	// a final symlink itself, which the following Stat rejects.
	pin, err := root.OpenFile(name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	defer pin.Close()
	info, err := pin.Stat()
	if err != nil || checkOpenFile(pin, info, directory, strict) != nil || !os.SameFile(before, info) {
		return nil, nil, errUnsafe
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	file, err := os.OpenFile("/proc/self/fd/"+strconv.FormatUint(uint64(pin.Fd()), 10), flags, 0)
	if err != nil {
		return nil, nil, err
	}
	actual, err := file.Stat()
	if err != nil || checkOpenFile(file, actual, directory, strict) != nil || !os.SameFile(info, actual) {
		file.Close()
		return nil, nil, errUnsafe
	}
	return file, actual, nil
}

func renameNative(root *os.Root, old, next string, noReplace bool) error {
	if !noReplace {
		return root.Rename(old, next)
	}
	// Both names are internal single components; keep the directory pinned.
	dir, _, err := openNative(root, ".", true, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := unix.Renameat2(int(dir.Fd()), old, int(dir.Fd()), next, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return nil
}

func syncRoot(root *os.Root) error {
	dir, _, err := openNative(root, ".", true, false)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

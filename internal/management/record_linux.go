//go:build linux

package management

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func runtimeIdentity() (string, bool) {
	uid := os.Geteuid()
	if uid == 0 {
		return "root", true
	}
	return fmt.Sprintf("uid:%d", uid), false
}

func readSystemConsent() ([]byte, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(fd) }()
	for _, component := range []string{"etc", "server-panel"} {
		next, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return nil, err
		}
		_ = unix.Close(fd)
		fd = next
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			return nil, err
		}
		if stat.Uid != 0 || stat.Mode&0022 != 0 || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			return nil, errors.New("unsafe consent parent")
		}
	}
	fileFD, err := unix.Openat(fd, "management-consent.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fileFD), "management-consent.json")
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fileFD, &stat); err != nil {
		return nil, err
	}
	if err := validateConsentStat(&stat); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConsentBytes+1))
	if err != nil || len(data) > maxConsentBytes {
		return nil, errors.New("cannot read bounded consent")
	}
	return data, nil
}

func validateConsentStat(stat *unix.Stat_t) error {
	if stat.Uid != 0 || stat.Gid != 0 || stat.Mode&07777 != 0644 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Size < 1 || stat.Size > maxConsentBytes {
		return errors.New("unsafe consent file")
	}
	return nil
}

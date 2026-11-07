//go:build linux

package management

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestConsentRequiresRootOwnedRegularSingleLinkFile(t *testing.T) {
	base := unix.Stat_t{Uid: 0, Gid: 0, Mode: unix.S_IFREG | 0644, Nlink: 1, Size: 256}
	if err := validateConsentStat(&base); err != nil {
		t.Fatal(err)
	}
	cases := []func(*unix.Stat_t){
		func(s *unix.Stat_t) { s.Uid = 1000 },
		func(s *unix.Stat_t) { s.Gid = 1000 },
		func(s *unix.Stat_t) { s.Mode = unix.S_IFREG | 0664 },
		func(s *unix.Stat_t) { s.Mode = unix.S_IFREG | 04755 },
		func(s *unix.Stat_t) { s.Mode = unix.S_IFLNK | 0644 },
		func(s *unix.Stat_t) { s.Mode = unix.S_IFIFO | 0644 },
		func(s *unix.Stat_t) { s.Nlink = 2 },
		func(s *unix.Stat_t) { s.Size = 0 },
		func(s *unix.Stat_t) { s.Size = maxConsentBytes + 1 },
	}
	for i, mutate := range cases {
		stat := base
		mutate(&stat)
		if err := validateConsentStat(&stat); err == nil {
			t.Fatalf("unsafe metadata case %d accepted", i)
		}
	}
}

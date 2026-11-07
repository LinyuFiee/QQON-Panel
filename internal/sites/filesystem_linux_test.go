//go:build linux

package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxWebrootModeSurvivesPrivateServiceUmask(t *testing.T) {
	base, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	previous := unix.Umask(0077)
	defer unix.Umask(previous)
	child, err := descend(base, "new-webroot", true, 0755, false)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	info, err := child.Stat(".")
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("new webroot must be readable/traversable by non-root workers: %v %v", info, err)
	}
	if err := base.Mkdir("existing", 0700); err != nil {
		t.Fatal(err)
	}
	existing, err := descend(base, "existing", true, 0755, false)
	if err != nil {
		t.Fatal(err)
	}
	defer existing.Close()
	info, _ = existing.Stat(".")
	if info.Mode().Perm() != 0700 {
		t.Fatal("pre-existing directory permissions changed")
	}
}

func TestLinuxRejectsFIFODeviceAndSymlinkParents(t *testing.T) {
	s, _, _ := fixture(t)
	site := createFixtureSite(t, s)
	root, err := s.ownedWebroot(site)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := unix.Mkfifo(filepath.Join(site.Site.Root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(root, "pipe", 256, false); err == nil {
		t.Fatal("FIFO opened")
	}
	if err := os.Symlink("/dev/null", filepath.Join(site.Site.Root, "device")); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(root, "device", 256, false); err == nil {
		t.Fatal("device symlink opened")
	}
	if err := os.Mkdir(filepath.Join(site.Site.Root, "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site.Site.Root, "dir", "text"), []byte("safe"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir", filepath.Join(site.Site.Root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(root, "alias/text", 256, false); err == nil {
		t.Fatal("contained symlink parent opened")
	}
	if err := atomicFile(root, "alias/text", []byte("overwrite"), 0644, digest([]byte("safe")), 256, false); err == nil {
		t.Fatal("symlink parent write accepted")
	}
	if os.Geteuid() == 0 {
		if err := os.WriteFile(filepath.Join(site.Site.Root, "writable"), []byte("unsafe"), 0666); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(site.Site.Root, "writable"), 0666); err != nil {
			t.Fatal(err)
		}
		if _, err := readFile(root, "writable", 256, true); err == nil {
			t.Fatal("world writable file accepted")
		}
	}
}

func TestLinuxSymlinkSwapNeverEscapesRoot(t *testing.T) {
	s, _, _ := fixture(t)
	site := createFixtureSite(t, s)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("never disclose"), 0600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(site.Site.Root, "flip")
	if err := os.Mkdir(inside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "secret"), []byte("allowed"), 0644); err != nil {
		t.Fatal(err)
	}
	root, err := s.ownedWebroot(site)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			if os.Rename(inside, inside+"-held") == nil {
				_ = os.Symlink(outside, inside)
				_ = os.Remove(inside)
				_ = os.Rename(inside+"-held", inside)
			}
		}
	}()
	for i := 0; i < 200; i++ {
		data, err := readFile(root, "flip/secret", 256, false)
		if err == nil && string(data) != "allowed" {
			cancel()
			wg.Wait()
			t.Fatalf("escaped root: %q", data)
		}
	}
	cancel()
	wg.Wait()
	if data, err := os.ReadFile(filepath.Join(outside, "secret")); err != nil || string(data) != "never disclose" {
		t.Fatal("outside changed", err)
	}
	if _, err := os.Lstat(inside); errors.Is(err, os.ErrNotExist) {
		t.Fatal("fixture restoration failed")
	}
}

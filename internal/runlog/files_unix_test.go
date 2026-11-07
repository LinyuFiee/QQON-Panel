//go:build !windows

package runlog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixPrivateModesAndUnsafePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, logNames[0]): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s not mode %o", path, mode)
		}
		if err := os.Chmod(path, mode|0044); err != nil {
			t.Fatal(err)
		}
		if w, err := Open(dir); err == nil {
			_ = w.Close()
			t.Fatal("broad permissions accepted")
		}
		info, err = os.Stat(path)
		if err != nil || info.Mode().Perm() != mode|0044 {
			t.Fatal("unsafe existing permissions silently changed")
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnixLogSymlinksRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "logs")
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if w, err := Open(link); err == nil {
		_ = w.Close()
		t.Fatal("directory link accepted")
	}
	if w, err := Open(filepath.Join(link, "child")); err == nil {
		_ = w.Close()
		t.Fatal("linked parent accepted")
	}
	if err := os.Symlink(filepath.Join(dir, logNames[0]), filepath.Join(dir, logNames[1])); err != nil {
		t.Fatal(err)
	}
	if w, err := Open(dir); err == nil {
		_ = w.Close()
		t.Fatal("linked backup accepted")
	}
}

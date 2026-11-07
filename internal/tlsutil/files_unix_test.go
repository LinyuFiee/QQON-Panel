//go:build !windows

package tlsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixServiceReadableKey(t *testing.T) {
	info, err := Create(filepath.Join(t.TempDir(), "generation"), "localhost", 1)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.Stat(filepath.Dir(info.KeyFile))
	if err != nil || dir.Mode().Perm() != 0700 {
		t.Fatal("directory not 0700")
	}
	for _, path := range []string{info.CertFile, info.KeyFile} {
		file, err := os.Stat(path)
		if err != nil || file.Mode().Perm() != 0600 {
			t.Fatal("generated file not 0600")
		}
	}
	for _, mode := range []os.FileMode{0640, 0440, 0400} {
		if err := os.Chmod(info.KeyFile, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := Inspect(info.CertFile, info.KeyFile, "localhost"); err != nil {
			t.Fatalf("service-readable mode %o rejected: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0644, 0660, 0604} {
		if err := os.Chmod(info.KeyFile, mode); err != nil {
			t.Fatal(err)
		}
		_, err := Inspect(info.CertFile, info.KeyFile, "localhost")
		assertSafeError(t, err)
	}
}

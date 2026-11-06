//go:build windows

package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"panel/internal/platform"
)

func broadenTestDACL(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsAuthWritesInheritPrivateACL(t *testing.T) {
	h := newHarness(t, false)
	if err := platform.CheckPrivateFile(h.s.tokenPath); err != nil {
		t.Fatal("setup token ACL:", err)
	}
	path := filepath.Join(h.s.dataDir, "test-state.json")
	for _, value := range []string{"first", "replacement"} {
		if err := atomicJSON(path, map[string]string{"value": value}, 1024); err != nil {
			t.Fatal(err)
		}
		if err := platform.CheckPrivateFile(path); err != nil {
			t.Fatal("atomic state ACL:", err)
		}
		if _, err := readPrivateFile(path, 1024); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broadenTestDACL(t, path)
	if _, err := readPrivateFile(path, 1024); err == nil {
		t.Fatal("auth accepted a broad file ACL")
	}
	if err := atomicJSON(path, map[string]bool{"changed": true}, 1024); err == nil {
		t.Fatal("auth silently replaced unsafe state")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("unsafe existing state was modified")
	}
}

func TestWindowsAuthRejectsBroadDirectoryBeforeCreatingSecrets(t *testing.T) {
	dir := tPrivateDir(t)
	broadenTestDACL(t, dir)
	if s, err := New(Options{DataDir: dir}); err == nil {
		_ = s.Close()
		t.Fatal("auth accepted broad directory")
	}
	if _, err := loadOrCreateBootstrap(filepath.Join(dir, "setup-token")); err == nil {
		t.Fatal("setup token written in broad directory")
	}
	if err := atomicJSON(filepath.Join(dir, "state.json"), map[string]bool{"secret": true}, 1024); err == nil {
		t.Fatal("temporary secret written in broad directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed validation left secret or temporary files")
	}
}

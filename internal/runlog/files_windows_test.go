//go:build windows

package runlog

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"panel/internal/platform"
)

func TestWindowsTempNewPrivateLogInheritance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("safe lifecycle event\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, logNames[0])
	if err := platform.CheckPrivateFile(path); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount < 3 {
		t.Fatal("missing inherited private ACL")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil || ace.Header.AceFlags&windows.INHERITED_ACE == 0 {
			t.Fatal("new log did not inherit its private directory ACL")
		}
	}
}

func TestWindowsUnsafeLogACLIsNotRepaired(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
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
	for _, path := range []string{filepath.Join(dir, logNames[0]), dir} {
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
		before, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		if w, err := Open(dir); err == nil {
			_ = w.Close()
			t.Fatal("broad ACL accepted")
		}
		after, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil || before.String() != after.String() {
			t.Fatal("existing ACL silently repaired")
		}
	}
}

func TestWindowsPreexistingTempACLRejected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ordinary-child")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if w, err := Open(dir); err == nil {
		_ = w.Close()
		t.Fatal("ordinary Temp directory was treated as a protected private directory")
	}
}

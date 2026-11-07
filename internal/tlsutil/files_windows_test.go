//go:build windows

package tlsutil

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"panel/internal/platform"
)

func TestWindowsServiceReadOnlyKeyACL(t *testing.T) {
	info, err := Create(filepath.Join(t.TempDir(), "generation"), "localhost", 1)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	current := user.User.Sid.String()
	full := "D:P(A;;FA;;;" + current + ")(A;;FA;;;SY)(A;;FA;;;BA)"
	t.Cleanup(func() { setTestACL(t, info.KeyFile, full) })
	readonly := "D:P(A;;FR;;;" + current + ")(A;;FA;;;SY)(A;;FA;;;BA)"
	setTestACL(t, info.KeyFile, readonly)
	if err := platform.CheckPrivateFile(info.KeyFile); err == nil {
		t.Fatal("fixture still grants current-user full control")
	}
	before, err := windows.GetNamedSecurityInfo(info.KeyFile, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(info.CertFile, info.KeyFile, "localhost"); err != nil {
		t.Fatal("service read-only key rejected:", err)
	}
	after, err := windows.GetNamedSecurityInfo(info.KeyFile, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || before.String() != after.String() {
		t.Fatal("inspection changed installed permissions")
	}
	for _, broad := range []string{"WD", "AU", "BU"} {
		setTestACL(t, info.KeyFile, full+"(A;;FR;;;"+broad+")")
		_, err := Inspect(info.CertFile, info.KeyFile, "localhost")
		assertSafeError(t, err)
	}
}

func TestWindowsRejectsAmbiguousCertificatePaths(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{filepath.Join(root, "trailing."), filepath.Join(root, "trailing "), filepath.Join(root, "file:stream"), `\\server\share\generation`, `\\?\C:\generation`} {
		_, err := Create(path, "localhost", 1)
		assertSafeError(t, err)
	}
}

func setTestACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
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

//go:build windows

package platform

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateDirectoryAndInheritedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "nested")
	if err := PreparePrivateDirectory(path); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateDirectory(path); err != nil {
		t.Fatal("existing private directory rejected:", err)
	}
	file := filepath.Join(path, "secret")
	if err := os.WriteFile(file, []byte("temporary test data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(file); err != nil {
		t.Fatal("inherited file ACL rejected:", err)
	}
	sd, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil || ace.Header.AceFlags&windows.INHERITED_ACE == 0 {
			t.Fatal("new file did not inherit private directory permissions")
		}
	}
	if err := CheckPrivateFile(path); err == nil {
		t.Fatal("directory accepted as private file")
	}
	if err := PreparePrivateDirectory(file); err == nil {
		t.Fatal("file accepted as private directory")
	}
	link := filepath.Join(path, "hardlink")
	if err := os.Link(file, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(file); err == nil {
		t.Fatal("hard-linked secret accepted")
	}
	for _, bad := range []string{`\\server\share\state`, `\\?\C:\state`, file + ":stream", filepath.Join(path, "trailing."), filepath.VolumeName(path) + `\`} {
		if err := PreparePrivateDirectory(bad); err == nil {
			t.Fatalf("unsafe path accepted: %q", bad)
		}
	}
}

func TestWindowsRejectsUntrustedDescriptors(t *testing.T) {
	sid, err := currentSID()
	if err != nil {
		t.Fatal(err)
	}
	entries := "(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	for name, sddl := range map[string]string{
		"broad":                 "O:" + sid.String() + "D:P" + entries + "(A;OICI;FR;;;WD)",
		"null-dacl":             "O:" + sid.String() + "D:NO_ACCESS_CONTROL",
		"empty-dacl":            "O:" + sid.String() + "D:P",
		"missing-owner":         "D:P" + entries,
		"untrusted-owner":       "O:WDD:P" + entries,
		"unprotected-directory": "O:" + sid.String() + "D:" + entries,
		"non-inheriting":        "O:" + sid.String() + "D:P(A;;FA;;;" + sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)",
		"inherit-only":          "O:" + sid.String() + "D:P(A;OICIIO;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
		"missing-system":        "O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;BA)",
		"deny-entry":            "O:" + sid.String() + "D:P(D;OICI;FR;;;WD)" + entries,
	} {
		t.Run(name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(sddl)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkPrivateDescriptor(sd, sid, true); err == nil {
				t.Fatal("unsafe descriptor accepted")
			}
		})
	}
	// Installer-owned state may be owned by Administrators or SYSTEM, but its
	// access entries must still explicitly cover the current runtime identity.
	for _, owner := range []string{"BA", "SY"} {
		sd, err := windows.SecurityDescriptorFromString("O:" + owner + "D:P" + entries)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkPrivateDescriptor(sd, sid, true); err != nil {
			t.Fatal("trusted installer owner rejected:", err)
		}
	}
}

func TestWindowsUnsafeExistingACLIsNotRepaired(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := PreparePrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "secret")
	if err := os.WriteFile(file, []byte("temporary test data"), 0600); err != nil {
		t.Fatal(err)
	}
	sid, err := currentSID()
	if err != nil {
		t.Fatal(err)
	}
	broad, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := broad.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, root} {
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			t.Fatal(err)
		}
		before, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		if path == file {
			if err := CheckPrivateFile(path); err == nil {
				t.Fatal("broad file DACL accepted")
			}
		} else if err := PreparePrivateDirectory(path); err == nil {
			t.Fatal("broad directory DACL accepted")
		}
		after, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil || before.String() != after.String() {
			t.Fatal("validation silently changed an existing ACL")
		}
	}
}

func makePrivateTestJunction(link, target string) error {
	if err := os.Mkdir(link, 0700); err != nil {
		return err
	}
	ptr, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(ptr, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	substitute, printName := utf16.Encode([]rune(`\??\`+target)), utf16.Encode([]rune(target))
	units := append(append(append([]uint16{}, substitute...), 0), printName...)
	units = append(units, 0)
	data := make([]byte, 16+2*len(units))
	binary.LittleEndian.PutUint32(data, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(data[4:], uint16(len(data)-8))
	binary.LittleEndian.PutUint16(data[10:], uint16(len(substitute)*2))
	binary.LittleEndian.PutUint16(data[12:], uint16((len(substitute)+1)*2))
	binary.LittleEndian.PutUint16(data[14:], uint16(len(printName)*2))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[16+2*i:], unit)
	}
	var returned uint32
	return windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &returned, nil)
}

func TestWindowsRejectsReparseComponents(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := PreparePrivateDirectory(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "secret"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(root, "junction")
	if err := makePrivateTestJunction(junction, target); err != nil {
		t.Skipf("temporary junction unavailable: %v", err)
	}
	if err := PreparePrivateDirectory(junction); err == nil {
		t.Fatal("junction directory accepted")
	}
	if err := CheckPrivateFile(filepath.Join(junction, "secret")); err == nil {
		t.Fatal("reparse ancestor accepted")
	}
	if err := PreparePrivateDirectory(filepath.Join(junction, "new")); err == nil {
		t.Fatal("creation through reparse ancestor accepted")
	}
	if _, err := os.Stat(filepath.Join(target, "new")); !os.IsNotExist(err) {
		t.Fatal("reparse target mutated")
	}
}

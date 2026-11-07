//go:build windows

package management

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"panel/internal/platform"
)

func runtimeIdentity() (string, bool) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		return "unknown", false
	}
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return "LocalSystem", true
	}
	return user.User.Sid.String(), false
}

func readSystemConsent() ([]byte, error) {
	programData, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return nil, err
	}
	parent := filepath.Join(programData, "QQON-Panel")
	if err := platform.CheckPrivateDirectory(parent); err != nil {
		return nil, err
	}
	path := filepath.Join(parent, "management-consent.json")
	if err := platform.CheckPrivateFile(path); err != nil {
		return nil, err
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 || info.FileSizeHigh != 0 || info.FileSizeLow < 1 || info.FileSizeLow > maxConsentBytes {
		return nil, errors.New("unsafe consent file")
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	if err := validateConsentDescriptor(sd); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConsentBytes+1))
	if err != nil || len(data) > maxConsentBytes {
		return nil, errors.New("cannot read bounded consent")
	}
	return data, nil
}

func validateConsentDescriptor(sd *windows.SECURITY_DESCRIPTOR) error {
	if sd == nil || !sd.IsValid() {
		return errors.New("invalid consent security descriptor")
	}
	allowed := func(sid *windows.SID) bool {
		return sid != nil && sid.IsValid() && (sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
	}
	owner, _, err := sd.Owner()
	if err != nil || !allowed(owner) {
		return errors.New("untrusted consent owner")
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("consent ACL must be protected")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount < 2 || acl.AceCount > 16 {
		return errors.New("invalid consent ACL")
	}
	var granted [2]windows.ACCESS_MASK
	const full = windows.ACCESS_MASK(0x001f01ff)
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || uintptr(ace.Header.AceSize) < unsafe.Offsetof(ace.SidStart)+8 {
			return errors.New("unsupported consent ACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !allowed(sid) || uintptr(sid.Len()) > uintptr(ace.Header.AceSize)-unsafe.Offsetof(ace.SidStart) {
			return errors.New("consent grants access to another identity")
		}
		mask := ace.Mask
		if mask&windows.GENERIC_ALL != 0 {
			mask |= full
		}
		if sid.IsWellKnown(windows.WinLocalSystemSid) {
			granted[0] |= mask
		} else {
			granted[1] |= mask
		}
	}
	if granted[0]&full != full || granted[1]&full != full {
		return errors.New("consent must grant full control to SYSTEM and Administrators")
	}
	return nil
}

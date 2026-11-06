//go:build windows

package platform

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const privateFullControl = windows.ACCESS_MASK(0x001f01ff) // FILE_ALL_ACCESS

// PreparePrivateDirectory never repairs an existing Windows ACL. New components
// are created with a protected DACL before they become visible, and children
// inherit access for the current token identity, SYSTEM and Administrators only.
func PreparePrivateDirectory(path string) error {
	return inspectPrivatePath(path, true, true)
}

// CheckPrivateFile checks the actual NTFS object, not emulated Unix mode bits.
func CheckPrivateFile(path string) error {
	return inspectPrivatePath(path, false, false)
}

func currentSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user.User.Sid == nil || !user.User.Sid.IsValid() {
		return nil, errors.New("platform: current token has no valid SID")
	}
	return user.User.Sid, nil
}

func privateDescriptor(sid *windows.SID) (*windows.SECURITY_DESCRIPTOR, error) {
	return windows.SecurityDescriptorFromString("O:" + sid.String() +
		"D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
}

func privatePath(path string) (string, []string, error) {
	// GetFullPathName (used by Abs) strips trailing dots; reject aliases before
	// that normalization so validation never refers to a different filename.
	for _, part := range strings.Split(filepath.FromSlash(path), `\`) {
		if part != "." && part != ".." && strings.TrimRight(part, " .") != part {
			return "", nil, errors.New("platform: ambiguous private path component")
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil || path == "" || len(absolute) > 32767 {
		return "", nil, errors.New("platform: invalid private path")
	}
	volume := filepath.VolumeName(absolute)
	// Local drive paths only: no device namespace, UNC shares or alternate streams.
	if len(volume) != 2 || volume[1] != ':' || !((volume[0] >= 'A' && volume[0] <= 'Z') || (volume[0] >= 'a' && volume[0] <= 'z')) {
		return "", nil, errors.New("platform: private state requires a local NTFS drive")
	}
	root := volume + `\`
	relative := strings.TrimPrefix(absolute, root)
	if relative == "" {
		return "", nil, errors.New("platform: a drive root cannot be private state")
	}
	parts := strings.Split(relative, `\`)
	if len(parts) > 128 {
		return "", nil, errors.New("platform: private path exceeds component limit")
	}
	for _, part := range parts {
		if !filepath.IsLocal(part) || strings.TrimRight(part, " .") != part || strings.ContainsAny(part, ":\x00") {
			return "", nil, errors.New("platform: unsafe private path component")
		}
	}
	return root, parts, nil
}

func inspectPrivatePath(path string, create, directory bool) error {
	root, parts, err := privatePath(path)
	if err != nil {
		return err
	}
	sid, err := currentSID()
	if err != nil {
		return err
	}
	var attributes *windows.SecurityAttributes
	if create {
		sd, err := privateDescriptor(sid)
		if err != nil {
			return err
		}
		attributes = &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	}
	// Keep ancestor handles open without delete sharing while inspecting/creating
	// the leaf; reject every reparse component instead of following a junction.
	handles := make([]windows.Handle, 0, len(parts)+1)
	defer func() {
		for i := len(handles) - 1; i >= 0; i-- {
			windows.CloseHandle(handles[i])
		}
	}()
	name := root
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			name = filepath.Join(name, parts[i])
		}
		last := i == len(parts)-1
		ptr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		open := func() (windows.Handle, error) {
			return windows.CreateFile(ptr, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
				windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
				windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		}
		handle, err := open()
		created := false
		if create && i >= 0 && (errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)) {
			err = windows.CreateDirectory(ptr, attributes)
			if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
				return fmt.Errorf("platform: cannot create private directory: %w", err)
			}
			created = err == nil
			handle, err = open()
		}
		if err != nil {
			return fmt.Errorf("platform: cannot inspect private path: %w", err)
		}
		handles = append(handles, handle)
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			return err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("platform: private paths cannot contain reparse points")
		}
		isDir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
		if (!last || directory) != isDir {
			return errors.New("platform: unexpected private path object type")
		}
		if i == -1 {
			var fs [32]uint16
			var flags uint32
			if err := windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, &flags, &fs[0], uint32(len(fs))); err != nil {
				return err
			}
			if !strings.EqualFold(windows.UTF16ToString(fs[:]), "NTFS") || flags&windows.FILE_PERSISTENT_ACLS == 0 {
				return errors.New("platform: private state requires NTFS persistent ACLs")
			}
		}
		if last || created {
			if !isDir && info.NumberOfLinks != 1 {
				return errors.New("platform: private files cannot have hard links")
			}
			sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				return err
			}
			if err := checkPrivateDescriptor(sd, sid, isDir); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkPrivateDescriptor(sd *windows.SECURITY_DESCRIPTOR, current *windows.SID, directory bool) error {
	if sd == nil || !sd.IsValid() {
		return errors.New("platform: invalid private security descriptor")
	}
	owner, _, err := sd.Owner()
	if err != nil || !allowedPrivateSID(owner, current) {
		return errors.New("platform: private state has an untrusted or missing owner")
	}
	control, _, err := sd.Control()
	if err != nil || (directory && control&windows.SE_DACL_PROTECTED == 0) {
		return errors.New("platform: private directory must have a protected DACL")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 || acl.AceCount > 64 {
		return errors.New("platform: private state has a missing, null or unsupported DACL")
	}
	var granted [3]windows.ACCESS_MASK
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("platform: private DACL requires simple allow entries")
		}
		flags := ace.Header.AceFlags
		if flags&^(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE|windows.INHERITED_ACE) != 0 ||
			(directory && flags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) {
			return errors.New("platform: private directory entries must inherit to files and directories")
		}
		if uintptr(ace.Header.AceSize) < unsafe.Offsetof(ace.SidStart)+8 {
			return errors.New("platform: malformed private DACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !allowedPrivateSID(sid, current) || uintptr(sid.Len()) > uintptr(ace.Header.AceSize)-unsafe.Offsetof(ace.SidStart) {
			return errors.New("platform: private DACL grants access outside the current identity, SYSTEM and Administrators")
		}
		mask := ace.Mask
		if mask&windows.GENERIC_ALL != 0 {
			mask |= privateFullControl
		}
		if sid.Equals(current) {
			granted[0] |= mask
		}
		if sid.IsWellKnown(windows.WinLocalSystemSid) {
			granted[1] |= mask
		}
		if sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			granted[2] |= mask
		}
	}
	for _, mask := range granted {
		if mask&privateFullControl != privateFullControl {
			return errors.New("platform: private DACL must grant full control to the current identity, SYSTEM and Administrators")
		}
	}
	return nil
}

func allowedPrivateSID(sid, current *windows.SID) bool {
	return sid != nil && sid.IsValid() && (sid.Equals(current) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

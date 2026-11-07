//go:build windows

package tlsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func validNativePath(path string) bool {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' || !((volume[0] >= 'A' && volume[0] <= 'Z') || (volume[0] >= 'a' && volume[0] <= 'z')) {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.FromSlash(path), volume+`\`), `\`) {
		if part == "" || part == "." || part == ".." || !filepath.IsLocal(part) || strings.TrimRight(part, " .") != part || strings.ContainsAny(part, ":\x00") {
			return false
		}
	}
	return true
}

func safeNativeComponent(path string) bool {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attributes, err := windows.GetFileAttributes(ptr)
	return err == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

func createPrivateDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user.User.Sid == nil {
		return errors.New("无法确定证书目录的当前访问身份")
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() +
		"D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return errors.New("无法设置证书目录的私有权限")
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return errors.New("证书目录路径无效")
	}
	attributes := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	// CreateDirectory is exclusive and applies the protected ACL atomically;
	// a normal Mkdir under Windows Temp would inherit its permissive ACL first.
	if err := windows.CreateDirectory(ptr, attributes); err != nil {
		return errors.New("无法创建全新的私有证书目录，已有目标不会被覆盖")
	}
	return nil
}

func openReadFile(path string) (*os.File, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.READ_CONTROL,
		windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func checkReadableFile(file *os.File, _ os.FileInfo, private bool) error {
	handle := windows.Handle(file.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return errors.New("证书或私钥必须是普通文件，不能包含链接")
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil || !sd.IsValid() {
		return errors.New("无法检查证书或私钥的访问权限")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 || acl.AceCount > 64 {
		return errors.New("证书或私钥的访问权限为空或不受支持")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil || ace == nil || uintptr(ace.Header.AceSize) < unsafe.Offsetof(ace.SidStart)+8 {
			return errors.New("证书或私钥的访问权限条目无效")
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			return errors.New("证书或私钥的访问权限类型不受支持")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || uintptr(sid.Len()) > uintptr(ace.Header.AceSize)-unsafe.Offsetof(ace.SidStart) {
			return errors.New("证书或私钥的访问身份无效")
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		broad := false
		switch sid.String() {
		case "S-1-1-0", "S-1-5-7", "S-1-5-11", "S-1-5-32-545", "S-1-5-32-546", "S-1-5-2", "S-1-5-4":
			broad = true
		}
		unsafeMask := windows.ACCESS_MASK(windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE)
		if private {
			unsafeMask |= windows.GENERIC_READ | windows.GENERIC_EXECUTE | windows.FILE_READ_DATA | windows.FILE_EXECUTE
		}
		if broad && ace.Mask&unsafeMask != 0 {
			return errors.New("证书或私钥向过多用户开放了访问权限")
		}
	}
	// The successful read handle establishes current readability. Do not require
	// current-user ownership/full control: installer-owned service-read ACLs pass.
	return nil
}

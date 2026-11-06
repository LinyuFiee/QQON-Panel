//go:build windows

package machine

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
)

// Create a junction only in the test's temporary tree. Do not run mklink/cmd
// or modify any existing filesystem path.
func createTestJunction(link, target string) error {
	if err := os.Mkdir(link, 0700); err != nil {
		return err
	}
	linkPtr, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(linkPtr, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	substitute := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))
	pathUnits := append(append(append([]uint16{}, substitute...), 0), printName...)
	pathUnits = append(pathUnits, 0)
	data := make([]byte, 16+len(pathUnits)*2)
	binary.LittleEndian.PutUint32(data[0:4], 0xA0000003) // IO_REPARSE_TAG_MOUNT_POINT
	binary.LittleEndian.PutUint16(data[4:6], uint16(len(data)-8))
	binary.LittleEndian.PutUint16(data[8:10], 0)
	binary.LittleEndian.PutUint16(data[10:12], uint16(len(substitute)*2))
	binary.LittleEndian.PutUint16(data[12:14], uint16((len(substitute)+1)*2))
	binary.LittleEndian.PutUint16(data[14:16], uint16(len(printName)*2))
	for i, unit := range pathUnits {
		binary.LittleEndian.PutUint16(data[16+i*2:], unit)
	}
	var returned uint32
	return syscall.DeviceIoControl(handle, 0x000900A4, &data[0], uint32(len(data)), nil, 0, &returned, nil) // FSCTL_SET_REPARSE_POINT
}

func TestWindowsJunctionEscapeDenied(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("JUNCTION_MUST_NOT_LEAK"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := createTestJunction(link, outside); err != nil {
		t.Skipf("junction creation unavailable in temporary directory: %v", err)
	}
	s := fixtureService(t, Options{FileRoot: root}, nil)
	for _, target := range []string{"/api/files?path=escape", "/api/files/preview?path=escape/secret"} {
		r := request(t, s, "GET", target, "")
		if r.Code == 200 || strings.Contains(r.Body.String(), "JUNCTION_MUST_NOT_LEAK") {
			t.Fatalf("junction escaped root: %d %s", r.Code, r.Body.String())
		}
	}
	page := decodeBody[filesView](t, request(t, s, "GET", "/api/files", ""))
	if len(page.Entries) != 1 || page.Entries[0].Type != "symlink" {
		t.Fatalf("junction not labeled as a link: %+v", page.Entries)
	}
}

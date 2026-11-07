//go:build windows

package jobrunner

import "golang.org/x/sys/windows"

// platform.CheckPrivateFile/PreparePrivateDirectory validate the native ACLs.
func checkOwner(path string, directory bool) error { return nil }

func replaceState(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

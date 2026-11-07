//go:build windows

package runlog

import "os"

// The platform validates every NTFS component, protected directory ACL,
// inherited file ACL and file link count; do not substitute Unix mode bits.
func checkDirectory(string) error              { return nil }
func checkFileLinks(string, os.FileInfo) error { return nil }

//go:build !linux

package apps

import (
	"context"
	"errors"
	"io/fs"
)

var errPlatform = errors.New("not_supported: Linux apt/dnf and systemd are required")

func (h realHost) executable(path string) bool                           { return false }
func (h realHost) readFile(path string, limit int64) ([]byte, error)     { return nil, errPlatform }
func (h realHost) readDir(path string, limit int) ([]fs.DirEntry, error) { return nil, errPlatform }
func (h realHost) socket(path string) bool                               { return false }
func (h realHost) query(ctx context.Context, executable string, args []string) ([]byte, error) {
	return nil, errPlatform
}

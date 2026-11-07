//go:build !linux && !windows

package jobrunner

import (
	"errors"
	"os/exec"
)

func newProcessScope(cmd *exec.Cmd) (processScope, error) {
	return nil, errors.New("managed subprocesses are supported only on Linux and Windows")
}

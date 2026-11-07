//go:build linux

package jobrunner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type linuxScope struct{ cmd *exec.Cmd }

func newProcessScope(cmd *exec.Cmd) (processScope, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	return &linuxScope{cmd}, nil
}

func (s *linuxScope) started() error { return nil }
func (s *linuxScope) close()         {}
func (s *linuxScope) kill() error {
	if s.cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

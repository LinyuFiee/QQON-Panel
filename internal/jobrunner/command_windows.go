//go:build windows

package jobrunner

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsScope struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	job       windows.Handle
	attached  bool
	cancelled bool
}

func newProcessScope(cmd *exec.Cmd) (processScope, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// Suspend before assignment so a fixture cannot race Job membership by
	// launching descendants before the parent is contained. No host SCM calls.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP}
	return &windowsScope{cmd: cmd, job: job}, nil
}

func (s *windowsScope) started() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled {
		return os.ErrProcessDone
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(s.cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(s.job, process); err != nil {
		return err
	}
	s.attached = true
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for scanned := 0; scanned < 131072; scanned++ {
		if entry.OwnerProcessID == uint32(s.cmd.Process.Pid) {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, err = windows.ResumeThread(thread)
			windows.CloseHandle(thread)
			return err
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return err
		}
	}
	return errors.New("cannot locate suspended command thread")
}

func (s *windowsScope) kill() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = true
	if s.attached {
		return windows.TerminateJobObject(s.job, 1)
	}
	if s.cmd.Process != nil {
		return s.cmd.Process.Kill()
	}
	return os.ErrProcessDone
}

func (s *windowsScope) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != 0 {
		windows.CloseHandle(s.job)
		s.job = 0
	}
}

//go:build linux

package apps

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func protectedPath(path string, allowSocket bool) (string, fs.FileInfo, error) {
	if !filepath.IsAbs(path) {
		return "", nil, errors.New("system path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", nil, err
	}
	for current := resolved; ; current = filepath.Dir(current) {
		part, err := os.Lstat(current)
		if err != nil {
			return "", nil, err
		}
		stat, ok := part.Sys().(*syscall.Stat_t)
		isSocket := current == resolved && allowSocket && part.Mode()&os.ModeSocket != 0
		if !ok || part.Mode()&os.ModeSymlink != 0 {
			return "", nil, errors.New("system path is not protected by root")
		}
		trustedOwner := stat.Uid == 0
		// Debian/Ubuntu php-common's tmpfiles entry uses www-data for /run/php.
		// This sole known parent is allowed only with the distro account and no
		// group/world writes. socket() independently verifies the actual root peer.
		if allowSocket && current == "/run/php" && part.IsDir() && !trustedOwner {
			uid, _, valid := account(realHost{goos: "linux"}, "www-data")
			trustedOwner = valid && uint64(stat.Uid) == uid
		}
		if !isSocket && (!trustedOwner || part.Mode().Perm()&0022 != 0) {
			return "", nil, errors.New("system path is not protected by root")
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	return resolved, info, nil
}

func (h realHost) executable(path string) bool {
	_, info, err := protectedPath(path, false)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func (h realHost) readFile(path string, limit int64) ([]byte, error) {
	resolved, before, err := protectedPath(path, false)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, errors.New("system configuration must be a bounded regular file")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("system file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if len(data) > int(limit) {
		return nil, errors.New("system file exceeds limit")
	}
	return data, err
}

func (h realHost) readDir(path string, limit int) ([]fs.DirEntry, error) {
	resolved, info, err := protectedPath(path, false)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("system directory is not a directory")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(limit + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if len(entries) > limit {
		return nil, errors.New("system directory exceeds discovery limit")
	}
	return entries, err
}

func (h realHost) socket(path string) bool {
	resolved, info, err := protectedPath(path, true)
	// A socket cannot redirect to a different pool, even through a root-owned
	// symlink. /var/run spelling is normalized by runtime discovery beforehand.
	if err != nil || resolved != path || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0002 != 0 {
		return false
	}
	connection, err := net.DialTimeout("unix", resolved, 150*time.Millisecond)
	if err != nil {
		return false
	}
	defer connection.Close()
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return false
	}
	var credentials *unix.Ucred
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, credentialErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return false
	}
	// PHP-FPM's systemd master opens the listener as root, then creates workers
	// as the configured unprivileged user. An attacker-owned socket is not FPM.
	return credentialErr == nil && credentials != nil && credentials.Uid == 0
}

type queryBudget struct {
	mu     sync.Mutex
	stdout []byte
	used   int
	over   bool
	cancel context.CancelFunc
}

type queryWriter struct {
	budget *queryBudget
	stdout bool
}

func (w queryWriter) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	n := min(len(p), (64<<10)-w.budget.used)
	w.budget.used += n
	if w.stdout {
		w.budget.stdout = append(w.budget.stdout, p[:n]...)
	}
	if n != len(p) {
		w.budget.over = true
		w.budget.cancel()
	}
	return len(p), nil
}

func (h realHost) query(ctx context.Context, executable string, args []string) ([]byte, error) {
	if !h.executable(executable) {
		return nil, errors.New("untrusted or missing system executable")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "HOME=/root", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0", "SYSTEMD_ASK_PASSWORD_AGENT=0"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.WaitDelay = 200 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	budget := &queryBudget{cancel: cancel}
	cmd.Stdout, cmd.Stderr = queryWriter{budget, true}, queryWriter{budget, false}
	err := cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if budget.over {
		return nil, errors.New("system query exceeded 64 KiB")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return budget.stdout, err
}

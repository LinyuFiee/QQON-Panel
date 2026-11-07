package jobrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const commandOutputLimit = 32 << 20

type processScope interface {
	started() error
	kill() error
	close()
}

// RunCommand never invokes a shell or inherits privileged environment secrets.
// Callers must choose executable/argv from their own fixed allowlists. A command
// has a hard time/output budget even if the provided context has no deadline.
func RunCommand(ctx context.Context, executable string, args []string, p *Progress) error {
	if ctx == nil || !filepath.IsAbs(executable) || strings.ContainsRune(executable, 0) {
		return errors.New("command requires an absolute executable and context")
	}
	if len(args) > 64 {
		return errors.New("command argument limit exceeded")
	}
	for _, arg := range args {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return errors.New("invalid command argument")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = commandEnvironment()
	cmd.WaitDelay = 500 * time.Millisecond
	scope, err := newProcessScope(cmd)
	if err != nil {
		return err
	}
	defer scope.close()
	cmd.Cancel = scope.kill
	writer := &commandWriter{progress: p, cancel: cancel}
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", filepath.Base(executable), err)
	}
	if err := scope.started(); err != nil {
		_ = scope.kill()
		_ = cmd.Wait()
		return fmt.Errorf("isolate command process group: %w", err)
	}
	err = cmd.Wait()
	_ = scope.kill() // also remove lingering descendants holding output pipes.
	writer.flush()
	if writer.exceeded {
		return errors.New("command output exceeded the 32 MiB processing limit; process group cancelled")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("%s failed: %w", filepath.Base(executable), err)
	}
	return nil
}

func commandEnvironment() []string {
	if runtime.GOOS == "windows" {
		// Windows is for bounded test fixtures only, never package or SCM actions.
		return []string{"SystemRoot=" + os.Getenv("SystemRoot"), "WINDIR=" + os.Getenv("WINDIR"), "TEMP=" + os.TempDir(), "TMP=" + os.TempDir()}
	}
	return []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "HOME=/root",
		"DEBIAN_FRONTEND=noninteractive", "APT_LISTCHANGES_FRONTEND=none", "NEEDRESTART_MODE=l",
		"SYSTEMD_PAGER=", "SYSTEMD_COLORS=0", "SYSTEMD_ASK_PASSWORD_AGENT=0",
	}
}

type commandWriter struct {
	mu       sync.Mutex
	progress *Progress
	cancel   context.CancelFunc
	pending  []byte
	used     int
	exceeded bool
}

func (w *commandWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(data)
	if w.exceeded {
		return n, nil
	}
	if len(data) > commandOutputLimit-w.used {
		data = data[:commandOutputLimit-w.used]
		w.exceeded = true
		w.cancel()
	}
	w.used += len(data)
	if w.progress == nil {
		return n, nil
	}
	for _, c := range data {
		if c == '\n' || c == '\r' {
			if len(w.pending) > 0 {
				w.progress.Log(string(w.pending))
				w.pending = w.pending[:0]
			}
			continue
		}
		w.pending = append(w.pending, c)
		if len(w.pending) == 4096 {
			w.progress.Log(string(w.pending))
			w.pending = w.pending[:0]
		}
	}
	return n, nil
}

func (w *commandWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.progress.Log(string(w.pending))
		w.pending = nil
	}
}

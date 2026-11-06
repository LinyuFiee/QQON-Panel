//go:build linux || windows

package lock

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	release, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	path := filepath.Join(dir, ".panel.lock")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" && before.Mode().Perm() != 0600 {
		t.Fatalf("lock permissions = %o, want 600", before.Mode().Perm())
	}
	// Keep the release closure alive across GC: the file must remain open.
	runtime.GC()
	if second, err := Acquire(dir); err == nil {
		second()
		t.Fatal("second acquisition succeeded while the first still holds the lock")
	}

	release()
	release() // Release is idempotent.
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("release removed the lock file: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("release replaced the lock file")
	}
	second, err := Acquire(dir)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	t.Cleanup(second)
	// Calling an old release must not unlock the new owner's descriptor.
	release()
	if third, err := Acquire(dir); err == nil {
		third()
		t.Fatal("old release unlocked the new owner")
	}
}

func TestAcquireStaleContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".panel.lock")
	// Even a PID that is currently alive is not proof of an OS lock.
	stale := fmt.Sprintf("pid=%d\nstarted=2000-01-01T00:00:00Z\n", os.Getpid())
	if err := os.WriteFile(path, []byte(stale), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	release, err := Acquire(dir)
	if err != nil {
		t.Fatalf("stale lock contents prevented acquisition: %v", err)
	}
	t.Cleanup(release)
	release()
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("stale file was removed or replaced: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != stale {
		t.Fatal("acquiring or releasing the lock changed existing contents")
	}
}

func TestAcquireRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".panel.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if release, err := Acquire(dir); err == nil {
		release()
		t.Fatal("accepted a directory as the lock file")
	}
}

func TestAcquireRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".panel.lock")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("Windows symlink creation is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if release, err := Acquire(dir); err == nil {
		release()
		t.Fatal("accepted a symlink as the lock file")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "unchanged" {
		t.Fatalf("symlink target was modified: contents=%q, err=%v", contents, err)
	}
}

func TestAcquireAcrossProcessesAndExit(t *testing.T) {
	dir := t.TempDir()
	release, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	blocked := lockHelper(t, "blocked", dir)
	if output, err := blocked.CombinedOutput(); err != nil {
		t.Fatalf("another process was not refused: %v\n%s", err, output)
	}
	release()

	// This child exits without calling release. Its OS lock must disappear
	// while the file and its inode remain, allowing a new owner immediately.
	exiting := lockHelper(t, "exit", dir)
	if output, err := exiting.CombinedOutput(); err != nil {
		t.Fatalf("child could not acquire and exit: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(dir, ".panel.lock")); err != nil {
		t.Fatalf("child exit removed the lock file: %v", err)
	}
	acquired, err := Acquire(dir)
	if err != nil {
		t.Fatalf("process exit did not release its OS lock: %v", err)
	}
	acquired()
}

func TestAcquireAfterKilledProcess(t *testing.T) {
	dir := t.TempDir()
	child := lockHelper(t, "hold", dir)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "locked\n" {
		t.Fatalf("child did not report a held lock: %q, %v", ready, err)
	}
	if release, err := Acquire(dir); err == nil {
		release()
		t.Fatal("acquired a lock held by the child process")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	waited = true
	if err == nil {
		t.Fatal("killed child unexpectedly exited successfully")
	}
	release, err := Acquire(dir)
	if err != nil {
		t.Fatalf("killed process did not release its OS lock: %v", err)
	}
	release()
}

func lockHelper(t *testing.T, mode, dir string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockHelperProcess$")
	// Supply only our chosen helper values, even if the parent has similarly
	// named environment variables. Never accept a directory outside TempDir.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "PANEL_LOCK_TEST_MODE") && !strings.EqualFold(key, "PANEL_LOCK_TEST_DIR") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PANEL_LOCK_TEST_MODE="+mode, "PANEL_LOCK_TEST_DIR="+dir)
	cmd.WaitDelay = time.Second
	return cmd
}

func TestLockHelperProcess(t *testing.T) {
	mode := os.Getenv("PANEL_LOCK_TEST_MODE")
	if mode == "" {
		return
	}
	release, err := Acquire(os.Getenv("PANEL_LOCK_TEST_DIR"))
	if mode == "blocked" {
		if err == nil {
			release()
			t.Fatal("child acquired a lock held by the parent")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "exit":
		runtime.KeepAlive(release)
		os.Exit(0)
	case "hold":
		fmt.Println("locked")
		time.Sleep(30 * time.Second)
		runtime.KeepAlive(release)
		os.Exit(2)
	default:
		release()
		t.Fatalf("unknown helper mode %q", mode)
	}
}

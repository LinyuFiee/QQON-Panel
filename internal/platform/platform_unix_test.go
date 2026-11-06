//go:build !windows

package platform

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestUnixPrivatePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "nested")
	if err := PreparePrivateDirectory(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("directory not mode 0700")
	}
	file := filepath.Join(path, "secret")
	if err := os.WriteFile(file, []byte("temporary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(file); err == nil {
		t.Fatal("world-readable file accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateDirectory(link); err == nil {
		t.Fatal("directory symlink accepted")
	}
	if err := CheckPrivateFile(link); err == nil {
		t.Fatal("symlink accepted as secret")
	}
}

func TestUnixRunSignals(t *testing.T) {
	if os.Getenv("QQON_PLATFORM_SIGNAL_HELPER") == "1" {
		err := Run("QQONPanel", func(ctx context.Context, ready func()) error {
			ready()
			fmt.Println("ready")
			<-ctx.Done()
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUnixRunSignals$")
			cmd.Env = append(os.Environ(), "QQON_PLATFORM_SIGNAL_HELPER=1")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() || scanner.Text() != "ready" {
				t.Fatal("signal helper not ready")
			}
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal("signal did not produce graceful return:", err)
			}
		})
	}
}

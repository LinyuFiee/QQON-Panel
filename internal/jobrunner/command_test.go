package jobrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only the current test executable runs; no shells, package tools or SCM calls.
func TestCommandFixture(t *testing.T) {
	args := os.Args
	at := -1
	for i, arg := range args {
		if arg == "qqon-command-fixture" {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	args = args[at+1:]
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "output":
		_, _ = fmt.Fprintln(os.Stdout, "environment="+os.Getenv("QQON_TEST_SECRET"))
		_, _ = io.WriteString(os.Stdout, strings.Repeat("bounded output ", 24<<10))
		_, _ = io.WriteString(os.Stderr, "\nEND-OF-OUTPUT\n")
	case "flood":
		block := strings.Repeat("x", 1<<20)
		for i := 0; i < 34; i++ {
			if _, err := io.WriteString(os.Stdout, block); err != nil {
				os.Exit(3)
			}
		}
	case "tree":
		if len(args) != 2 {
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestCommandFixture$", "--", "qqon-command-fixture", "heartbeat", args[1])
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(3)
		}
		_ = child.Wait()
	case "heartbeat":
		if len(args) != 2 {
			os.Exit(2)
		}
		deadline := time.Now().Add(4 * time.Second)
		for i := 0; time.Now().Before(deadline); i++ {
			_ = os.WriteFile(args[1], []byte(fmt.Sprint(i)), 0600)
			time.Sleep(25 * time.Millisecond)
		}
	case "fail":
		os.Exit(7)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func fixtureArgs(mode string, extra ...string) []string {
	return append([]string{"-test.run=^TestCommandFixture$", "--", "qqon-command-fixture", mode}, extra...)
}

func TestRunCommandBoundedOutputAndCleanEnvironment(t *testing.T) {
	t.Setenv("QQON_TEST_SECRET", "secret-value-not-in-child")
	r := newTestRunner(t)
	task, err := r.Submit("fixture", "output", 5*time.Second, func(ctx context.Context, p *Progress) error {
		return RunCommand(ctx, os.Args[0], fixtureArgs("output"), p)
	})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitTask(t, r, task.ID, "terminal")
	if result.Status != "succeeded" {
		t.Fatalf("command result %+v", result)
	}
	logs := strings.Join(result.Logs, "\n")
	if len(logs) > logLimit || !strings.Contains(logs, "END-OF-OUTPUT") || strings.Contains(logs, "secret-value-not-in-child") {
		t.Fatalf("unexpected command logs (%d bytes)", len(logs))
	}
	if err := RunCommand(context.Background(), "relative-program", nil, nil); err == nil {
		t.Fatal("relative executable accepted")
	}
	if err := RunCommand(context.Background(), os.Args[0], fixtureArgs("fail"), nil); err == nil {
		t.Fatal("nonzero exit ignored")
	}
}

func TestRunCommandCancelsDescendants(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "heartbeat")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunCommand(ctx, os.Args[0], fixtureArgs("tree", marker), nil) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && len(data) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		cancel()
		<-done
		t.Fatalf("fixture grandchild did not start: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command cancellation did not drain")
	}
	time.Sleep(100 * time.Millisecond)
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("descendant continued after group cancellation")
	}
}

func TestRunCommandOutputBudgetAndCancelledContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := RunCommand(ctx, os.Args[0], fixtureArgs("flood"), nil); err == nil || !strings.Contains(err.Error(), "32 MiB") {
		t.Fatalf("output budget error %v", err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := RunCommand(cancelled, os.Args[0], fixtureArgs("output"), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context %v", err)
	}
}

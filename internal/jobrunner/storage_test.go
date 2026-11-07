package jobrunner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTerminalPersistenceFailureCannotReportSuccess(t *testing.T) {
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	original := r.persist
	calls := 0
	r.persist = func(tasks []*Task) error {
		calls++
		if calls >= 3 {
			return errors.New("terminal sync failed")
		}
		return original(tasks)
	}
	r.mu.Unlock()
	task, err := r.Submit("test", "terminal-sync", time.Second, func(context.Context, *Progress) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	result := awaitTask(t, r, task.ID, "terminal")
	if result.Status != "interrupted" || !strings.Contains(result.Error, "持久化") {
		t.Fatalf("undurable success leaked: %+v", result)
	}
	if err := r.Close(); err == nil {
		t.Fatal("Close did not return storage error")
	}
}

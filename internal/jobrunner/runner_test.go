package jobrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"panel/internal/platform"
)

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return r
}

func awaitTask(t *testing.T, r *Runner, id string, status string) Task {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		var found Task
		for _, task := range r.tasks {
			if task.ID == id {
				found = cloneTask(task)
			}
		}
		r.mu.Unlock()
		if status == "terminal" && terminal(found.Status) || found.Status == status {
			return found
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s", id, status)
	return Task{}
}

func requireStatus(t *testing.T, err error, status int) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) || failure.Status != status {
		t.Fatalf("want status %d; got %v", status, err)
	}
}

func TestQueueBoundSingleWorkerAndDurableAdmission(t *testing.T) {
	r := newTestRunner(t)
	started, release := make(chan struct{}), make(chan struct{})
	var active, maximum atomic.Int32
	work := func(ctx context.Context, p *Progress) error {
		n := active.Add(1)
		defer active.Add(-1)
		if n > maximum.Load() {
			maximum.Store(n)
		}
		body, err := os.ReadFile(r.path)
		if err != nil {
			return err
		}
		var saved document
		if err := json.Unmarshal(body, &saved); err != nil {
			return err
		}
		found := false
		for _, task := range saved.Tasks {
			if task.ID == p.task.ID && task.Status == "running" {
				found = true
			}
		}
		if !found {
			return errors.New("running state was not durable before work")
		}
		if p.task.Target == "blocker" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	first, err := r.Submit("test", "blocker", time.Minute, work)
	if err != nil || first.Status != "queued" {
		t.Fatalf("submit: %+v %v", first, err)
	}
	<-started
	_, err = r.Submit("test", "blocker", time.Minute, work)
	requireStatus(t, err, http.StatusConflict)
	ids := []string{first.ID}
	for i := 0; i < queueLimit; i++ {
		task, err := r.Submit("test", fmt.Sprint(i), time.Minute, work)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	before := time.Now()
	_, err = r.Submit("test", "ninth-waiter", time.Minute, work)
	requireStatus(t, err, http.StatusTooManyRequests)
	if time.Since(before) > 300*time.Millisecond {
		t.Fatal("full queue blocked admission")
	}
	close(release)
	for _, id := range ids {
		task := awaitTask(t, r, id, "terminal")
		if task.Status != "succeeded" {
			t.Fatalf("work: %+v", task)
		}
	}
	if maximum.Load() != 1 {
		t.Fatalf("worker concurrency %d", maximum.Load())
	}
	if err := platform.CheckPrivateFile(r.path); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryLogBoundsRedactionAndProgress(t *testing.T) {
	r := newTestRunner(t)
	firstID := ""
	for i := 0; i < 35; i++ {
		task, err := r.Submit("log", fmt.Sprint(i), time.Minute, func(ctx context.Context, p *Progress) error {
			p.Update(strings.Repeat("x", 100), -5, strings.Repeat("m", 4096))
			r.mu.Lock()
			valid := p.task.Progress == 0 && len(p.task.Stage) == 64 && len(p.task.Message) == 1024
			r.mu.Unlock()
			if !valid {
				return errors.New("progress was not bounded")
			}
			p.Update("copy", 500, "bounded")
			for n := 0; n < 48; n++ {
				p.Log(strings.Repeat("<&", 2000))
			}
			p.Log("password=must-not-persist")
			p.Log("-----BEGIN PRIVATE KEY-----\nsensitive-key-body\n-----END PRIVATE KEY-----")
			p.Log("FINAL-MARKER")
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstID = task.ID
		}
		value := awaitTask(t, r, task.ID, "terminal")
		if value.Status != "succeeded" || value.Progress != 100 {
			t.Fatalf("task result %+v", value)
		}
		encoded, _ := json.Marshal(value.Logs)
		if len(encoded) > logLimit || len(value.Logs) > 1024 {
			t.Fatalf("unbounded logs %d", len(encoded))
		}
		if strings.Contains(string(encoded), "must-not-persist") || strings.Contains(string(encoded), "sensitive-key-body") || !strings.Contains(string(encoded), "FINAL-MARKER") {
			t.Fatal("log redaction or tail preservation failed")
		}
	}
	r.mu.Lock()
	if len(r.tasks) != historyLimit {
		t.Errorf("history len %d", len(r.tasks))
	}
	for _, task := range r.tasks {
		if task.ID == firstID {
			t.Error("oldest terminal task not evicted")
		}
	}
	r.mu.Unlock()
	info, err := os.Stat(r.path)
	if err != nil || info.Size() > maxStateBytes {
		t.Fatalf("state bound %v %v", info, err)
	}
	if _, err := loadState(r.path); err != nil {
		t.Fatalf("cannot reload bounded logs: %v", err)
	}
}

func TestRestartInterruptsAndDoesNotReplay(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := r.path
	if _, err := New(dir); err == nil {
		t.Fatal("concurrent runner accepted same store")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tasks := []*Task{
		{ID: strings.Repeat("1", 32), Action: "apps.install", Target: "nginx", Status: "queued", Stage: "queued", CreatedAt: now, Logs: []string{}},
		{ID: strings.Repeat("2", 32), Action: "sites.create", Target: "example.test", Status: "running", Stage: "writing", CreatedAt: now, StartedAt: &now, Logs: []string{"partial"}},
	}
	if err := saveState(path, tasks); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	for _, task := range recovered.tasks {
		if task.Status != "interrupted" || task.FinishedAt == nil {
			t.Fatalf("not interrupted %+v", task)
		}
	}
	persisted, err := loadState(path)
	if err != nil || persisted[0].Status != "interrupted" {
		t.Fatalf("recovery not durable: %v", err)
	}
	if len(recovered.queue) != 0 {
		t.Fatal("restarted queue replayed payloads")
	}
}

func TestStorageFailureNeverExecutesUnpersistedWork(t *testing.T) {
	for _, phase := range []string{"admission", "start"} {
		t.Run(phase, func(t *testing.T) {
			r, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			var invoked atomic.Bool
			r.mu.Lock()
			original := r.persist
			calls := 0
			r.persist = func(tasks []*Task) error {
				calls++
				if phase == "admission" || calls == 2 {
					return errors.New("disk unavailable")
				}
				return original(tasks)
			}
			r.mu.Unlock()
			task, err := r.Submit("test", "persist", time.Second, func(context.Context, *Progress) error { invoked.Store(true); return nil })
			if phase == "admission" {
				requireStatus(t, err, http.StatusServiceUnavailable)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				awaitTask(t, r, task.ID, "interrupted")
			}
			if invoked.Load() {
				t.Fatal("unpersisted work executed")
			}
			_, err = r.Submit("test", "later", time.Second, func(context.Context, *Progress) error { return nil })
			requireStatus(t, err, http.StatusServiceUnavailable)
			if err := r.Close(); err == nil {
				t.Fatal("storage failure not returned by Close")
			}
		})
	}
}

func TestCloseTimeoutPanicAndAPIBoundaries(t *testing.T) {
	r := newTestRunner(t)
	timeout, err := r.Submit("test", "timeout", 40*time.Millisecond, func(ctx context.Context, p *Progress) error { <-ctx.Done(); return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	if task := awaitTask(t, r, timeout.ID, "terminal"); task.Status != "failed" || !strings.Contains(task.Error, "deadline") {
		t.Fatalf("timeout %+v", task)
	}
	panicked, err := r.Submit("test", "panic", time.Second, func(context.Context, *Progress) error { panic("secret panic payload") })
	if err != nil {
		t.Fatal(err)
	}
	if task := awaitTask(t, r, panicked.ID, "terminal"); task.Status != "failed" || strings.Contains(task.Error, "secret") {
		t.Fatalf("panic %+v", task)
	}
	for _, test := range []struct {
		action, target string
		duration       time.Duration
		work           Work
	}{
		{"", "x", time.Second, func(context.Context, *Progress) error { return nil }},
		{"test", "bad\n", time.Second, func(context.Context, *Progress) error { return nil }},
		{"test", "x", time.Hour, func(context.Context, *Progress) error { return nil }},
		{"test", "x", time.Second, nil},
	} {
		_, err := r.Submit(test.action, test.target, test.duration, test.work)
		requireStatus(t, err, http.StatusBadRequest)
	}
	mux := http.NewServeMux()
	r.Register(mux)
	for _, url := range []string{"/api/jobs", "/api/jobs/" + panicked.ID} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), panicked.ID) {
			t.Fatalf("GET %s: %d %s", url, w.Code, w.Body.String())
		}
	}
	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, httptest.NewRequest("GET", "/api/jobs/no-such-id", nil))
	if missing.Code != 404 {
		t.Fatal(missing.Code)
	}
	started := make(chan struct{})
	var queuedRan atomic.Bool
	live, err := r.Submit("test", "close", time.Minute, func(ctx context.Context, p *Progress) error {
		close(started)
		<-ctx.Done()
		p.Update("late", 100, "late")
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	queued, err := r.Submit("test", "not-run", time.Minute, func(context.Context, *Progress) error { queuedRan.Store(true); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if queuedRan.Load() {
		t.Fatal("closed queued work ran")
	}
	awaitTask(t, r, live.ID, "interrupted")
	awaitTask(t, r, queued.ID, "interrupted")
	_, err = r.Submit("test", "closed", time.Second, func(context.Context, *Progress) error { return nil })
	requireStatus(t, err, http.StatusServiceUnavailable)
}

func TestProgressPersistenceIsThrottled(t *testing.T) {
	r := newTestRunner(t)
	started, release := make(chan struct{}), make(chan struct{})
	task, err := r.Submit("test", "throttle", time.Minute, func(ctx context.Context, p *Progress) error {
		close(started)
		for i := 0; i < 2000; i++ {
			p.Update("busy", i, "working")
			p.Log("a line")
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	r.mu.Lock()
	original := r.persist
	var writes atomic.Int32
	r.persist = func(tasks []*Task) error { writes.Add(1); return original(tasks) }
	r.mu.Unlock()
	time.Sleep(1250 * time.Millisecond)
	if n := writes.Load(); n > 1 || n < 1 {
		t.Fatalf("progress writes in 1.25s: %d", n)
	}
	close(release)
	awaitTask(t, r, task.ID, "succeeded")
	if writes.Load() != 2 {
		t.Fatalf("terminal write missing: %d", writes.Load())
	}
}

func TestRejectMalformedAndOversizedStore(t *testing.T) {
	for _, body := range []string{`{"format":1,"tasks":null}`, `{"format":2,"tasks":[]}`, `{"format":1,"tasks":[]} {}`, strings.Repeat(" ", maxStateBytes+1)} {
		dir := t.TempDir()
		r, err := New(dir)
		if err != nil {
			t.Fatal(err)
		}
		path := r.path
		r.Close()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if other, err := New(dir); err == nil {
			other.Close()
			t.Fatal("malformed state accepted")
		}
	}
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := r.path
	r.Close()
	if err := os.Link(path, filepath.Join(filepath.Dir(path), "hardlink")); err == nil {
		if other, err := New(dir); err == nil {
			other.Close()
			t.Fatal("hard linked state accepted")
		}
	}
}

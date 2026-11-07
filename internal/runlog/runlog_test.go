package runlog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"panel/internal/platform"
)

func TestPrivateTempLifecycleAndSanitization(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private", "logs")
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := []byte("服务已启动\r\x00\x1b[31m\t\u202e\u2028\xff\n第二条")
	if n, err := w.Write(p); err != nil || n != len(p) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if n, err := w.Write(nil); err != nil || n != 0 {
		t.Fatalf("empty Write = %d, %v", n, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal("Close must be idempotent:", err)
	}
	if n, err := w.Write([]byte("must not appear")); n != 0 || err == nil {
		t.Fatal("write after Close succeeded")
	}
	path := filepath.Join(dir, "runtime.log")
	if err := platform.CheckPrivateFile(path); err != nil {
		t.Fatal("file failed private inheritance validation:", err)
	}
	data := readLog(t, path)
	if !utf8.Valid(data) || !bytes.Contains(data, []byte("服务已启动")) || !bytes.HasSuffix(data, []byte("第二条\n")) || bytes.Count(data, []byte("\n")) != 2 || bytes.Contains(data, []byte("must not appear")) {
		t.Fatalf("unexpected sanitized output: %q", data)
	}
	for _, r := range string(data) {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029') {
			t.Fatalf("control character survived: %U", r)
		}
	}
	w, err = Open(dir)
	if err != nil {
		t.Fatal("reopen failed:", err)
	}
	if _, err := w.Write([]byte("reopened\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readLog(t, path), append(data, []byte("reopened\n")...)) {
		t.Fatal("reopen overwrote prior events")
	}
}

func TestLineLimitAndUTF8(t *testing.T) {
	for _, p := range [][]byte{bytes.Repeat([]byte("x"), 2*maxFileBytes), bytes.Repeat([]byte("中"), maxLineBytes)} {
		w, err := Open(filepath.Join(t.TempDir(), "logs"))
		if err != nil {
			t.Fatal(err)
		}
		if n, err := w.Write(p); err != nil || n != len(p) {
			t.Fatalf("truncated Write = %d, %v", n, err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		data := readLog(t, filepath.Join(w.directory, logNames[0]))
		if len(data) > maxLineBytes || !utf8.Valid(data) || !bytes.HasSuffix(data, []byte(truncated+"\n")) || bytes.Count(data, []byte("\n")) != 1 {
			t.Fatal("long line was not safely bounded")
		}
	}
}

func TestRotationIsBoundedAndKeepsNewest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 4000)
	for i := 0; i < 1400; i++ {
		p := []byte(fmt.Sprintf("%06d %s\n", i, payload))
		if n, err := w.Write(p); n != len(p) || err != nil {
			t.Fatalf("write %d = %d, %v", i, n, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != backupCount+1 {
		t.Fatalf("expected exactly four fixed files, got %d, %v", len(entries), err)
	}
	last := -1
	for i := backupCount; i >= 0; i-- {
		path := filepath.Join(dir, logNames[i])
		data := readLog(t, path)
		if len(data) == 0 || len(data) > maxFileBytes {
			t.Fatal("rotation exceeded file bound")
		}
		if err := platform.CheckPrivateFile(path); err != nil {
			t.Fatal("rotated file lost private permissions:", err)
		}
		for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
			var number int
			if _, err := fmt.Sscanf(string(line[:6]), "%d", &number); err != nil {
				t.Fatal(err)
			}
			if last >= 0 && number != last+1 {
				t.Fatal("rotation lost or reordered events inside retention window")
			}
			last = number
		}
	}
	if last != 1399 {
		t.Fatal("newest event missing")
	}
}

func TestConcurrentWritesAndClose(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "logs"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for event := 0; event < 50; event++ {
				p := []byte(fmt.Sprintf("worker-%02d-event-%02d\n", worker, event))
				if n, err := w.Write(p); n != len(p) || err != nil {
					t.Errorf("concurrent write failed: %d, %v", n, err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(readLog(t, filepath.Join(w.directory, logNames[0]))), "\n"), "\n")
	seen := make(map[string]bool)
	for _, line := range lines {
		if len(line) != len("worker-00-event-00") || seen[line] {
			t.Fatal("concurrent writes were interleaved or duplicated")
		}
		seen[line] = true
	}
	if len(seen) != 800 {
		t.Fatal("concurrent writes lost events")
	}
	w, err = Open(filepath.Join(t.TempDir(), "logs"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = w.Write([]byte("safe event\n"))
			_ = w.Close()
		}()
	}
	wg.Wait()
	if n, err := w.Write([]byte("closed")); n != 0 || err == nil {
		t.Fatal("concurrent Close was not final")
	}
}

func TestUnsafeTargetsAreNotOverwritten(t *testing.T) {
	for _, kind := range []string{"directory", "hardlink", "oversized", "unsafe-backup"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "logs")
			if err := platform.PreparePrivateDirectory(dir); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, logNames[0])
			var before []byte
			switch kind {
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "unsafe-backup":
				path = filepath.Join(dir, logNames[1])
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				before = bytes.Repeat([]byte("x"), maxFileBytes+1)
			case "hardlink":
				before = []byte("untouched")
			}
			if before != nil {
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "hardlink" {
				if err := os.Link(path, filepath.Join(dir, "alias")); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
			}
			if w, err := Open(dir); err == nil {
				_ = w.Close()
				t.Fatal("unsafe target accepted")
			}
			if before != nil && !bytes.Equal(readLog(t, path), before) {
				t.Fatal("rejected target mutated")
			}
		})
	}
}

func TestRotationFailureStopsWithoutDeletingTarget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	obstacle := filepath.Join(dir, logNames[3])
	if err := os.Mkdir(obstacle, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		_, err = w.Write(bytes.Repeat([]byte("x"), 8000))
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("rotation into unsafe target succeeded")
	}
	if n, err := w.Write([]byte("credential-sentinel")); n != 0 || err == nil || strings.Contains(err.Error(), "credential-sentinel") {
		t.Fatal("failed writer did not stop safely")
	}
	if err := w.Close(); err == nil {
		t.Fatal("Close hid the rotation failure")
	}
	if info, err := os.Stat(obstacle); err != nil || !info.IsDir() {
		t.Fatal("unsafe rotation target was removed")
	}
}

func readLog(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

//go:build linux

package machine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fixtureProc(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"net", "self"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func putProc(t *testing.T, root, name, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxProcFixtureSampling(t *testing.T) {
	root := fixtureProc(t)
	putProc(t, root, "stat", "cpu  100 0 50 850 0 0 0 0 5 0\ncpu0 100 0 50 850\n")
	putProc(t, root, "meminfo", "MemTotal: 1000 kB\nMemFree: 100 kB\nMemAvailable: 400 kB\n")
	putProc(t, root, "uptime", "1234.25 9876.0\n")
	putProc(t, root, "net/dev", "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n lo: 500 0 0 0 0 0 0 0 500 0 0 0 0 0 0 0\n eth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0\n")
	putProc(t, root, "self/statm", "1000 20 0 0 0 0 0\n")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &linuxSampler{started: base.Add(-time.Minute), procRoot: root, osName: "fixture linux", statfs: func(path string, fs *syscall.Statfs_t) error {
		if path != "/" {
			t.Fatalf("unexpected statfs target %s", path)
		}
		fs.Bsize, fs.Blocks, fs.Bfree = 4096, 100, 30
		return nil
	}}
	first := p.sample(base)
	if !first.Available || first.CPUPercent != nil || first.Network.RX != nil || first.Reason == "" {
		t.Fatalf("first sample should wait for deltas: %+v", first)
	}
	if first.Memory.Used == nil || *first.Memory.Used != 600*1024 || *first.Memory.Total != 1000*1024 || *first.Memory.Percent != 60 {
		t.Fatalf("incorrect memory: %+v", first.Memory)
	}
	if *first.Disk.Used != 70*4096 || *first.Disk.Total != 100*4096 || *first.Disk.Percent != 70 || *first.UptimeSeconds != 1234.25 {
		t.Fatal("incorrect disk or uptime")
	}
	if first.Panel.RSSBytes == nil || *first.Panel.RSSBytes != uint64(20*os.Getpagesize()) || first.Panel.HeapBytes == 0 || first.Panel.Goroutines == 0 {
		t.Fatal("invalid process metrics")
	}
	putProc(t, root, "stat", "cpu  140 0 70 890 0 0 0 0 10 0\n")
	putProc(t, root, "net/dev", "eth0: 1400 0 0 0 0 0 0 0 2600 0 0 0 0 0 0 0\n")
	second := p.sample(base.Add(2 * time.Second))
	if second.CPUPercent == nil || *second.CPUPercent != 60 || second.Network.RX == nil || *second.Network.RX != 200 || *second.Network.TX != 300 || second.Reason != "" {
		t.Fatalf("incorrect sample delta: %+v", second)
	}
	// Counter resets and missing data remain null; no underflow or fabricated 0.
	putProc(t, root, "stat", "cpu 1 0 1 1\n")
	putProc(t, root, "net/dev", "eth0: 1 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0\n")
	third := p.sample(base.Add(4 * time.Second))
	if third.CPUPercent != nil || third.Network.RX != nil {
		t.Fatal("counter reset became bogus usage")
	}
	if err := os.Remove(filepath.Join(root, "meminfo")); err != nil {
		t.Fatal(err)
	}
	missing := p.sample(base.Add(6 * time.Second))
	if missing.Available || missing.Memory.Total != nil || missing.Memory.Used != nil || !strings.Contains(missing.Reason, "内存") {
		t.Fatal("missing memory was not reported")
	}
}

func TestLinuxParserRejectsBadAndOverflowingSamples(t *testing.T) {
	for _, data := range []string{"", "cpu 1 2", "cpu x 0 0 1", "cpu 18446744073709551615 1 0 1"} {
		if _, err := parseCPU([]byte(data)); err == nil {
			t.Fatalf("accepted bad CPU %q", data)
		}
	}
	for _, data := range []string{"MemTotal: 100 kB\n", "MemTotal: 100 kB\nMemAvailable: 200 kB\n", "MemTotal: 18446744073709551615 kB\nMemAvailable: 1 kB\n", "MemTotal: 1 MB\nMemAvailable: 0 kB\n"} {
		if _, err := parseMemory([]byte(data)); err == nil {
			t.Fatalf("accepted bad memory %q", data)
		}
	}
	for _, data := range []string{"", "eth0: 1 2", "eth0: -1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"} {
		if _, err := parseNetwork([]byte(data)); err == nil {
			t.Fatalf("accepted bad network %q", data)
		}
	}
	root := fixtureProc(t)
	putProc(t, root, "oversize", strings.Repeat("x", 100))
	if _, err := readBoundedFile(filepath.Join(root, "oversize"), 50); err == nil {
		t.Fatal("proc read limit failed")
	}
}

func TestLinuxSpecialFilesAndReplacementRace(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	s := fixtureService(t, Options{FileRoot: root}, nil)
	before := time.Now()
	requireStatus(t, request(t, s, "GET", "/api/files/preview?path=fifo", ""), 403)
	if time.Since(before) > time.Second {
		t.Fatal("FIFO preview blocked")
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("MUST_NOT_LEAK"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			tmp := filepath.Join(root, fmt.Sprintf("swap-%d", i%2))
			_ = os.Remove(tmp)
			if i%2 == 0 {
				_ = os.Link(filepath.Join(root, "safe"), tmp)
			} else {
				_ = os.Symlink(filepath.Join(outside, "secret"), tmp)
			}
			_ = os.Rename(tmp, filepath.Join(root, "racing"))
		}
	}()
	defer func() { close(stop); <-stopped }()
	for i := 0; i < 100; i++ {
		r := request(t, s, "GET", "/api/files/preview?path=racing", "")
		if strings.Contains(r.Body.String(), "MUST_NOT_LEAK") {
			t.Fatal("symlink replacement escaped root")
		}
		if r.Code == 200 && !strings.Contains(r.Body.String(), `"content":"safe"`) {
			t.Fatalf("unexpected preview during race: %s", r.Body.String())
		}
	}
}

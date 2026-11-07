package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"panel/internal/apps"
	"panel/internal/jobrunner"
)

type testHost struct {
	mu         sync.Mutex
	commands   []string
	fail       string
	remaining  int
	verifyFail int
	verified   []record
}

func (h *testHost) command(ctx context.Context, action, binary string) error {
	if !knownNginxBinary(binary) {
		return errors.New("unexpected binary")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.commands = append(h.commands, action)
	if action == h.fail && h.remaining != 0 {
		if h.remaining > 0 {
			h.remaining--
		}
		return errors.New("unsafe command output PRIVATE SOURCE")
	}
	return ctx.Err()
}
func (h *testHost) verify(ctx context.Context, r record, fresh bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.verified = append(h.verified, r)
	if h.verifyFail > 0 {
		h.verifyFail--
		return errors.New("PRIVATE HTTP CONTENT")
	}
	return ctx.Err()
}

func fixture(t *testing.T) (*Service, *testHost, *http.ServeMux) {
	t.Helper()
	base := t.TempDir()
	data, err := os.MkdirTemp(base, "private-")
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := jobrunner.New(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := jobs.Close(); err != nil {
			t.Error(err)
		}
	})
	conf := filepath.Join(base, "nginx-conf")
	if err := os.Mkdir(conf, 0755); err != nil {
		t.Fatal(err)
	}
	host := &testHost{}
	options := Options{DataDir: data, Jobs: jobs, Permission: func() error { return nil }, PanelHost: "panel.example.test", PanelPort: 5173,
		Runtime: func(context.Context) (apps.RuntimeInfo, error) { return testRuntime(), nil },
	}
	s, err := newService(options, hostAdapters{goos: "linux", strict: false, webRoot: filepath.Join(base, "web"), configRoot: conf, command: host.command, verify: host.verify, verifyRemoved: func(context.Context, record) error { return nil }, caBundle: func() (string, error) { return "/etc/ssl/certs/ca-certificates.crt", nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	mux := http.NewServeMux()
	s.Register(mux)
	jobs.Register(mux)
	return s, host, mux
}

func testRuntime() apps.RuntimeInfo {
	return apps.RuntimeInfo{Available: true, NginxInstalled: true, NginxBinary: "/usr/sbin/nginx", NginxRunning: true, WebUser: "www-data", WebGroup: "www-data", PHPRuntimes: []apps.PHPRuntime{{ID: "php8.2-fpm", Version: "8.2", Unit: "php8.2-fpm.service", Socket: "/run/php/php8.2-fpm.sock"}}}
}
func staticInput(domain string) siteInput {
	return siteInput{Name: "Test site", Domains: []string{domain}, Type: "static", Port: 8181, Enabled: true}
}

func request(t *testing.T, mux http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	switch v := body.(type) {
	case nil:
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		var err error
		data, err = json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func taskFrom(t *testing.T, w *httptest.ResponseRecorder) jobrunner.Task {
	t.Helper()
	if w.Code != 202 {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Job jobrunner.Task `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Job
}

func waitTask(t *testing.T, mux http.Handler, id string) jobrunner.Task {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		w := request(t, mux, "GET", "/api/jobs/"+id, nil)
		if w.Code != 200 {
			t.Fatalf("job get: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Task jobrunner.Task `json:"job"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Task.ID == "" {
			if err := json.Unmarshal(w.Body.Bytes(), &result.Task); err != nil {
				t.Fatal(err)
			}
		}
		switch result.Task.Status {
		case "succeeded", "failed", "interrupted":
			return result.Task
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return jobrunner.Task{}
}

func createFixtureSite(t *testing.T, s *Service) record {
	t.Helper()
	id, _ := randomID()
	if err := s.create(context.Background(), id, staticInput("web.example.test"), nil); err != nil {
		t.Fatal(err)
	}
	r, err := s.snapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCRUDLifecycleAndRetainedFiles(t *testing.T) {
	s, host, mux := fixture(t)
	unknown := filepath.Join(s.adapters.configRoot, "customer.conf")
	original := []byte("# user configuration must remain untouched\n")
	if err := os.WriteFile(unknown, original, 0644); err != nil {
		t.Fatal(err)
	}
	job := taskFrom(t, request(t, mux, "POST", "/api/sites", map[string]any{"confirmed": true, "site": staticInput("Web.Example.Test.")}))
	job = waitTask(t, mux, job.ID)
	if job.Status != "succeeded" {
		t.Fatalf("create: %+v", job)
	}
	r, err := s.snapshot(job.Target)
	if err != nil {
		t.Fatal(err)
	}
	if r.Site.Domains[0] != "web.example.test" || r.Site.Revision != 1 {
		t.Fatalf("bad canonical site %+v", r.Site)
	}
	conf, err := os.ReadFile(filepath.Join(s.adapters.configRoot, "qqon-"+r.Site.ID+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# qqon-managed-site-v1 " + r.Site.ID, "location = /.__qqon_health", "if ($host !~*", "return 421", "disable_symlinks on", "try_files"} {
		if !bytes.Contains(conf, []byte(expected)) {
			t.Fatalf("missing %q", expected)
		}
	}
	if strings.Contains(string(conf), "default_server") {
		t.Fatal("must not hijack default config")
	}
	in := inputFromSite(r.Site)
	in.Enabled = false
	patch := taskFrom(t, request(t, mux, "PATCH", "/api/sites/"+r.Site.ID, map[string]any{"confirmed": true, "revision": r.Site.Revision, "site": in}))
	if got := waitTask(t, mux, patch.ID); got.Status != "succeeded" {
		t.Fatalf("patch: %+v", got)
	}
	updated, _ := s.snapshot(r.Site.ID)
	if updated.Site.Enabled || updated.Site.Revision != 2 {
		t.Fatal("disable not persisted")
	}
	conf, _ = os.ReadFile(filepath.Join(s.adapters.configRoot, "qqon-"+r.Site.ID+".conf"))
	if !bytes.Contains(conf, []byte("location / { return 503; }")) {
		t.Fatal("disabled host can fall through")
	}
	if got := request(t, mux, "PATCH", "/api/sites/"+r.Site.ID, map[string]any{"confirmed": true, "revision": 1, "site": in}); got.Code != 409 {
		t.Fatalf("stale patch %d", got.Code)
	}
	restarted, err := newService(s.options, s.adapters)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.snapshot(r.Site.ID)
	restarted.Close()
	if err != nil || !reflect.DeepEqual(updated, restored) {
		t.Fatalf("restart mismatch %v", err)
	}
	deletion := taskFrom(t, request(t, mux, "DELETE", "/api/sites/"+r.Site.ID, map[string]any{"confirmed": true, "revision": 2}))
	if got := waitTask(t, mux, deletion.ID); got.Status != "succeeded" {
		t.Fatalf("delete: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(r.Site.Root, "index.html")); err != nil {
		t.Fatal("delete removed web files", err)
	}
	if _, err := os.Stat(filepath.Join(s.adapters.configRoot, "qqon-"+r.Site.ID+".conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("managed config remains", err)
	}
	if data, _ := os.ReadFile(unknown); !bytes.Equal(data, original) {
		t.Fatal("modified user config")
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if !reflect.DeepEqual(host.commands, []string{"test", "reload", "test", "reload", "test", "reload"}) {
		t.Fatalf("unexpected commands %v", host.commands)
	}
}

func TestRollbackOnTestReloadAndHealthFailure(t *testing.T) {
	for _, stage := range []string{"test", "reload", "health"} {
		t.Run(stage, func(t *testing.T) {
			s, h, _ := fixture(t)
			old := createFixtureSite(t, s)
			oldConfig, _ := os.ReadFile(filepath.Join(s.adapters.configRoot, "qqon-"+old.Site.ID+".conf"))
			h.mu.Lock()
			if stage == "health" {
				h.verifyFail = 1
			} else {
				h.fail = stage
				h.remaining = 1
			}
			h.mu.Unlock()
			in := inputFromSite(old.Site)
			in.Enabled = false
			if err := s.patch(context.Background(), old.Site.ID, 1, in, nil); err == nil {
				t.Fatal("accepted failed operation")
			}
			current, _ := s.snapshot(old.Site.ID)
			if !reflect.DeepEqual(current, old) {
				t.Fatal("metadata not rolled back")
			}
			after, _ := os.ReadFile(filepath.Join(s.adapters.configRoot, "qqon-"+old.Site.ID+".conf"))
			if !bytes.Equal(after, oldConfig) {
				t.Fatal("config not rolled back")
			}
			if _, err := os.Stat(filepath.Join(s.statePath, "transaction.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("journal remained after successful rollback", err)
			}
			if err := s.ready(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnedConfigTamperNeverOverwritten(t *testing.T) {
	s, _, _ := fixture(t)
	old := createFixtureSite(t, s)
	name := filepath.Join(s.adapters.configRoot, "qqon-"+old.Site.ID+".conf")
	data := []byte("# unknown config\nserver { listen 9191; }\n")
	if err := os.WriteFile(name, data, 0644); err != nil {
		t.Fatal(err)
	}
	in := inputFromSite(old.Site)
	in.Enabled = false
	if err := s.patch(context.Background(), old.Site.ID, 1, in, nil); err == nil {
		t.Fatal("overwrote unknown config")
	}
	if err := s.delete(context.Background(), old.Site.ID, 1, nil); err == nil {
		t.Fatal("deleted unknown config")
	}
	got, _ := os.ReadFile(name)
	if !bytes.Equal(got, data) {
		t.Fatal("unknown config modified")
	}
	if got := s.configStatus(old); got != "conflict" {
		t.Fatalf("status %s", got)
	}
}

func TestPermissionAtAdmissionAndExecution(t *testing.T) {
	s, _, mux := fixture(t)
	var allow atomic.Bool
	allow.Store(true)
	var checks atomic.Int32
	s.options.Permission = func() error {
		checks.Add(1)
		if !allow.Load() {
			return errors.New("PRIVATE permission diagnostic")
		}
		return nil
	}
	started := make(chan struct{})
	release := make(chan struct{})
	_, err := s.options.Jobs.Submit("test.hold", "fixture", time.Minute, func(ctx context.Context, p *jobrunner.Progress) error {
		close(started)
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
	accepted := taskFrom(t, request(t, mux, "POST", "/api/sites", map[string]any{"confirmed": true, "site": staticInput("queued.example.test")}))
	allow.Store(false)
	close(release)
	job := waitTask(t, mux, accepted.ID)
	if job.Status != "failed" || strings.Contains(job.Error, "PRIVATE") {
		t.Fatalf("permission not enforced safely: %+v", job)
	}
	if len(s.siteList()) != 0 {
		t.Fatal("unauthorized queued work mutated")
	}
	if _, err := os.Stat(s.adapters.webRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unauthorized work created webroot")
	}
	w := request(t, mux, "POST", "/api/sites", map[string]any{"confirmed": true, "site": staticInput("denied.example.test")})
	if w.Code != 403 || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatalf("bad denial %d %s", w.Code, w.Body.String())
	}
	if checks.Load() < 3 {
		t.Fatal("permission was not rechecked")
	}
}

func TestWindowsUnsupportedAndNoEnvironmentBypass(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production adapter test")
	}
	s, _, _ := fixture(t)
	t.Setenv("QQON_ALLOW_SITE_WRITES", "1")
	t.Setenv("QQON_REAL_HOST_TEST", "1")
	prod, err := New(s.options)
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()
	mux := http.NewServeMux()
	prod.Register(mux)
	w := request(t, mux, "GET", "/api/sites", nil)
	if !strings.Contains(w.Body.String(), `"available":false`) || !strings.Contains(w.Body.String(), "Windows") {
		t.Fatal(w.Body.String())
	}
	w = request(t, mux, "POST", "/api/sites", map[string]any{"confirmed": true, "site": staticInput("web.example.test")})
	if w.Code != 501 {
		t.Fatalf("Windows accepted mutation %d", w.Code)
	}
}

func TestValidationIsolationAndInjection(t *testing.T) {
	s, _, _ := fixture(t)
	for _, domain := range []string{"panel.example.test", "PANEL.EXAMPLE.TEST.", "example.test", "sub.panel.example.test", "localhost", "test.localhost", "127.0.0.1", "127.42.3.4", "::1", "::ffff:127.0.0.1", "0.0.0.0", "224.1.1.1", "*.example.test", "site;return 200", "a\nserver", "https://x.test", ""} {
		in := staticInput(domain)
		if _, err := s.validateInput(in); err == nil {
			t.Errorf("accepted domain %q", domain)
		}
	}
	for _, upstream := range []string{"file:///etc/passwd", "http://user:secret@x.test", "http://x.test/?secret=yes", "http://x.test/#part", "http://x.test/?", "http://x.test/$uri", "http://x.test/\";return", "http://panel.example.test:9000", "http://127.0.0.1:5173", "http://[::1]:8080", "http://0.0.0.0:8888", "http://x.test:22?x", "http://x.test:99999", "http://x.test/%0aevil"} {
		if _, err := s.validateUpstream(upstream); err == nil {
			t.Errorf("accepted upstream %q", upstream)
		}
	}
	for _, upstream := range []string{"http://127.0.0.1:9000/", "http://[::1]:9000/app/", "https://upstream.example.test/app"} {
		if _, err := s.validateUpstream(upstream); err != nil {
			t.Errorf("valid upstream %s: %v", upstream, err)
		}
	}
	for _, port := range []int{0, 22, 1023, 5173, 65536} {
		in := staticInput("x.test")
		in.Port = port
		if _, err := s.validateInput(in); err == nil {
			t.Errorf("accepted port %d", port)
		}
	}
}

func TestBoundedJSONRejectsAmbiguity(t *testing.T) {
	_, _, mux := fixture(t)
	valid := `{"confirmed":true,"site":{"name":"demo","domains":["x.test"],"type":"static","port":8181,"enabled":true}}`
	for _, body := range []string{`null`, valid + `{}`, strings.Replace(valid, `"confirmed":true`, `"confirmed":true,"confirmed":false`, 1), strings.Replace(valid, `"confirmed"`, `"Confirmed"`, 1), strings.Replace(valid, `"enabled":true`, `"enabled":null`, 1), strings.Replace(valid, `,"enabled":true`, ``, 1), strings.Replace(valid, `"type":"static"`, `"type":"static","root":"/etc"`, 1), strings.Replace(valid, `"port":8181`, `"port":8181.5`, 1)} {
		w := request(t, mux, "POST", "/api/sites", body)
		if w.Code != 400 {
			t.Errorf("body accepted (%d) %s", w.Code, body)
		}
	}
	large := fmt.Sprintf(`{"confirmed":true,"site":{"name":%q}}`, strings.Repeat("x", 70<<10))
	if w := request(t, mux, "POST", "/api/sites", large); w.Code != 413 {
		t.Fatalf("body bound %d", w.Code)
	}
}

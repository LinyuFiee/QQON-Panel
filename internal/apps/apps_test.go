package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"panel/internal/jobrunner"
)

type fakeExit int

func (e fakeExit) Error() string { return fmt.Sprintf("fake exit %d", e) }
func (e fakeExit) ExitCode() int { return int(e) }

type fakeEntry string

func (e fakeEntry) Name() string               { return string(e) }
func (e fakeEntry) IsDir() bool                { return false }
func (e fakeEntry) Type() fs.FileMode          { return 0 }
func (e fakeEntry) Info() (fs.FileInfo, error) { return nil, errors.New("unused fake stat") }

type invocation struct {
	executable string
	args       []string
	remaining  time.Duration
}

type fakeHost struct {
	mu         sync.Mutex
	goos       string
	manager    string
	files      map[string]string
	dirs       map[string][]fs.DirEntry
	binaries   map[string]bool
	sockets    map[string]bool
	packages   map[string]string
	states     map[string]string
	queries    []invocation
	commands   []invocation
	queryErr   error
	commandErr error
	apply      bool
	onCommand  func(invocation)
}

func linuxFixture(manager string) *fakeHost {
	h := &fakeHost{goos: "linux", manager: manager, files: map[string]string{}, dirs: map[string][]fs.DirEntry{}, binaries: map[string]bool{}, sockets: map[string]bool{}, packages: map[string]string{}, states: map[string]string{}, apply: true}
	h.binaries["/usr/bin/systemctl"], h.binaries["/usr/sbin/nginx"] = true, true
	if manager == "apt" {
		h.binaries["/usr/bin/apt-get"], h.binaries["/usr/bin/dpkg-query"] = true, true
	} else {
		h.binaries["/usr/bin/dnf"], h.binaries["/usr/bin/rpm"] = true, true
	}
	h.files["/etc/passwd"] = "root:x:0:0:root:/root:/bin/bash\nwww-data:x:33:33:www-data:/var/www:/usr/sbin/nologin\nnginx:x:992:992:nginx:/var/lib/nginx:/sbin/nologin\napache:x:48:48:apache:/usr/share/httpd:/sbin/nologin\n"
	h.files["/etc/group"] = "root:x:0:\nwww-data:x:33:\nnginx:x:992:\napache:x:48:\n"
	h.files["/etc/nginx/nginx.conf"] = "user www-data;\nworker_processes auto;\nevents {worker_connections 768;}\nhttp {include /etc/nginx/conf.d/*.conf;}\n"
	if manager == "dnf" {
		h.files["/etc/nginx/nginx.conf"] = "user nginx nginx;\nevents {}\nhttp {}"
	}
	h.dirs["/usr/lib/systemd/system"] = []fs.DirEntry{}
	return h
}

func (h *fakeHost) platform() string { return h.goos }
func (h *fakeHost) executable(path string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.binaries[path]
}
func (h *fakeHost) readFile(path string, limit int64) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	if int64(len(v)) > limit {
		return nil, errors.New("too big")
	}
	return []byte(v), nil
}
func (h *fakeHost) readDir(path string, limit int) ([]fs.DirEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.dirs[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	if len(v) > limit {
		return nil, errors.New("too many entries")
	}
	return append([]fs.DirEntry{}, v...), nil
}
func (h *fakeHost) socket(path string) bool { h.mu.Lock(); defer h.mu.Unlock(); return h.sockets[path] }

func (h *fakeHost) addPHP(version string) {
	unit := "php" + version + "-fpm.service"
	if h.manager == "dnf" {
		unit = "php-fpm.service"
	}
	h.dirs["/usr/lib/systemd/system"] = append(h.dirs["/usr/lib/systemd/system"], fakeEntry(unit))
	h.files["/usr/lib/systemd/system/"+unit] = "[Service]\nExecStart=/usr/sbin/" + strings.TrimSuffix(unit, ".service") + "\n"
	config, socket, user := "/etc/php/"+version+"/fpm/pool.d/www.conf", "/run/php/php"+version+"-fpm.sock", "www-data"
	if h.manager == "dnf" {
		config, socket, user = "/etc/php-fpm.d/www.conf", "/run/php-fpm/www.sock", "apache"
	}
	h.files[config] = "[www]\nuser = " + user + "\ngroup = " + user + "\nlisten = " + socket + "\n"
	h.packages[strings.TrimSuffix(unit, ".service")] = version + ".12-1"
	h.states[unit] = "active"
	h.sockets[socket] = true
}

func (h *fakeHost) query(ctx context.Context, executable string, args []string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	deadline, _ := ctx.Deadline()
	h.queries = append(h.queries, invocation{executable, append([]string{}, args...), time.Until(deadline)})
	if h.queryErr != nil {
		return nil, h.queryErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	marker := -1
	for i, arg := range args {
		if arg == "--" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return nil, errors.New("query has no positional boundary")
	}
	names := args[marker+1:]
	var out strings.Builder
	if filepath.Base(executable) == "dpkg-query" || filepath.Base(executable) == "rpm" {
		missing := false
		for _, name := range names {
			version, ok := h.packages[name]
			if !ok {
				missing = true
				continue
			}
			if h.manager == "apt" {
				fmt.Fprintf(&out, "%s\tinstall ok installed\t%s\n", name, version)
			} else {
				fmt.Fprintf(&out, "%s\t%s\n", name, version)
			}
		}
		if missing {
			return []byte(out.String()), fakeExit(1)
		}
		return []byte(out.String()), nil
	}
	if filepath.Base(executable) == "systemctl" {
		for _, name := range names {
			state, ok := h.states[name]
			load := "loaded"
			if !ok {
				load, state = "not-found", "inactive"
			}
			fmt.Fprintf(&out, "Id=%s\nLoadState=%s\nActiveState=%s\n\n", name, load, state)
		}
		return []byte(out.String()), nil
	}
	return nil, errors.New("unexpected executable")
}

func (h *fakeHost) command(ctx context.Context, executable string, args []string, p *jobrunner.Progress) error {
	h.mu.Lock()
	deadline, _ := ctx.Deadline()
	call := invocation{executable, append([]string{}, args...), time.Until(deadline)}
	h.commands = append(h.commands, call)
	callback := h.onCommand
	if h.commandErr != nil {
		err := h.commandErr
		h.mu.Unlock()
		if callback != nil {
			callback(call)
		}
		return err
	}
	if h.apply {
		if filepath.Base(executable) == "systemctl" {
			action := ""
			for _, arg := range args {
				if arg == "start" || arg == "stop" || arg == "restart" {
					action = arg
				}
			}
			for _, arg := range args {
				if strings.HasSuffix(arg, ".service") {
					if action == "stop" {
						h.states[arg] = "inactive"
					} else {
						h.states[arg] = "active"
					}
				}
			}
		} else if slicesContain(args, "install") {
			pkg := args[len(args)-1]
			h.packages[pkg] = "1.2.3-fixture"
			unit := map[string]string{"nginx": "nginx.service", "mariadb-server": "mariadb.service", "redis-server": "redis-server.service", "redis": "redis.service"}[pkg]
			if pkg == "php-fpm" {
				h.addPHP("8.2")
			} else {
				h.states[unit] = "inactive"
			}
		}
	}
	h.mu.Unlock()
	if callback != nil {
		callback(call)
	}
	p.Log("fake command completed")
	return ctx.Err()
}

func slicesContain(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func setupService(t *testing.T, h *fakeHost, permission func() error) (*Service, *jobrunner.Runner, *http.ServeMux) {
	t.Helper()
	jobs, err := jobrunner.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Jobs: jobs, Permission: permission})
	if err != nil {
		t.Fatal(err)
	}
	if h != nil {
		s.host = h
	}
	mux := http.NewServeMux()
	s.Register(mux)
	jobs.Register(mux)
	t.Cleanup(func() {
		s.Close()
		if err := jobs.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, jobs, mux
}

func post(t *testing.T, mux *http.ServeMux, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest("POST", url, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, request)
	return w
}

func awaitJob(t *testing.T, mux *http.ServeMux, id string) jobrunner.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/jobs/"+id, nil))
		var body struct {
			Job jobrunner.Task `json:"job"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Job.Status == "succeeded" || body.Job.Status == "failed" || body.Job.Status == "interrupted" {
			return body.Job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return jobrunner.Task{}
}

func acceptedJob(t *testing.T, w *httptest.ResponseRecorder) jobrunner.Task {
	t.Helper()
	if w.Code != 202 {
		t.Fatalf("not accepted: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Job jobrunner.Task `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Job
}

func TestNoPermissionAndWindowsNeverRunHostCommands(t *testing.T) {
	for _, permission := range []func() error{nil, func() error { return errors.New("consent missing") }} {
		h := linuxFixture("apt")
		s, _, mux := setupService(t, h, permission)
		info, err := s.Capabilities(context.Background())
		if err != nil || info.Available || info.Reason == "" {
			t.Fatalf("no authority %+v %v", info, err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/apps", nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		if w := post(t, mux, "/api/apps/nginx/install", `{"confirmed":true}`); w.Code != 403 {
			t.Fatalf("want denied: %d %s", w.Code, w.Body.String())
		}
		if len(h.commands) != 0 || len(h.queries) != 0 {
			t.Fatal("no-permission request ran commands")
		}
	}
	h := linuxFixture("apt")
	h.goos = "windows"
	s, _, mux := setupService(t, h, func() error { return nil })
	info, err := s.Capabilities(context.Background())
	if err != nil || info.Available || !strings.Contains(info.Reason, "not_supported") {
		t.Fatalf("Windows %+v %v", info, err)
	}
	if w := post(t, mux, "/api/apps/nginx/install", `{"confirmed":true}`); w.Code != 501 {
		t.Fatal(w.Code)
	}
	if len(h.queries) != 0 || len(h.commands) != 0 {
		t.Fatal("Windows invoked SCM or package tool")
	}
}

func TestActualPHPRuntimesDebianUbuntuAndRHEL(t *testing.T) {
	for _, test := range []struct {
		manager  string
		versions []string
	}{{"apt", []string{"8.2", "8.3"}}, {"apt", []string{"8.4"}}, {"dnf", []string{"8.2"}}} {
		t.Run(test.manager+strings.Join(test.versions, "-"), func(t *testing.T) {
			h := linuxFixture(test.manager)
			h.packages["nginx"] = "1.24.0-ubuntu"
			h.states["nginx.service"] = "active"
			for _, version := range test.versions {
				h.addPHP(version)
			}
			h.dirs["/usr/lib/systemd/system"] = append(h.dirs["/usr/lib/systemd/system"], fakeEntry("php99-bad.service"), fakeEntry("../../malicious.service"))
			s, _, _ := setupService(t, h, func() error { return nil })
			info, err := s.Capabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !info.Available || !info.NginxInstalled || !info.NginxRunning || info.NginxBinary != "/usr/sbin/nginx" || len(info.PHPRuntimes) != len(test.versions) {
				t.Fatalf("runtime %+v", info)
			}
			for i, version := range test.versions {
				id, socket := "php"+version+"-fpm", "/run/php/php"+version+"-fpm.sock"
				if test.manager == "dnf" {
					id, socket = "php-fpm", "/run/php-fpm/www.sock"
				}
				got := info.PHPRuntimes[i]
				if got.ID != id || got.Unit != id+".service" || got.Socket != socket || got.Version != version+".12-1" {
					t.Fatalf("php %+v", got)
				}
			}
			wantUser := "www-data"
			if test.manager == "dnf" {
				wantUser = "nginx"
			}
			if info.WebUser != wantUser || info.WebGroup != wantUser {
				t.Fatalf("identity %+v", info)
			}
		})
	}
}

func TestRuntimeRejectsUnsafeInactiveAndUnknownSockets(t *testing.T) {
	for _, mode := range []string{"inactive", "socket_missing", "custom_path", "tcp", "root_pool", "root_nginx", "unknown_account", "duplicate_listen"} {
		t.Run(mode, func(t *testing.T) {
			h := linuxFixture("apt")
			h.addPHP("8.2")
			h.packages["nginx"] = "1.22"
			h.states["nginx.service"] = "active"
			config := "/etc/php/8.2/fpm/pool.d/www.conf"
			switch mode {
			case "inactive":
				h.states["php8.2-fpm.service"] = "inactive"
			case "socket_missing":
				delete(h.sockets, "/run/php/php8.2-fpm.sock")
			case "custom_path":
				h.files[config] = strings.ReplaceAll(h.files[config], "/run/php/php8.2-fpm.sock", "/tmp/hostile.sock")
			case "tcp":
				h.files[config] = strings.ReplaceAll(h.files[config], "/run/php/php8.2-fpm.sock", "127.0.0.1:9000")
			case "root_pool":
				h.files[config] = strings.ReplaceAll(h.files[config], "www-data", "root")
			case "root_nginx":
				h.files["/etc/nginx/nginx.conf"] = "user root; events {}"
			case "unknown_account":
				h.files[config] = strings.ReplaceAll(h.files[config], "www-data", "missing-user")
			case "duplicate_listen":
				h.files[config] += "listen = /run/php/php8.2-fpm.sock\n"
			}
			s, _, _ := setupService(t, h, func() error { return nil })
			info, err := s.Capabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "root_nginx" {
				if info.Available || info.WebUser != "" {
					t.Fatalf("unsafe Nginx identity %+v", info)
				}
			} else if len(info.PHPRuntimes) != 0 {
				t.Fatalf("unsafe PHP runtime %+v", info.PHPRuntimes)
			}
		})
	}
}

func TestCacheTenSecondsAndPermissionRevocation(t *testing.T) {
	h := linuxFixture("apt")
	h.addPHP("8.2")
	var allowed atomic.Bool
	allowed.Store(true)
	s, _, _ := setupService(t, h, func() error {
		if allowed.Load() {
			return nil
		}
		return errors.New("revoked")
	})
	first, err := s.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	queries := len(h.queries)
	first.PHPRuntimes[0].ID = "caller-mutation"
	second, err := s.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(h.queries) != queries || second.PHPRuntimes[0].ID == "caller-mutation" {
		t.Fatal("cache did not isolate/copy results")
	}
	s.mu.Lock()
	s.cached.at = time.Now().Add(-11 * time.Second)
	s.mu.Unlock()
	if _, err := s.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.queries) <= queries {
		t.Fatal("10 second cache never refreshed")
	}
	queries = len(h.queries)
	allowed.Store(false)
	info, err := s.Capabilities(context.Background())
	if err != nil || info.Available || len(info.PHPRuntimes) != 0 {
		t.Fatalf("revoked cached runtime %+v %v", info, err)
	}
	if len(h.queries) != queries {
		t.Fatal("revoked read ran a query")
	}
}

func TestInstallAllowlistAPTAndDNF(t *testing.T) {
	for _, manager := range []string{"apt", "dnf"} {
		for _, id := range []string{"nginx", "php-fpm", "mariadb", "redis"} {
			t.Run(manager+"-"+id, func(t *testing.T) {
				h := linuxFixture(manager)
				_, _, mux := setupService(t, h, func() error { return nil })
				queued := acceptedJob(t, post(t, mux, "/api/apps/"+id+"/install", `{"confirmed":true}`))
				job := awaitJob(t, mux, queued.ID)
				if job.Status != "succeeded" {
					t.Fatalf("install result %+v", job)
				}
				h.mu.Lock()
				defer h.mu.Unlock()
				expectedCount := 1
				if manager == "apt" {
					expectedCount = 2
				}
				if len(h.commands) != expectedCount {
					t.Fatalf("command count %d", len(h.commands))
				}
				last := h.commands[len(h.commands)-1]
				pkg := map[string]string{"nginx": "nginx", "php-fpm": "php-fpm", "mariadb": "mariadb-server", "redis": "redis-server"}[id]
				if manager == "dnf" && id == "redis" {
					pkg = "redis"
				}
				if last.args[len(last.args)-1] != pkg || !slicesContain(last.args, "install") || last.remaining > 10*time.Minute || last.remaining < 9*time.Minute {
					t.Fatalf("bad install argv/deadline %+v", last)
				}
				for _, call := range h.commands {
					joined := strings.Join(call.args, " ")
					for _, bad := range []string{"upgrade", "remove", "purge", "allow-unauthenticated", "nogpgcheck", "curl", "wget"} {
						if strings.Contains(joined, bad) {
							t.Fatalf("unsafe argv %s", joined)
						}
					}
				}
				if manager == "apt" {
					first := h.commands[0]
					if !slicesContain(first.args, "update") || first.remaining > 2*time.Minute || !slicesContain(last.args, "--no-install-recommends") || !slicesContain(last.args, "DPkg::Lock::Timeout=30") {
						t.Fatalf("apt bounds %+v %+v", first, last)
					}
				}
				if len(h.queries) < 2 {
					t.Fatal("did not verify package and service state after install")
				}
			})
		}
	}
}

func TestServicesUseActualPHPUnitsAndVerifyPostState(t *testing.T) {
	h := linuxFixture("apt")
	h.addPHP("8.2")
	h.addPHP("8.3")
	_, _, mux := setupService(t, h, func() error { return nil })
	for _, action := range []string{"stop", "start", "restart"} {
		queued := acceptedJob(t, post(t, mux, "/api/apps/php-fpm/"+action, `{"confirmed":true}`))
		job := awaitJob(t, mux, queued.ID)
		if job.Status != "succeeded" {
			t.Fatalf("service %s: %+v", action, job)
		}
		h.mu.Lock()
		last := h.commands[len(h.commands)-1]
		h.mu.Unlock()
		if last.executable != "/usr/bin/systemctl" || !slicesContain(last.args, action) || !slicesContain(last.args, "php8.2-fpm.service") || !slicesContain(last.args, "php8.3-fpm.service") || slicesContain(last.args, "php-fpm.service") {
			t.Fatalf("actual php units %+v", last)
		}
	}
	h.mu.Lock()
	h.apply = false
	h.mu.Unlock()
	queued := acceptedJob(t, post(t, mux, "/api/apps/php-fpm/stop", `{"confirmed":true}`))
	job := awaitJob(t, mux, queued.ID)
	if job.Status != "failed" || !strings.Contains(job.Error, "inactive") {
		t.Fatalf("fake zero-exit stop succeeded %+v", job)
	}
}

func TestPermissionRecheckedAtExecutionAndBetweenCommands(t *testing.T) {
	for _, phase := range []string{"queued", "between"} {
		t.Run(phase, func(t *testing.T) {
			h := linuxFixture("apt")
			var allowed atomic.Bool
			allowed.Store(true)
			s, jobs, mux := setupService(t, h, func() error {
				if allowed.Load() {
					return nil
				}
				return errors.New("revoked")
			})
			var unblock chan struct{}
			if phase == "queued" {
				unblock = make(chan struct{})
				started := make(chan struct{})
				_, err := jobs.Submit("fixture", "block", time.Minute, func(ctx context.Context, p *jobrunner.Progress) error {
					close(started)
					select {
					case <-unblock:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				<-started
			} else {
				h.onCommand = func(call invocation) { allowed.Store(false) }
			}
			queued := acceptedJob(t, post(t, mux, "/api/apps/nginx/install", `{"confirmed":true}`))
			if phase == "queued" {
				allowed.Store(false)
				close(unblock)
			}
			job := awaitJob(t, mux, queued.ID)
			if job.Status != "failed" {
				t.Fatalf("revoked task %+v", job)
			}
			h.mu.Lock()
			count := len(h.commands)
			queries := len(h.queries)
			h.mu.Unlock()
			want := 0
			if phase == "between" {
				want = 1
			}
			if count != want {
				t.Fatalf("executed %d commands after revoke, want %d", count, want)
			}
			if queries != 0 {
				t.Fatal("revoked verification queried packages")
			}
			info, err := s.Capabilities(context.Background())
			if err != nil || info.Available {
				t.Fatalf("revoked %+v %v", info, err)
			}
		})
	}
}

func TestStrictConfirmationAndUnknownInputs(t *testing.T) {
	h := linuxFixture("apt")
	_, _, mux := setupService(t, h, func() error { return nil })
	for _, body := range []string{`{}`, `null`, `{"confirmed":false}`, `{"Confirmed":true}`, `{"confirmed":true,"confirmed":true}`, `{"confirmed":true,"package":"curl"}`, `{"confirmed":true} {}`, strings.Repeat("x", 1025)} {
		w := post(t, mux, "/api/apps/nginx/install", body)
		if w.Code != 400 && w.Code != 413 {
			t.Fatalf("body %q accepted %d", body, w.Code)
		}
	}
	for _, url := range []string{"/api/apps/curl/install", "/api/apps/nginx/uninstall", "/api/apps/nginx/exec", "/api/apps/sh/install"} {
		if w := post(t, mux, url, `{"confirmed":true}`); w.Code != 404 {
			t.Fatalf("unknown route %s: %d", url, w.Code)
		}
	}
	if len(h.commands) != 0 || len(h.queries) != 0 {
		t.Fatal("invalid request reached host commands")
	}
	request := httptest.NewRequest("POST", "/api/apps/nginx/install", strings.NewReader(`{"confirmed":true}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, request)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
}

func TestCommandFailureMissingDependenciesAndNoFakeSuccess(t *testing.T) {
	for _, mode := range []string{"command_failure", "zero_exit_missing", "query_failure", "missing_service"} {
		t.Run(mode, func(t *testing.T) {
			h := linuxFixture("apt")
			switch mode {
			case "command_failure":
				h.commandErr = errors.New("fake apt failure")
			case "zero_exit_missing":
				h.apply = false
			case "query_failure":
				h.queryErr = errors.New("fake query failure")
			case "missing_service":
				h.packages["nginx"] = "1.2"
				h.apply = false
			}
			_, _, mux := setupService(t, h, func() error { return nil })
			queued := acceptedJob(t, post(t, mux, "/api/apps/nginx/install", `{"confirmed":true}`))
			job := awaitJob(t, mux, queued.ID)
			if job.Status != "failed" {
				t.Fatalf("unverified operation reported success %+v", job)
			}
		})
	}
	h := linuxFixture("apt")
	delete(h.binaries, "/usr/bin/systemctl")
	s, _, mux := setupService(t, h, func() error { return nil })
	info, err := s.Capabilities(context.Background())
	if err != nil || info.Available || !strings.Contains(info.Reason, "dependency_unknown") {
		t.Fatalf("missing dependencies %+v %v", info, err)
	}
	if w := post(t, mux, "/api/apps/nginx/install", `{"confirmed":true}`); w.Code != 503 {
		t.Fatal(w.Code)
	}
	if len(h.queries) != 0 || len(h.commands) != 0 {
		t.Fatal("missing dependencies ran commands")
	}
}

func TestRequestCancellationDoesNotCancelAdmittedJob(t *testing.T) {
	h := linuxFixture("apt")
	_, jobs, mux := setupService(t, h, func() error { return nil })
	started, unblock := make(chan struct{}), make(chan struct{})
	_, err := jobs.Submit("fixture", "wait", time.Minute, func(ctx context.Context, p *jobrunner.Progress) error {
		close(started)
		select {
		case <-unblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("POST", "/api/apps/nginx/install", strings.NewReader(`{"confirmed":true}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, request)
	queued := acceptedJob(t, w)
	cancel()
	close(unblock)
	if job := awaitJob(t, mux, queued.ID); job.Status != "succeeded" {
		t.Fatalf("request cancellation leaked into work %+v", job)
	}
}

func TestServiceCloseDoesNotCloseSharedJobs(t *testing.T) {
	h := linuxFixture("apt")
	s, jobs, mux := setupService(t, h, func() error { return nil })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if w := post(t, mux, "/api/apps/nginx/install", `{"confirmed":true}`); w.Code != 403 {
		t.Fatal(w.Code)
	}
	task, err := jobs.Submit("sites.fixture", "shared", time.Second, func(context.Context, *jobrunner.Progress) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if job := awaitJob(t, mux, task.ID); job.Status != "succeeded" {
		t.Fatal(job.Status)
	}
}

func TestRuntimeDiscoveryLimitsAndDefaultSocketCanonicalization(t *testing.T) {
	h := linuxFixture("apt")
	h.addPHP("8.2")
	h.files["/etc/php/8.2/fpm/pool.d/www.conf"] = strings.ReplaceAll(h.files["/etc/php/8.2/fpm/pool.d/www.conf"], "/run/", "/var/run/")
	s, _, _ := setupService(t, h, func() error { return nil })
	info, err := s.Capabilities(context.Background())
	if err != nil || len(info.PHPRuntimes) != 1 || info.PHPRuntimes[0].Socket != "/run/php/php8.2-fpm.sock" {
		t.Fatalf("canonical socket %+v %v", info, err)
	}
	h2 := linuxFixture("apt")
	for i := 0; i < 17; i++ {
		h2.addPHP(fmt.Sprintf("8.%d", i))
	}
	if _, err := discoverPHPUnits(h2, "apt"); err == nil {
		t.Fatal("unbounded PHP runtime enumeration")
	}
	units, err := discoverPHPUnits(h, "apt")
	if err != nil || !sort.StringsAreSorted(units) {
		t.Fatalf("units %v %v", units, err)
	}
}

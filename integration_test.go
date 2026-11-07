package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"panel/internal/auth"
	"panel/internal/buildinfo"
	"panel/internal/machine"
	"panel/internal/management"
	"panel/internal/platform"
	"panel/internal/server"
	"panel/internal/sources"
	"panel/internal/update"
)

type integrationApp struct {
	handler http.Handler
	auth    *auth.Service
	machine *machine.Service
}

func newIntegrationApp(t testing.TB) *integrationApp {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := platform.PreparePrivateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	authService, err := auth.New(auth.Options{DataDir: dir, SessionTTL: 12 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	machineService, err := machine.New(machine.Options{})
	if err != nil {
		_ = authService.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = machineService.Close(); _ = authService.Close() })
	machineService.SetAudit(authService.Record)
	sourceService, err := sources.New(dir, authService.Record)
	if err != nil {
		t.Fatal(err)
	}
	static, err := server.Static(assets)
	if err != nil {
		t.Fatal(err)
	}
	build := buildinfo.Current(version, commit, buildTime, githubRepository)
	updateService, err := update.New(update.Options{Current: build, Audit: authService.Record})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = updateService.Close() })
	hosting, err := openHostingServices(dir, "panel.test", 5173, authService.Record, management.New(), func() error {
		return errors.New("isolated test: no system management consent")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hosting.Close() })
	rootMux, apiMux := http.NewServeMux(), http.NewServeMux()
	authService.Register(rootMux)
	rootMux.HandleFunc("GET /healthz", healthHandler(build))
	machineService.Register(apiMux)
	sourceService.Register(apiMux)
	updateService.Register(apiMux)
	hosting.Register(apiMux)
	rootMux.Handle("/api/", authService.Protect(apiMux))
	rootMux.Handle("/", static)
	handler, err := server.Guard(rootMux, server.Options{Origins: []string{"http://panel.test"}, MaxConcurrent: 24})
	if err != nil {
		t.Fatal(err)
	}
	return &integrationApp{handler: handler, auth: authService, machine: machineService}
}

func integrationRequest(handler http.Handler, method, path string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	var encoded bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&encoded).Encode(body)
	}
	r := httptest.NewRequest(method, "http://panel.test"+path, &encoded)
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://panel.test")
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func initializeIntegration(t testing.TB, app *integrationApp) ([]*http.Cookie, string) {
	t.Helper()
	token, err := os.ReadFile(app.auth.SetupTokenPath())
	if err != nil {
		t.Fatal(err)
	}
	response := integrationRequest(app.handler, "POST", "/api/auth/setup", map[string]string{"token": string(bytes.TrimSpace(token)), "username": "integration-admin", "password": "Integration-Test-Only-76!"}, nil, "")
	if response.Code != 200 && response.Code != 201 {
		t.Fatalf("setup: %d %s", response.Code, response.Body.String())
	}
	var status struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrfToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || !status.Authenticated || status.CSRFToken == "" {
		t.Fatalf("setup session missing: %v", err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie security attributes missing")
	}
	return cookies, status.CSRFToken
}

func TestEndToEndAuthenticatedAPIBoundary(t *testing.T) {
	app := newIntegrationApp(t)
	for _, path := range []string{"/api/system", "/api/files", "/api/docker", "/api/sources", "/api/tasks", "/api/account"} {
		response := integrationRequest(app.handler, "GET", path, nil, nil, "")
		if response.Code != 401 {
			t.Fatalf("unprotected %s: %d", path, response.Code)
		}
	}
	cookies, csrf := initializeIntegration(t, app)
	for _, path := range []string{"/api/system", "/api/capabilities", "/api/docker", "/api/sources", "/api/tasks", "/api/account", "/api/account/sessions", "/api/account/audit"} {
		response := integrationRequest(app.handler, "GET", path, nil, cookies, "")
		if response.Code != 200 || !json.Valid(response.Body.Bytes()) {
			t.Fatalf("authenticated %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	sourceResponse := integrationRequest(app.handler, "GET", "/api/sources", nil, cookies, "")
	var saved struct {
		Revision uint64         `json:"revision"`
		Config   sources.Config `json:"config"`
	}
	if err := json.Unmarshal(sourceResponse.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	saved.Config.APT.Provider = "tuna"
	write := map[string]any{"revision": saved.Revision, "config": saved.Config}
	withoutCSRF := integrationRequest(app.handler, "PUT", "/api/sources", write, cookies, "")
	if withoutCSRF.Code != 403 {
		t.Fatalf("write without CSRF accepted: %d", withoutCSRF.Code)
	}
	withCSRF := integrationRequest(app.handler, "PUT", "/api/sources", write, cookies, csrf)
	if withCSRF.Code != 200 {
		t.Fatalf("source save: %d %s", withCSRF.Code, withCSRF.Body.String())
	}
	logout := integrationRequest(app.handler, "POST", "/api/auth/logout", map[string]any{}, cookies, csrf)
	if logout.Code != 200 {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body.String())
	}
	if response := integrationRequest(app.handler, "GET", "/api/system", nil, cookies, ""); response.Code != 401 {
		t.Fatal("session remained valid after logout")
	}
}

func TestUpdateAPIAndHealthBoundary(t *testing.T) {
	app := newIntegrationApp(t)
	health := integrationRequest(app.handler, "GET", "/healthz", nil, nil, "")
	var result map[string]any
	if health.Code != 200 || json.Unmarshal(health.Body.Bytes(), &result) != nil || len(result) != 3 || result["status"] != "ok" || result["version"] != version {
		t.Fatalf("invalid minimal health response: %s", health.Body.String())
	}
	if unauthenticated := integrationRequest(app.handler, "GET", "/api/update", nil, nil, ""); unauthenticated.Code != 401 {
		t.Fatal("update information leaked without authentication")
	}
	cookies, csrf := initializeIntegration(t, app)
	if unprotected := integrationRequest(app.handler, "POST", "/api/update/check", map[string]any{}, cookies, ""); unprotected.Code != 403 {
		t.Fatal("manual network check accepted without CSRF")
	}
	check := integrationRequest(app.handler, "POST", "/api/update/check", map[string]any{}, cookies, csrf)
	var status update.Status
	if check.Code != 200 || json.Unmarshal(check.Body.Bytes(), &status) != nil || status.State != "unconfigured" || status.CanApply {
		t.Fatalf("unconfigured update: %d %s", check.Code, check.Body.String())
	}
}

func TestBoundedConcurrentSnapshotReads(t *testing.T) {
	app := newIntegrationApp(t)
	cookies, _ := initializeIntegration(t, app)
	var wg sync.WaitGroup
	results := make(chan int, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				response := integrationRequest(app.handler, "GET", "/api/system", nil, cookies, "")
				if response.Code != 200 || !json.Valid(response.Body.Bytes()) {
					results <- response.Code
					return
				}
			}
		}()
	}
	wg.Wait()
	close(results)
	for status := range results {
		t.Errorf("bounded snapshot read failed: %d", status)
	}
}

func TestEmbeddedAssetsDoNotExposeRuntimeData(t *testing.T) {
	app := newIntegrationApp(t)
	for _, path := range []string{"/", "/index.html", "/styles.css", "/api.js", "/app.js"} {
		w := integrationRequest(app.handler, "GET", path, nil, nil, "")
		if w.Code != 200 {
			t.Fatalf("missing static asset %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/account.json", "/bootstrap.token", "/main.go", "/.panel.lock", "/README.md", "/sources.json"} {
		w := integrationRequest(app.handler, "GET", path, nil, nil, "")
		if w.Code != 404 {
			t.Errorf("private file route %s: %d", path, w.Code)
		}
	}
}

func BenchmarkAuthenticatedSystemSnapshot(b *testing.B) {
	app := newIntegrationApp(b)
	cookies, _ := initializeIntegration(b, app)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := integrationRequest(app.handler, "GET", "/api/system", nil, cookies, "")
		if w.Code != 200 {
			b.Fatal(w.Code)
		}
		_, _ = io.Copy(io.Discard, w.Result().Body)
	}
}

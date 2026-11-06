package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPassword = "correct-horse-battery-123!"

var (
	fixtureOnce     sync.Once
	fixturePassword passwordRecord
	fixtureError    error
)

type testClient struct {
	Cookie *http.Cookie
	CSRF   string
}

type testHarness struct {
	t   *testing.T
	s   *Service
	mux *http.ServeMux
	now *time.Time
}

func newHarness(t *testing.T, initialized bool) *testHarness {
	t.Helper()
	return harnessIn(t, t.TempDir(), initialized)
}

func harnessIn(t *testing.T, dir string, initialized bool) *testHarness {
	t.Helper()
	s, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if initialized && s.account == nil {
		fixtureOnce.Do(func() { fixturePassword, fixtureError = newPassword(testPassword) })
		if fixtureError != nil {
			t.Fatal(fixtureError)
		}
		candidate := accountRecord{Version: 1, Username: "admin", Password: fixturePassword, LastTOTPStep: -1}
		if err := s.saveAccountLocked(cloneAccount(&candidate)); err != nil {
			t.Fatal(err)
		}
		s.credentialVersion = 1
		s.bootstrapDigest = [32]byte{}
		if err := os.Remove(s.tokenPath); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	s.Register(mux)
	mux.Handle("/api/private", s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})))
	t.Cleanup(func() { _ = s.Close() })
	return &testHarness{t: t, s: s, mux: mux, now: &now}
}

func (h *testHarness) request(method, path, body string, client *testClient) *httptest.ResponseRecorder {
	h.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.5:12345"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if client != nil {
		r.AddCookie(client.Cookie)
		if client.CSRF != "" {
			r.Header.Set("X-CSRF-Token", client.CSRF)
		}
	}
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	return w
}

func encode(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func decode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	return value
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		// Never print successful bodies: an unexpected response can include a
		// bootstrap session, enrollment secret or one-time recovery codes.
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &failure)
		t.Fatalf("status = %d, want %d (error code %q)", response.Code, status, failure.Error.Code)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatal("response did not use JSON content type")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("sensitive response is cacheable")
	}
	if status >= 400 {
		failure := decode[struct {
			Error struct{ Code, Message string } `json:"error"`
		}](t, response)
		if failure.Error.Code == "" || failure.Error.Message == "" {
			t.Fatal("missing stable JSON error")
		}
	}
}

func responseClient(t *testing.T, response *httptest.ResponseRecorder) *testClient {
	t.Helper()
	requireStatus(t, response, http.StatusOK)
	status := decode[statusResponse](t, response)
	if !status.Authenticated || !status.Initialized || status.CSRFToken == "" || status.User == nil {
		t.Fatal("missing authenticated status")
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName || !cookies[0].HttpOnly ||
		cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" || cookies[0].Domain != "" ||
		!validRandomToken(cookies[0].Value, 32) {
		t.Fatal("invalid session cookie")
	}
	if bytes.Contains(response.Body.Bytes(), []byte(cookies[0].Value)) {
		t.Fatal("bearer token appeared in JSON")
	}
	return &testClient{Cookie: cookies[0], CSRF: status.CSRFToken}
}

func (h *testHarness) login(username, password, code string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.request(http.MethodPost, "/api/auth/login", encode(h.t, map[string]string{
		"username": username, "password": password, "code": code,
	}), nil)
}

func (h *testHarness) issue(remote string) *testClient {
	h.t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	r.Header.Set("X-Forwarded-For", "203.0.113.91")
	r.Header.Set("Forwarded", "for=203.0.113.92")
	r.Header.Set("User-Agent", "unit-agent\n\x00test")
	bearer, sess, err := prepareSession(r, *h.now, h.s.ttl)
	if err != nil {
		h.t.Fatal(err)
	}
	h.s.mu.Lock()
	h.s.addSessionLocked(bearer, sess)
	h.s.mu.Unlock()
	return &testClient{Cookie: &http.Cookie{Name: cookieName, Value: bearer}, CSRF: sess.CSRF}
}

func readSetupToken(t *testing.T, s *Service) string {
	t.Helper()
	path := s.SetupTokenPath()
	if path == "" {
		t.Fatal("missing setup token path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := strings.TrimSuffix(string(data), "\n")
	if !validRandomToken(value, 32) {
		t.Fatal("invalid bootstrap entropy/format")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("setup credential is not mode 0600")
		}
	}
	return value
}

func TestSetupRequiresBootstrapAndPersists(t *testing.T) {
	h := newHarness(t, false)
	token := readSetupToken(t, h.s)
	status := h.request(http.MethodGet, "/api/auth/status", "", nil)
	requireStatus(t, status, http.StatusOK)
	initial := decode[statusResponse](t, status)
	if initial.Initialized || initial.Authenticated || initial.CSRFToken != "" || initial.User != nil || len(status.Result().Cookies()) != 0 {
		t.Fatal("status created authority before initialization")
	}
	if strings.Contains(status.Body.String(), token) {
		t.Fatal("status exposed setup credential")
	}
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", nil), http.StatusUnauthorized)
	for _, wrong := range []string{"", strings.Repeat("A", len(token))} {
		result := h.request(http.MethodPost, "/api/auth/setup", encode(t, map[string]string{
			"token": wrong, "username": "intruder", "password": testPassword,
		}), nil)
		requireStatus(t, result, http.StatusUnauthorized)
	}
	if h.s.account != nil || len(h.s.sessions) != 0 {
		t.Fatal("unauthorized caller initialized the account")
	}
	if _, err := os.Stat(h.s.accountPath); !os.IsNotExist(err) {
		t.Fatal("unauthorized setup wrote account state")
	}
	for _, password := range []string{"short", strings.Repeat("x", 129)} {
		result := h.request(http.MethodPost, "/api/auth/setup", encode(t, map[string]string{
			"token": token, "username": "admin", "password": password,
		}), nil)
		requireStatus(t, result, http.StatusBadRequest)
	}
	result := h.request(http.MethodPost, "/api/auth/setup", encode(t, map[string]string{
		"token": token, "username": "admin", "password": testPassword,
	}), nil)
	client := responseClient(t, result)
	if h.s.SetupTokenPath() != "" {
		t.Fatal("initialized service still advertises bootstrap token")
	}
	if _, err := os.Stat(h.s.tokenPath); !os.IsNotExist(err) {
		t.Fatal("bootstrap token was not removed")
	}
	requireStatus(t, h.request(http.MethodPost, "/api/auth/setup", encode(t, map[string]string{
		"token": token, "username": "second_admin", "password": testPassword,
	}), nil), http.StatusConflict)
	data, err := os.ReadFile(h.s.accountPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(testPassword)) || bytes.Contains(data, []byte(token)) {
		t.Fatal("account file contains plaintext password/bootstrap token")
	}
	if h.s.account.Password.Iterations != 600000 || len(h.s.account.Password.Salt) != passwordSaltBytes || len(h.s.account.Password.Hash) != passwordHashBytes {
		t.Fatal("inadequate password record")
	}
	dir := h.s.dataDir
	if err := h.s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := harnessIn(t, dir, false)
	if restarted.s.SetupTokenPath() != "" || restarted.s.account.Username != "admin" {
		t.Fatal("restart lost initialized account")
	}
	requireStatus(t, restarted.request(http.MethodGet, "/api/account", "", client), http.StatusUnauthorized)
	responseClient(t, restarted.login("admin", testPassword, ""))
}

func TestBootstrapSurvivesRestartAndSetupIsSingleWinner(t *testing.T) {
	h := newHarness(t, false)
	token := readSetupToken(t, h.s)
	dir := h.s.dataDir
	if err := h.s.Close(); err != nil {
		t.Fatal(err)
	}
	h = harnessIn(t, dir, false)
	if readSetupToken(t, h.s) != token {
		t.Fatal("restart silently replaced the setup credential")
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for _, username := range []string{"first_admin", "second_admin"} {
		body := encode(t, map[string]string{"token": token, "username": username, "password": testPassword})
		go func() {
			<-start
			r := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.RemoteAddr = "192.0.2.10:2000"
			w := httptest.NewRecorder()
			h.mux.ServeHTTP(w, r)
			results <- w
		}()
	}
	close(start)
	counts := map[int]int{}
	for range 2 {
		select {
		case result := <-results:
			counts[result.Code]++
		case <-time.After(20 * time.Second):
			t.Fatal("concurrent setup did not complete")
		}
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 || len(h.s.sessions) != 1 {
		t.Fatalf("setup was not single-winner: statuses %v", counts)
	}
}

func TestCSRFSessionIDsRevocationAndRealRemoteAddress(t *testing.T) {
	h := newHarness(t, true)
	first := h.issue("[2001:db8::9]:3456")
	second := h.issue("192.0.2.60:9000")
	requireStatus(t, h.request(http.MethodPost, "/api/private", "{}", nil), http.StatusUnauthorized)
	missingCSRF := &testClient{Cookie: first.Cookie}
	wrongCSRF := &testClient{Cookie: first.Cookie, CSRF: "not-the-token"}
	requireStatus(t, h.request(http.MethodPost, "/api/private", "{}", missingCSRF), http.StatusForbidden)
	requireStatus(t, h.request(http.MethodPatch, "/api/private", "{}", wrongCSRF), http.StatusForbidden)
	requireStatus(t, h.request(http.MethodPost, "/api/private", "{}", first), http.StatusOK)
	requireStatus(t, h.request(http.MethodGet, "/api/account", "", first), http.StatusOK)

	r := httptest.NewRequest(http.MethodPost, "/api/private", strings.NewReader("{}"))
	r.AddCookie(first.Cookie)
	r.Header.Add("X-CSRF-Token", first.CSRF)
	r.Header.Add("X-CSRF-Token", first.CSRF)
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	requireStatus(t, w, http.StatusForbidden)
	r = httptest.NewRequest(http.MethodGet, "/api/private", nil)
	r.AddCookie(first.Cookie)
	r.AddCookie(first.Cookie)
	w = httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	requireStatus(t, w, http.StatusUnauthorized)

	result := h.request(http.MethodGet, "/api/account/sessions", "", first)
	requireStatus(t, result, http.StatusOK)
	sessions := decode[struct {
		Sessions []sessionView `json:"sessions"`
	}](t, result).Sessions
	if len(sessions) != 2 {
		t.Fatal("session list did not contain two sessions")
	}
	var currentID, otherID string
	for _, sess := range sessions {
		if sess.ID == first.Cookie.Value || sess.ID == second.Cookie.Value || sess.ID == first.CSRF || sess.ID == second.CSRF {
			t.Fatal("public session ID is a credential")
		}
		if strings.ContainsAny(sess.UserAgent, "\r\n\x00") || len(sess.UserAgent) > 160 {
			t.Fatal("unbounded or unsafe user agent")
		}
		if sess.Current {
			currentID = sess.ID
			if sess.IP != "2001:db8::9" {
				t.Fatal("session trusted forwarded IP instead of RemoteAddr")
			}
		} else {
			otherID = sess.ID
		}
	}
	if currentID == "" || otherID == "" || strings.Contains(result.Body.String(), first.Cookie.Value) || strings.Contains(result.Body.String(), first.CSRF) {
		t.Fatal("unsafe session metadata")
	}
	publicIDClient := &testClient{Cookie: &http.Cookie{Name: cookieName, Value: currentID}, CSRF: first.CSRF}
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", publicIDClient), http.StatusUnauthorized)
	result = h.request(http.MethodDelete, "/api/account/sessions/"+otherID, "", first)
	requireStatus(t, result, http.StatusOK)
	if decode[struct {
		Reauthenticate bool `json:"reauthenticate"`
	}](t, result).Reauthenticate {
		t.Fatal("deleting another session revoked current session")
	}
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", second), http.StatusUnauthorized)
	result = h.request(http.MethodDelete, "/api/account/sessions/"+currentID, "{}", first)
	requireStatus(t, result, http.StatusOK)
	if !decode[struct {
		Reauthenticate bool `json:"reauthenticate"`
	}](t, result).Reauthenticate {
		t.Fatal("deleting current session did not require reauthentication")
	}
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", first), http.StatusUnauthorized)

	third := h.issue("192.0.2.88:2000")
	logout := h.request(http.MethodPost, "/api/auth/logout", "{}", third)
	requireStatus(t, logout, http.StatusOK)
	if cookies := logout.Result().Cookies(); len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("logout did not clear the session cookie")
	}
	requireStatus(t, h.request(http.MethodGet, "/api/account", "", third), http.StatusUnauthorized)
}

func TestCredentialChangesReauthenticateRevokeAndPersist(t *testing.T) {
	h := newHarness(t, true)
	first := responseClient(t, h.login("admin", testPassword, ""))
	second := responseClient(t, h.login("admin", testPassword, ""))
	originalSalt := append([]byte(nil), h.s.account.Password.Salt...)
	bad := h.request(http.MethodPatch, "/api/account", encode(t, map[string]string{
		"currentPassword": "wrong-current-password", "username": "renamed_admin",
	}), first)
	requireStatus(t, bad, http.StatusUnauthorized)
	if h.s.account.Username != "admin" {
		t.Fatal("failed reauthentication changed account")
	}
	requireStatus(t, h.request(http.MethodGet, "/api/account", "", second), http.StatusOK)
	const replacement = "new-credential-value-456!"
	changed := h.request(http.MethodPatch, "/api/account", encode(t, map[string]string{
		"currentPassword": testPassword, "username": "renamed_admin", "newPassword": replacement,
	}), first)
	requireStatus(t, changed, http.StatusOK)
	if !decode[struct {
		Reauthenticate bool `json:"reauthenticate"`
	}](t, changed).Reauthenticate {
		t.Fatal("credential change did not force reauthentication")
	}
	if bytes.Equal(originalSalt, h.s.account.Password.Salt) || len(h.s.sessions) != 0 {
		t.Fatal("password salt was reused or sessions survived credential change")
	}
	for _, client := range []*testClient{first, second} {
		requireStatus(t, h.request(http.MethodGet, "/api/private", "", client), http.StatusUnauthorized)
	}
	wrongName := h.login("admin", replacement, "")
	wrongPassword := h.login("renamed_admin", testPassword, "")
	requireStatus(t, wrongName, http.StatusUnauthorized)
	requireStatus(t, wrongPassword, http.StatusUnauthorized)
	if wrongName.Body.String() != wrongPassword.Body.String() {
		t.Fatal("login revealed which credential was incorrect")
	}
	fresh := responseClient(t, h.login("renamed_admin", replacement, ""))
	dir := h.s.dataDir
	if err := h.s.Close(); err != nil {
		t.Fatal(err)
	}
	h = harnessIn(t, dir, false)
	requireStatus(t, h.request(http.MethodGet, "/api/account", "", fresh), http.StatusUnauthorized)
	responseClient(t, h.login("renamed_admin", replacement, ""))
}

func TestCookieSecurePolicyIgnoresForwardedHeaders(t *testing.T) {
	h := newHarness(t, true)
	for _, tc := range []struct {
		name         string
		secureOption bool
		target       string
		wantSecure   bool
	}{
		{"plain-http", false, "http://example.test/", false},
		{"tls", false, "https://example.test/", true},
		{"explicit-secure", true, "http://example.test/", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.s.secureCookies = tc.secureOption
			r := httptest.NewRequest(http.MethodGet, tc.target, nil)
			r.Header.Set("X-Forwarded-Proto", "https")
			w := httptest.NewRecorder()
			h.s.setCookie(w, r, "test-cookie", h.now.Add(time.Hour))
			cookie := w.Result().Cookies()[0]
			if cookie.Secure != tc.wantSecure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("incorrect secure cookie policy")
			}
		})
	}
}

func TestKDFAndAttemptAdmissionAreBoundedAndNonblocking(t *testing.T) {
	h := newHarness(t, true)
	client := h.issue("192.0.2.5:12345")
	if cap(h.s.kdf) != 2 || h.s.acquireKDF() != nil || h.s.acquireKDF() != nil {
		t.Fatal("KDF concurrency is not exactly two")
	}
	if h.s.acquireKDF() != kdfBusy {
		t.Fatal("KDF admission was not bounded")
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"correct-horse-battery-123!"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "192.0.2.200:3000"
		w := httptest.NewRecorder()
		h.mux.ServeHTTP(w, r)
		done <- w
	}()
	select {
	case result := <-done:
		requireStatus(t, result, http.StatusTooManyRequests)
	case <-time.After(2 * time.Second):
		t.Fatal("saturated KDF queued instead of failing immediately")
	}
	requireStatus(t, h.request(http.MethodGet, "/api/auth/status", "", nil), http.StatusOK)
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", client), http.StatusOK)
	h.s.releaseKDF()
	h.s.releaseKDF()

	h.s.attempts = make(map[string]attemptWindow)
	h.s.globalAttempts = attemptWindow{}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "192.0.2.100:1234"
	for range attemptsPerIP {
		if failure := h.s.admitAttempt(r); failure != nil {
			t.Fatal("per-IP allowance ended early")
		}
	}
	r.Header.Set("X-Forwarded-For", "192.0.2.101")
	if h.s.admitAttempt(r) != rateLimited {
		t.Fatal("forwarded address bypassed attempt limit")
	}
	*h.now = h.now.Add(attemptWindowTTL)
	if h.s.admitAttempt(r) != nil {
		t.Fatal("expired attempt window was not reset")
	}
	h.s.attempts = make(map[string]attemptWindow)
	for i := range maxAttemptIPs {
		h.s.attempts[time.Unix(int64(i), 0).String()] = attemptWindow{Start: *h.now}
	}
	h.s.globalAttempts = attemptWindow{Start: *h.now}
	if h.s.admitAttempt(r) != rateLimited || len(h.s.attempts) != maxAttemptIPs {
		t.Fatal("full limiter table grew or evicted entries")
	}
	h.s.attempts = make(map[string]attemptWindow)
	h.s.globalAttempts = attemptWindow{Start: *h.now, Count: attemptsGlobal - 1}
	if h.s.admitAttempt(r) != nil {
		t.Fatal("global allowance ended early")
	}
	r.RemoteAddr = "192.0.2.102:1111"
	if h.s.admitAttempt(r) != rateLimited {
		t.Fatal("global attempt cap was bypassed")
	}
}

func TestSessionsHaveCountIdleAndFixedLifetimeBounds(t *testing.T) {
	h := newHarness(t, true)
	h.s.ttl = time.Hour
	oldest := h.issue("192.0.2.1:1000")
	for range maxSessions {
		*h.now = h.now.Add(time.Millisecond)
		h.issue("192.0.2.1:1000")
	}
	if len(h.s.sessions) != maxSessions {
		t.Fatal("session count is unbounded")
	}
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", oldest), http.StatusUnauthorized)
	h.s.mu.Lock()
	h.s.clearSessionsLocked()
	h.s.mu.Unlock()
	fresh := h.issue("192.0.2.1:1000")
	start := *h.now
	for _, elapsed := range []time.Duration{29 * time.Minute, 58 * time.Minute} {
		*h.now = start.Add(elapsed)
		requireStatus(t, h.request(http.MethodGet, "/api/private", "", fresh), http.StatusOK)
	}
	*h.now = start.Add(time.Hour)
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", fresh), http.StatusUnauthorized)
	idle := h.issue("192.0.2.1:1000")
	*h.now = h.now.Add(maxIdleTTL)
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", idle), http.StatusUnauthorized)
	if len(h.s.sessions) != 0 {
		t.Fatal("expired sessions were not pruned")
	}
	if err := h.s.Close(); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, h.request(http.MethodGet, "/api/auth/status", "", nil), http.StatusServiceUnavailable)
	if err := h.s.Close(); err != nil {
		t.Fatal("Close was not idempotent")
	}
}

func TestStrictJSONAndBodyLimits(t *testing.T) {
	h := newHarness(t, false)
	for name, body := range map[string]string{
		"unknown":           `{"username":"admin","password":"long-enough-password","extra":true}`,
		"duplicate":         `{"username":"admin","username":"other","password":"long-enough-password"}`,
		"alias-duplicate":   `{"username":"admin","Username":"other","password":"long-enough-password"}`,
		"wrong-case":        `{"Username":"admin","password":"long-enough-password"}`,
		"unpaired-high":     `{"username":"admin","password":"long-enough-\ud800"}`,
		"unpaired-low":      `{"username":"admin","password":"long-enough-\udc00"}`,
		"escaped-duplicate": `{"username":"admin","user\u006eame":"other"}`,
		"trailing":          `{"username":"admin","password":"long-enough-password"} {}`,
		"null":              `null`,
		"array":             `[]`,
		"null-field":        `{"username":null,"password":"long-enough-password"}`,
		"wrong-type":        `{"username":123,"password":"long-enough-password"}`,
		"malformed":         `{`,
		"utf8":              "{\"username\":\"\xff\"}",
		"depth":             `{"x":` + strings.Repeat(`[`, 16) + `1` + strings.Repeat(`]`, 16) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			requireStatus(t, h.request(http.MethodPost, "/api/auth/login", body, nil), http.StatusBadRequest)
		})
	}
	oversize := `{"username":"admin","password":"` + strings.Repeat("x", maxBodyBytes) + `"}`
	requireStatus(t, h.request(http.MethodPost, "/api/auth/login", oversize, nil), http.StatusRequestEntityTooLarge)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(oversize))
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = -1
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	requireStatus(t, w, http.StatusRequestEntityTooLarge)
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		r = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", contentType)
		w = httptest.NewRecorder()
		h.mux.ServeHTTP(w, r)
		requireStatus(t, w, http.StatusUnsupportedMediaType)
	}
	r = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Content-Encoding", "gzip")
	w = httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	requireStatus(t, w, http.StatusUnsupportedMediaType)
	requireStatus(t, h.request(http.MethodGet, "/api/auth/login", "", nil), http.StatusMethodNotAllowed)
	if len(h.s.kdf) != 0 || len(h.s.attempts) != 0 {
		t.Fatal("invalid JSON entered credential verification")
	}
}

func TestMalformedPersistenceFailsClosedAndPrivateAtomicWrites(t *testing.T) {
	h := newHarness(t, true)
	candidate := cloneAccount(h.s.account)
	for name, mutate := range map[string]func(*accountRecord){
		"version":               func(a *accountRecord) { a.Version = 2 },
		"username":              func(a *accountRecord) { a.Username = "x" },
		"iterations":            func(a *accountRecord) { a.Password.Iterations++ },
		"salt":                  func(a *accountRecord) { a.Password.Salt = []byte("short") },
		"hash":                  func(a *accountRecord) { a.Password.Hash = nil },
		"disabled-replay-state": func(a *accountRecord) { a.LastTOTPStep = 1 },
		"invalid-secret":        func(a *accountRecord) { a.TOTPSecret = []byte("short"); a.LastTOTPStep = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			copy := cloneAccount(&candidate)
			mutate(&copy)
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(encode(t, copy)), 0600); err != nil {
				t.Fatal(err)
			}
			if s, err := New(Options{DataDir: dir}); err == nil {
				_ = s.Close()
				t.Fatal("malformed account state was accepted")
			}
		})
	}
	for _, body := range []string{`{`, `null`, `{}`, `{"version":1,"version":1}`, strings.Repeat("x", maxStateBytes+1)} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if s, err := New(Options{DataDir: dir}); err == nil {
			_ = s.Close()
			t.Fatal("invalid account file was reset or accepted")
		}
		if _, err := os.Stat(filepath.Join(dir, "setup-token")); !os.IsNotExist(err) {
			t.Fatal("malformed persistence allowed re-initialization")
		}
	}
	statePath := filepath.Join(t.TempDir(), "private.json")
	if err := atomicJSON(statePath, map[string]string{"version": "old"}, 1024); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(statePath)
	if err := atomicJSON(statePath, map[string]string{"version": strings.Repeat("x", 1024)}, 1024); err == nil {
		t.Fatal("oversized state write succeeded")
	}
	after, _ := os.ReadFile(statePath)
	if !bytes.Equal(before, after) {
		t.Fatal("failed write modified old state")
	}
	if err := atomicJSON(statePath, map[string]string{"version": "new"}, 1024); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(statePath), ".auth-write-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary secret files were left behind")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(statePath)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("atomic state is not mode 0600")
		}
		if err := os.Chmod(statePath, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := readPrivateFile(statePath, 1024); err == nil {
			t.Fatal("world-readable secrets were accepted")
		}
	}
}

func TestAuditIsBoundedRotatedAndSafe(t *testing.T) {
	h := newHarness(t, true)
	client := h.issue("192.0.2.5:3000")
	for i := range maxAuditEvents + 12 {
		*h.now = h.now.Add(time.Millisecond)
		h.s.Record("machine.test\n\x00", strings.Repeat("记录\n", 100))
		if i == 0 && h.s.auditError != nil {
			t.Fatal(h.s.auditError)
		}
	}
	if h.s.auditError != nil || len(h.s.auditEvents) != maxAuditEvents || len(h.s.auditSegment) > auditSegmentEvents {
		t.Fatal("audit buffers are unbounded or failed to persist")
	}
	result := h.request(http.MethodGet, "/api/account/audit", "", client)
	requireStatus(t, result, http.StatusOK)
	events := decode[struct {
		Events []auditEvent `json:"events"`
	}](t, result).Events
	if len(events) != maxAuditEvents || !events[0].Time.After(events[len(events)-1].Time) {
		t.Fatal("audit is not bounded latest-first")
	}
	for _, event := range events {
		if !validAuditEvent(event) || strings.ContainsAny(event.Detail, "\n\x00") {
			t.Fatal("unsafe audit text")
		}
	}
	for index := 0; index <= auditBackups; index++ {
		info, err := os.Stat(h.s.auditPath(index))
		if err != nil || info.Size() > maxAuditBytes {
			t.Fatal("missing or unbounded audit rotation")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("audit is not owner-only")
		}
	}
	dir := h.s.dataDir
	if err := h.s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := harnessIn(t, dir, false)
	if len(restarted.s.auditEvents) == 0 || len(restarted.s.auditEvents) > maxAuditEvents {
		t.Fatal("audit persistence failed or loaded unbounded records")
	}
	for _, path := range []string{restarted.s.auditPath(0), restarted.s.accountPath} {
		data, err := os.ReadFile(path)
		if err != nil || bytes.Contains(data, []byte(testPassword)) || bytes.Contains(data, []byte(client.Cookie.Value)) || bytes.Contains(data, []byte(client.CSRF)) {
			t.Fatal("credential leaked to persistent audit/account text")
		}
	}
}

func TestInvalidAuditAndTokenStateAreRejected(t *testing.T) {
	for _, name := range []string{"auth-audit.json", "setup-token"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("broken-state"), 0600); err != nil {
			t.Fatal(err)
		}
		if s, err := New(Options{DataDir: dir}); err == nil {
			_ = s.Close()
			t.Fatal("malformed private state was accepted")
		}
	}
}

func TestReadBodyNeverReadsBeyondBound(t *testing.T) {
	count := 0
	reader := &countedReader{reader: strings.NewReader(strings.Repeat("x", maxBodyBytes*3)), count: &count}
	r := httptest.NewRequest(http.MethodPost, "/", reader)
	r.Header.Set("Content-Type", "application/json")
	r.ContentLength = -1
	w := httptest.NewRecorder()
	var target struct{}
	if readBody(w, r, &target) {
		t.Fatal("oversize body was accepted")
	}
	if count > maxBodyBytes+1 {
		t.Fatal("body reader exceeded its limit")
	}
	requireStatus(t, w, http.StatusRequestEntityTooLarge)
}

type countedReader struct {
	reader io.Reader
	count  *int
}

func (r *countedReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	*r.count += n
	return n, err
}

func TestUninitializedTokenDigestIsNotBearer(t *testing.T) {
	h := newHarness(t, false)
	token := readSetupToken(t, h.s)
	if h.s.bootstrapDigest != sha256.Sum256([]byte(token)) {
		t.Fatal("bootstrap secret is not retained as a digest")
	}
	client := &testClient{Cookie: &http.Cookie{Name: cookieName, Value: token}, CSRF: token}
	requireStatus(t, h.request(http.MethodGet, "/api/private", "", client), http.StatusUnauthorized)
}

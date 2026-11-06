package auth

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStrictJSONPreservesUnicodeAndCredentialBytes(t *testing.T) {
	for _, body := range []string{
		`{"value":"escaped \\"}`,
		`{"value":"quote \" and slash \\ and unicode \ud83d\ude00"}`,
		`{"value":"literal unicode 中文"}`,
		`{"value":"literal backslash \\ud800"}`,
		`{"value":"\u0000\u2028\ud800\udc00"}`,
	} {
		var result struct {
			Value string `json:"value"`
		}
		if err := strictJSON([]byte(body), &result); err != nil {
			t.Fatal("valid credential bytes were rejected")
		}
	}
	for _, body := range []string{
		`{"value":"\ud800"}`, `{"value":"\udc00"}`, `{"value":"\ud800\ud800"}`,
		`{"value":"\ud800x\udc00"}`, `{"value":"\ud800\u0000"}`, `{"Value":"wrong-case"}`,
	} {
		var result struct {
			Value string `json:"value"`
		}
		if err := strictJSON([]byte(body), &result); err == nil {
			t.Fatal("invalid JSON credential representation was accepted")
		}
	}
}

func TestRevocationFencesVerifiedInFlightSecurityChanges(t *testing.T) {
	h := newHarness(t, true)
	client := h.issue("192.0.2.5:1000")
	var captured *http.Request
	capture := h.s.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { captured = r }))
	r, err := http.NewRequest(http.MethodPatch, "http://panel.test/api/account", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(client.Cookie)
	r.Header.Set("X-CSRF-Token", client.CSRF)
	capture.ServeHTTP(newDiscardResponseWriter(), r)
	if captured == nil {
		t.Fatal("session did not authenticate")
	}
	version, failure := h.s.recheckPassword(captured, testPassword)
	if failure != nil {
		t.Fatal("password verification failed")
	}
	h.s.mu.Lock()
	h.s.credentialVersion++
	if _, failure := h.s.securitySessionLocked(captured, version); failure != unauthorized {
		h.s.mu.Unlock()
		t.Fatal("credential version change did not fence in-flight authorization")
	}
	h.s.credentialVersion = version
	h.s.clearSessionsLocked()
	if _, failure := h.s.securitySessionLocked(captured, version); failure != unauthorized {
		h.s.mu.Unlock()
		t.Fatal("session revocation did not fence in-flight authorization")
	}
	h.s.mu.Unlock()
}

type discardResponseWriter struct{ header http.Header }

func newDiscardResponseWriter() *discardResponseWriter {
	return &discardResponseWriter{header: make(http.Header)}
}
func (w *discardResponseWriter) Header() http.Header             { return w.header }
func (w *discardResponseWriter) Write(value []byte) (int, error) { return len(value), nil }
func (w *discardResponseWriter) WriteHeader(int)                 {}

func TestAuditWorstCaseEscapingStillFitsAndRotate(t *testing.T) {
	h := newHarness(t, true)
	for range auditSegmentEvents + 1 {
		h.s.Record(strings.Repeat("a", 64), strings.Repeat("<\\", 128))
	}
	if h.s.auditError != nil {
		t.Fatal(h.s.auditError)
	}
	for index := 0; index <= 1; index++ {
		info, err := os.Stat(h.s.auditPath(index))
		if err != nil || info.Size() > maxAuditBytes {
			t.Fatal("valid audit data exceeded disk segment bound")
		}
	}
}

func TestNewRejectsInvalidOptionsAndSymlinkTargets(t *testing.T) {
	for _, options := range []Options{
		{}, {DataDir: tPrivateDir(t), SessionTTL: -time.Second},
		{DataDir: tPrivateDir(t), SessionTTL: time.Second},
		{DataDir: tPrivateDir(t), SessionTTL: maxSessionTTL + time.Second},
	} {
		if s, err := New(options); err == nil {
			_ = s.Close()
			t.Fatal("invalid auth options were accepted")
		}
	}
	dir := tPrivateDir(t)
	outside := filepath.Join(tPrivateDir(t), "outside")
	if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "auth.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("creating symlinks is unavailable for this Windows account")
	}
	if s, err := New(Options{DataDir: dir}); err == nil {
		_ = s.Close()
		t.Fatal("symlinked account state was accepted")
	}
	if err := atomicJSON(link, map[string]bool{"changed": true}, 1024); err == nil {
		t.Fatal("atomic persistence followed symlink target")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "unchanged" {
		t.Fatal("symlink target was modified")
	}
}

func TestSecurityChangesFailClosedWhenStateCannotBeSaved(t *testing.T) {
	h := newHarness(t, true)
	client := h.issue("192.0.2.5:1000")
	path := h.s.accountPath
	h.s.accountPath = h.s.dataDir
	requireStatus(t, h.request(http.MethodPatch, "/api/account", encode(t, map[string]string{
		"currentPassword": testPassword, "username": "replacement",
	}), client), http.StatusServiceUnavailable)
	if h.s.account.Username != "admin" || len(h.s.sessions) != 1 {
		t.Fatal("failed credential persistence changed live account/session state")
	}
	h.s.accountPath = path
	secret, _ := beginEnrollment(t, h, client)
	h.s.accountPath = h.s.dataDir
	response := h.request(http.MethodPost, "/api/account/totp/enable", encode(t, map[string]string{
		"currentPassword": testPassword, "code": totpCode(secret, h.now.Unix()/30),
	}), client)
	requireStatus(t, response, http.StatusServiceUnavailable)
	if len(h.s.account.TOTPSecret) != 0 || strings.Contains(response.Body.String(), "recoveryCodes") {
		t.Fatal("failed TOTP persistence activated or disclosed recovery state")
	}
	h.s.accountPath = path
	enableEnrollment(t, h, client, secret)
}

package management

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func validConsent(identity string) []byte {
	actor := "root"
	if identity == "LocalSystem" {
		actor = "Administrator"
	}
	data, _ := json.Marshal(Consent{Format: 1, Mode: "system", Identity: identity, NoticeVersion: 1, AcceptedAt: "2026-11-07T11:00:00Z", AcceptedBy: actor, Notice: "full-system-management"})
	return data
}

func TestConsentRequiresCompleteExactDocument(t *testing.T) {
	valid := string(validConsent("root"))
	cases := []string{
		"", "null", "[]", "{}", valid + valid,
		strings.Replace(valid, `"format":1`, `"format":1,"format":1`, 1),
		strings.Replace(valid, `"format":1`, `"Format":1`, 1),
		strings.Replace(valid, `"format":1`, `"format":null`, 1),
		strings.Replace(valid, `"format":1`, `"format":1.0`, 1),
		strings.Replace(valid, `"format":1,`, "", 1),
		strings.Replace(valid, `"format":1`, `"format":1,"extra":true`, 1),
		strings.Replace(valid, `"system"`, `"restricted"`, 1),
		strings.Replace(valid, `"noticeVersion":1`, `"noticeVersion":2`, 1),
		strings.Replace(valid, `"acceptedBy":"root"`, `"acceptedBy":"admin"`, 1),
		strings.Replace(valid, "2026-11-07T11:00:00Z", "2026-11-08T11:00:00Z", 1),
		strings.Replace(valid, "2026-11-07T11:00:00Z", "2026-11-07T11:00:00+00:00", 1),
		strings.Replace(valid, "full-system-management", "", 1),
		valid + strings.Repeat(" ", maxConsentBytes),
	}
	for i, data := range cases {
		if _, err := parseConsent([]byte(data), "root", testNow); err == nil {
			t.Fatalf("invalid consent case %d accepted", i)
		}
	}
	for _, identity := range []string{"root", "LocalSystem"} {
		if _, err := parseConsent(validConsent(identity), identity, testNow); err != nil {
			t.Fatalf("valid %s consent rejected: %v", identity, err)
		}
	}
	if _, err := parseConsent(validConsent("root"), "LocalSystem", testNow); err == nil {
		t.Fatal("cross-platform consent accepted")
	}
}

func TestAuthorityRequiresIdentityAndRechecksRecord(t *testing.T) {
	highest := false
	data := validConsent("root")
	readCount := 0
	a := &Authority{
		platform: "linux", now: func() time.Time { return testNow },
		identity: func() (string, bool) { return "root", highest },
		read:     func() ([]byte, error) { readCount++; return data, nil },
	}
	if !errors.Is(a.Check(), ErrNotAuthorized) || readCount != 0 {
		t.Fatal("restricted identity can read or use consent")
	}
	highest = true
	if err := a.Check(); err != nil || a.Status().Mode != "system" {
		t.Fatalf("authorized identity rejected: %v", err)
	}
	data = []byte("{}")
	if !errors.Is(a.Check(), ErrNotAuthorized) {
		t.Fatal("revoked consent was cached")
	}
	a.read = func() ([]byte, error) { return nil, errors.New("unsafe record") }
	if a.Status().Authorized {
		t.Fatal("unsafe record granted permission")
	}
	a.platform = "darwin"
	if a.Status().Authorized {
		t.Fatal("unsupported platform granted permission")
	}
}

func TestManagementHasNoPermissionMutationEndpoint(t *testing.T) {
	a := &Authority{platform: "linux", identity: func() (string, bool) { return "uid:1000", false }}
	mux := http.NewServeMux()
	a.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/management", nil))
	var status Status
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Authorized || status.Mode != "restricted" {
		t.Fatalf("unexpected capability response: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/management", strings.NewReader(`{"authorized":true}`)))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected mutation status: %d", w.Code)
	}
}

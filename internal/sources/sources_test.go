package sources

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresetsSeparateInstallAndRegistry(t *testing.T) {
	config := defaultConfig(Detection{})
	config.APT.Provider = "tuna"
	config.DockerCE.Provider = "aliyun"
	config, err := normalize(config)
	if err != nil {
		t.Fatal(err)
	}
	if config.APT.URL != "https://mirrors.tuna.tsinghua.edu.cn/debian" || config.APT.SecurityURL != "https://security.debian.org/debian-security" {
		t.Fatalf("unexpected APT sources: %+v", config.APT)
	}
	if config.DockerCE.URL != "https://mirrors.aliyun.com/docker-ce/linux/debian" || config.Registry.URL != "" {
		t.Fatal("install repository and registry are mixed")
	}
	config.OS, config.Codename, config.Architecture = "ubuntu", "noble", "arm64"
	config.APT.Provider = "official"
	config, err = normalize(config)
	if err != nil || !strings.Contains(config.APT.URL, "ubuntu-ports") {
		t.Fatalf("arm64 repository not selected: %+v %v", config, err)
	}
}

func TestRejectUnsafeURLs(t *testing.T) {
	cases := []string{
		"http://example.com/repo", "https://user:pass@example.com", "https://example.com/?token=secret",
		"https://example.com/#frag", "https://example.com/\nSigned-By:x", "https://example.com/%0atrusted=yes",
		"https://example.com/%5cetc", "https://example.com/../repo", "file:///etc/passwd", "https://example.com/?",
		"https://example.com/space here", "https://example.com/%09x", "https://example.com/`exec`",
	}
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			if _, err := validateURL(value); err == nil {
				t.Fatal("unsafe address accepted")
			}
		})
	}
	for _, value := range []string{"https://mirror.example.org/debian/", "https://mirror.example.org:8443/repo", "https://10.0.0.2/repository"} {
		if _, err := validateURL(value); err != nil {
			t.Fatalf("valid configuration URL rejected: %v", err)
		}
	}
}

func TestRegistryRequiresOwnAliyunAddress(t *testing.T) {
	config := defaultConfig(Detection{})
	config.Registry = Registry{Provider: "aliyun", URL: "https://mirrors.aliyun.com/docker-ce"}
	if _, err := normalize(config); err == nil {
		t.Fatal("package source incorrectly accepted as registry")
	}
	config.Registry.URL = "https://allocated-example.mirror.aliyuncs.com"
	if _, err := normalize(config); err != nil {
		t.Fatal(err)
	}
	config.Registry.URL = "https://allocated-example.mirror.aliyuncs.com.evil.example"
	if _, err := normalize(config); err == nil {
		t.Fatal("fake suffix accepted")
	}
}

func request(mux http.Handler, method, path string, value any) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if value != nil {
		_ = json.NewEncoder(&body).Encode(value)
	}
	r := httptest.NewRequest(method, path, &body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestPersistenceConflictAndPreview(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	config := defaultConfig(Detection{})
	config.APT.Provider = "tuna"
	saved := request(mux, http.MethodPut, "/api/sources", document{Revision: 1, Config: config})
	if saved.Code != http.StatusOK {
		t.Fatalf("save failed: %d %s", saved.Code, saved.Body.String())
	}
	conflict := request(mux, http.MethodPut, "/api/sources", document{Revision: 1, Config: config})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("stale overwrite accepted: %d", conflict.Code)
	}
	reloaded, err := New(dir, nil)
	if err != nil || reloaded.data.Revision != 2 || reloaded.data.Config.APT.Provider != "tuna" {
		t.Fatalf("reload mismatch: %+v %v", reloaded, err)
	}
	preview := request(mux, http.MethodPost, "/api/sources/preview", map[string]any{"config": config})
	if preview.Code != http.StatusOK {
		t.Fatal(preview.Body.String())
	}
	var result struct {
		APT      string   `json:"apt"`
		DockerCE string   `json:"dockerCE"`
		Registry string   `json:"registry"`
		Applied  bool     `json:"applied"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Applied || !strings.Contains(result.APT, "Signed-By:") || !strings.Contains(result.APT, "security.debian.org") || len(result.Warnings) < 3 {
		t.Fatalf("unsafe preview: %+v", result)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "sources.json" {
		t.Fatalf("unexpected writes: %v", entries)
	}
}

func TestRejectInvalidStoredConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sources.json"), []byte(`{"revision":2,"config":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, nil); err == nil {
		t.Fatal("corrupt source settings silently accepted")
	}
}

func TestStrictJSON(t *testing.T) {
	s, _ := New(t.TempDir(), nil)
	mux := http.NewServeMux()
	s.Register(mux)
	for _, body := range []string{`{"unexpected":true}`, `{} {}`, strings.Repeat(" ", 33000) + `{}`} {
		r := httptest.NewRequest(http.MethodPut, "/api/sources", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("bad request accepted: %d", w.Code)
		}
	}
}

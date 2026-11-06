package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"panel/internal/buildinfo"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testService(t *testing.T, repository string) *Service {
	t.Helper()
	current := buildinfo.Current("0.3.0", "unknown", "unknown", "")
	current.OS, current.Arch = "linux", "amd64"
	s, err := New(Options{Current: current, Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func response(data []byte, status int, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}

func request(t *testing.T, s *Service, method string) (Status, int) {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	path := "/api/update"
	if method == http.MethodPost {
		path += "/check"
	}
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var result Status
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result, w.Code
}

func fixture(version string, schema int) (githubRelease, []byte, []byte, []byte) {
	m := manifest{Format: 2, Version: version, Tag: "v" + version, Repository: "example/panel", DataSchema: schema, Commit: strings.Repeat("a", 40), BuildTime: "2026-11-06T00:00:00Z"}
	checksums := make(map[string]string)
	for _, arch := range []string{"amd64", "arm64"} {
		data := []byte("fake binary " + arch)
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		archiveData := []byte("fake archive " + arch)
		archiveDigest := sha256.Sum256(archiveData)
		archiveHash := hex.EncodeToString(archiveDigest[:])
		archiveName := "panel-linux-" + arch + ".tar.gz"
		m.Binaries = append(m.Binaries, binaryRecord{OS: "linux", Arch: arch, Name: "panel", Size: int64(len(data)), SHA256: hash,
			Archive: archiveRecord{Name: archiveName, Size: int64(len(archiveData)), SHA256: archiveHash}})
		checksums[archiveName] = archiveHash
	}
	metadata, _ := json.Marshal(m)
	digest := sha256.Sum256(metadata)
	checksums["release.json"] = hex.EncodeToString(digest[:])
	checksums["install.sh"], checksums["update.sh"] = strings.Repeat("1", 64), strings.Repeat("2", 64)
	var sumLines []string
	for _, name := range []string{"install.sh", "update.sh", "panel-linux-amd64.tar.gz", "panel-linux-arm64.tar.gz", "release.json"} {
		sumLines = append(sumLines, checksums[name]+"  "+name)
	}
	sums := []byte(strings.Join(sumLines, "\n") + "\n")
	release := githubRelease{Tag: m.Tag, PublishedAt: "2026-11-06T00:00:00Z", Body: "<script>never render me as HTML</script>"}
	for name := range legacyAssets {
		size := int64(50)
		if name == "release.json" {
			size = int64(len(metadata))
		} else if name == "SHA256SUMS" {
			size = int64(len(sums))
		} else if strings.HasPrefix(name, "panel-linux-") {
			arch := strings.TrimSuffix(strings.TrimPrefix(name, "panel-linux-"), ".tar.gz")
			size = int64(len("fake archive " + arch))
		}
		release.Assets = append(release.Assets, struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		}{name, canonicalAsset("example/panel", m.Tag, name), size})
	}
	api, _ := json.Marshal(release)
	return release, api, metadata, sums
}

func fakeRelease(t *testing.T, s *Service, api, metadata, sums []byte, calls *atomic.Int32) {
	t.Helper()
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		switch {
		case r.URL.String() == "https://api.github.com/repos/example/panel/releases/latest":
			return response(api, 200, nil), nil
		case r.URL.Host == "github.com" && strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			return response(sums, 200, nil), nil
		case r.URL.Host == "github.com" && strings.HasSuffix(r.URL.Path, "/release.json"):
			return response(metadata, 200, nil), nil
		default:
			t.Errorf("unexpected address: %s", r.URL)
			return response(nil, 404, nil), nil
		}
	})
}

func TestVersionAndRepository(t *testing.T) {
	for _, version := range []string{"1.2", "v1.2.3", "01.2.3", "1.2.3-rc.1", "1.2.3+build", "1.2.2147483648", "1.2.-1", "1.2.3\n"} {
		if _, err := ParseVersion(version); err == nil {
			t.Errorf("invalid stable version accepted: %q", version)
		}
	}
	a, _ := ParseVersion("1.10.0")
	b, _ := ParseVersion("1.9.99")
	if a.Compare(b) != 1 || b.Compare(a) != -1 || a.Compare(a) != 0 {
		t.Fatal("version ordering is not numeric")
	}
	for _, repo := range []string{"https://github.com/a/b", "a/../b", "a/b?x", "a/b;id", "a/.git", "a/b/", "a.b/c", "a/..", "a/$(id)"} {
		if err := ValidateRepository(repo); err == nil {
			t.Errorf("unsafe repository accepted: %q", repo)
		}
	}
	if err := ValidateRepository("Example-Org/my.panel-repo"); err != nil {
		t.Fatal(err)
	}
}

func TestUnconfiguredNeverContactsNetwork(t *testing.T) {
	s := testService(t, "")
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { t.Fatal("unexpected network request"); return nil, nil })
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		status, code := request(t, s, method)
		if code != 200 || status.State != "unconfigured" || status.CanApply || status.UpdateAvailable {
			t.Fatalf("wrong unconfigured result: %+v %d", status, code)
		}
	}
}

func TestReleaseStatesAndCache(t *testing.T) {
	for _, tc := range []struct{ version, state string }{{"0.3.1", "available"}, {"0.3.0", "up_to_date"}, {"0.2.9", "ahead"}} {
		t.Run(tc.state, func(t *testing.T) {
			s := testService(t, "example/panel")
			_, api, metadata, sums := fixture(tc.version, 1)
			var calls atomic.Int32
			fakeRelease(t, s, api, metadata, sums, &calls)
			before, _ := request(t, s, http.MethodGet)
			if before.State != "not_checked" || calls.Load() != 0 {
				t.Fatal("GET performed automatic check")
			}
			status, code := request(t, s, http.MethodPost)
			if code != 200 || status.State != tc.state || status.Latest == nil || !status.Latest.AssetsReady || status.CanApply || status.Latest.URL != "https://github.com/example/panel/releases/tag/v"+tc.version {
				t.Fatalf("release check failed: %+v %d", status, code)
			}
			if status.UpdateAvailable != (tc.state == "available") {
				t.Fatal("incorrect update flag")
			}
			_, _ = request(t, s, http.MethodPost)
			if calls.Load() != 3 {
				t.Fatalf("cache missed; %d network calls", calls.Load())
			}
		})
	}
}

func TestIncompatibleAndTamperedManifest(t *testing.T) {
	for _, change := range []string{"schema", "manifest", "missing", "url", "tag", "duplicate-key"} {
		t.Run(change, func(t *testing.T) {
			s := testService(t, "example/panel")
			release, api, metadata, sums := fixture("0.3.1", 1)
			switch change {
			case "schema":
				_, api, metadata, sums = fixture("0.3.1", 2)
			case "manifest":
				metadata = bytes.Replace(metadata, []byte("0.3.1"), []byte("0.3.2"), 1)
			case "missing":
				release.Assets = release.Assets[1:]
				api, _ = json.Marshal(release)
			case "url":
				release.Assets[0].URL = "https://evil.example/installer"
				api, _ = json.Marshal(release)
			case "tag":
				release.Tag = "v0.3.1;id"
				api, _ = json.Marshal(release)
			case "duplicate-key":
				metadata = append([]byte(`{"format":2,`), metadata[1:]...)
				digest := sha256.Sum256(metadata)
				lines := strings.Split(string(sums), "\n")
				for index, line := range lines {
					if strings.HasSuffix(line, "  release.json") {
						lines[index] = hex.EncodeToString(digest[:]) + "  release.json"
					}
				}
				sums = []byte(strings.Join(lines, "\n"))
			}
			var calls atomic.Int32
			fakeRelease(t, s, api, metadata, sums, &calls)
			status, _ := request(t, s, http.MethodPost)
			if status.State != "incompatible" && status.State != "error" || status.UpdateAvailable || status.CanApply {
				t.Fatalf("unsafe release accepted: %+v", status)
			}
		})
	}
}

func TestFailureAndRateLimit(t *testing.T) {
	for _, tc := range []struct {
		code  int
		state string
	}{{404, "no_release"}, {429, "rate_limited"}, {502, "error"}} {
		s := testService(t, "example/panel")
		s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
			return response(nil, tc.code, http.Header{"Retry-After": {"120"}}), nil
		})
		status, _ := request(t, s, http.MethodPost)
		if status.State != tc.state || status.NextCheckAt == nil || status.Latest != nil {
			t.Fatalf("unexpected failure: %+v", status)
		}
	}
	s := testService(t, "example/panel")
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(bytes.Repeat([]byte("x"), maxResponse+1), 200, nil), nil
	})
	status, _ := request(t, s, http.MethodPost)
	if status.State != "error" {
		t.Fatal("oversized response accepted")
	}
}

func TestCheckIsSingleFlight(t *testing.T) {
	s := testService(t, "example/panel")
	entered, finish := make(chan struct{}), make(chan struct{})
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-finish
		return response(nil, 404, nil), nil
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = request(t, s, http.MethodPost) }()
	<-entered
	_, code := request(t, s, http.MethodPost)
	close(finish)
	wg.Wait()
	if code != http.StatusConflict {
		t.Fatalf("concurrent check not bounded: %d", code)
	}
}

func TestRedirectAllowlist(t *testing.T) {
	original, _ := http.NewRequest("GET", "https://github.com/example/panel/releases/download/v1.0.0/release.json", nil)
	for _, address := range []string{"http://release-assets.githubusercontent.com/x", "https://github.com.evil.example/x", "https://127.0.0.1/x", "https://github.com/other/repo", "https://u:p@objects.githubusercontent.com/x", "https://objects.githubusercontent.com:444/x"} {
		r, _ := http.NewRequest("GET", address, nil)
		if err := checkRedirect(r, []*http.Request{original}); err == nil {
			t.Errorf("unsafe redirect accepted: %s", address)
		}
	}
	u, _ := url.Parse("https://release-assets.githubusercontent.com/a?signed=query")
	if err := checkRedirect(&http.Request{URL: u}, []*http.Request{original}); err != nil {
		t.Fatal(err)
	}
	api, _ := http.NewRequest("GET", "https://api.github.com/repos/example/panel/releases/latest", nil)
	if err := checkRedirect(api, []*http.Request{api}); err == nil {
		t.Fatal("API redirect accepted")
	}
}

func TestNoInputsAndBoundedNotes(t *testing.T) {
	s := testService(t, "")
	mux := http.NewServeMux()
	s.Register(mux)
	for _, payload := range []string{`{"url":"https://evil.example"}`, `{} {}`, strings.Repeat(" ", 1100)} {
		r := httptest.NewRequest("POST", "/api/update/check", strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Errorf("payload unexpectedly accepted: %d", w.Code)
		}
	}
	if len(boundedNotes(strings.Repeat("a", 100000))) > 34<<10 {
		t.Fatal("release note not bounded")
	}
	for _, data := range []string{`{"format":1,"FORMAT":2}`, `{} {}`, `{"x":[[[[[[[[[0]]]]]]]]]}`} {
		if rejectDuplicateKeys([]byte(data)) == nil {
			t.Error(fmt.Sprintf("ambiguous JSON accepted: %s", data))
		}
	}
	if rateReset(http.Header{}, time.Now()).Before(time.Now()) {
		t.Fatal("negative cooldown")
	}
}

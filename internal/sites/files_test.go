package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"panel/internal/jobrunner"
)

func uploadRequest(mux http.Handler, path string, data []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-Confirm-Operation", "true")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestFilesBoundedUploadEditCASAndNoPayloadPersistence(t *testing.T) {
	s, _, mux := fixture(t)
	site := createFixtureSite(t, s)
	base := "/api/sites/" + site.Site.ID
	content := request(t, mux, "GET", base+"/content?path=index.html", nil)
	if content.Code != 200 {
		t.Fatal(content.Body.String())
	}
	var view struct {
		Content string `json:"content"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal(content.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	secret := "<?php /* usercode-private-DO-NOT-LOG-81373 */ echo 'hello'; ?>"
	if w := request(t, mux, "PUT", base+"/content", map[string]any{"confirmed": true, "path": "index.html", "content": secret}); w.Code != 409 {
		t.Fatalf("blind overwrite %d %s", w.Code, w.Body.String())
	}
	job := taskFrom(t, request(t, mux, "PUT", base+"/content", map[string]any{"confirmed": true, "path": "index.html", "content": secret, "sha256": view.SHA256}))
	if got := waitTask(t, mux, job.ID); got.Status != "succeeded" {
		t.Fatalf("edit %+v", got)
	}
	if w := request(t, mux, "PUT", base+"/content", map[string]any{"confirmed": true, "path": "index.html", "content": "old", "sha256": view.SHA256}); w.Code != 409 {
		t.Fatalf("stale overwrite %d", w.Code)
	}
	if w := uploadRequest(mux, base+"/upload?path=index.html", []byte("replace")); w.Code != 409 {
		t.Fatalf("upload overwrite %d", w.Code)
	}
	upload := taskFrom(t, uploadRequest(mux, base+"/upload?path=data.bin", []byte{0, 1, 2, 3}))
	if got := waitTask(t, mux, upload.ID); got.Status != "succeeded" {
		t.Fatalf("upload %+v", got)
	}
	if w := request(t, mux, "GET", base+"/content?path=data.bin", nil); w.Code != 415 {
		t.Fatalf("binary preview %d", w.Code)
	}
	if w := uploadRequest(mux, base+"/upload?path=large.bin", make([]byte, UploadLimitBytes+1)); w.Code != 413 {
		t.Fatalf("upload bound %d", w.Code)
	}
	if w := request(t, mux, "PUT", base+"/content", map[string]any{"confirmed": true, "path": "big.txt", "content": strings.Repeat("x", EditLimitBytes+1)}); w.Code != 413 {
		t.Fatalf("edit bound %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(site.Site.Root, "large.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oversize upload left target")
	}
	// Inspect private descriptors, never arbitrary web files. Jobs and site state
	// may contain IDs/hashes but not the queued source payload.
	for _, dir := range []string{s.statePath, filepath.Join(s.options.DataDir, "jobs")} {
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".json" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte("usercode-private-DO-NOT-LOG-81373")) {
				t.Errorf("payload leaked into %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	jobsView := request(t, mux, "GET", "/api/jobs", nil)
	if strings.Contains(jobsView.Body.String(), "usercode-private") {
		t.Fatal("payload in API logs")
	}
}

func TestPathTraversalLinksAndOwnedMarker(t *testing.T) {
	s, _, mux := fixture(t)
	site := createFixtureSite(t, s)
	base := "/api/sites/" + site.Site.ID
	for _, path := range []string{"../outside", "/etc/passwd", "C:/secret", "..\\secret", "a/../b", "a//b", "a/./b", "a:b", "NUL", "COM1.txt", "a. ", ".qqon-owned", ".qqon-tmp-hack", ".__qqon_health", "a\x00b", "/", ""} {
		w := request(t, mux, "GET", base+"/content?path="+url.QueryEscape(path), nil)
		if w.Code != 400 {
			t.Errorf("path %q -> %d", path, w.Code)
		}
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(site.Site.Root, "hard.txt")
	if err := os.Link(outside, link); err != nil {
		t.Fatal(err)
	}
	if w := request(t, mux, "GET", base+"/content?path=hard.txt", nil); w.Code != 403 {
		t.Fatalf("hardlink read %d %s", w.Code, w.Body.String())
	}
	if err := s.writeContent(context.Background(), site.Site.ID, 1, "hard.txt", []byte("changed"), digest([]byte("outside-secret")), false, nil); err == nil {
		t.Fatal("hardlink overwrite succeeded")
	}
	if data, _ := os.ReadFile(outside); string(data) != "outside-secret" {
		t.Fatal("hardlink changed outside file")
	}
	if err := os.Symlink("index.html", filepath.Join(site.Site.Root, "link.txt")); err == nil {
		if w := request(t, mux, "GET", base+"/content?path=link.txt", nil); w.Code != 403 {
			t.Fatalf("contained symlink accepted %d", w.Code)
		}
	} else {
		t.Log("symlink fixture unavailable on host:", err)
	}
	if err := os.WriteFile(filepath.Join(site.Site.Root, ".qqon-owned"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := request(t, mux, "GET", base+"/content?path=index.html", nil); w.Code != 403 {
		t.Fatalf("unowned webroot read %d", w.Code)
	}
}

func TestDirectoryListingBoundedAndReservedHidden(t *testing.T) {
	s, _, mux := fixture(t)
	site := createFixtureSite(t, s)
	for i := 0; i < 205; i++ {
		if err := os.WriteFile(filepath.Join(site.Site.Root, fmt.Sprintf("f%03d.txt", i)), []byte("a"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	base := "/api/sites/" + site.Site.ID + "/files"
	w := request(t, mux, "GET", base+"?path=&offset=0", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var view fileView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) > 200 || view.NextOffset == nil || *view.NextOffset != 200 {
		t.Fatalf("unbounded listing %+v", view)
	}
	for _, entry := range view.Entries {
		if strings.HasPrefix(entry.Name, ".qqon-") {
			t.Fatal("private marker exposed")
		}
	}
	w = request(t, mux, "GET", base+"?path=&offset=200", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) > 7 {
		t.Fatal("wrong finite page")
	}
	if w := request(t, mux, "GET", base+"?offset=10001", nil); w.Code != 400 {
		t.Fatalf("scan bound %d", w.Code)
	}
	if w := request(t, mux, "GET", base+"?path=a&path=b", nil); w.Code != 400 {
		t.Fatalf("duplicate query %d", w.Code)
	}
}

func TestQueuedFileRechecksCASAndSiteRevision(t *testing.T) {
	s, _, _ := fixture(t)
	site := createFixtureSite(t, s)
	old, _ := os.ReadFile(filepath.Join(site.Site.Root, "index.html"))
	if err := os.WriteFile(filepath.Join(site.Site.Root, "index.html"), []byte("concurrent change"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.writeContent(context.Background(), site.Site.ID, 1, "index.html", []byte("overwrite"), digest(old), false, nil); err == nil {
		t.Fatal("lost queued CAS")
	}
	in := inputFromSite(site.Site)
	in.Enabled = false
	if err := s.patch(context.Background(), site.Site.ID, 1, in, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.writeContent(context.Background(), site.Site.ID, 1, "new.txt", []byte("new"), "", true, nil); err == nil {
		t.Fatal("queued write ignored site revision")
	}
}

func TestQueueHasFinitePayloadAdmission(t *testing.T) {
	s, _, mux := fixture(t)
	site := createFixtureSite(t, s)
	started, release := make(chan struct{}), make(chan struct{})
	_, err := s.options.Jobs.Submit("fixture.hold", "one", 30e9, func(ctx context.Context, p *jobrunner.Progress) error {
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
	defer close(release)
	for i := 0; i < 8; i++ {
		_, err := s.options.Jobs.Submit("fixture.queue", fmt.Sprint(i), 30e9, func(context.Context, *jobrunner.Progress) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	w := uploadRequest(mux, "/api/sites/"+site.Site.ID+"/upload?path=queued.txt", []byte("not-admitted"))
	if w.Code != 429 {
		t.Fatalf("queue bound %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(site.Site.Root, "queued.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("non-admitted upload wrote file")
	}
}

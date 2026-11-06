package machine

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFilePaginationPreviewAndContainment(t *testing.T) {
	if !safeFilesSupported() {
		t.Skip("safe file browsing unsupported")
	}
	root := t.TempDir()
	for i := 0; i < 205; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("item-%03d.txt", i)), []byte("hello <script> & 中文\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := fixtureService(t, Options{FileRoot: root}, nil)
	first := request(t, s, "GET", "/api/files?path=&offset=0", "")
	requireStatus(t, first, 200)
	page := decodeBody[filesView](t, first)
	if len(page.Entries) != 200 || page.NextOffset == nil || *page.NextOffset != 200 || page.Path != "" || page.Root != root {
		t.Fatalf("bad first page: %d entries, next %v", len(page.Entries), page.NextOffset)
	}
	last := request(t, s, "GET", "/api/files?offset=200", "")
	requireStatus(t, last, 200)
	second := decodeBody[filesView](t, last)
	if len(second.Entries) != 5 || second.NextOffset != nil {
		t.Fatalf("bad final page: %+v", second)
	}
	seen := map[string]bool{}
	for _, e := range append(page.Entries, second.Entries...) {
		if seen[e.Name] || e.Type != "file" || e.Size == 0 || e.Modified.IsZero() {
			t.Fatalf("bad entry: %+v", e)
		}
		seen[e.Name] = true
	}
	preview := request(t, s, "GET", "/api/files/preview?path=item-000.txt", "")
	requireStatus(t, preview, 200)
	if bytes.Contains(preview.Body.Bytes(), []byte("<script>")) {
		t.Fatal("unescaped HTML in JSON preview")
	}
	var content struct {
		Content, Encoding string
		Truncated         bool
	}
	content = decodeBody[struct {
		Content, Encoding string
		Truncated         bool
	}](t, preview)
	if content.Content != "hello <script> & 中文\n" || content.Encoding != "utf-8" || content.Truncated {
		t.Fatalf("invalid preview: %+v", content)
	}
	for _, path := range []string{"../secret", "a/../../secret", "/etc/passwd", `..\secret`, `C:\secret`, `C:secret`, `\\server\share`, `file:stream`, ".", "a//b", "a/../b", "NUL", "COM1.txt", "LPT²", "trailing.", "trailing ", "file\x00name"} {
		r := request(t, s, "GET", "/api/files/preview?path="+url.QueryEscape(path), "")
		requireStatus(t, r, 400)
	}
	for _, query := range []string{"offset=-1", "offset=10001", "offset=9999999999999999999999999999", "offset=abc", "offset=", "offset=0&offset=1", "path=a&path=b", "bad=1", "path=%zz"} {
		requireStatus(t, request(t, s, "GET", "/api/files?"+query, ""), 400)
	}
	requireStatus(t, request(t, s, "GET", "/api/files/preview?path=missing", ""), 404)
	requireStatus(t, request(t, s, "GET", "/api/files/preview?path=", ""), 403)
	requireStatus(t, request(t, s, "GET", "/api/files?path=item-000.txt", ""), 403)
}

func TestPreviewTextLimits(t *testing.T) {
	if !safeFilesSupported() {
		t.Skip("safe file browsing unsupported")
	}
	root := t.TempDir()
	cases := []struct {
		name string
		data []byte
		want int
	}{
		{"empty", []byte{}, 200},
		{"boundary", bytes.Repeat([]byte("x"), maxPreview), 200},
		{"large", bytes.Repeat([]byte("x"), maxPreview+1), 413},
		{"binary", []byte{'a', 0, 'b'}, 415},
		{"invalid-utf8", []byte{0xff, 0xfe}, 415},
		{"escape", []byte("\x1b[31m"), 415},
		{"unicode", []byte("第一行\r\n\t第二行"), 200},
	}
	for _, tc := range cases {
		if err := os.WriteFile(filepath.Join(root, tc.name), tc.data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := fixtureService(t, Options{FileRoot: root}, nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireStatus(t, request(t, s, "GET", "/api/files/preview?path="+tc.name, ""), tc.want)
		})
	}
}

func TestEscapingSymlinkNeverRead(t *testing.T) {
	if !safeFilesSupported() {
		t.Skip("safe file browsing unsupported")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("MUST_NOT_LEAK"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("OS does not permit unprivileged symlinks: %v", err)
	}
	s := fixtureService(t, Options{FileRoot: root}, nil)
	for _, target := range []string{"/api/files?path=escape", "/api/files/preview?path=escape/secret"} {
		r := request(t, s, "GET", target, "")
		if r.Code == 200 || strings.Contains(r.Body.String(), "MUST_NOT_LEAK") {
			t.Fatalf("escaped root: %d %s", r.Code, r.Body.String())
		}
	}
	page := decodeBody[filesView](t, request(t, s, "GET", "/api/files", ""))
	if len(page.Entries) != 1 || page.Entries[0].Type != "symlink" {
		t.Fatalf("link classification: %+v", page)
	}
}

func TestInvalidFileRootIsNotSilentlyEnabled(t *testing.T) {
	if !safeFilesSupported() {
		t.Skip("safe file browsing unsupported")
	}
	file := filepath.Join(t.TempDir(), "not-dir")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := newService(Options{FileRoot: file}, &serviceDependencies{sampler: &fixtureSampler{}, noBackground: true})
	if err == nil {
		t.Fatal("regular-file root was accepted")
	}
}

func TestFileScanBound(t *testing.T) {
	if !safeFilesSupported() {
		t.Skip("safe file browsing unsupported")
	}
	s := fixtureService(t, Options{FileRoot: t.TempDir()}, nil)
	for _, offset := range []int{0, filePageSize, maxFileOffset} {
		r := request(t, s, "GET", "/api/files?offset="+strconv.Itoa(offset), "")
		requireStatus(t, r, 200)
		page := decodeBody[filesView](t, r)
		if len(page.Entries) != 0 || page.NextOffset != nil {
			t.Fatal("empty directory has phantom entries")
		}
	}
}

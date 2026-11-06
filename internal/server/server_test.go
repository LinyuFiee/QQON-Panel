package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func TestGuardOriginAndHost(t *testing.T) {
	handler, err := Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }), Options{Origins: []string{"http://127.0.0.1:5173"}, MaxConcurrent: 4})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, host, origin, fetchSite string
		status                                int
	}{
		{"read", "GET", "127.0.0.1:5173", "", "", 204},
		{"write", "POST", "127.0.0.1:5173", "http://127.0.0.1:5173", "same-origin", 204},
		{"host attack", "GET", "evil.example", "", "", 403},
		{"origin attack", "POST", "127.0.0.1:5173", "https://evil.example", "", 403},
		{"missing origin", "POST", "127.0.0.1:5173", "", "", 403},
		{"null origin", "POST", "127.0.0.1:5173", "null", "", 403},
		{"same site sibling", "GET", "127.0.0.1:5173", "", "same-site", 403},
		{"cross site", "GET", "127.0.0.1:5173", "", "cross-site", 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+"/api/test", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
				t.Fatal("security headers missing")
			}
		})
	}
}

func TestCanonicalDefaultPort(t *testing.T) {
	handler, err := Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), Options{Origins: []string{"https://panel.example:443"}, MaxConcurrent: 2, Secure: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"panel.example", "panel.example:443"} {
		r := httptest.NewRequest("POST", "https://"+host+"/api/example", nil)
		r.Header.Set("Origin", "https://panel.example")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("valid default port rejected: %s %d", host, w.Code)
		}
	}
}

func TestGuardAdmissionIsNonblocking(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	handler, _ := Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(204)
	}), Options{Origins: []string{"http://localhost"}, MaxConcurrent: 2})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://localhost/", nil))
		}()
	}
	<-entered
	<-entered
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/", nil))
	close(release)
	wg.Wait()
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("overload not rejected: %d", w.Code)
	}
}

func TestExactStaticAllowlist(t *testing.T) {
	files := fstest.MapFS{}
	for _, name := range []string{"index.html", "app.js", "api.js", "styles.css", "account.json", "main.go"} {
		files[name] = &fstest.MapFile{Data: []byte(name)}
	}
	handler, err := Static(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/account.json", "/main.go", "/.panel.lock", "/sources.json", "/../account.json", "/x"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("private path served: %s => %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || w.Body.String() != "index.html" {
		t.Fatal("index not served")
	}
}

func TestGlobalBodyLimit(t *testing.T) {
	handler, _ := Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err == nil {
			t.Error("oversized body accepted")
		}
	}), Options{Origins: []string{"http://localhost"}, MaxConcurrent: 2})
	r := httptest.NewRequest("POST", "http://localhost/", strings.NewReader(strings.Repeat("x", 65537)))
	r.Header.Set("Origin", "http://localhost")
	handler.ServeHTTP(httptest.NewRecorder(), r)
}

func TestLimitListenerShutdown(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := LimitListener(base, 1)
	defer listener.Close()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() { _, err := listener.Accept(); done <- err }()
	_ = listener.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed listener accepted a connection")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked by connection limit")
	}
}

func BenchmarkCachedGuardedRead(b *testing.B) {
	handler, _ := Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"available":true}`) }), Options{Origins: []string{"http://localhost"}, MaxConcurrent: 24})
	b.ReportAllocs()
	for b.Loop() {
		r := httptest.NewRequest("GET", "http://localhost/api/system", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			b.Fatal(fmt.Sprint(w.Code))
		}
	}
}

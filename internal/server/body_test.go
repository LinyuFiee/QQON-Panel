package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSiteBodyLimitsAreEndpointSpecific(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		method, path string
		limit        int64
	}{
		{"POST", "/api/sites/" + id + "/upload", 2 << 20},
		{"PUT", "/api/sites/" + id + "/content", 512 << 10},
		{"POST", "/api/sites/" + id + "/tls", 128 << 10},
		{"GET", "/api/sites/" + id + "/upload", 64 << 10},
		{"POST", "/api/sites/" + id + "/content", 64 << 10},
		{"PUT", "/api/sites/" + id + "/tls", 64 << 10},
		{"POST", "/api/auth/login", 64 << 10},
		{"POST", "/api/sites/invalid/upload", 64 << 10},
		{"POST", "/api/sites/" + id + "/upload/extra", 64 << 10},
		{"POST", "/api/sites//" + id + "/upload", 64 << 10},
		{"POST", "/api/sites/" + id + "/%75pload", 64 << 10},
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://localhost"+tc.path, nil)
			if got := requestBodyLimit(r); got != tc.limit {
				t.Fatalf("limit %d, want %d", got, tc.limit)
			}
		})
	}
}

func TestSiteBodyLimitsApplyToUnknownLength(t *testing.T) {
	handler, err := Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), Options{Origins: []string{"http://localhost"}, MaxConcurrent: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ size, want int }{{2 << 20, 204}, {2<<20 + 1, 413}} {
		r := httptest.NewRequest("POST", "http://localhost/api/sites/0123456789abcdef/upload", strings.NewReader(strings.Repeat("a", tc.size)))
		r.Header.Set("Origin", "http://localhost")
		r.ContentLength = -1
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%d-byte body returned %d, want %d", tc.size, w.Code, tc.want)
		}
	}
}

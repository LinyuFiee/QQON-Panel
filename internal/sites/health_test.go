package sites

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serverPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	_, raw, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestHTTPVerificationUsesLoopbackHostAndExactRevision(t *testing.T) {
	s, _, _ := fixture(t)
	id, _ := randomID()
	token, _ := randomID()
	r := record{Site: Site{ID: id, Domains: []string{"does-not-resolve.invalid"}, Enabled: true, Type: "static"}, Token: token}
	var wrong atomic.Bool
	var seen atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen.Add(1)
		if !strings.HasPrefix(request.Host, "does-not-resolve.invalid:") {
			t.Errorf("unexpected Host %s", request.Host)
		}
		if request.URL.Path == "/.__qqon_health" {
			w.Header().Set("X-QQON-Site", id)
			if wrong.Load() {
				fmt.Fprintf(w, "qqon:%s:wrong\n", id)
			} else {
				fmt.Fprintf(w, "qqon:%s:%s\n", id, token)
			}
			return
		}
		io.WriteString(w, "<html>QQON static "+id+"</html>")
	}))
	defer server.Close()
	r.Site.Port = serverPort(t, server)
	if err := s.verifyHTTP(context.Background(), r, true); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 2 {
		t.Fatalf("missing static content check: %d", seen.Load())
	}
	wrong.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := s.verifyHTTP(ctx, r, false); err == nil {
		t.Fatal("wrong revision accepted")
	}
}

func TestHTTPVerificationRejectsRedirectOversizeAndRawPHP(t *testing.T) {
	for _, kind := range []string{"redirect", "oversize", "raw_php", "disabled_content"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := fixture(t)
			id, _ := randomID()
			token, _ := randomID()
			r := record{Site: Site{ID: id, Domains: []string{"fixture.invalid"}, Enabled: true, Type: "php"}, Token: token}
			var forwarded atomic.Int32
			external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
			defer external.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if kind == "redirect" {
					http.Redirect(w, request, external.URL, 302)
					return
				}
				if kind == "oversize" {
					io.WriteString(w, strings.Repeat("x", 8192))
					return
				}
				if request.URL.Path == "/.__qqon_health" {
					w.Header().Set("X-QQON-Site", id)
					fmt.Fprintf(w, "qqon:%s:%s\n", id, token)
					return
				}
				if kind == "raw_php" {
					io.WriteString(w, "<?php echo 'QQON PHP "+id+"'; ?>")
					return
				}
				io.WriteString(w, "unexpected default host content")
			}))
			defer server.Close()
			r.Site.Port = serverPort(t, server)
			if kind == "disabled_content" {
				r.Site.Enabled = false
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			if err := s.verifyHTTP(ctx, r, true); err == nil {
				t.Fatal("unsafe health passed")
			}
			if forwarded.Load() != 0 {
				t.Fatal("health followed external redirect")
			}
		})
	}
}

func TestHTTPRemovalChecksOldMarker(t *testing.T) {
	s, _, _ := fixture(t)
	id, _ := randomID()
	token, _ := randomID()
	r := record{Site: Site{ID: id, Domains: []string{"deleted.fixture.invalid"}}, Token: token}
	var loaded atomic.Bool
	loaded.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if loaded.Load() {
			w.Header().Set("X-QQON-Site", id)
			fmt.Fprintf(w, "qqon:%s:%s\\n", id, token)
		} else {
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	r.Site.Port = serverPort(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	if err := s.verifyRemovedHTTP(ctx, r); err == nil {
		t.Fatal("old loaded marker accepted as removed")
	}
	cancel()
	loaded.Store(false)
	if err := s.verifyRemovedHTTP(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if err := s.verifyRemovedHTTP(context.Background(), r); err != nil {
		t.Fatal("closed listener not accepted", err)
	}
}

func TestHTTPVerificationOfImportedTLSAndDisabledHost(t *testing.T) {
	s, _, _ := fixture(t)
	id, _ := randomID()
	token, _ := randomID()
	r := record{Site: Site{ID: id, Domains: []string{"tls.fixture.invalid"}, Enabled: false, Type: "static", TLSEnabled: true}, Token: token}
	cert, key := certificateFixture(t, r.Site.Domains)
	if err := s.storeCertificate(&r, cert, key); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || request.TLS.ServerName != "tls.fixture.invalid" {
			t.Error("missing expected TLS SNI")
		}
		if request.URL.Path == "/.__qqon_health" {
			w.Header().Set("X-QQON-Site", id)
			fmt.Fprintf(w, "qqon:%s:%s\n", id, token)
			return
		}
		w.WriteHeader(503)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	r.Site.Port = serverPort(t, server)
	if err := s.verifyHTTP(context.Background(), r, false); err != nil {
		t.Fatal(err)
	}
}

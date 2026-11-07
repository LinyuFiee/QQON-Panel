package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"panel/internal/buildinfo"
	"panel/internal/server"
	"panel/internal/tlsutil"
)

func TestHTTPErrorLogDoesNotStoreRawRequestDetails(t *testing.T) {
	var output bytes.Buffer
	writer := safeHTTPErrorWriter{output: &output}
	sensitive := []byte("panic: test-password token=example-private-token Cookie: session=test-value")
	n, err := writer.Write(sensitive)
	if err != nil || n != len(sensitive) {
		t.Fatalf("writer contract failed: %d %v", n, err)
	}
	if bytes.Contains(output.Bytes(), []byte("test-password")) || bytes.Contains(output.Bytes(), []byte("example-private-token")) || bytes.Contains(output.Bytes(), []byte("test-value")) {
		t.Fatal("raw error leaked into runtime log")
	}
}

func TestDirectHTTPSHealthUsesVerifiedNameAndOrigin(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "certificate")
	if _, err := tlsutil.Create(directory, "127.0.0.1", 30); err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	pair, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthHandler(buildinfo.Current(version, commit, buildTime, githubRepository)))
	secure := httptest.NewUnstartedServer(nil)
	host := secure.Listener.Addr().String()
	guard, err := server.Guard(mux, server.Options{Origins: []string{"https://" + host}, MaxConcurrent: 4, Secure: true})
	if err != nil {
		t.Fatal(err)
	}
	secure.Config.Handler = guard
	secure.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	secure.StartTLS()
	t.Cleanup(secure.Close)
	pemBytes, err := os.ReadFile(certificatePath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemBytes) {
		t.Fatal("certificate root is not readable")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(secure.URL + "/healthz")
	if err != nil {
		t.Fatalf("trusted loopback HTTPS health check failed: %v", err)
	}
	defer response.Body.Close()
	var health map[string]any
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil || response.StatusCode != 200 || health["version"] != version {
		t.Fatalf("invalid HTTPS health: %d %v %v", response.StatusCode, health, err)
	}
	badHost, _ := http.NewRequest("GET", secure.URL+"/healthz", nil)
	badHost.Host = "unconfigured.example"
	denied, err := client.Do(badHost)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, denied.Body)
	_ = denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatal("HTTPS must not bypass Host validation")
	}

	wrongNameTransport := transport.Clone()
	wrongNameTransport.TLSClientConfig = transport.TLSClientConfig.Clone()
	wrongNameTransport.TLSClientConfig.ServerName = "not-in-san.example"
	t.Cleanup(wrongNameTransport.CloseIdleConnections)
	wrongName := &http.Client{Transport: wrongNameTransport, Timeout: 3 * time.Second}
	if response, err := wrongName.Get(secure.URL + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("certificate hostname mismatch was ignored")
	}
	untrustedTransport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: x509.NewCertPool()}}
	t.Cleanup(untrustedTransport.CloseIdleConnections)
	untrusted := &http.Client{Transport: untrustedTransport, Timeout: 3 * time.Second}
	if response, err := untrusted.Get(secure.URL + "/healthz"); err == nil {
		response.Body.Close()
		t.Fatal("unknown certificate was accepted without trust")
	}
	_, port, _ := net.SplitHostPort(host)
	if port == "" {
		t.Fatal("test did not bind a real loopback TLS listener")
	}
}

package sites

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func certificateFixture(t *testing.T, domains []string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: domains[0]}, DNSNames: domains, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
}

func TestCertificateImportPrivateStorageAndSafeFailure(t *testing.T) {
	s, h, mux := fixture(t)
	old := createFixtureSite(t, s)
	cert, key := certificateFixture(t, old.Site.Domains)
	job := taskFrom(t, request(t, mux, "POST", "/api/sites/"+old.Site.ID+"/tls", map[string]any{"confirmed": true, "revision": 1, "certificate": string(cert), "privateKey": string(key)}))
	if got := waitTask(t, mux, job.ID); got.Status != "succeeded" {
		t.Fatalf("TLS import %+v", got)
	}
	installed, err := s.snapshot(old.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Site.TLSEnabled || installed.Site.Revision != 2 || !strings.HasPrefix(installed.Site.URL, "https://") {
		t.Fatal("TLS metadata not set")
	}
	private := filepath.Join(s.certPath, old.Site.ID, installed.CertGeneration, "key.pem")
	if bytesOnDisk, err := os.ReadFile(private); err != nil || !bytes.Equal(bytesOnDisk, key) {
		t.Fatal("private key not stored privately", err)
	}
	if err := filepath.WalkDir(old.Site.Root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte("PRIVATE KEY")) {
			t.Fatal("PEM in webroot")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, _ := os.ReadFile(filepath.Join(s.statePath, "state.json"))
	if bytes.Contains(state, []byte("BEGIN")) {
		t.Fatal("PEM in metadata")
	}
	for _, path := range []string{"/api/sites", "/api/sites/" + old.Site.ID, "/api/jobs", "/api/jobs/" + job.ID} {
		w := request(t, mux, "GET", path, nil)
		if strings.Contains(w.Body.String(), "PRIVATE KEY") || strings.Contains(w.Body.String(), "BEGIN CERTIFICATE") {
			t.Fatalf("PEM exposed by %s", path)
		}
	}
	cert2, key2 := certificateFixture(t, old.Site.Domains)
	h.mu.Lock()
	h.fail = "reload"
	h.remaining = 1
	h.mu.Unlock()
	failed := taskFrom(t, request(t, mux, "POST", "/api/sites/"+old.Site.ID+"/tls", map[string]any{"confirmed": true, "revision": 2, "certificate": string(cert2), "privateKey": string(key2)}))
	failed = waitTask(t, mux, failed.ID)
	if failed.Status != "failed" || strings.Contains(failed.Error, "PRIVATE") {
		t.Fatalf("bad TLS failure %+v", failed)
	}
	got, _ := s.snapshot(old.Site.ID)
	if got.CertGeneration != installed.CertGeneration || got.Site.Revision != 2 {
		t.Fatal("TLS rollback did not preserve old cert")
	}
	if bytesOnDisk, _ := os.ReadFile(private); !bytes.Equal(bytesOnDisk, key) {
		t.Fatal("old cert overwritten")
	}
	wrongCert, wrongKey := certificateFixture(t, []string{"other.test"})
	if err := validateCertificate(wrongCert, wrongKey, old.Site.Domains); err == nil {
		t.Fatal("accepted SAN mismatch")
	}
	if err := validateCertificate(cert, wrongKey, old.Site.Domains); err == nil {
		t.Fatal("accepted mismatched key")
	}
	if err := validateCertificate(bytes.Repeat([]byte("x"), maxCertBytes+1), key, old.Site.Domains); err == nil {
		t.Fatal("unbounded PEM")
	}
}

func TestPHPAndProxyUseOnlyContractRuntime(t *testing.T) {
	s, _, _ := fixture(t)
	php := staticInput("php.example.test")
	php.Type = "php"
	php.PHPRuntime = "php8.2-fpm"
	id, _ := randomID()
	if err := s.create(context.Background(), id, php, nil); err != nil {
		t.Fatal(err)
	}
	r, _ := s.snapshot(id)
	config, _ := s.render(r)
	for _, expected := range []string{"fastcgi_pass \"unix:/run/php/php8.2-fpm.sock\"", "fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name", "try_files $uri =404", "HTTP_PROXY \"\""} {
		if !bytes.Contains(config, []byte(expected)) {
			t.Fatalf("missing PHP directive %s", expected)
		}
	}
	if bytes.Contains(config, []byte("include ")) {
		t.Fatal("included unknown config")
	}
	php.PHPRuntime = "php9.9-fpm"
	id, _ = randomID()
	if err := s.create(context.Background(), id, php, nil); err == nil {
		t.Fatal("accepted undiscovered PHP")
	}
	proxy := staticInput("proxy.example.test")
	proxy.Type = "proxy"
	proxy.Upstream = "https://upstream.example.test/app/"
	id, _ = randomID()
	if err := s.create(context.Background(), id, proxy, nil); err != nil {
		t.Fatal(err)
	}
	r, _ = s.snapshot(id)
	config, _ = s.render(r)
	if !bytes.Contains(config, []byte("proxy_pass \"https://upstream.example.test/app/\"")) || !bytes.Contains(config, []byte("proxy_ssl_verify on")) {
		t.Fatal("proxy rendering not safe")
	}
	if knownPHPSocket("php8.2-fpm", "/tmp/arbitrary.sock") || knownPHPSocket("php-fpm", "/run/php-fpm/php-fpm.sock") {
		t.Fatal("accepted unknown PHP socket")
	}
}

package tlsutil

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"panel/internal/platform"
)

func TestCreateAndInspect(t *testing.T) {
	serials := make(map[string]bool)
	for _, host := range []string{"127.0.0.1", "192.0.2.42", "LOCALHOST", "Panel.Example.COM."} {
		t.Run(host, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "generation")
			started := time.Now().UTC()
			info, err := Create(directory, host, 365)
			if err != nil {
				t.Fatal(err)
			}
			if info.Host != strings.ToLower(strings.TrimSuffix(host, ".")) || info.CertFile != filepath.Join(directory, "cert.pem") || info.KeyFile != filepath.Join(directory, "key.pem") {
				t.Fatalf("unexpected metadata: %+v", info)
			}
			block, _ := pem.Decode(readTestFile(t, info.CertFile))
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			key, ok := cert.PublicKey.(*ecdsa.PublicKey)
			if !ok || key.Curve != elliptic.P256() || cert.IsCA || cert.KeyUsage != x509.KeyUsageDigitalSignature || len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
				t.Fatal("not a P256 server certificate")
			}
			if cert.SerialNumber.Sign() <= 0 || cert.SerialNumber.BitLen() > 128 || serials[cert.SerialNumber.String()] {
				t.Fatal("invalid or repeated random serial")
			}
			serials[cert.SerialNumber.String()] = true
			if cert.NotBefore.Before(started.Add(-5*time.Minute-time.Second)) || cert.NotBefore.After(time.Now().Add(-5*time.Minute)) || cert.NotAfter.Sub(cert.NotBefore) != 365*24*time.Hour+5*time.Minute {
				t.Fatal("unexpected validity window")
			}
			if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
				t.Fatal("not self-signed:", err)
			}
			if err := cert.VerifyHostname(info.Host); err != nil {
				t.Fatal("missing IP/DNS SAN:", err)
			}
			fingerprint := sha256.Sum256(cert.Raw)
			if info.Fingerprint != hex.EncodeToString(fingerprint[:]) || info.NotAfter != cert.NotAfter.UTC().Format(time.RFC3339) {
				t.Fatal("metadata does not identify the leaf certificate")
			}
			for _, path := range []string{info.CertFile, info.KeyFile} {
				if err := platform.CheckPrivateFile(path); err != nil {
					t.Fatal("private Temp child did not inherit safe file permissions:", err)
				}
			}
			inspected, err := Inspect(info.CertFile, info.KeyFile, host)
			if err != nil || inspected != info {
				t.Fatalf("inspection mismatch: %+v, %v", inspected, err)
			}
			encoded, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]string
			if err := json.Unmarshal(encoded, &fields); err != nil || len(fields) != 5 || fields["host"] != info.Host || fields["fingerprint"] != info.Fingerprint || fields["notAfter"] != info.NotAfter || fields["certFile"] != info.CertFile || fields["keyFile"] != info.KeyFile || bytes.Contains(encoded, []byte("PRIVATE KEY")) {
				t.Fatal("unexpected public JSON shape")
			}
		})
	}
}

func TestCreateLimitsAndNoOverwrite(t *testing.T) {
	for _, days := range []int{-1, 0, 398} {
		path := filepath.Join(t.TempDir(), "generation")
		_, err := Create(path, "localhost", days)
		assertSafeError(t, err)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("invalid days created output")
		}
	}
	for _, days := range []int{1, 397} {
		if _, err := Create(filepath.Join(t.TempDir(), "generation"), "localhost", days); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"", " localhost", "localhost:443", "https://host/?credential-sentinel", "foo\nbar", "*.example.com", "a..b", "-a.test", "a_.test", "999.0.0.1", "::1", "例子.test"} {
		_, err := Create(filepath.Join(t.TempDir(), "generation"), host, 1)
		assertSafeError(t, err)
	}
	for _, path := range []string{"", "relative-generation"} {
		_, err := Create(path, "localhost", 1)
		assertSafeError(t, err)
	}
	for _, kind := range []string{"empty-directory", "directory", "file"} {
		target := filepath.Join(t.TempDir(), "existing")
		sentinel := target
		if kind != "file" {
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			sentinel = filepath.Join(target, "key.pem")
		}
		if kind != "empty-directory" {
			writeTestFile(t, sentinel, []byte("credential-sentinel"))
		}
		_, err := Create(target, "localhost", 1)
		assertSafeError(t, err)
		if kind != "empty-directory" && string(readTestFile(t, sentinel)) != "credential-sentinel" {
			t.Fatal("existing output overwritten or removed")
		}
		if kind == "empty-directory" {
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 0 {
				t.Fatal("existing directory mutated")
			}
		}
	}
}

func TestConcurrentCreateOnlyOneGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generation")
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Create(path, "localhost", 1)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected one exclusive creator, got %d", successes)
	}
	if _, err := Inspect(filepath.Join(path, "cert.pem"), filepath.Join(path, "key.pem"), "localhost"); err != nil {
		t.Fatal(err)
	}
}

func TestInspectRejectsInvalidInputs(t *testing.T) {
	for _, name := range []string{"wrong-host", "mismatched-key", "oversized-cert", "oversized-key", "malformed-cert", "malformed-key", "extra-key-content", "directory", "hardlink"} {
		t.Run(name, func(t *testing.T) {
			info, err := Create(filepath.Join(t.TempDir(), "generation"), "localhost", 1)
			if err != nil {
				t.Fatal(err)
			}
			host := "localhost"
			switch name {
			case "wrong-host":
				host = "wrong.example.com"
			case "mismatched-key":
				other, err := Create(filepath.Join(t.TempDir(), "other"), "localhost", 1)
				if err != nil {
					t.Fatal(err)
				}
				info.KeyFile = other.KeyFile
			case "oversized-cert":
				writeTestFile(t, info.CertFile, bytes.Repeat([]byte("x"), maxCertificateBytes+1))
			case "oversized-key":
				writeTestFile(t, info.KeyFile, bytes.Repeat([]byte("x"), maxPrivateKeyBytes+1))
			case "malformed-cert":
				writeTestFile(t, info.CertFile, []byte("credential-sentinel"))
			case "malformed-key":
				writeTestFile(t, info.KeyFile, []byte("credential-sentinel"))
			case "extra-key-content":
				writeTestFile(t, info.KeyFile, append(readTestFile(t, info.KeyFile), []byte("credential-sentinel")...))
			case "directory":
				info.KeyFile = filepath.Dir(info.KeyFile)
			case "hardlink":
				if err := os.Link(info.KeyFile, filepath.Join(filepath.Dir(info.KeyFile), "linked-key")); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
			}
			got, err := Inspect(info.CertFile, info.KeyFile, host)
			assertSafeError(t, err)
			if got != (Info{}) {
				t.Fatal("failed inspection returned metadata")
			}
		})
	}
}

func TestRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	info, err := Create(filepath.Join(root, "generation"), "localhost", 1)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-key")
	if err := os.Symlink(info.KeyFile, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err = Inspect(info.CertFile, link, "localhost")
	assertSafeError(t, err)
	parent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(filepath.Dir(info.KeyFile), parent); err != nil {
		t.Fatal(err)
	}
	_, err = Inspect(filepath.Join(parent, "cert.pem"), filepath.Join(parent, "key.pem"), "localhost")
	assertSafeError(t, err)
	_, err = Create(filepath.Join(parent, "new"), "localhost", 1)
	assertSafeError(t, err)
	if _, err := os.Lstat(filepath.Join(filepath.Dir(info.KeyFile), "new")); !os.IsNotExist(err) {
		t.Fatal("creation followed linked parent")
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	if err == nil || strings.Contains(err.Error(), "credential-sentinel") || !strings.ContainsFunc(err.Error(), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		t.Fatalf("expected a Chinese error without source contents, got %v", err)
	}
}

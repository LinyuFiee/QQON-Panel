package tlsutil

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"panel/internal/platform"
)

func TestInspectCertificateConstraints(t *testing.T) {
	for _, name := range []string{"expired", "future", "client-only", "no-signing", "common-name-only", "weak-rsa", "bad-signature", "broken-chain", "too-many-certificates"} {
		t.Run(name, func(t *testing.T) {
			cert, key := testCertificate(t)
			switch name {
			case "expired":
				cert.NotBefore, cert.NotAfter = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
			case "future":
				cert.NotBefore, cert.NotAfter = time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)
			case "client-only":
				cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			case "no-signing":
				cert.KeyUsage = x509.KeyUsageKeyEncipherment
			case "common-name-only":
				cert.DNSNames = nil
			case "weak-rsa":
				weak, err := rsa.GenerateKey(rand.Reader, 1024)
				if err != nil {
					t.Fatal(err)
				}
				key = weak
			}
			certPEM, keyPEM := encodeTestPair(t, cert, cert, key, key)
			switch name {
			case "bad-signature":
				block, _ := pem.Decode(certPEM)
				block.Bytes[len(block.Bytes)-1] ^= 1
				certPEM = pem.EncodeToMemory(block)
			case "broken-chain":
				other, otherKey := testCertificate(t)
				otherPEM, _ := encodeTestPair(t, other, other, otherKey, otherKey)
				certPEM = append(certPEM, otherPEM...)
			case "too-many-certificates":
				certPEM = bytes.Repeat(certPEM, maxChainLength+1)
			}
			certPath, keyPath := testPairFiles(t, certPEM, keyPEM)
			_, err := Inspect(certPath, keyPath, "localhost")
			assertSafeError(t, err)
		})
	}
}

func TestInspectLeafFirstChain(t *testing.T) {
	root, rootKey := testCertificate(t)
	root.Subject.CommonName = "temporary test root"
	root.IsCA = true
	root.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	root.DNSNames = nil
	rootPEM, _ := encodeTestPair(t, root, root, rootKey, rootKey)
	leaf, leafKey := testCertificate(t)
	leafPEM, keyPEM := encodeTestPair(t, leaf, root, leafKey, rootKey)
	certPath, keyPath := testPairFiles(t, append(leafPEM, rootPEM...), keyPEM)
	if _, err := Inspect(certPath, keyPath, "localhost"); err != nil {
		t.Fatal("valid user-supplied chain rejected:", err)
	}
	writeTestFile(t, certPath, append(rootPEM, leafPEM...))
	_, err := Inspect(certPath, keyPath, "localhost")
	assertSafeError(t, err)
}

func testCertificate(t *testing.T) (*x509.Certificate, crypto.Signer) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &x509.Certificate{SerialNumber: big.NewInt(23), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, DNSNames: []string{"localhost"}}, key
}

func encodeTestPair(t *testing.T, cert, parent *x509.Certificate, key, signer crypto.Signer) ([]byte, []byte) {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, cert, parent, key.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func testPairFiles(t *testing.T, cert, key []byte) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := platform.PreparePrivateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeTestFile(t, certPath, cert)
	writeTestFile(t, keyPath, key)
	return certPath, keyPath
}

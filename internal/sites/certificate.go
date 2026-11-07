package sites

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"time"
)

const maxCertBytes = 64 << 10
const maxKeyBytes = 16 << 10

func validateCertificate(certPEM, keyPEM []byte, domains []string) error {
	bad := failure(400, "invalid_certificate", "证书须为有效的叶证书优先 PEM 链，匹配所有网站域名和未加密私钥（证书≤64 KiB、私钥≤16 KiB）")
	if len(certPEM) == 0 || len(certPEM) > maxCertBytes || len(keyPEM) == 0 || len(keyPEM) > maxKeyBytes || len(domains) == 0 {
		return bad
	}
	var chain []*x509.Certificate
	for rest := bytes.TrimSpace(certPEM); len(rest) != 0; {
		if len(chain) >= 8 || !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return bad
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return bad
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !strongKey(cert.PublicKey) {
			return bad
		}
		chain = append(chain, cert)
		rest = bytes.TrimSpace(remaining)
	}
	if len(chain) == 0 {
		return bad
	}
	key, rest := pem.Decode(bytes.TrimSpace(keyPEM))
	if key == nil || len(key.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || (key.Type != "PRIVATE KEY" && key.Type != "EC PRIVATE KEY" && key.Type != "RSA PRIVATE KEY") || !bytes.HasPrefix(bytes.TrimSpace(keyPEM), []byte("-----BEGIN "+key.Type+"-----")) {
		return bad
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return bad
	}
	now := time.Now()
	for i, cert := range chain {
		if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return bad
		}
		if i+1 < len(chain) && cert.CheckSignatureFrom(chain[i+1]) != nil {
			return bad
		}
	}
	leaf, last := chain[0], chain[len(chain)-1]
	if leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return bad
	}
	if bytes.Equal(last.RawSubject, last.RawIssuer) && last.CheckSignature(last.SignatureAlgorithm, last.RawTBSCertificate, last.Signature) != nil {
		return bad
	}
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(last)
	for i := 1; i+1 < len(chain); i++ {
		intermediates.AddCert(chain[i])
	}
	// Supplied final certificate is only a local anchor. No CA trust claim,
	// AIA fetch, external validation request or ACME operation is made.
	for _, domain := range domains {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: domain, Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return bad
		}
	}
	return nil
}

func strongKey(key any) bool {
	switch k := key.(type) {
	case *ecdsa.PublicKey:
		return k.Curve != nil && k.Curve.Params().BitSize >= 256 && k.Curve.Params().BitSize <= 521
	case *rsa.PublicKey:
		return k.N != nil && k.N.BitLen() >= 2048 && k.N.BitLen() <= 8192 && k.E >= 3 && k.E%2 == 1
	case ed25519.PublicKey:
		return len(k) == ed25519.PublicKeySize
	}
	return false
}

func (s *Service) certRoot(r record, create bool) (*os.Root, error) {
	if !idPattern.MatchString(r.Site.ID) || !idPattern.MatchString(r.CertGeneration) {
		return nil, errUnsafe
	}
	root, err := absoluteRoot(s.certPath, create, 0700, true, s.adapters.strict)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	site, err := descend(root, r.Site.ID, create, 0700, s.adapters.strict)
	if err != nil {
		return nil, err
	}
	defer site.Close()
	if create {
		if _, err := site.Lstat(r.CertGeneration); !errors.Is(err, os.ErrNotExist) {
			return nil, errConflict
		}
	}
	return descend(site, r.CertGeneration, create, 0700, s.adapters.strict)
}

func (s *Service) storeCertificate(r *record, cert, key []byte) error {
	if err := validateCertificate(cert, key, r.Site.Domains); err != nil {
		return err
	}
	generation, err := randomID()
	if err != nil {
		return err
	}
	r.CertGeneration, r.CertHash = generation, digest(cert)
	root, err := s.certRoot(*r, true)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := atomicFile(root, "key.pem", key, 0600, "", maxKeyBytes, s.adapters.strict); err != nil {
		return err
	}
	return atomicFile(root, "cert.pem", cert, 0600, "", maxCertBytes, s.adapters.strict)
}

func (s *Service) readCertificate(r record) ([]byte, error) {
	root, err := s.certRoot(r, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := readFile(root, "cert.pem", maxCertBytes, s.adapters.strict)
	if err != nil {
		return nil, err
	}
	if digest(data) != r.CertHash {
		return nil, errConflict
	}
	return data, nil
}

func (s *Service) checkCertificate(r record) error {
	root, err := s.certRoot(r, false)
	if err != nil {
		return err
	}
	defer root.Close()
	cert, err := readFile(root, "cert.pem", maxCertBytes, s.adapters.strict)
	if err != nil {
		return err
	}
	if digest(cert) != r.CertHash {
		return errConflict
	}
	key, err := readFile(root, "key.pem", maxKeyBytes, s.adapters.strict)
	if err != nil {
		return err
	}
	defer clear(key)
	return validateCertificate(cert, key, r.Site.Domains)
}

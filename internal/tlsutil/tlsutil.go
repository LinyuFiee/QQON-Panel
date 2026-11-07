// Package tlsutil creates immutable certificate generations and inspects PEM
// inputs. Inspection checks usability, not public certification-authority trust.
package tlsutil

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"panel/internal/platform"
)

const (
	maxCertificateBytes = 1 << 20
	maxPrivateKeyBytes  = 64 << 10
	maxChainLength      = 16
)

// Info deliberately contains no private-key material or source-file contents.
type Info struct {
	Host        string `json:"host"`
	Fingerprint string `json:"fingerprint"`
	NotAfter    string `json:"notAfter"`
	CertFile    string `json:"certFile"`
	KeyFile     string `json:"keyFile"`
}

// Create writes cert.pem and key.pem in a NEW absolute directory. It never
// replaces or deletes an existing target, including on failure. A failed write
// can leave an incomplete, private generation; callers must choose a new path.
func Create(output, host string, days int) (Info, error) {
	host, err := canonicalHost(host)
	if err != nil {
		return Info{}, err
	}
	if days < 1 || days > 397 {
		return Info{}, errors.New("证书有效天数必须在 1 至 397 之间")
	}
	output, err = absolutePath(output)
	if err != nil {
		return Info{}, err
	}
	if err := checkPath(filepath.Dir(output)); err != nil {
		return Info{}, err
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return Info{}, errors.New("证书输出目录必须是全新目录，不能覆盖已有目标")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Info{}, errors.New("无法生成证书私钥")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return Info{}, errors.New("无法生成证书序列号")
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	now := time.Now().UTC().Truncate(time.Second)
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(time.Duration(days) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return Info{}, errors.New("无法生成自签名证书")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Info{}, errors.New("无法编码证书私钥")
	}
	if err := createPrivateDirectory(output); err != nil {
		return Info{}, err
	}
	if err := platform.PreparePrivateDirectory(output); err != nil {
		return Info{}, errors.New("证书输出目录的私有权限验证失败")
	}
	certPath, keyPath := filepath.Join(output, "cert.pem"), filepath.Join(output, "key.pem")
	if err := writeNewFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})); err != nil {
		return Info{}, err
	}
	if err := writeNewFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return Info{}, err
	}
	return certificateInfo(host, certPath, keyPath, der, template.NotAfter), nil
}

// Inspect accepts a leaf-first PEM chain and an unencrypted PEM private key.
// Installed service-readable keys (such as root:service 0640) are intentional:
// unlike generation, inspection does not require current-user full control.
func Inspect(certPath, keyPath, host string) (Info, error) {
	host, err := canonicalHost(host)
	if err != nil {
		return Info{}, err
	}
	certPath, err = absolutePath(certPath)
	if err != nil {
		return Info{}, err
	}
	keyPath, err = absolutePath(keyPath)
	if err != nil {
		return Info{}, err
	}
	certPEM, err := readBoundedFile(certPath, maxCertificateBytes, false)
	if err != nil {
		return Info{}, err
	}
	keyPEM, err := readBoundedFile(keyPath, maxPrivateKeyBytes, true)
	if err != nil {
		return Info{}, err
	}
	chain, err := parseChain(certPEM)
	if err != nil {
		return Info{}, err
	}
	block, rest := pem.Decode(bytes.TrimSpace(keyPEM))
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 ||
		!bytes.HasPrefix(bytes.TrimSpace(keyPEM), []byte("-----BEGIN "+block.Type+"-----")) ||
		(block.Type != "PRIVATE KEY" && block.Type != "EC PRIVATE KEY" && block.Type != "RSA PRIVATE KEY") {
		return Info{}, errors.New("私钥必须是单个未加密的 PEM 私钥")
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return Info{}, errors.New("证书与私钥不匹配，或私钥格式无效")
	}
	now := time.Now()
	for i, cert := range chain {
		if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return Info{}, errors.New("证书或证书链已过期，或尚未生效")
		}
		if !strongPublicKey(cert.PublicKey) {
			return Info{}, errors.New("证书使用了不支持或强度不足的密钥")
		}
		if i+1 < len(chain) && cert.CheckSignatureFrom(chain[i+1]) != nil {
			return Info{}, errors.New("证书链的顺序、签名或颁发者约束无效")
		}
	}
	leaf := chain[0]
	if err := leaf.VerifyHostname(host); err != nil {
		return Info{}, errors.New("证书的 SAN 与指定主机不匹配")
	}
	if leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return Info{}, errors.New("证书的密钥用途不允许 TLS 服务器签名")
	}
	last := chain[len(chain)-1]
	if bytes.Equal(last.RawSubject, last.RawIssuer) && last.CheckSignature(last.SignatureAlgorithm, last.RawTBSCertificate, last.Signature) != nil {
		return Info{}, errors.New("自签名证书的签名无效")
	}
	// The final supplied certificate is only a local validation anchor. This
	// verifies SAN, EKU and chain constraints without claiming public trust or
	// fetching AIA/CRL URLs from untrusted certificate contents.
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(last)
	for i := 1; i+1 < len(chain); i++ {
		intermediates.AddCert(chain[i])
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: host, Roots: roots, Intermediates: intermediates,
		CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return Info{}, errors.New("证书链或服务器身份验证用途无效")
	}
	return certificateInfo(host, certPath, keyPath, leaf.Raw, leaf.NotAfter), nil
}

func certificateInfo(host, certPath, keyPath string, der []byte, expiry time.Time) Info {
	fingerprint := sha256.Sum256(der)
	return Info{Host: host, Fingerprint: hex.EncodeToString(fingerprint[:]),
		NotAfter: expiry.UTC().Format(time.RFC3339), CertFile: certPath, KeyFile: keyPath}
}

func canonicalHost(host string) (string, error) {
	bad := errors.New("证书主机必须是有效的 IPv4 地址或 ASCII 域名，不能包含端口或 URL")
	if host == "" || host != strings.TrimSpace(host) {
		return "", bad
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if ip, err := netip.ParseAddr(host); err == nil {
		if !ip.Is4() {
			return "", bad
		}
		return ip.String(), nil
	}
	if len(host) == 0 || len(host) > 253 || strings.Trim(host, "0123456789.") == "" {
		return "", bad
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", bad
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", bad
			}
		}
	}
	return host, nil
}

func absolutePath(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || len(path) > 32767 || !validNativePath(path) {
		return "", errors.New("证书路径必须是明确的本地绝对路径")
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) == path {
		return "", errors.New("证书路径不能是磁盘根目录")
	}
	return path, nil
}

func checkPath(path string) error {
	for {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !safeNativeComponent(path) {
			return errors.New("证书路径不存在，或包含链接及不安全的路径组件")
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func readBoundedFile(path string, limit int64, private bool) ([]byte, error) {
	if err := checkPath(path); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		return nil, errors.New("证书或私钥必须是大小受限的普通非空文件")
	}
	file, err := openReadFile(path)
	if err != nil {
		return nil, errors.New("无法安全读取证书或私钥文件")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() > limit {
		return nil, errors.New("证书或私钥文件在打开时发生变化")
	}
	if err := checkReadableFile(file, opened, private); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("证书或私钥文件读取失败，或超过大小限制")
	}
	return data, nil
}

func writeNewFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("无法创建证书文件，已有文件不会被覆盖")
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return errors.New("无法设置证书文件的私有权限")
	}
	if err := platform.CheckPrivateFile(path); err != nil {
		return errors.New("证书文件未正确继承私有权限")
	}
	if n, err := file.Write(data); err != nil || n != len(data) {
		return errors.New("无法完整写入证书文件")
	}
	if err := file.Sync(); err != nil {
		return errors.New("无法同步证书文件")
	}
	if err := file.Close(); err != nil {
		return errors.New("无法关闭证书文件")
	}
	return nil
}

func parseChain(data []byte) ([]*x509.Certificate, error) {
	var chain []*x509.Certificate
	for rest := bytes.TrimSpace(data); len(rest) > 0; {
		if len(chain) >= maxChainLength || !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("证书链必须是大小受限且不含额外内容的 PEM 证书")
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errors.New("证书 PEM 格式无效")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("无法解析 X.509 证书")
		}
		chain = append(chain, cert)
		rest = bytes.TrimSpace(remaining)
	}
	if len(chain) == 0 {
		return nil, errors.New("证书文件中没有 PEM 证书")
	}
	return chain, nil
}

func strongPublicKey(key any) bool {
	switch key := key.(type) {
	case *ecdsa.PublicKey:
		return key.Curve != nil && key.Curve.Params().BitSize >= 256 && key.Curve.Params().BitSize <= 521
	case *rsa.PublicKey:
		return key.N != nil && key.N.BitLen() >= 2048 && key.N.BitLen() <= 8192 && key.E >= 3 && key.E%2 == 1
	case ed25519.PublicKey:
		return len(key) == ed25519.PublicKeySize
	default:
		return false
	}
}

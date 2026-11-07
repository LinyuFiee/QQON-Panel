// Package sites owns only panel-generated Nginx virtual hosts and their webroots.
package sites

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"panel/internal/apps"
	"panel/internal/jobrunner"
)

const (
	UploadLimitBytes = 2 << 20
	EditLimitBytes   = 256 << 10
	maxSites         = 128
	maxStateBytes    = 2 << 20
	maxConfigBytes   = 32 << 10
	webRootPath      = "/srv/qqon-sites"
	configRootPath   = "/etc/nginx/conf.d"
)

// Options intentionally exposes no command, host-directory or authorization bypass.
// Permission must check both current identity and independently recorded consent.
type Options struct {
	DataDir    string
	Jobs       *jobrunner.Runner
	Permission func() error
	Runtime    func(context.Context) (apps.RuntimeInfo, error)
	Audit      func(string, string)
	PanelHost  string
	PanelPort  int
}

type Site struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Domains      []string  `json:"domains"`
	Type         string    `json:"type"`
	Port         int       `json:"port"`
	Enabled      bool      `json:"enabled"`
	Upstream     string    `json:"upstream,omitempty"`
	PHPRuntime   string    `json:"phpRuntime,omitempty"`
	Root         string    `json:"root"`
	Revision     uint64    `json:"revision"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	TLSEnabled   bool      `json:"tlsEnabled"`
	URL          string    `json:"url"`
	ConfigStatus string    `json:"configStatus"`
}

type siteInput struct {
	Name       string   `json:"name"`
	Domains    []string `json:"domains"`
	Type       string   `json:"type"`
	Port       int      `json:"port"`
	Enabled    bool     `json:"enabled"`
	Upstream   string   `json:"upstream,omitempty"`
	PHPRuntime string   `json:"phpRuntime,omitempty"`
}

// Records and the recovery journal contain descriptors and hashes, never PEM,
// uploaded bytes, PHP source, command output or arbitrary Nginx configuration.
type record struct {
	Site           Site   `json:"site"`
	Token          string `json:"token"`
	ConfigHash     string `json:"configHash"`
	PHPSocket      string `json:"phpSocket,omitempty"`
	CertGeneration string `json:"certGeneration,omitempty"`
	CertHash       string `json:"certHash,omitempty"`
	ProxyCA        string `json:"proxyCA,omitempty"`
}

type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }
func failure(status int, code, message string) error {
	return &apiError{status, code, message}
}

var (
	errUnsafe      = errors.New("sites: unsafe filesystem object")
	errConflict    = failure(http.StatusConflict, "conflict", "目标已改变或不是面板拥有的配置；未覆盖现有内容")
	errDenied      = failure(http.StatusForbidden, "permission_denied", "网站管理需要最高系统权限及独立管理授权")
	errUnavailable = failure(http.StatusServiceUnavailable, "unavailable", "网站服务暂不可用")
	idPattern      = regexp.MustCompile(`^[a-f0-9]{32}$`)
	hashPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	phpPattern     = regexp.MustCompile(`^php(?:[0-9]{1,2}\.[0-9]{1,2})?-fpm$`)
)

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("sites: cannot generate identifier")
	}
	return hex.EncodeToString(b[:]), nil
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func canonicalHost(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 254 {
		return "", errors.New("invalid host")
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if ip, err := netip.ParseAddr(value); err == nil {
		if ip.Zone() != "" {
			return "", errors.New("invalid host")
		}
		return ip.Unmap().String(), nil
	}
	if len(value) == 0 || len(value) > 253 || strings.Trim(value, "0123456789.") == "" {
		return "", errors.New("invalid host")
	}
	for _, part := range strings.Split(value, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return "", errors.New("invalid host")
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", errors.New("invalid host")
			}
		}
	}
	return value, nil
}

func localHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && (ip.Unmap().IsLoopback() || ip.IsUnspecified())
}

func overlapsHost(a, b string) bool {
	if a == b {
		return true
	}
	if _, err := netip.ParseAddr(a); err == nil {
		return false
	}
	if _, err := netip.ParseAddr(b); err == nil {
		return false
	}
	return strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a)
}

func (s *Service) validateInput(in siteInput) (siteInput, error) {
	bad := failure(http.StatusBadRequest, "invalid_site", "网站名称、域名、类型或端口无效")
	if !utf8.ValidString(in.Name) || utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 64 || strings.TrimSpace(in.Name) != in.Name {
		return in, bad
	}
	for _, c := range in.Name {
		if unicode.IsControl(c) || c == '<' || c == '>' {
			return in, bad
		}
	}
	if len(in.Domains) < 1 || len(in.Domains) > 10 || s.panelHost == "" {
		return in, bad
	}
	in.Domains = append([]string(nil), in.Domains...)
	seen := make(map[string]bool, len(in.Domains))
	for i, domain := range in.Domains {
		host, err := canonicalHost(domain)
		if err != nil || seen[host] {
			return in, bad
		}
		if localHost(host) || overlapsHost(host, s.panelHost) {
			return in, failure(http.StatusBadRequest, "panel_host_overlap", "网站域名不能与面板 Cookie 主机、其父子域或本机回环主机重叠")
		}
		if ip, err := netip.ParseAddr(host); err == nil && ip.IsMulticast() {
			return in, bad
		}
		in.Domains[i], seen[host] = host, true
	}
	if (in.Port != 80 && in.Port != 443 && (in.Port < 1024 || in.Port > 65535)) || in.Port == s.options.PanelPort {
		return in, failure(http.StatusBadRequest, "invalid_port", "网站端口仅允许 80、443 或 1024–65535，且不能占用面板端口")
	}
	switch in.Type {
	case "static":
		if in.Upstream != "" || in.PHPRuntime != "" {
			return in, bad
		}
	case "proxy":
		if in.PHPRuntime != "" {
			return in, bad
		}
		upstream, err := s.validateUpstream(in.Upstream)
		if err != nil {
			return in, err
		}
		in.Upstream = upstream
	case "php":
		if in.Upstream != "" || !phpPattern.MatchString(in.PHPRuntime) {
			return in, bad
		}
	default:
		return in, bad
	}
	return in, nil
}

func (s *Service) validateUpstream(raw string) (string, error) {
	bad := failure(http.StatusBadRequest, "invalid_upstream", "代理目标必须是无凭据、查询和片段的 HTTP(S) URL，且不能指向面板或本机管理端口")
	if len(raw) == 0 || len(raw) > 2048 || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\x00\r\n\\\"'$<>{}#") {
		return "", bad
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", bad
	}
	host, err := canonicalHost(u.Hostname())
	if err != nil {
		return "", bad
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != u.Port() {
			return "", bad
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", bad
	}
	// No DNS resolution or probing of user-supplied upstreams takes place here.
	// Loopback proxying to ordinary application ports is intentional admin power.
	if (overlapsHost(host, s.panelHost) && !localHost(host)) || port == s.options.PanelPort || (localHost(host) && (port == 8080 || port == 8443 || port == 5173 || port == 9090)) {
		return "", bad
	}
	if ip, err := netip.ParseAddr(host); err == nil && (ip.IsUnspecified() || ip.IsMulticast()) {
		return "", bad
	}
	for _, c := range u.Path {
		if unicode.IsControl(c) {
			return "", bad
		}
	}
	explicitPort := u.Port()
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	if explicitPort != "" {
		u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return u.String(), nil
}

func knownPHPSocket(id, socket string) bool {
	if !phpPattern.MatchString(id) {
		return false
	}
	if id == "php-fpm" {
		return socket == "/run/php-fpm/www.sock"
	}
	return socket == "/run/php/"+id+".sock"
}

func knownNginxBinary(binary string) bool {
	return binary == "/usr/sbin/nginx" || binary == "/sbin/nginx" || binary == "/usr/local/sbin/nginx"
}

func knownCA(path string) bool {
	return path == "/etc/ssl/certs/ca-certificates.crt" || path == "/etc/pki/tls/certs/ca-bundle.crt" || path == "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem"
}

func publicSite(r record) Site {
	out := r.Site
	out.Domains = append([]string(nil), out.Domains...)
	return out
}

func inputFromSite(site Site) siteInput {
	return siteInput{site.Name, site.Domains, site.Type, site.Port, site.Enabled, site.Upstream, site.PHPRuntime}
}

func (s *Service) setDerived(r *record) {
	r.Site.Root = filepath.Join(s.adapters.webRoot, r.Site.ID)
	scheme := "http"
	if r.Site.TLSEnabled {
		scheme = "https"
	}
	host := r.Site.Domains[0]
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if (scheme == "http" && r.Site.Port != 80) || (scheme == "https" && r.Site.Port != 443) {
		host = net.JoinHostPort(r.Site.Domains[0], strconv.Itoa(r.Site.Port))
	}
	r.Site.URL = scheme + "://" + host + "/"
	r.Site.ConfigStatus = "active"
	if !r.Site.Enabled {
		r.Site.ConfigStatus = "disabled"
	}
}

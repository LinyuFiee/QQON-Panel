package sites

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// A reload exit code alone does not prove that a deleted server disappeared.
// Probe its old listener without ever following redirects or contacting DNS.
// TLS removal uses a probe-only connection with no cookies or credentials: an
// unrelated default certificate must not mask the presence of the old marker.
func (s *Service) verifyRemovedHTTP(ctx context.Context, r record) error {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 8 << 10, ResponseHeaderTimeout: time.Second, TLSHandshakeTimeout: time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", portString(r.Site.Port)))
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, // fixed-loopback, marker-absence probe only
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	scheme := "http"
	if r.Site.TLSEnabled {
		scheme = "https"
	}
	origin := scheme + "://" + net.JoinHostPort(r.Site.Domains[0], portString(r.Site.Port)) + "/.__qqon_health"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin, nil)
		if err != nil {
			return errUnsafe
		}
		res, err := client.Do(req)
		if err != nil {
			if nativeConnectionRefused(err) {
				return nil
			}
		} else {
			data, readErr := io.ReadAll(io.LimitReader(res.Body, 4097))
			res.Body.Close()
			if readErr == nil && len(data) <= 4096 && res.Header.Get("X-QQON-Site") != r.Site.ID && !strings.Contains(string(data), "qqon:"+r.Site.ID+":") {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return failure(503, "http_removal_unverified", "未能通过固定本机连接确认旧网站已卸载；将恢复上一版配置")
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// Verification never dials a client-provided hostname or follows a redirect. The
// domain is used only as the Nginx Host/SNI identity on a fixed loopback socket.
func (s *Service) verifyHTTP(ctx context.Context, r record, fresh bool) error {
	transport := &http.Transport{
		Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", portString(r.Site.Port)))
		},
		ResponseHeaderTimeout: time.Second, TLSHandshakeTimeout: time.Second, MaxResponseHeaderBytes: 8 << 10,
	}
	if r.Site.TLSEnabled {
		pem, err := s.readCertificate(r)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errUnsafe
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: r.Site.Domains[0]}
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	scheme := "http"
	if r.Site.TLSEnabled {
		scheme = "https"
	}
	origin := scheme + "://" + net.JoinHostPort(r.Site.Domains[0], portString(r.Site.Port))
	probe := func(path string, status int, expected string, marker bool) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
		if err != nil {
			return false
		}
		req.Host = net.JoinHostPort(r.Site.Domains[0], portString(r.Site.Port))
		res, err := client.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 4097))
		if err != nil || len(data) > 4096 || res.StatusCode != status {
			return false
		}
		if marker && res.Header.Get("X-QQON-Site") != r.Site.ID {
			return false
		}
		if expected != "" && (marker && string(data) != expected || !marker && (r.Site.Type == "php" && strings.TrimSpace(string(data)) != expected || r.Site.Type != "php" && !strings.Contains(string(data), expected))) {
			return false
		}
		return true
	}
	for {
		ok := probe("/.__qqon_health", 200, "qqon:"+r.Site.ID+":"+r.Token+"\n", true)
		if ok && !r.Site.Enabled {
			ok = probe("/", 503, "", false)
		}
		if ok && fresh && r.Site.Enabled && r.Site.Type != "proxy" {
			path, expected := "/index.html", "QQON static "+r.Site.ID
			if r.Site.Type == "php" {
				path, expected = "/index.php", "QQON PHP "+r.Site.ID
			}
			ok = probe(path, 200, expected, false)
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return failure(503, "http_verification_failed", "本机 HTTP 验证未确认新站点标识、禁用状态或初始内容；不会按成功保存")
		case <-time.After(150 * time.Millisecond):
		}
	}
}

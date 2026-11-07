package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Origins       []string
	MaxConcurrent int
	Secure        bool
}

// Guard protects even unauthenticated setup/login and static resource requests.
func Guard(next http.Handler, options Options) (http.Handler, error) {
	if options.MaxConcurrent < 2 || options.MaxConcurrent > 256 {
		return nil, errors.New("maximum concurrent requests must be between 2 and 256")
	}
	if len(options.Origins) == 0 {
		return nil, errors.New("at least one explicit allowed origin is required")
	}
	origins := make(map[string]bool, len(options.Origins))
	hosts := make(map[string]bool, len(options.Origins))
	for _, origin := range options.Origins {
		normalized, err := NormalizeOrigin(origin)
		if err != nil {
			return nil, err
		}
		u, _ := url.Parse(normalized)
		origins[normalized] = true
		hosts[u.Host] = true
	}
	requests := make(chan struct{}, options.MaxConcurrent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if options.Secure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if len(r.RequestURI) > 4096 {
			Error(w, http.StatusRequestURITooLong, "uri_too_long", "请求地址过长。")
			return
		}
		host := strings.ToLower(r.Host)
		requestScheme := "http"
		if options.Secure {
			requestScheme = "https"
		}
		if parsed, err := url.Parse(requestScheme + "://" + host); err == nil {
			host = canonicalHost(parsed)
		}
		if !hosts[host] {
			Error(w, http.StatusForbidden, "invalid_host", "此地址未获准访问面板。")
			return
		}
		if fetchSite := r.Header.Get("Sec-Fetch-Site"); fetchSite != "" && fetchSite != "same-origin" && fetchSite != "none" {
			Error(w, http.StatusForbidden, "cross_origin", "禁止跨站访问面板接口。")
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			normalized, err := NormalizeOrigin(origin)
			if err != nil || len(r.Header.Values("Origin")) != 1 || !origins[normalized] {
				Error(w, http.StatusForbidden, "cross_origin", "请求来源与面板不匹配。")
				return
			}
			u, _ := url.Parse(normalized)
			if u.Host != host {
				Error(w, http.StatusForbidden, "cross_origin", "请求来源与访问地址不一致。")
				return
			}
		} else if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			Error(w, http.StatusForbidden, "origin_required", "修改操作必须携带正确的 Origin 请求头。")
			return
		}
		select {
		case requests <- struct{}{}:
			defer func() { <-requests }()
		default:
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Connection", "close")
			Error(w, http.StatusServiceUnavailable, "server_busy", "面板请求繁忙，请稍后重试。")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, requestBodyLimit(r))
		}
		next.ServeHTTP(w, r)
	}), nil
}

func NormalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid panel origin %q: expected scheme://host[:port]", raw)
	}
	if strings.ContainsAny(u.Host, " \t\r\n\\") {
		return "", errors.New("invalid origin host")
	}
	return u.Scheme + "://" + canonicalHost(u), nil
}

func canonicalHost(u *url.URL) string {
	host := strings.ToLower(u.Host)
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return host
}

func Static(files fs.FS) (http.Handler, error) {
	types := map[string]string{"index.html": "text/html; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "api.js": "text/javascript; charset=utf-8", "styles.css": "text/css; charset=utf-8"}
	type asset struct {
		body []byte
		mime string
	}
	assets := make(map[string]asset, len(types))
	for name, mime := range types {
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("embedded asset %s: %w", name, err)
		}
		assets["/"+name] = asset{body: body, mime: mime}
	}
	assets["/"] = assets["/index.html"]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持该请求方法。")
			return
		}
		value, ok := assets[r.URL.Path]
		if !ok {
			Error(w, http.StatusNotFound, "not_found", "页面或资源不存在。")
			return
		}
		w.Header().Set("Content-Type", value.mime)
		w.Header().Set("Content-Length", fmt.Sprint(len(value.body)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(value.body)
		}
	}), nil
}

func Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

type limitedListener struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
}

type limitedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

// LimitListener caps accepted connections before net/http creates handlers.
func LimitListener(listener net.Listener, limit int) net.Listener {
	return &limitedListener{Listener: listener, slots: make(chan struct{}, limit), done: make(chan struct{})}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitedConn{Conn: conn, release: func() { <-l.slots }}, nil
}

func (l *limitedListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func HTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

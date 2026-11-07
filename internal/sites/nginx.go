package sites

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"panel/internal/jobrunner"
)

func nginxQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "$", `\$`)
	return `"` + value + `"`
}

func (s *Service) render(r record) ([]byte, error) {
	if !idPattern.MatchString(r.Site.ID) || !idPattern.MatchString(r.Token) {
		return nil, errUnsafe
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# qqon-managed-site-v1 %s\n# token %s\nserver {\n    listen %d", r.Site.ID, r.Token, r.Site.Port)
	if r.Site.TLSEnabled {
		b.WriteString(" ssl")
	}
	b.WriteString(";\n    server_name")
	for _, domain := range r.Site.Domains {
		b.WriteByte(' ')
		b.WriteString(nginxQuote(domain))
	}
	b.WriteString(";\n")
	// Nginx's first server on a listen address is also its default. Reject
	// unlisted Host values even when this is the only vhost; otherwise a request
	// carrying the panel's cookie Host could receive untrusted site content.
	var hosts []string
	for _, domain := range r.Site.Domains {
		hosts = append(hosts, regexp.QuoteMeta(domain))
	}
	fmt.Fprintf(&b, "    if ($host !~* %s) { return 421; }\n", nginxQuote("^(?:"+strings.Join(hosts, "|")+")\\z"))
	fmt.Fprintf(&b, "    root %s;\n    access_log off;\n    error_log /dev/null crit;\n    autoindex off;\n    disable_symlinks on;\n    client_max_body_size 2m;\n", nginxQuote(filepath.ToSlash(r.Site.Root)))
	if r.Site.TLSEnabled {
		if !idPattern.MatchString(r.CertGeneration) {
			return nil, errUnsafe
		}
		base := filepath.ToSlash(filepath.Join(s.certPath, r.Site.ID, r.CertGeneration))
		fmt.Fprintf(&b, "    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols TLSv1.2 TLSv1.3;\n", nginxQuote(base+"/cert.pem"), nginxQuote(base+"/key.pem"))
	}
	// This exact location wins even for disabled hosts; it proves the selected
	// server block and revision, not the upstream or arbitrary file contents.
	fmt.Fprintf(&b, "    location = /.__qqon_health {\n        default_type text/plain;\n        add_header X-QQON-Site %s always;\n        add_header Cache-Control no-store always;\n        return 200 \"qqon:%s:%s\\n\";\n    }\n", nginxQuote(r.Site.ID), r.Site.ID, r.Token)
	if !r.Site.Enabled {
		b.WriteString("    location / { return 503; }\n}\n")
		return []byte(b.String()), nil
	}
	b.WriteString("    location ~ /\\. { return 404; }\n")
	switch r.Site.Type {
	case "static":
		b.WriteString("    index index.html;\n    location ~* \\.(?:php[0-9]?|phtml|phar)(?:/|$) { return 404; }\n    location / { try_files $uri $uri/ =404; }\n")
	case "proxy":
		u, err := url.Parse(r.Site.Upstream)
		if err != nil {
			return nil, errUnsafe
		}
		fmt.Fprintf(&b, "    location / {\n        proxy_pass %s;\n        proxy_http_version 1.1;\n        proxy_set_header Host $proxy_host;\n        proxy_set_header Connection \"\";\n        proxy_set_header X-Forwarded-For $remote_addr;\n        proxy_set_header X-Real-IP $remote_addr;\n        proxy_set_header X-Forwarded-Proto $scheme;\n        proxy_connect_timeout 5s;\n        proxy_read_timeout 30s;\n        proxy_send_timeout 30s;\n", nginxQuote(r.Site.Upstream))
		if u.Scheme == "https" {
			if !knownCA(r.ProxyCA) {
				return nil, errUnsafe
			}
			fmt.Fprintf(&b, "        proxy_ssl_server_name on;\n        proxy_ssl_name %s;\n        proxy_ssl_verify on;\n        proxy_ssl_verify_depth 8;\n        proxy_ssl_trusted_certificate %s;\n", nginxQuote(u.Hostname()), nginxQuote(r.ProxyCA))
		}
		b.WriteString("    }\n")
	case "php":
		if !knownPHPSocket(r.Site.PHPRuntime, r.PHPSocket) {
			return nil, errUnsafe
		}
		b.WriteString("    index index.php index.html;\n    location / { try_files $uri $uri/ =404; }\n    location ~* \\.(?:phtml|phar|php[0-9]+)(?:/|$) { return 404; }\n    location ~* \\.php$ {\n        try_files $uri =404;\n")
		fmt.Fprintf(&b, "        fastcgi_pass %s;\n", nginxQuote("unix:"+r.PHPSocket))
		// Deliberately no include of administrator-editable fastcgi snippets.
		for _, pair := range [][2]string{
			{"SCRIPT_FILENAME", "$document_root$fastcgi_script_name"}, {"SCRIPT_NAME", "$fastcgi_script_name"}, {"QUERY_STRING", "$query_string"}, {"REQUEST_METHOD", "$request_method"}, {"CONTENT_TYPE", "$content_type"}, {"CONTENT_LENGTH", "$content_length"}, {"REQUEST_URI", "$request_uri"}, {"DOCUMENT_URI", "$document_uri"}, {"DOCUMENT_ROOT", "$document_root"}, {"SERVER_PROTOCOL", "$server_protocol"}, {"REQUEST_SCHEME", "$scheme"}, {"GATEWAY_INTERFACE", "CGI/1.1"}, {"SERVER_SOFTWARE", "nginx"}, {"REMOTE_ADDR", "$remote_addr"}, {"REMOTE_PORT", "$remote_port"}, {"SERVER_ADDR", "$server_addr"}, {"SERVER_PORT", "$server_port"}, {"SERVER_NAME", "$server_name"}, {"REDIRECT_STATUS", "200"}, {"HTTP_PROXY", `""`},
		} {
			fmt.Fprintf(&b, "        fastcgi_param %s %s;\n", pair[0], pair[1])
		}
		b.WriteString("        fastcgi_param HTTPS $https if_not_empty;\n        fastcgi_connect_timeout 5s;\n        fastcgi_read_timeout 30s;\n        fastcgi_send_timeout 30s;\n    }\n")
	default:
		return nil, errUnsafe
	}
	b.WriteString("}\n")
	if b.Len() > maxConfigBytes {
		return nil, errUnsafe
	}
	return []byte(b.String()), nil
}

func realCommand(ctx context.Context, action, nginx string) error {
	if !knownNginxBinary(nginx) {
		return errUnsafe
	}
	executable, args := nginx, []string{"-t", "-q"}
	if action == "reload" {
		executable, args = "/usr/bin/systemctl", []string{"reload", "nginx.service"}
	} else if action != "test" {
		return errUnsafe
	}
	root, err := absoluteRoot(filepath.Dir(executable), false, 0755, false, true)
	if err != nil {
		return err
	}
	defer root.Close()
	file, info, err := openNative(root, filepath.Base(executable), false, true)
	if err != nil {
		return err
	}
	file.Close()
	if info.Mode()&0111 == 0 {
		return errUnsafe
	}
	// Nginx errors can include unknown admin configuration or private paths.
	// The shared runner drains bounded output without copying it into task logs.
	return jobrunner.RunCommand(ctx, executable, args, nil)
}

func (s *Service) runCommand(ctx context.Context, action, nginx string) error {
	if err := s.permission(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if s.adapters.command == nil || s.adapters.command(ctx, action, nginx) != nil {
		if action == "test" {
			return failure(503, "nginx_test_failed", "Nginx 配置测试失败；仅恢复面板拥有的配置，不修改主配置或其它网站")
		}
		return failure(503, "nginx_reload_failed", "Nginx reload 失败；操作未被视为成功")
	}
	return nil
}

func realCABundle() (string, error) {
	for _, name := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem"} {
		root, err := absoluteRoot(filepath.Dir(name), false, 0755, false, true)
		if err != nil {
			continue
		}
		file, _, err := openNative(root, filepath.Base(name), false, true)
		root.Close()
		if err == nil {
			file.Close()
			return name, nil
		}
	}
	return "", failure(503, "proxy_trust_unavailable", "HTTPS 代理需要受支持的本机 CA 证书包")
}

func (s *Service) createWebroot(r record) error {
	base, err := absoluteRoot(s.adapters.webRoot, true, 0755, false, s.adapters.strict)
	if err != nil {
		return err
	}
	defer base.Close()
	if _, err := base.Lstat(r.Site.ID); !os.IsNotExist(err) {
		return errConflict
	}
	root, err := descend(base, r.Site.ID, true, 0755, s.adapters.strict)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := atomicFile(root, ".qqon-owned", []byte("qqon-webroot-v1:"+r.Site.ID+"\n"), 0600, "", 128, s.adapters.strict); err != nil {
		return err
	}
	name, body := "index.html", "<!doctype html><meta charset=\"utf-8\"><title>QQON website</title><h1>QQON static "+r.Site.ID+"</h1>\n"
	if r.Site.Type == "php" {
		name, body = "index.php", "<?php header('Content-Type: text/plain; charset=utf-8'); echo 'QQON PHP "+r.Site.ID+"'; ?>\n"
	}
	if r.Site.Type == "proxy" {
		return nil
	}
	return atomicFile(root, name, []byte(body), 0644, "", UploadLimitBytes, s.adapters.strict)
}

func portString(port int) string { return strconv.Itoa(port) }

package main

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"panel/internal/auth"
	"panel/internal/buildinfo"
	"panel/internal/lock"
	"panel/internal/machine"
	"panel/internal/server"
	"panel/internal/sources"
	"panel/internal/update"
)

//go:embed index.html styles.css api.js app.js
var assets embed.FS

var (
	version          = "0.4.0"
	commit           = "unknown"
	buildTime        = "unknown"
	githubRepository = "LinyuFiee/QQON-Panel"
)

func main() {
	if err := run(); err != nil {
		log.Printf("panel: %v", err)
		os.Exit(1)
	}
}

func run() error {
	defaultData := ""
	if userConfig, err := os.UserConfigDir(); err == nil {
		defaultData = filepath.Join(userConfig, "server-panel")
	}
	listen := flag.String("listen", "127.0.0.1:5173", "listen address; non-loopback requires direct TLS")
	dataDir := flag.String("data-dir", defaultData, "private account/configuration directory")
	fileRoot := flag.String("file-root", "", "explicit read-only file root, blank disables browsing")
	dockerSocket := flag.String("docker-socket", "", "explicit local Docker Unix socket, blank disables Docker")
	allowWrites := flag.Bool("allow-docker-writes", false, "enable confirmed container start/stop/restart")
	origin := flag.String("origin", "", "single external origin, for example https://panel.example.com")
	certPath := flag.String("tls-cert", "", "TLS certificate path")
	keyPath := flag.String("tls-key", "", "TLS private key path")
	memoryMiB := flag.Int64("memory-budget-mib", 64, "Go soft managed-memory limit, not an RSS hard limit")
	connections := flag.Int("max-connections", 64, "maximum accepted TCP connections (8–256)")
	requests := flag.Int("max-requests", 24, "maximum concurrent HTTP requests (4–128)")
	printVersion := flag.Bool("version", false, "print version and exit")
	printBuild := flag.Bool("build-info", false, "print JSON release identity and exit without starting services")
	repository := flag.String("github-repo", githubRepository, "public GitHub owner/repo used only for manual update checks")
	flag.Parse()
	build := buildinfo.Current(version, commit, buildTime, githubRepository)
	if *printBuild {
		return json.NewEncoder(os.Stdout).Encode(build)
	}
	if *printVersion {
		fmt.Printf("server-panel %s (%s; %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return nil
	}
	if *dataDir == "" {
		return errors.New("no default user configuration directory; supply an explicit -data-dir")
	}
	if _, err := update.ParseVersion(version); err != nil {
		return fmt.Errorf("invalid build version: %w", err)
	}
	if *repository != "" {
		if err := update.ValidateRepository(*repository); err != nil {
			return err
		}
	}
	if *memoryMiB < 32 || *memoryMiB > 512 || *connections < 8 || *connections > 256 || *requests < 4 || *requests > 128 {
		return errors.New("invalid resource limits: memory 32–512 MiB, connections 8–256, requests 4–128")
	}
	debug.SetMemoryLimit(*memoryMiB << 20)
	if (*certPath == "") != (*keyPath == "") {
		return errors.New("tls-cert and tls-key must be supplied together")
	}
	if *allowWrites && *dockerSocket == "" {
		return errors.New("allow-docker-writes requires an explicit docker-socket")
	}
	if *dockerSocket != "" && !filepath.IsAbs(*dockerSocket) {
		return errors.New("docker-socket must be an absolute local path")
	}
	host, port, err := net.SplitHostPort(*listen)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	loopback := ip != nil && ip.IsLoopback()
	directTLS := *certPath != ""
	if !loopback && !directTLS {
		return errors.New("non-loopback listener requires TLS; bind 127.0.0.1 behind an HTTPS reverse proxy instead")
	}
	origins := []string{}
	secure := directTLS
	if *origin != "" {
		normalized, err := server.NormalizeOrigin(*origin)
		if err != nil {
			return err
		}
		u, _ := url.Parse(normalized)
		secure = u.Scheme == "https"
		if directTLS && !secure || !loopback && !secure {
			return errors.New("public or TLS listener requires an HTTPS origin")
		}
		if !secure {
			originIP := net.ParseIP(u.Hostname())
			originPort := u.Port()
			if originPort == "" {
				originPort = "80"
			}
			if originIP == nil || !originIP.IsLoopback() || !loopback || originPort != port {
				return errors.New("HTTP origins are allowed only for the local loopback listener")
			}
		}
		origins = append(origins, normalized)
	} else {
		if !loopback {
			return errors.New("non-loopback listener requires explicit -origin https://your-panel-host")
		}
		scheme := "http"
		if directTLS {
			scheme = "https"
		}
		origins = append(origins, scheme+"://"+net.JoinHostPort(host, port), scheme+"://"+net.JoinHostPort("localhost", port))
	}

	absoluteData, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absoluteData, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	info, err := os.Lstat(absoluteData)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("data directory must be a real private directory, not a symbolic link")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("data directory permissions must be owner-only (chmod 700)")
	}
	if *fileRoot != "" {
		root, err := filepath.EvalSymlinks(*fileRoot)
		if err != nil {
			return fmt.Errorf("resolve file root: %w", err)
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return err
		}
		realData, err := filepath.EvalSymlinks(absoluteData)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, realData)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("file-root must not contain the panel's private data directory")
		}
		*fileRoot = root
	}
	unlock, err := lockDirectory(absoluteData)
	if err != nil {
		return err
	}
	defer unlock()

	authService, err := auth.New(auth.Options{DataDir: absoluteData, SecureCookies: secure, SessionTTL: 12 * time.Hour})
	if err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
	}
	defer authService.Close()
	machineService, err := machine.New(machine.Options{FileRoot: *fileRoot, DockerSocket: *dockerSocket, AllowDockerWrites: *allowWrites})
	if err != nil {
		return fmt.Errorf("initialize machine adapters: %w", err)
	}
	defer machineService.Close()
	machineService.SetAudit(authService.Record)
	sourceService, err := sources.New(absoluteData, authService.Record)
	if err != nil {
		return err
	}
	updateService, err := update.New(update.Options{Current: build, Repository: *repository, Audit: authService.Record})
	if err != nil {
		return err
	}
	defer updateService.Close()
	static, err := server.Static(assets)
	if err != nil {
		return err
	}
	rootMux := http.NewServeMux()
	apiMux := http.NewServeMux()
	authService.Register(rootMux)
	rootMux.HandleFunc("GET /healthz", healthHandler(build))
	machineService.Register(apiMux)
	sourceService.Register(apiMux)
	updateService.Register(apiMux)
	apiMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		server.Error(w, http.StatusNotFound, "not_found", "接口不存在或当前版本尚未实现。")
	})
	rootMux.Handle("/api/", authService.Protect(apiMux))
	rootMux.Handle("/", static)
	handler, err := server.Guard(rootMux, server.Options{Origins: origins, MaxConcurrent: *requests, Secure: secure})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	listener = server.LimitListener(listener, *connections)
	defer listener.Close()
	httpServer := server.HTTPServer(*listen, handler)
	// HTTP/1.1 keeps the connection/request budget explicit for this small panel.
	httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	httpServer.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		timeout, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(timeout); err != nil {
			_ = httpServer.Close()
		}
	}()
	log.Printf("server-panel %s listening at %s; managed memory soft limit %d MiB", version, origins[0], *memoryMiB)
	if tokenPath := authService.SetupTokenPath(); tokenPath != "" {
		log.Printf("first-time setup required; read the private bootstrap token locally from: %s", tokenPath)
	}
	if runtime.GOOS != "linux" {
		log.Print("development platform: Linux host metrics and system services are unavailable")
	}
	if *dockerSocket != "" {
		log.Printf("Docker socket explicitly configured; writes enabled: %t (socket grants host-level authority)", *allowWrites)
	}
	if directTLS {
		err = httpServer.ServeTLS(listener, *certPath, *keyPath)
	} else {
		err = httpServer.Serve(listener)
	}
	stop()
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func healthHandler(build buildinfo.Info) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Status     string `json:"status"`
			Version    string `json:"version"`
			DataSchema int    `json:"dataSchema"`
		}{"ok", build.Version, build.DataSchema})
	}
}

func lockDirectory(directory string) (func(), error) {
	return lock.Acquire(directory)
}

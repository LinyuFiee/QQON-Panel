package main

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"panel/internal/auth"
	"panel/internal/buildinfo"
	"panel/internal/lock"
	"panel/internal/machine"
	"panel/internal/platform"
	"panel/internal/runlog"
	"panel/internal/server"
	"panel/internal/sources"
	"panel/internal/tlsutil"
	"panel/internal/update"
)

//go:embed index.html styles.css api.js app.js
var assets embed.FS

var (
	version          = "0.7.0"
	commit           = "unknown"
	buildTime        = "unknown"
	githubRepository = "LinyuFiee/QQON-Panel"
)

func main() {
	if err := platform.Run("QQONPanel", run); err != nil {
		log.Printf("panel: %v", err)
		os.Exit(1)
	}
}

func run(parent context.Context, ready func()) (runErr error) {
	defaultData := ""
	if userConfig, err := os.UserConfigDir(); err == nil {
		defaultData = filepath.Join(userConfig, "server-panel")
	}
	listen := flag.String("listen", "127.0.0.1:5173", "监听地址；非本机地址必须启用直接 HTTPS")
	dataDir := flag.String("data-dir", defaultData, "账号与配置的私有数据目录")
	fileRoot := flag.String("file-root", "", "显式授权的只读文件根目录；留空禁用浏览")
	dockerSocket := flag.String("docker-socket", "", "显式指定的本机 Docker Unix socket；留空禁用")
	allowWrites := flag.Bool("allow-docker-writes", false, "启用经确认的容器启动、停止和重启")
	origin := flag.String("origin", "", "唯一允许的访问来源，例如 https://panel.example.com")
	certPath := flag.String("tls-cert", "", "TLS 证书 PEM 文件路径")
	keyPath := flag.String("tls-key", "", "TLS 私钥 PEM 文件路径")
	createTLS := flag.Bool("tls-create", false, "生成自签证书后退出，不启动面板")
	inspectTLS := flag.Bool("tls-inspect", false, "校验证书、私钥与访问主机后退出")
	tlsHost := flag.String("tls-host", "", "证书对应的 IP 地址或域名（不含协议、端口）")
	tlsOutput := flag.String("tls-output", "", "全新的证书输出目录，不覆盖已有文件")
	tlsDays := flag.Int("tls-days", 365, "自签证书有效天数（1–397）")
	runtimeLogs := flag.Bool("runtime-log", runtime.GOOS == "windows", "在私有数据目录保存限量轮转运行日志")
	memoryMiB := flag.Int64("memory-budget-mib", 64, "Go 管理内存软限制 MiB，不等于 RSS 硬上限")
	connections := flag.Int("max-connections", 64, "最大已接受 TCP 连接数（8–256）")
	requests := flag.Int("max-requests", 24, "最大并发 HTTP 请求数（4–128）")
	printVersion := flag.Bool("version", false, "输出版本后退出")
	printBuild := flag.Bool("build-info", false, "输出 JSON 构建信息后退出，不启动服务")
	repository := flag.String("github-repo", githubRepository, "用于手动更新检查的公开 GitHub owner/repo")
	flag.Parse()
	build := buildinfo.Current(version, commit, buildTime, githubRepository)
	if *createTLS || *inspectTLS {
		if *createTLS == *inspectTLS || *printBuild || *printVersion {
			return errors.New("证书操作不能同时执行或与版本查询混用")
		}
		var info tlsutil.Info
		var err error
		if *createTLS {
			if *tlsOutput == "" || *tlsHost == "" || *certPath != "" || *keyPath != "" {
				return errors.New("生成证书需指定 -tls-host 和 -tls-output，不能同时指定已有证书文件")
			}
			info, err = tlsutil.Create(*tlsOutput, *tlsHost, *tlsDays)
		} else {
			if *tlsOutput != "" || *tlsHost == "" || *certPath == "" || *keyPath == "" {
				return errors.New("检查证书需指定 -tls-host、-tls-cert 与 -tls-key")
			}
			info, err = tlsutil.Inspect(*certPath, *keyPath, *tlsHost)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(info)
	}
	if *tlsOutput != "" || *tlsHost != "" {
		return errors.New("-tls-output 和 -tls-host 仅用于 -tls-create 或 -tls-inspect")
	}
	if *printBuild {
		return json.NewEncoder(os.Stdout).Encode(build)
	}
	if *printVersion {
		fmt.Printf("server-panel %s (%s; %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return nil
	}
	if *dataDir == "" {
		return errors.New("没有默认用户配置目录，请显式指定 -data-dir")
	}
	if _, err := update.ParseVersion(version); err != nil {
		return fmt.Errorf("构建版本无效：%w", err)
	}
	if *repository != "" {
		if err := update.ValidateRepository(*repository); err != nil {
			return err
		}
	}
	if *memoryMiB < 32 || *memoryMiB > 512 || *connections < 8 || *connections > 256 || *requests < 4 || *requests > 128 {
		return errors.New("资源限额无效：内存须为 32–512 MiB，连接数 8–256，请求数 4–128")
	}
	debug.SetMemoryLimit(*memoryMiB << 20)
	if (*certPath == "") != (*keyPath == "") {
		return errors.New("必须同时提供 -tls-cert 和 -tls-key")
	}
	if *allowWrites && *dockerSocket == "" {
		return errors.New("启用 Docker 写操作前须显式指定 -docker-socket")
	}
	if *dockerSocket != "" && !filepath.IsAbs(*dockerSocket) {
		return errors.New("Docker socket 必须使用本机绝对路径")
	}
	host, port, err := net.SplitHostPort(*listen)
	if err != nil {
		return fmt.Errorf("监听地址无效：%w", err)
	}
	ip := net.ParseIP(host)
	loopback := ip != nil && ip.IsLoopback()
	directTLS := *certPath != ""
	if !loopback && !directTLS {
		return errors.New("非本机监听必须启用 TLS；如使用 HTTPS 代理，请让面板监听 127.0.0.1")
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
			return errors.New("公网或 TLS 监听必须配置 HTTPS 来源地址")
		}
		if !secure {
			originIP := net.ParseIP(u.Hostname())
			originPort := u.Port()
			if originPort == "" {
				originPort = "80"
			}
			if originIP == nil || !originIP.IsLoopback() || !loopback || originPort != port {
				return errors.New("HTTP 来源地址仅允许本机回环访问")
			}
		}
		origins = append(origins, normalized)
	} else {
		if !loopback {
			return errors.New("非本机监听须显式配置 -origin https://面板IP或域名:端口")
		}
		scheme := "http"
		if directTLS {
			scheme = "https"
		}
		origins = append(origins, scheme+"://"+net.JoinHostPort(host, port), scheme+"://"+net.JoinHostPort("localhost", port))
	}

	if directTLS {
		certificateHost := host
		if *origin != "" {
			parsed, _ := url.Parse(origins[0])
			certificateHost = parsed.Hostname()
		}
		if _, err := tlsutil.Inspect(*certPath, *keyPath, certificateHost); err != nil {
			return fmt.Errorf("HTTPS 证书与访问地址不匹配或不可用：%w", err)
		}
	}

	absoluteData, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	if err := platform.PreparePrivateDirectory(absoluteData); err != nil {
		return fmt.Errorf("无法准备私有数据目录：%w", err)
	}
	if *fileRoot != "" {
		root, err := filepath.EvalSymlinks(*fileRoot)
		if err != nil {
			return fmt.Errorf("无法解析文件浏览根目录：%w", err)
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
			return errors.New("文件浏览根目录不能包含面板私有数据")
		}
		*fileRoot = root
	}
	unlock, err := lockDirectory(absoluteData)
	if err != nil {
		return err
	}
	defer unlock()
	if *runtimeLogs {
		writer, err := runlog.Open(filepath.Join(absoluteData, "logs"))
		if err != nil {
			return fmt.Errorf("无法初始化受保护的运行日志：%w", err)
		}
		previous := log.Writer()
		log.SetOutput(io.MultiWriter(writer, previous))
		defer func() {
			if runErr != nil {
				log.Printf("面板运行结束：%v", runErr)
			}
			log.SetOutput(previous)
			_ = writer.Close()
		}()
	}

	authService, err := auth.New(auth.Options{DataDir: absoluteData, SecureCookies: secure, SessionTTL: 12 * time.Hour})
	if err != nil {
		return fmt.Errorf("身份验证模块初始化失败：%w", err)
	}
	defer authService.Close()
	panelOrigin, _ := url.Parse(origins[0])
	panelPort, err := strconv.Atoi(port)
	if err != nil || panelPort < 1 || panelPort > 65535 {
		return errors.New("面板监听端口须为 1–65535 的数字")
	}
	hosting, err := newHostingServices(absoluteData, panelOrigin.Hostname(), panelPort, authService.Record)
	if err != nil {
		return fmt.Errorf("软件与网站管理模块初始化失败：%w", err)
	}
	defer func() {
		if err := hosting.Close(); err != nil {
			log.Printf("后台任务关闭失败：%v", err)
		}
	}()
	machineService, err := machine.New(machine.Options{FileRoot: *fileRoot, DockerSocket: *dockerSocket, AllowDockerWrites: *allowWrites})
	if err != nil {
		return fmt.Errorf("系统适配初始化失败：%w", err)
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
	hosting.Register(apiMux)
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
		return fmt.Errorf("监听失败，请检查地址、端口或占用情况：%w", err)
	}
	listener = server.LimitListener(listener, *connections)
	defer listener.Close()
	httpServer := server.HTTPServer(*listen, handler)
	httpServer.ErrorLog = log.New(safeHTTPErrorWriter{output: log.Writer()}, "HTTP: ", log.LstdFlags)
	// HTTP/1.1 keeps the connection/request budget explicit for this small panel.
	httpServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	httpServer.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	ctx, stop := context.WithCancel(parent)
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
	log.Printf("QQON-Panel %s 预览版已监听 %s；Go 管理内存软限制 %d MiB", version, origins[0], *memoryMiB)
	permission := hosting.authority.Status()
	log.Printf("管理权限：%s；运行身份：%s；%s", permission.Mode, permission.Identity, permission.Reason)
	if tokenPath := authService.SetupTokenPath(); tokenPath != "" {
		log.Printf("首次使用需初始化管理员；请在本机读取私有令牌文件：%s", tokenPath)
	}
	if runtime.GOOS != "linux" {
		log.Print("当前平台不提供 Linux systemd 服务查询与 Docker Unix socket 功能")
	}
	if *dockerSocket != "" {
		log.Printf("已显式配置 Docker socket；写操作启用：%t（此接口具有较高主机权限）", *allowWrites)
	}
	if ready != nil {
		ready()
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

type safeHTTPErrorWriter struct{ output io.Writer }

func (w safeHTTPErrorWriter) Write(message []byte) (int, error) {
	// net/http errors may include panic values or attacker-controlled input.
	_, err := io.WriteString(w.output, "HTTP：连接处理失败，原始错误已省略以避免泄露请求内容。\n")
	if err != nil {
		return 0, err
	}
	return len(message), nil
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

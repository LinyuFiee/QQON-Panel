package sites

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"panel/internal/apps"
	"panel/internal/jobrunner"
)

// hostAdapters is deliberately unexported. Tests supply temp directories and
// fake commands; neither environment variables nor API payloads select it.
type hostAdapters struct {
	goos          string
	strict        bool
	webRoot       string
	configRoot    string
	command       func(context.Context, string, string) error
	verify        func(context.Context, record, bool) error
	verifyRemoved func(context.Context, record) error
	caBundle      func() (string, error)
}

type Service struct {
	options   Options
	adapters  hostAdapters
	panelHost string
	statePath string
	certPath  string
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.RWMutex
	opMu      sync.Mutex
	records   map[string]record
	stateHash string
	blocked   string
	closed    bool
}

func New(options Options) (*Service, error) {
	return newService(options, hostAdapters{goos: runtime.GOOS, strict: runtime.GOOS == "linux", webRoot: webRootPath, configRoot: configRootPath, command: realCommand, caBundle: realCABundle})
}

func newService(options Options, adapters hostAdapters) (*Service, error) {
	if options.Jobs == nil || !filepath.IsAbs(options.DataDir) || filepath.Clean(options.DataDir) != options.DataDir || len(options.DataDir) > 2048 || strings.ContainsAny(options.DataDir, "\x00\r\n\"$") || options.PanelPort < 1 || options.PanelPort > 65535 {
		return nil, errors.New("sites: invalid local options or shared job runner")
	}
	host, err := canonicalHost(options.PanelHost)
	if err != nil {
		return nil, errors.New("sites: explicit canonical panel cookie host is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{options: options, adapters: adapters, panelHost: host, statePath: filepath.Join(options.DataDir, "sites"), certPath: filepath.Join(options.DataDir, "site-certs"), ctx: ctx, cancel: cancel, records: map[string]record{}}
	if s.adapters.verify == nil {
		s.adapters.verify = s.verifyHTTP
	}
	if s.adapters.verifyRemoved == nil {
		s.adapters.verifyRemoved = s.verifyRemovedHTTP
	}
	if err := s.load(); err != nil {
		cancel()
		return nil, err
	}
	if adapters.goos == "linux" {
		ctx, stop := context.WithTimeout(ctx, 45*time.Second)
		if err := s.recover(ctx); err != nil {
			s.blocked = "网站事务恢复未完成；保留恢复记录并阻止写入，请在修复依赖或授权后重启面板"
		}
		stop()
	}
	return s, nil
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sites", s.handleList)
	mux.HandleFunc("POST /api/sites", s.handleCreate)
	mux.HandleFunc("GET /api/sites/{id}", s.handleGet)
	mux.HandleFunc("PATCH /api/sites/{id}", s.handlePatch)
	mux.HandleFunc("DELETE /api/sites/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/sites/{id}/tls", s.handleTLS)
	mux.HandleFunc("GET /api/sites/{id}/files", s.handleFiles)
	mux.HandleFunc("GET /api/sites/{id}/content", s.handleContent)
	mux.HandleFunc("PUT /api/sites/{id}/content", s.handleEdit)
	mux.HandleFunc("POST /api/sites/{id}/upload", s.handleUpload)
}

// Close cancels this service's work, not the shared runner. An in-flight
// configuration transaction gets a bounded rollback before handles are released.
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.opMu.Lock()
	s.opMu.Unlock()
	return nil
}

func (s *Service) permission() error {
	if s.adapters.goos != "linux" {
		return failure(http.StatusNotImplemented, "not_supported", "Windows 及其它非 Linux 平台不支持 Nginx 网站管理")
	}
	if s.options.Permission == nil || s.options.Permission() != nil {
		return errDenied
	}
	return nil
}

func (s *Service) ready() error {
	if err := s.permission(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return errUnavailable
	}
	if s.blocked != "" {
		return failure(http.StatusServiceUnavailable, "recovery_required", s.blocked)
	}
	return nil
}

func (s *Service) runtimeInfo(ctx context.Context) (apps.RuntimeInfo, error) {
	if s.options.Runtime == nil {
		return apps.RuntimeInfo{}, failure(503, "runtime_unavailable", "未配置软件运行时发现服务")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := s.options.Runtime(ctx)
	if err != nil || !info.Available {
		return info, failure(503, "runtime_unavailable", "无法确认可用的软件管理运行时")
	}
	if !info.NginxInstalled || !knownNginxBinary(info.NginxBinary) {
		return info, failure(503, "nginx_missing", "请先在软件页安装受支持的 Nginx")
	}
	if !info.NginxRunning {
		return info, failure(503, "nginx_stopped", "请先在软件页启动 Nginx")
	}
	if info.WebUser == "" || info.WebGroup == "" || info.WebUser == "root" || info.WebGroup == "root" || info.WebUser == "0" || info.WebGroup == "0" {
		return info, failure(503, "unsafe_web_user", "无法确认 Nginx 工作进程使用非 root 用户")
	}
	return info, nil
}

func (s *Service) snapshot(id string) (record, error) {
	if !idPattern.MatchString(id) {
		return record{}, failure(404, "site_not_found", "网站不存在")
	}
	s.mu.RLock()
	r, found := s.records[id]
	s.mu.RUnlock()
	if !found {
		return record{}, failure(404, "site_not_found", "网站不存在")
	}
	r.Site = publicSite(r)
	return r, nil
}

func (s *Service) revision(id string, revision uint64) (record, error) {
	r, err := s.snapshot(id)
	if err != nil {
		return r, err
	}
	if revision == 0 || r.Site.Revision != revision {
		return r, failure(409, "revision_conflict", "网站已改变，请刷新后重试")
	}
	return r, nil
}

func (s *Service) conflict(next record) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, found := s.records[next.Site.ID]; !found && len(s.records) >= maxSites {
		return failure(409, "site_limit", "已达到 128 个受管网站上限")
	}
	for id, old := range s.records {
		if id == next.Site.ID {
			continue
		}
		if old.Site.Port == next.Site.Port && old.Site.TLSEnabled != next.Site.TLSEnabled {
			return failure(409, "port_protocol_conflict", "同一端口不能同时配置 HTTP 和 HTTPS 网站")
		}
		for _, a := range old.Site.Domains {
			for _, b := range next.Site.Domains {
				if a == b {
					return failure(409, "domain_conflict", "域名已被另一个受管网站使用")
				}
			}
		}
	}
	return nil
}

func (s *Service) submit(action, id string, work jobrunner.Work) (jobrunner.Task, error) {
	if err := s.ready(); err != nil {
		return jobrunner.Task{}, err
	}
	return s.options.Jobs.Submit(action, id, 2*time.Minute, func(parent context.Context, progress *jobrunner.Progress) error {
		ctx, cancel := context.WithCancel(parent)
		stop := context.AfterFunc(s.ctx, cancel)
		defer func() { stop(); cancel() }()
		s.opMu.Lock()
		defer s.opMu.Unlock()
		if err := s.ready(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		err := work(ctx, progress)
		if s.options.Audit != nil {
			result := action + ".succeeded"
			if err != nil {
				result = action + ".failed"
			}
			s.options.Audit(result, id)
		}
		// Underlying parse/process/filesystem errors can contain payloads or
		// unknown Nginx directives. Only deliberately authored errors enter jobs.
		return publicError(err)
	})
}

func publicError(err error) error {
	if err == nil {
		return nil
	}
	var api *apiError
	if errors.As(err, &api) {
		return api
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New("网站操作已取消或超时；配置事务将回滚或保留待恢复记录")
	}
	if errors.Is(err, os.ErrNotExist) {
		return failure(404, "file_not_found", "文件或目录不存在")
	}
	if errors.Is(err, errUnsafe) {
		return failure(403, "unsafe_path", "路径包含链接、特殊文件、非受管对象或不安全权限")
	}
	return failure(503, "operation_failed", "无法安全完成网站操作；未输出私有内容，请检查本机依赖及受管目录权限")
}

func (s *Service) siteList() []Site {
	s.mu.RLock()
	out := make([]Site, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, publicSite(r))
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func updateProgress(p *jobrunner.Progress, stage string, percent int, message string) {
	if p != nil {
		p.Update(stage, percent, message)
	}
}

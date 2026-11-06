// Package machine exposes bounded, read-mostly host information and an opt-in
// local Docker control surface. Authentication and CSRF belong to the caller.
package machine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Options deliberately has no implicit filesystem root or Docker endpoint.
type Options struct {
	FileRoot          string
	DockerSocket      string
	AllowDockerWrites bool
}

const (
	sampleInterval  = 2 * time.Second
	serviceInterval = 30 * time.Second
	maxHistory      = 300
	maxJSONResponse = 4 << 20
)

type capability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

type filesCapability struct {
	Available bool   `json:"available"`
	Root      string `json:"root"`
	Reason    string `json:"reason"`
}

type dockerCapability struct {
	Configured   bool   `json:"configured"`
	WriteEnabled bool   `json:"writeEnabled"`
	Reason       string `json:"reason"`
}

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Code }

func problem(status int, code, message string) *apiError {
	return &apiError{Status: status, Code: code, Message: message}
}

func writeError(w http.ResponseWriter, err error) {
	var e *apiError
	if !errors.As(err, &e) {
		e = problem(http.StatusInternalServerError, "internal_error", "请求处理失败")
	}
	writeJSON(w, e.Status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{e.Code, e.Message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	b, err := json.Marshal(value)
	if err != nil || len(b) > maxJSONResponse {
		status = http.StatusInternalServerError
		b = []byte(`{"error":{"code":"response_limit","message":"响应超过安全限制"}}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

type serviceDependencies struct {
	sampler       hostSampler
	services      func(context.Context) servicesView
	initialStates servicesView
	docker        *dockerClient
	// Tests disable periodic work and advance fixture samples explicitly.
	noBackground bool
}

type Service struct {
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
	wg        sync.WaitGroup
	started   time.Time

	root       *os.Root
	rootName   string
	fileReason string

	metricsMu sync.RWMutex
	metrics   systemView
	history   [maxHistory]historyPoint
	histNext  int
	histCount int

	servicesMu sync.RWMutex
	services   servicesView

	docker *dockerClient

	tasksMu sync.Mutex
	tasks   []*dockerTask
	queue   chan *dockerTask
	closing bool

	auditMu sync.RWMutex
	audit   func(string, string)
}

// New opens only an explicitly supplied file root. A configured Docker socket
// is not dialed until an API request or an explicitly confirmed task needs it.
func New(opts Options) (*Service, error) {
	return newService(opts, nil)
}

func newService(opts Options, deps *serviceDependencies) (*Service, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{ctx: ctx, cancel: cancel, started: time.Now(), queue: make(chan *dockerTask, 8)}
	if opts.FileRoot == "" {
		s.fileReason = "未配置文件浏览根目录"
	} else if !safeFilesSupported() {
		s.fileReason = "当前平台不支持安全文件浏览"
	} else {
		name, err := filepath.Abs(opts.FileRoot)
		if err != nil {
			cancel()
			return nil, errors.New("machine: invalid file root")
		}
		root, err := os.OpenRoot(name)
		if err != nil {
			cancel()
			return nil, errors.New("machine: cannot open configured file root")
		}
		s.root, s.rootName = root, name
	}

	var err error
	if deps != nil && deps.docker != nil {
		s.docker = deps.docker
		s.docker.life = ctx
	} else {
		s.docker, err = newDockerClient(ctx, opts)
	}
	if err != nil {
		cancel()
		if s.root != nil {
			_ = s.root.Close()
		}
		return nil, err
	}

	var sampler hostSampler
	var fetchServices func(context.Context) servicesView
	var initial servicesView
	if deps == nil {
		sampler = newHostSampler(s.started)
		fetchServices, initial = newServicesSource()
	} else {
		sampler, fetchServices, initial = deps.sampler, deps.services, deps.initialStates
	}
	if sampler == nil {
		cancel()
		if s.root != nil {
			_ = s.root.Close()
		}
		s.docker.close()
		return nil, errors.New("machine: missing host sampler")
	}
	if initial.Services == nil {
		initial.Services = []serviceEntry{}
	}
	s.services = initial
	s.storeSample(sampler.sample(time.Now()))
	if deps == nil || !deps.noBackground {
		s.wg.Add(1)
		go s.sampleLoop(sampler)
		if fetchServices != nil {
			s.wg.Add(1)
			go s.servicesLoop(fetchServices)
		}
	}
	if s.docker.writeEnabled {
		s.wg.Add(1)
		go s.taskWorker()
	}
	return s, nil
}

// SetAudit connects the caller's bounded audit sink. It can be replaced safely
// while handlers are running. Details never contain daemon response bodies.
func (s *Service) SetAudit(fn func(action, detail string)) {
	s.auditMu.Lock()
	s.audit = fn
	s.auditMu.Unlock()
}

func (s *Service) record(action, detail string) {
	s.auditMu.RLock()
	fn := s.audit
	s.auditMu.RUnlock()
	if fn != nil {
		fn(action, detail)
	}
}

// Close cancels in-flight engine requests, stops all periodic work, and fails
// queued tasks without executing them. It is safe to call more than once.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.tasksMu.Lock()
		s.closing = true
		s.cancel()
		s.tasksMu.Unlock()
		s.wg.Wait()
		s.docker.close()
		if s.root != nil {
			s.closeErr = s.root.Close()
		}
	})
	return s.closeErr
}

func (s *Service) route(method string, fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeError(w, problem(http.StatusMethodNotAllowed, "method_not_allowed", "不支持此请求方法"))
			return
		}
		if s.ctx.Err() != nil {
			writeError(w, problem(http.StatusServiceUnavailable, "service_closed", "面板服务正在关闭"))
			return
		}
		fn(w, r)
	}
}

// Register registers only the contract's exact routes; it is not an arbitrary
// Docker, subprocess, or filesystem proxy. The caller MUST wrap with auth.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/capabilities", s.route(http.MethodGet, s.handleCapabilities))
	mux.HandleFunc("/api/system", s.route(http.MethodGet, s.handleSystem))
	mux.HandleFunc("/api/services", s.route(http.MethodGet, s.handleServices))
	mux.HandleFunc("/api/files", s.route(http.MethodGet, s.handleFiles))
	mux.HandleFunc("/api/files/preview", s.route(http.MethodGet, s.handlePreview))
	mux.HandleFunc("/api/docker", s.route(http.MethodGet, s.handleDocker))
	mux.HandleFunc("/api/docker/containers", s.route(http.MethodGet, s.handleContainers))
	mux.HandleFunc("/api/docker/images", s.route(http.MethodGet, s.handleImages))
	mux.HandleFunc("/api/docker/volumes", s.route(http.MethodGet, s.handleVolumes))
	mux.HandleFunc("/api/docker/networks", s.route(http.MethodGet, s.handleNetworks))
	mux.HandleFunc("/api/docker/containers/{id}/logs", s.route(http.MethodGet, s.handleLogs))
	mux.HandleFunc("/api/docker/containers/{id}/{action}", s.route(http.MethodPost, s.handleDockerAction))
	mux.HandleFunc("/api/tasks", s.route(http.MethodGet, s.handleTasks))
}

func (s *Service) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	s.metricsMu.RLock()
	metrics := capability{s.metrics.Available, s.metrics.Reason}
	s.metricsMu.RUnlock()
	s.servicesMu.RLock()
	services := capability{s.services.Available, s.services.Reason}
	s.servicesMu.RUnlock()
	writeJSON(w, http.StatusOK, struct {
		Platform string           `json:"platform"`
		Metrics  capability       `json:"metrics"`
		Files    filesCapability  `json:"files"`
		Services capability       `json:"services"`
		Docker   dockerCapability `json:"docker"`
	}{runtime.GOOS, metrics, filesCapability{s.root != nil, s.rootName, s.fileReason}, services, s.docker.capability()})
}

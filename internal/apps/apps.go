package apps

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"sync"
	"time"

	"panel/internal/jobrunner"
)

type Options struct {
	Jobs       *jobrunner.Runner
	Permission func() error
	Audit      func(string, string)
}

type RuntimeInfo struct {
	Available      bool         `json:"available"`
	Reason         string       `json:"reason"`
	NginxInstalled bool         `json:"nginxInstalled"`
	NginxBinary    string       `json:"nginxBinary"`
	NginxRunning   bool         `json:"nginxRunning"`
	WebUser        string       `json:"webUser"`
	WebGroup       string       `json:"webGroup"`
	PHPRuntimes    []PHPRuntime `json:"phpRuntimes"`
}

type PHPRuntime struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Unit    string `json:"unit"`
	Socket  string `json:"socket"`
}

type Item struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version"`
	State       string `json:"state"`
	Description string `json:"description"`
}

type View struct {
	Available   bool         `json:"available"`
	Reason      string       `json:"reason"`
	Manager     string       `json:"manager"`
	Items       []Item       `json:"items"`
	PHPRuntimes []PHPRuntime `json:"phpRuntimes"`
}

type snapshot struct {
	view    View
	runtime RuntimeInfo
	units   map[string][]string
	at      time.Time
}

type Service struct {
	jobs       *jobrunner.Runner
	permission func() error
	audit      func(string, string)
	host       host
	mu         sync.Mutex
	queryMu    sync.Mutex
	cached     snapshot
	generation uint64
	closed     bool
	ctx        context.Context
	cancel     context.CancelFunc
}

func New(options Options) (*Service, error) {
	if options.Jobs == nil {
		return nil, errors.New("apps: a shared job runner is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{jobs: options.Jobs, permission: options.Permission, audit: options.Audit, host: realHost{goos: runtime.GOOS}, ctx: ctx, cancel: cancel}, nil
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/apps", s.get)
	mux.HandleFunc("POST /api/apps/{id}/{action}", s.mutate)
}

// Closing Apps cancels only its own work; the shared queue belongs to main.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	return nil
}

func (s *Service) Capabilities(ctx context.Context) (RuntimeInfo, error) {
	value, err := s.state(ctx, false)
	return value.runtime, err
}

func (s *Service) checkPermission() error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("软件管理已关闭。")
	}
	if s.permission == nil {
		return errors.New("未明确授权最高系统管理权限；软件管理不可用。")
	}
	if err := s.permission(); err != nil {
		return errors.New("未获得有效的最高系统管理授权；软件管理不可用。")
	}
	return nil
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cached.at = time.Time{}
	s.generation++
	s.mu.Unlock()
}

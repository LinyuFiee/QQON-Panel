package machine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	dockerResponseLimit = 2 << 20
	dockerListLimit     = 1000
	dockerReadTimeout   = 6 * time.Second
	dockerJobTimeout    = 20 * time.Second
	dockerReadSlots     = 4
)

type dockerStatus struct {
	Configured   bool   `json:"configured"`
	Available    bool   `json:"available"`
	WriteEnabled bool   `json:"writeEnabled"`
	Reason       string `json:"reason"`
	Version      string `json:"version,omitempty"`
}

type dockerClient struct {
	life         context.Context
	configured   bool
	supported    bool
	writeEnabled bool
	reason       string
	client       *http.Client
	transport    *http.Transport
	readSlots    chan struct{}

	statusMu      sync.Mutex
	status        dockerStatus
	statusExpires time.Time
	statusLoading bool
}

func newDockerClient(life context.Context, opts Options) (*dockerClient, error) {
	d := &dockerClient{life: life, readSlots: make(chan struct{}, dockerReadSlots)}
	if opts.DockerSocket == "" {
		d.reason = "未配置 Docker Unix socket"
		return d, nil
	}
	d.configured = true
	if runtime.GOOS == "windows" {
		d.reason = "Windows 不支持本面板的 Docker Unix socket 接口"
		return d, nil
	}
	if !filepath.IsAbs(opts.DockerSocket) || strings.ContainsAny(opts.DockerSocket, "\x00\r\n") || strings.Contains(opts.DockerSocket, "://") {
		return nil, errors.New("machine: DockerSocket must be an absolute Unix socket path")
	}
	d.supported = true
	d.writeEnabled = opts.AllowDockerWrites
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "docker.local:80" {
				return nil, errors.New("unexpected docker destination")
			}
			return dialer.DialContext(ctx, "unix", opts.DockerSocket)
		},
		// Fresh connections prevent the Transport's reused-connection retry
		// path, including its otherwise retryable zero-byte POST writes.
		DisableKeepAlives:  true,
		DisableCompression: true, MaxConnsPerHost: 5, MaxIdleConns: 5,
		MaxIdleConnsPerHost: 5, IdleConnTimeout: 30 * time.Second,
		ResponseHeaderTimeout: dockerReadTimeout, MaxResponseHeaderBytes: 16 << 10,
	}
	d.transport = transport
	d.client = &http.Client{
		Transport: transport, Timeout: dockerJobTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return d, nil
}

func (d *dockerClient) close() {
	if d.transport != nil {
		d.transport.CloseIdleConnections()
	}
}

func (d *dockerClient) capability() dockerCapability {
	return dockerCapability{Configured: d.configured, WriteEnabled: d.writeEnabled, Reason: d.reason}
}

func (d *dockerClient) enabled() error {
	if !d.configured || !d.supported || d.client == nil {
		return problem(http.StatusServiceUnavailable, "docker_unavailable", d.reason)
	}
	if d.life.Err() != nil {
		return problem(http.StatusServiceUnavailable, "service_closed", "面板服务正在关闭")
	}
	return nil
}

func (d *dockerClient) acquireRead() (func(), error) {
	if err := d.enabled(); err != nil {
		return nil, err
	}
	select {
	case d.readSlots <- struct{}{}:
		return func() { <-d.readSlots }, nil
	default:
		return nil, problem(http.StatusTooManyRequests, "docker_busy", "Docker 读取请求过多，请稍后重试")
	}
}

func (d *dockerClient) requestContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	stop := context.AfterFunc(d.life, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// request is intentionally private. Every caller below supplies a fixed API
// route, a complete validated ID, and fixed query keys. No generic proxy exists.
func (d *dockerClient) request(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	if err := d.enabled(); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "http", Host: "docker.local", Path: path, RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, problem(http.StatusBadGateway, "docker_request_failed", "无法构建 Docker 请求")
	}
	req.Header.Set("Accept", "application/json")
	res, err := d.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, problem(http.StatusGatewayTimeout, "docker_timeout", "Docker 请求已超时或取消")
		}
		return nil, problem(http.StatusBadGateway, "docker_unreachable", "无法连接已配置的 Docker Unix socket")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		_ = res.Body.Close()
		if res.StatusCode == http.StatusNotFound {
			return nil, problem(http.StatusNotFound, "docker_not_found", "Docker 对象或接口不存在")
		}
		return nil, problem(http.StatusBadGateway, "docker_engine_error", "Docker 引擎拒绝了请求")
	}
	return res, nil
}

func readDockerBody(res *http.Response, limit int64) ([]byte, error) {
	defer res.Body.Close()
	if res.ContentLength > limit {
		return nil, problem(http.StatusBadGateway, "docker_response_limit", "Docker 响应超过安全大小限制")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, problem(http.StatusBadGateway, "docker_read_failed", "Docker 响应读取失败")
	}
	if int64(len(data)) > limit {
		return nil, problem(http.StatusBadGateway, "docker_response_limit", "Docker 响应超过安全大小限制")
	}
	return data, nil
}

func (d *dockerClient) getJSON(ctx context.Context, path string, query url.Values, value any) error {
	res, err := d.request(ctx, http.MethodGet, path, query)
	if err != nil {
		return err
	}
	data, err := readDockerBody(res, dockerResponseLimit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return problem(http.StatusBadGateway, "docker_invalid_response", "Docker 返回了无效数据")
	}
	return nil
}

func (d *dockerClient) inspectStatus(parent context.Context) dockerStatus {
	base := dockerStatus{Configured: d.configured, WriteEnabled: d.writeEnabled, Reason: d.reason}
	if d.enabled() != nil {
		return base
	}
	d.statusMu.Lock()
	if time.Now().Before(d.statusExpires) {
		status := d.status
		d.statusMu.Unlock()
		return status
	}
	if d.statusLoading {
		d.statusMu.Unlock()
		base.Reason = "正在检查 Docker 引擎状态"
		return base
	}
	d.statusLoading = true
	d.statusMu.Unlock()

	release, err := d.acquireRead()
	if err == nil {
		ctx, cancel := d.requestContext(parent, dockerReadTimeout)
		var version struct{ Version string }
		err = d.getJSON(ctx, "/version", nil, &version)
		cancel()
		release()
		if err == nil {
			if version.Version == "" || len(version.Version) > 128 {
				err = problem(http.StatusBadGateway, "docker_invalid_response", "Docker 版本响应无效")
			} else {
				base.Available = true
				base.Version = safeDisplay(version.Version, 128)
			}
		}
	}
	if err != nil {
		var api *apiError
		if errors.As(err, &api) {
			base.Reason = api.Message
		} else {
			base.Reason = "Docker 引擎暂不可用"
		}
	}
	d.statusMu.Lock()
	d.status, d.statusExpires, d.statusLoading = base, time.Now().Add(5*time.Second), false
	d.statusMu.Unlock()
	return base
}

func safeDisplay(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.Map(func(r rune) rune {
		if r < 32 || (r >= 127 && r <= 159) {
			return -1
		}
		return r
	}, value)
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func validContainerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *Service) handleDocker(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.docker.inspectStatus(r.Context()))
}

func (s *Service) dockerRead(w http.ResponseWriter, r *http.Request, fn func(context.Context) (any, error)) {
	release, err := s.docker.acquireRead()
	if err != nil {
		writeError(w, err)
		return
	}
	defer release()
	ctx, cancel := s.docker.requestContext(r.Context(), dockerReadTimeout)
	defer cancel()
	result, err := fn(ctx)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

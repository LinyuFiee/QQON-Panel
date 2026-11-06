// Package update checks one explicitly trusted public GitHub repository.
// It never installs a release, runs downloaded code, or starts background checks.
package update

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"panel/internal/buildinfo"
)

const (
	cacheTTL    = 10 * time.Minute
	failureTTL  = time.Minute
	maxResponse = 1 << 20
	maxBinary   = 64 << 20
)

type Options struct {
	Current    buildinfo.Info
	Repository string
	Audit      func(string, string)
}

type Latest struct {
	Version      string `json:"version"`
	Tag          string `json:"tag"`
	URL          string `json:"url"`
	PublishedAt  string `json:"publishedAt"`
	Notes        string `json:"notes"`
	AssetsReady  bool   `json:"assetsReady"`
	AssetMessage string `json:"assetMessage"`
}

type Status struct {
	Configured      bool           `json:"configured"`
	Repository      string         `json:"repository"`
	Current         buildinfo.Info `json:"current"`
	State           string         `json:"state"`
	CheckedAt       *time.Time     `json:"checkedAt"`
	NextCheckAt     *time.Time     `json:"nextCheckAt"`
	UpdateAvailable bool           `json:"updateAvailable"`
	CanApply        bool           `json:"canApply"`
	Latest          *Latest        `json:"latest"`
	Message         string         `json:"message"`
	Command         string         `json:"command"`
}

type Service struct {
	mu       sync.Mutex
	current  buildinfo.Info
	repo     string
	status   Status
	checking bool
	client   *http.Client
	audit    func(string, string)
}

func New(options Options) (*Service, error) {
	if _, err := ParseVersion(options.Current.Version); err != nil {
		return nil, err
	}
	if options.Repository != "" {
		if err := ValidateRepository(options.Repository); err != nil {
			return nil, err
		}
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   4 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          4, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2,
		DisableCompression: true,
	}
	s := &Service{current: options.Current, repo: options.Repository, audit: options.Audit}
	s.client = &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: checkRedirect}
	s.status = s.base()
	return s, nil
}

func (s *Service) Close() error {
	s.client.CloseIdleConnections()
	return nil
}

func (s *Service) base() Status {
	status := Status{Configured: s.repo != "", Repository: s.repo, Current: s.current, State: "not_checked", Message: "尚未检查 GitHub Release；只在手动操作时联网。", Command: "sudo panel-update"}
	if s.repo == "" {
		status.State, status.Message = "unconfigured", "未配置 GitHub 仓库。发行构建会自动写入仓库，也可使用 -github-repo owner/repo 指定。"
	}
	return status
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update", s.get)
	mux.HandleFunc("POST /api/update/check", s.check)
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	status := s.status
	s.mu.Unlock()
	respond(w, http.StatusOK, status)
}

func (s *Service) check(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "json_required", "请使用 JSON 请求。")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil || strings.TrimSpace(string(body)) != "{}" {
		fail(w, http.StatusBadRequest, "invalid_request", "检查更新只接受空 JSON 对象，不接受下载地址或参数。")
		return
	}
	s.mu.Lock()
	if s.repo == "" || s.status.NextCheckAt != nil && time.Now().Before(*s.status.NextCheckAt) {
		status := s.status
		s.mu.Unlock()
		respond(w, http.StatusOK, status)
		return
	}
	if s.checking {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "2")
		fail(w, http.StatusConflict, "check_in_progress", "已有一次更新检查正在进行。")
		return
	}
	s.checking = true
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status := s.fetch(ctx)
	now := time.Now().UTC()
	ttl := cacheTTL
	if status.State == "error" || status.State == "rate_limited" {
		ttl = failureTTL
	}
	next := now.Add(ttl)
	if status.NextCheckAt == nil || status.NextCheckAt.Before(next) {
		status.NextCheckAt = &next
	}
	status.CheckedAt = &now
	s.mu.Lock()
	s.status, s.checking = status, false
	s.mu.Unlock()
	if s.audit != nil {
		s.audit("update.checked", "GitHub 稳定版检查："+status.State)
	}
	respond(w, http.StatusOK, status)
}

func (s *Service) fetch(ctx context.Context) Status {
	status := s.base()
	apiURL := "https://api.github.com/repos/" + s.repo + "/releases/latest"
	data, code, headers, err := s.download(ctx, apiURL, maxResponse)
	if err != nil {
		status.State, status.Message = "error", "无法获取 GitHub Release：请求超时、网络异常或响应超出限制。"
		return status
	}
	if code == http.StatusNotFound {
		status.State, status.Message = "no_release", "仓库不存在、未公开，或尚无稳定版 Release。当前只支持公开仓库。"
		return status
	}
	if code == http.StatusTooManyRequests || code == http.StatusForbidden && headers.Get("X-RateLimit-Remaining") == "0" {
		status.State, status.Message = "rate_limited", "GitHub 检查频率受限，请在冷却后重试。"
		next := rateReset(headers, time.Now().UTC())
		status.NextCheckAt = &next
		return status
	}
	if code != http.StatusOK {
		status.State, status.Message = "error", "GitHub 暂未提供可用的 Release 响应。"
		return status
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil || release.Draft || release.Prerelease || !strings.HasPrefix(release.Tag, "v") {
		status.State, status.Message = "error", "Release 内容无效，或不是受支持的稳定版本。"
		return status
	}
	version := strings.TrimPrefix(release.Tag, "v")
	latestVersion, err := ParseVersion(version)
	if err != nil {
		status.State, status.Message = "error", "Release 标签必须采用 vMAJOR.MINOR.PATCH 稳定版格式。"
		return status
	}
	published, err := time.Parse(time.RFC3339, release.PublishedAt)
	if err != nil || published.IsZero() {
		status.State, status.Message = "error", "Release 发布时间无效。"
		return status
	}
	latest := &Latest{Version: version, Tag: release.Tag, URL: "https://github.com/" + s.repo + "/releases/tag/" + release.Tag, PublishedAt: published.UTC().Format(time.RFC3339), Notes: boundedNotes(release.Body)}
	status.Latest = latest
	if err := validateAssets(s.repo, release); err != nil {
		status.State, status.Message = "incompatible", "Release 产物不完整或下载地址不符合发行约定，暂不可升级。"
		latest.AssetMessage = err.Error()
		return status
	}
	base := "https://github.com/" + s.repo + "/releases/download/" + release.Tag + "/"
	sumData, sumCode, _, err := s.download(ctx, base+"SHA256SUMS", 16<<10)
	if err != nil || sumCode != http.StatusOK {
		status.State, status.Message = "error", "无法读取 Release 校验清单，未确认此版本可升级。"
		return status
	}
	manifestData, manifestCode, _, err := s.download(ctx, base+"release.json", 32<<10)
	if err != nil || manifestCode != http.StatusOK {
		status.State, status.Message = "error", "无法读取 Release 版本清单，未确认此版本可升级。"
		return status
	}
	if err := validateManifest(manifestData, sumData, s.repo, version, s.current.DataSchema, release); err != nil {
		status.State, status.Message = "incompatible", "Release 校验或数据格式兼容检查未通过。"
		latest.AssetMessage = err.Error()
		return status
	}
	latest.AssetsReady, latest.AssetMessage = true, "发行文件齐全；版本清单与 SHA-256 对应。升级脚本仍会重新校验程序文件。"
	current, _ := ParseVersion(s.current.Version)
	switch latestVersion.Compare(current) {
	case 1:
		status.State, status.UpdateAvailable = "available", true
		status.Message = "发现新的稳定版本。请在 Linux 安装主机上运行升级命令，面板不会自行提权或停服。"
		status.Command = "sudo panel-update --version " + release.Tag
	case -1:
		status.State, status.Message = "ahead", "当前版本高于此仓库最新稳定版，不进行降级。"
	default:
		status.State, status.Message = "up_to_date", "当前已是此仓库最新稳定版本。"
	}
	return status
}

type githubRelease struct {
	Tag         string `json:"tag_name"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (s *Service) download(ctx context.Context, address string, limit int64) ([]byte, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("User-Agent", "server-panel/"+s.current.Version)
	if req.URL.Host == "api.github.com" {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	response, err := s.client.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer response.Body.Close()
	if response.ContentLength > limit {
		return nil, 0, nil, errors.New("response too large")
	}
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, response.Header, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, 0, nil, errors.New("response read failed or exceeded limit")
	}
	return data, response.StatusCode, response.Header, nil
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 || len(via) == 0 || via[0].URL.Hostname() == "api.github.com" {
		return errors.New("API redirects and excessive redirects are not allowed")
	}
	u := req.URL
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" && u.Port() != "443" {
		return errors.New("unsafe release redirect")
	}
	switch strings.ToLower(u.Hostname()) {
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	case "github.com":
		if u.EscapedPath() == via[0].URL.EscapedPath() {
			return nil
		}
	}
	return errors.New("release redirect outside the trusted GitHub asset hosts")
}

func rateReset(headers http.Header, now time.Time) time.Time {
	next := now.Add(failureTTL)
	if seconds, err := strconv.ParseInt(headers.Get("Retry-After"), 10, 32); err == nil && seconds > 0 && seconds <= 3600 {
		next = now.Add(time.Duration(seconds) * time.Second)
	}
	if value, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		reset := time.Unix(value, 0).UTC()
		if reset.After(next) && reset.Before(now.Add(time.Hour)) {
			next = reset
		}
	}
	return next
}

func boundedNotes(notes string) string {
	if len(notes) <= 32<<10 {
		return notes
	}
	return strings.ToValidUTF8(notes[:32<<10], "") + "\n（发布说明过长，完整内容请查看 Release 页面。）"
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func canonicalAsset(repository, tag, name string) string {
	return (&url.URL{Scheme: "https", Host: "github.com", Path: "/" + repository + "/releases/download/" + tag + "/" + name}).String()
}

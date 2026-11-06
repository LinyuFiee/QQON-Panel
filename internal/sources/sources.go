package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"
)

type Repository struct {
	Provider    string `json:"provider"`
	URL         string `json:"url"`
	SecurityURL string `json:"securityUrl,omitempty"`
}

type Registry struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
}

type Config struct {
	OS           string     `json:"os"`
	Codename     string     `json:"codename"`
	Architecture string     `json:"architecture"`
	APT          Repository `json:"apt"`
	DockerCE     Repository `json:"dockerCE"`
	Registry     Registry   `json:"registry"`
}

type Detection struct {
	OS           string `json:"os"`
	Codename     string `json:"codename"`
	Architecture string `json:"architecture"`
	Supported    bool   `json:"supported"`
}

type document struct {
	Revision uint64 `json:"revision"`
	Config   Config `json:"config"`
}

type Service struct {
	mu       sync.RWMutex
	path     string
	data     document
	detected Detection
	audit    func(string, string)
}

func New(dataDir string, audit func(string, string)) (*Service, error) {
	s := &Service{path: filepath.Join(dataDir, "sources.json"), detected: detect(), audit: audit}
	s.data = document{Revision: 1, Config: defaultConfig(s.detected)}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read source settings: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<10 {
		return nil, errors.New("source settings must be a regular file smaller than 32 KiB")
	}
	file, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	dec := json.NewDecoder(io.LimitReader(file, 32<<10))
	dec.DisallowUnknownFields()
	var saved document
	if err := dec.Decode(&saved); err != nil {
		return nil, fmt.Errorf("invalid source settings: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("source settings contains trailing data")
	}
	if saved.Revision == 0 {
		return nil, errors.New("invalid source settings revision")
	}
	canonical, err := normalize(saved.Config)
	if err != nil {
		return nil, fmt.Errorf("invalid saved source settings: %w", err)
	}
	s.data = document{Revision: saved.Revision, Config: canonical}
	return s, nil
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sources", s.get)
	mux.HandleFunc("PUT /api/sources", s.put)
	mux.HandleFunc("POST /api/sources/preview", s.preview)
}

func (s *Service) responseLocked() any {
	return struct {
		Config       Config    `json:"config"`
		Revision     uint64    `json:"revision"`
		Detected     Detection `json:"detected"`
		Verification string    `json:"verification"`
		Applied      bool      `json:"applied"`
	}{s.data.Config, s.data.Revision, s.detected, "not_checked", false}
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	respond(w, http.StatusOK, s.responseLocked())
}

func (s *Service) put(w http.ResponseWriter, r *http.Request) {
	var input document
	if !decode(w, r, &input) {
		return
	}
	canonical, err := normalize(input.Config)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_sources", err.Error())
		return
	}
	s.mu.Lock()
	if input.Revision != s.data.Revision || s.data.Revision == ^uint64(0) {
		s.mu.Unlock()
		fail(w, http.StatusConflict, "revision_conflict", "配置已被其他请求修改，请重新读取后再保存。")
		return
	}
	next := document{Revision: s.data.Revision + 1, Config: canonical}
	if err := persist(s.path, next); err != nil {
		s.mu.Unlock()
		fail(w, http.StatusInternalServerError, "save_failed", "配置未保存，请检查数据目录权限与剩余空间。")
		return
	}
	s.data = next
	response := s.responseLocked()
	s.mu.Unlock()
	if s.audit != nil {
		s.audit("sources.saved", "已保存安装源配置；未应用到操作系统或 Docker。")
	}
	respond(w, http.StatusOK, response)
}

func (s *Service) preview(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Config Config `json:"config"`
	}
	if !decode(w, r, &input) {
		return
	}
	config, err := normalize(input.Config)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_sources", err.Error())
		return
	}
	aptKey := "/usr/share/keyrings/debian-archive-keyring.gpg"
	components := "main contrib non-free non-free-firmware"
	if config.OS == "ubuntu" {
		aptKey = "/usr/share/keyrings/ubuntu-archive-keyring.gpg"
		components = "main restricted universe multiverse"
	}
	apt := fmt.Sprintf("# 仅供审阅，尚未写入系统；保留系统官方发行版签名验证。\nTypes: deb\nURIs: %s\nSuites: %s %s-updates %s-backports\nComponents: %s\nArchitectures: %s\nSigned-By: %s\n\nTypes: deb\nURIs: %s\nSuites: %s-security\nComponents: %s\nArchitectures: %s\nSigned-By: %s\n",
		config.APT.URL, config.Codename, config.Codename, config.Codename, components, config.Architecture, aptKey,
		config.APT.SecurityURL, config.Codename, components, config.Architecture, aptKey)
	docker := fmt.Sprintf("# Docker CE 安装仓库，不是 Docker Hub 加速器。\n# 先从 Docker 官方安全安装流程获取并验证签名密钥；本面板不自动下载或导入。\nTypes: deb\nURIs: %s\nSuites: %s\nComponents: stable\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/docker.asc\n", config.DockerCE.URL, config.Codename, config.Architecture)
	mirrors := []string{}
	if config.Registry.Provider != "none" {
		mirrors = append(mirrors, config.Registry.URL)
	}
	registryJSON, _ := json.MarshalIndent(map[string]any{"registry-mirrors": mirrors}, "", "  ")
	warnings := []string{
		"仅生成配置预览；没有访问这些地址、覆盖系统源、安装应用或重启 Docker。",
		"镜像服务可用性、发行版及架构支持未在线验证。应用前请核对官方镜像文档与目标服务器。",
		"系统已有 sources.list/.sources 配置需要合并和去重，并先备份；不要直接追加重复条目。",
		"Docker daemon.json 只展示需要合并的字段，不能用它覆盖现有配置。",
	}
	if !s.detected.Supported || config.OS != s.detected.OS || config.Codename != s.detected.Codename || config.Architecture != s.detected.Architecture {
		warnings = append(warnings, "当前模板与本机环境未匹配，请勿直接用于本机。")
	}
	if config.Registry.Provider == "aliyun" {
		warnings = append(warnings, "阿里镜像加速器必须使用本人控制台分配的地址，服务范围及限制以阿里云当前说明为准。")
	}
	respond(w, http.StatusOK, map[string]any{"apt": apt, "dockerCE": docker, "registry": string(registryJSON), "warnings": warnings, "applied": false})
}

func defaultConfig(d Detection) Config {
	config := Config{OS: "debian", Codename: "bookworm", Architecture: "amd64", APT: Repository{Provider: "official"}, DockerCE: Repository{Provider: "official"}, Registry: Registry{Provider: "none"}}
	if d.Supported {
		config.OS, config.Codename, config.Architecture = d.OS, d.Codename, d.Architecture
	}
	config, _ = normalize(config)
	return config
}

func normalize(config Config) (Config, error) {
	validRelease := config.OS == "debian" && (config.Codename == "bookworm" || config.Codename == "trixie") || config.OS == "ubuntu" && (config.Codename == "jammy" || config.Codename == "noble")
	if !validRelease {
		return config, errors.New("只支持 Debian 12/13 或 Ubuntu 22.04/24.04 对应的发行代号。")
	}
	if config.Architecture != "amd64" && config.Architecture != "arm64" {
		return config, errors.New("架构必须为 amd64 或 arm64。")
	}
	var err error
	config.APT, err = aptRepository(config)
	if err != nil {
		return config, err
	}
	config.DockerCE, err = dockerRepository(config)
	if err != nil {
		return config, err
	}
	switch config.Registry.Provider {
	case "none":
		config.Registry.URL = ""
	case "aliyun", "custom":
		config.Registry.URL, err = validateURL(config.Registry.URL)
		if err != nil {
			return config, fmt.Errorf("Docker 镜像加速地址：%w", err)
		}
		u, _ := url.Parse(config.Registry.URL)
		if u.Path != "" && u.Path != "/" {
			return config, errors.New("Docker 镜像加速地址不能包含仓库路径。")
		}
		if config.Registry.Provider == "aliyun" {
			host := u.Hostname()
			if !strings.HasSuffix(host, ".mirror.aliyuncs.com") || len(host) <= len(".mirror.aliyuncs.com") {
				return config, errors.New("请填写阿里云控制台分配的个人 mirror.aliyuncs.com 加速地址。")
			}
		}
	default:
		return config, errors.New("未知的 Docker 镜像加速选项。")
	}
	return config, nil
}

func aptRepository(config Config) (Repository, error) {
	repo := config.APT
	if repo.Provider == "custom" {
		var err error
		repo.URL, err = validateURL(repo.URL)
		if err != nil {
			return repo, fmt.Errorf("APT 基础源：%w", err)
		}
		repo.SecurityURL, err = validateURL(repo.SecurityURL)
		if err != nil {
			return repo, fmt.Errorf("APT 安全更新源：%w", err)
		}
		return repo, nil
	}
	hosts := map[string]string{"tuna": "https://mirrors.tuna.tsinghua.edu.cn", "aliyun": "https://mirrors.aliyun.com"}
	if repo.Provider == "official" {
		if config.OS == "debian" {
			repo.URL, repo.SecurityURL = "https://deb.debian.org/debian", "https://security.debian.org/debian-security"
		} else if config.Architecture == "arm64" {
			repo.URL, repo.SecurityURL = "https://ports.ubuntu.com/ubuntu-ports", "https://ports.ubuntu.com/ubuntu-ports"
		} else {
			repo.URL, repo.SecurityURL = "https://archive.ubuntu.com/ubuntu", "https://security.ubuntu.com/ubuntu"
		}
		return repo, nil
	}
	host, ok := hosts[repo.Provider]
	if !ok {
		return repo, errors.New("未知的 APT 镜像提供方。")
	}
	path := config.OS
	if config.OS == "ubuntu" && config.Architecture == "arm64" {
		path = "ubuntu-ports"
	}
	repo.URL = host + "/" + path
	// Keep security updates on the distribution's official origin by default.
	if config.OS == "debian" {
		repo.SecurityURL = "https://security.debian.org/debian-security"
	} else if config.Architecture == "arm64" {
		repo.SecurityURL = "https://ports.ubuntu.com/ubuntu-ports"
	} else {
		repo.SecurityURL = "https://security.ubuntu.com/ubuntu"
	}
	return repo, nil
}

func dockerRepository(config Config) (Repository, error) {
	repo := config.DockerCE
	repo.SecurityURL = ""
	if repo.Provider == "custom" {
		var err error
		repo.URL, err = validateURL(repo.URL)
		if err != nil {
			return repo, fmt.Errorf("Docker CE 安装源：%w", err)
		}
		return repo, nil
	}
	bases := map[string]string{"official": "https://download.docker.com", "tuna": "https://mirrors.tuna.tsinghua.edu.cn/docker-ce", "aliyun": "https://mirrors.aliyun.com/docker-ce"}
	base, ok := bases[repo.Provider]
	if !ok {
		return repo, errors.New("未知的 Docker CE 安装源提供方。")
	}
	repo.URL = base + "/linux/" + config.OS
	return repo, nil
}

func validateURL(value string) (string, error) {
	if len(value) < 9 || len(value) > 2048 || strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || strings.ContainsAny(value, "\\\"'<>`") {
		return "", errors.New("必须填写长度不超过 2048 字符且不含空白的 HTTPS 地址。")
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("只允许 HTTPS 地址，不能包含账号密码、查询参数或片段。")
	}
	if strings.IndexFunc(u.Path, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || strings.Contains(u.Path, "\\") {
		return "", errors.New("地址路径不能包含编码后的控制字符、空白或反斜线。")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", errors.New("地址路径不能包含相对跳转。")
		}
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func detect() Detection {
	d := Detection{OS: runtime.GOOS, Architecture: runtime.GOARCH}
	if runtime.GOOS != "linux" {
		return d
	}
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return d
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16<<10))
	if err != nil {
		return d
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, "\"'")
		if key == "ID" {
			d.OS = value
		}
		if key == "VERSION_CODENAME" {
			d.Codename = value
		}
	}
	d.Supported = (d.Architecture == "amd64" || d.Architecture == "arm64") && (d.OS == "debian" && (d.Codename == "bookworm" || d.Codename == "trixie") || d.OS == "ubuntu" && (d.Codename == "jammy" || d.Codename == "noble"))
	return d
}

func persist(path string, data document) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".sources-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0600); err != nil {
		return err
	}
	if _, err := temp.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err == nil {
			defer dir.Close()
			_ = dir.Sync()
		}
	}
	return nil
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "json_required", "请求必须使用 application/json。")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		fail(w, http.StatusBadRequest, "invalid_json", "请求内容无效、过大或包含未知字段。")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		fail(w, http.StatusBadRequest, "invalid_json", "请求只允许一个 JSON 对象。")
		return false
	}
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

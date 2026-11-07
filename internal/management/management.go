package management

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
)

const NoticeVersion = 1
const maxConsentBytes = 4096

var ErrNotAuthorized = errors.New("尚未获得最高权限管理授权")

type Consent struct {
	Format        int    `json:"format"`
	Mode          string `json:"mode"`
	Identity      string `json:"identity"`
	NoticeVersion int    `json:"noticeVersion"`
	AcceptedAt    string `json:"acceptedAt"`
	AcceptedBy    string `json:"acceptedBy"`
	Notice        string `json:"notice"`
}

type Status struct {
	Mode          string `json:"mode"`
	Identity      string `json:"identity"`
	Authorized    bool   `json:"authorized"`
	NoticeVersion int    `json:"noticeVersion"`
	AcceptedAt    string `json:"acceptedAt,omitempty"`
	Reason        string `json:"reason"`
	Platform      string `json:"platform"`
}

type Authority struct {
	identity func() (string, bool)
	read     func() ([]byte, error)
	now      func() time.Time
	platform string
}

// New has no configurable consent path or API for granting permission. Only the
// separately confirmed installer can create the protected system record.
func New() *Authority {
	return &Authority{identity: runtimeIdentity, read: readSystemConsent, now: time.Now, platform: runtime.GOOS}
}

func (a *Authority) Status() Status {
	identity, highest := a.identity()
	status := Status{Mode: "restricted", Identity: identity, NoticeVersion: NoticeVersion, Platform: a.platform}
	if a.platform != "linux" && a.platform != "windows" {
		status.Reason = "当前系统不支持最高权限管理模式。"
		return status
	}
	if !highest {
		status.Reason = "面板仍以受限身份运行；请在服务器上通过 qqon privilege 单独授权并迁移服务。"
		return status
	}
	data, err := a.read()
	if err != nil {
		status.Reason = "未找到有效的受保护授权记录；root 或 LocalSystem 身份本身不代表已同意系统管理。"
		return status
	}
	consent, err := parseConsent(data, identity, a.now())
	if err != nil {
		status.Reason = "最高权限授权记录无效或告知版本不匹配，请通过本机维护命令重新检查。"
		return status
	}
	status.Mode = "system"
	status.Authorized = true
	status.AcceptedAt = consent.AcceptedAt
	status.Reason = "已明确授权最高权限管理；软件安装、服务和网站修改仍需登录、同源校验与操作确认。"
	return status
}

func (a *Authority) Check() error {
	status := a.Status()
	if !status.Authorized {
		return fmt.Errorf("%w：%s", ErrNotAuthorized, status.Reason)
	}
	return nil
}

func (a *Authority) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/management", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(a.Status())
	})
}

func parseConsent(data []byte, identity string, now time.Time) (Consent, error) {
	var result Consent
	if len(data) == 0 || len(data) > maxConsentBytes {
		return result, errors.New("invalid consent size")
	}
	// Exact keys and scalar values reject duplicate/case aliases and nulls before
	// typed decoding; a partial document must never turn into implied consent.
	fields := map[string]bool{"format": false, "mode": false, "identity": false, "noticeVersion": false, "acceptedAt": false, "acceptedBy": false, "notice": false}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return result, errors.New("expected consent object")
	}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		seen, known := fields[key]
		if err != nil || !ok || !known || seen {
			return result, errors.New("unexpected consent field")
		}
		fields[key] = true
		token, err = decoder.Token()
		if err != nil || token == nil {
			return result, errors.New("missing consent value")
		}
		switch token.(type) {
		case string, json.Number:
		default:
			return result, errors.New("invalid consent value")
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return result, errors.New("incomplete consent object")
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return result, errors.New("trailing consent data")
	}
	for _, found := range fields {
		if !found {
			return result, errors.New("incomplete consent")
		}
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	actor := "root"
	if identity == "LocalSystem" {
		actor = "Administrator"
	} else if identity != "root" {
		return result, errors.New("unsupported consent identity")
	}
	accepted, err := time.Parse(time.RFC3339, result.AcceptedAt)
	if err != nil || accepted.Format(time.RFC3339) != result.AcceptedAt || accepted.Location() != time.UTC || accepted.Year() < 2026 || accepted.After(now.Add(5*time.Minute)) {
		return result, errors.New("invalid consent time")
	}
	if result.Format != 1 || result.Mode != "system" || result.Identity != identity || result.NoticeVersion != NoticeVersion || result.AcceptedBy != actor || result.Notice != "full-system-management" {
		return result, errors.New("unrecognized consent")
	}
	return result, nil
}

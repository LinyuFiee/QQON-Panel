package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"panel/internal/jobrunner"
)

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	value, _ := s.state(r.Context(), false)
	respond(w, http.StatusOK, value.view)
}

func (s *Service) mutate(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	if id != "nginx" && id != "php-fpm" && id != "mariadb" && id != "redis" {
		fail(w, http.StatusNotFound, "app_not_found", "只支持 Nginx、PHP-FPM、MariaDB 与 Redis。")
		return
	}
	if action != "install" && action != "start" && action != "stop" && action != "restart" {
		fail(w, http.StatusNotFound, "action_not_supported", "只支持安装、启动、停止与重启；不提供卸载或数据重置。")
		return
	}
	if !confirmed(w, r) {
		return
	}
	if err := s.checkPermission(); err != nil {
		fail(w, http.StatusForbidden, "permission_denied", err.Error())
		return
	}
	if s.host.platform() != "linux" {
		fail(w, http.StatusNotImplemented, "not_supported", "此平台不支持 apt/dnf 软件管理，不会调用 Windows SCM。")
		return
	}
	tools := detectTools(s.host)
	if tools.manager == "" || tools.queryTool == "" || tools.systemctl == "" {
		fail(w, http.StatusServiceUnavailable, "dependency_unknown", "缺少受信任的包管理器或 systemd；未接受操作。")
		return
	}
	// Check immediately before durable admission, in addition to the work-time
	// check. The callback remains authoritative; root UID is not implicit consent.
	if err := s.checkPermission(); err != nil {
		fail(w, http.StatusForbidden, "permission_denied", err.Error())
		return
	}
	timeout := 2 * time.Minute
	if action == "install" {
		timeout = 13 * time.Minute
	}
	job, err := s.jobs.Submit("apps."+action, id, timeout, func(ctx context.Context, progress *jobrunner.Progress) error {
		if err := s.checkPermission(); err != nil {
			return err
		}
		if s.host.platform() != "linux" {
			return errors.New("not_supported: Linux is required")
		}
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
		defer cancel()
		err := s.operate(ctx, id, action, progress)
		if s.audit != nil {
			outcome := "completed"
			if err != nil {
				outcome = "failed"
			}
			s.audit("apps."+action+"."+outcome, id)
		}
		return err
	})
	if err != nil {
		var failure *jobrunner.Error
		if errors.As(err, &failure) {
			if failure.Status == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "5")
			}
			fail(w, failure.Status, failure.Code, failure.Message)
		} else {
			fail(w, http.StatusServiceUnavailable, "jobs_unavailable", "任务未接受。")
		}
		return
	}
	if s.audit != nil {
		s.audit("apps."+action+".queued", id+" job="+job.ID)
	}
	respond(w, http.StatusAccepted, map[string]any{"job": job})
}

func confirmed(w http.ResponseWriter, r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		fail(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "请使用未压缩的 application/json 正文。")
		return false
	}
	if r.Body == nil {
		fail(w, http.StatusBadRequest, "confirmation_required", "此操作需要明确确认。")
		return false
	}
	body := http.MaxBytesReader(w, r.Body, 1024)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "body_too_large", "确认正文超过 1 KiB。")
		return false
	}
	// Exact one-field shape also rejects duplicate keys, case aliases and null.
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		fail(w, http.StatusBadRequest, "invalid_json", "需要 {\"confirmed\":true}。")
		return false
	}
	key, err := decoder.Token()
	if err != nil || key != "confirmed" {
		fail(w, http.StatusBadRequest, "confirmation_required", "需要 confirmed:true 明确确认。")
		return false
	}
	value, err := decoder.Token()
	if err != nil || value != true {
		fail(w, http.StatusBadRequest, "confirmation_required", "此操作未明确确认。")
		return false
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		fail(w, http.StatusBadRequest, "invalid_json", "确认正文包含未知或重复字段。")
		return false
	}
	if _, err := decoder.Token(); err != io.EOF {
		fail(w, http.StatusBadRequest, "invalid_json", "确认正文包含多余数据。")
		return false
	}
	return true
}

func (s *Service) operate(ctx context.Context, id, action string, progress *jobrunner.Progress) (result error) {
	tools := detectTools(s.host)
	if tools.manager == "" || tools.packageTool == "" || tools.queryTool == "" || tools.systemctl == "" {
		return errors.New("dependency_unknown: required trusted package manager or systemd is missing")
	}
	// Always invalidate and query after an attempted operation, including command
	// failures. No command's zero exit code alone can mark the task successful.
	defer func() {
		s.invalidate()
		if err := ctx.Err(); err != nil {
			result = errors.Join(result, err)
			return
		}
		if err := s.checkPermission(); err != nil {
			result = errors.Join(result, err)
			return
		}
		progress.Update("verify", 90, "读取实际软件包版本和服务状态。")
		value, err := s.state(ctx, true)
		if err != nil {
			result = errors.Join(result, errors.New("操作后无法验证实际软件版本和服务状态。"))
			return
		}
		if !value.view.Available {
			result = errors.Join(result, errors.New(value.view.Reason))
			return
		}
		item, _ := findItem(value, id)
		if result != nil {
			return
		}
		if !item.Installed || item.Version == "" || item.State == "unknown" {
			result = errors.New("软件包或对应 systemd 单元未能验证；未报告成功。")
			return
		}
		if action == "start" || action == "restart" {
			if item.State != "active" {
				result = errors.New("服务命令已返回，但实际服务未处于 active 状态。")
			}
		} else if action == "stop" && item.State != "inactive" {
			result = errors.New("停止命令已返回，但实际服务未处于 inactive 状态。")
		}
		if result == nil {
			progress.Update("verified", 100, "实际软件版本："+item.Version+"；服务状态："+item.State+"。")
		}
	}()
	if action == "install" {
		pkg := map[string]string{"nginx": "nginx", "php-fpm": "php-fpm", "mariadb": "mariadb-server", "redis": "redis-server"}[id]
		if tools.manager == "dnf" && id == "redis" {
			pkg = "redis"
		}
		if tools.manager == "apt" {
			progress.Update("repository", 10, "更新已有受信任仓库索引；不改写软件源。")
			if err := s.run(ctx, tools.packageTool, []string{"-o", "DPkg::Lock::Timeout=30", "-o", "Acquire::Retries=1", "-o", "Acquire::http::Timeout=30", "-o", "Acquire::https::Timeout=30", "-o", "APT::Update::Error-Mode=any", "update"}, 2*time.Minute, progress); err != nil {
				return err
			}
			progress.Update("install", 30, "安装发行版软件包 "+pkg+"；不升级整个系统、不覆盖已有配置。")
			return s.run(ctx, tools.packageTool, []string{"-y", "--no-install-recommends", "-o", "DPkg::Lock::Timeout=30", "-o", "Acquire::Retries=1", "-o", "Acquire::http::Timeout=30", "-o", "Acquire::https::Timeout=30", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "install", "--", pkg}, 10*time.Minute, progress)
		}
		progress.Update("install", 20, "从已有受信任仓库安装发行版软件包 "+pkg+"。")
		return s.run(ctx, tools.packageTool, []string{"-y", "--setopt=timeout=30", "--setopt=retries=1", "--setopt=exit_on_lock=True", "install", "--", pkg}, 10*time.Minute, progress)
	}
	value, err := s.state(ctx, true)
	if err != nil {
		return errors.New("无法确认当前已安装的软件和服务单元。")
	}
	if !value.view.Available {
		return errors.New(value.view.Reason)
	}
	item, _ := findItem(value, id)
	if !item.Installed {
		return errors.New("软件尚未安装，未调用服务控制。")
	}
	units := value.units[id]
	if len(units) == 0 {
		return errors.New("未发现可管理的实际发行版服务单元。")
	}
	for _, unit := range units {
		if !unitNamePattern.MatchString(unit) {
			return errors.New("服务单元不在固定白名单内。")
		}
	}
	progress.Update("service", 35, "执行 "+action+"："+strings.Join(units, ", "))
	args := append([]string{"--no-ask-password", action, "--"}, units...)
	return s.run(ctx, tools.systemctl, args, 60*time.Second, progress)
}

func (s *Service) run(ctx context.Context, executable string, args []string, timeout time.Duration, progress *jobrunner.Progress) error {
	if err := s.checkPermission(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if !s.host.executable(executable) {
		return errors.New("受信任的系统命令已消失或权限已改变。")
	}
	return s.host.command(ctx, executable, args, progress)
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

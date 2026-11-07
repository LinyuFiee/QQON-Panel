package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"panel/internal/apps"
	"panel/internal/jobrunner"
)

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func respondError(w http.ResponseWriter, err error) {
	var queue *jobrunner.Error
	if errors.As(err, &queue) {
		if queue.Status == 429 {
			w.Header().Set("Retry-After", "5")
		}
		respond(w, queue.Status, map[string]any{"error": map[string]string{"code": queue.Code, "message": queue.Message}})
		return
	}
	api, ok := publicError(err).(*apiError)
	if !ok {
		api = &apiError{503, "operation_failed", "网站操作未完成"}
	}
	respond(w, api.status, map[string]any{"error": map[string]string{"code": api.code, "message": api.message}})
}

// Walk tokens against exact JSON tag names before decoding. This rejects
// duplicate/case-alias fields and null instead of silently accepting Go's
// case-insensitive or last-value-wins struct decoding behavior.
func jsonShape(dec *json.Decoder, typ reflect.Type, depth int) error {
	if depth > 8 {
		return errUnsafe
	}
	value, err := dec.Token()
	if err != nil || value == nil {
		return errUnsafe
	}
	switch typ.Kind() {
	case reflect.Struct:
		if value != json.Delim('{') {
			return errUnsafe
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			fields[name] = f.Type
		}
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			name, ok := key.(string)
			field, exists := fields[name]
			if err != nil || !ok || !exists || seen[name] {
				return errUnsafe
			}
			seen[name] = true
			if err := jsonShape(dec, field, depth+1); err != nil {
				return err
			}
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			tag := field.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if !strings.Contains(tag, ",omitempty") && !seen[name] {
				return errUnsafe
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return errUnsafe
		}
	case reflect.Slice:
		if value != json.Delim('[') {
			return errUnsafe
		}
		count := 0
		for dec.More() {
			count++
			if count > 16 {
				return errUnsafe
			}
			if err := jsonShape(dec, typ.Elem(), depth+1); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return errUnsafe
		}
	case reflect.String:
		if _, ok := value.(string); !ok {
			return errUnsafe
		}
	case reflect.Bool:
		if _, ok := value.(bool); !ok {
			return errUnsafe
		}
	case reflect.Int, reflect.Uint64:
		if _, ok := value.(json.Number); !ok {
			return errUnsafe
		}
	default:
		return errUnsafe
	}
	return nil
}

func boundedJSON(w http.ResponseWriter, r *http.Request, limit int64, out any) error {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(params) > 1 || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		return failure(415, "unsupported_media_type", "请使用未压缩的 UTF-8 application/json")
	}
	if r.Body == nil {
		return failure(400, "invalid_json", "需要 JSON 正文")
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return failure(413, "body_too_large", "请求正文超过此操作的有界限制")
	}
	defer clear(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if jsonShape(decoder, reflect.TypeOf(out).Elem(), 0) != nil {
		return failure(400, "invalid_json", "JSON 字段、类型或重复字段无效")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return failure(400, "invalid_json", "JSON 包含额外数据")
	}
	if parseJSON(data, out) != nil {
		return failure(400, "invalid_json", "JSON 正文无效")
	}
	return nil
}

func requireConfirmed(confirmed bool) error {
	if !confirmed {
		return failure(400, "confirmation_required", "此操作需要 confirmed:true 明确确认")
	}
	return nil
}

func (s *Service) accept(w http.ResponseWriter, action, id string, work jobrunner.Work) {
	job, err := s.submit(action, id, work)
	if err != nil {
		respondError(w, err)
		return
	}
	if s.options.Audit != nil {
		s.options.Audit(action+".queued", id)
	}
	respond(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	available, reason := true, ""
	php := []apps.PHPRuntime{}
	if err := s.ready(); err != nil {
		available, reason = false, err.Error()
	} else {
		info, err := s.runtimeInfo(r.Context())
		for _, runtime := range info.PHPRuntimes {
			if runtime.Unit == runtime.ID+".service" && knownPHPSocket(runtime.ID, runtime.Socket) {
				php = append(php, runtime)
			}
		}
		if err != nil {
			available, reason = false, err.Error()
		}
	}
	list := s.siteList()
	if s.adapters.goos == "linux" && s.permission() == nil {
		for i := range list {
			if saved, err := s.snapshot(list[i].ID); err == nil {
				list[i].ConfigStatus = s.configStatus(saved)
			}
		}
	}
	respond(w, 200, map[string]any{"available": available, "reason": reason, "sites": list, "phpRuntimes": php, "uploadLimitBytes": UploadLimitBytes, "editLimitBytes": EditLimitBytes})
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	saved, err := s.snapshot(r.PathValue("id"))
	if err != nil {
		respondError(w, err)
		return
	}
	site := publicSite(saved)
	if s.adapters.goos == "linux" && s.permission() == nil {
		site.ConfigStatus = s.configStatus(saved)
	}
	respond(w, 200, map[string]any{"site": site})
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	if err := s.ready(); err != nil {
		respondError(w, err)
		return
	}
	var request struct {
		Confirmed bool      `json:"confirmed"`
		Site      siteInput `json:"site"`
	}
	if err := boundedJSON(w, r, 64<<10, &request); err != nil {
		respondError(w, err)
		return
	}
	if err := requireConfirmed(request.Confirmed); err != nil {
		respondError(w, err)
		return
	}
	in, err := s.validateInput(request.Site)
	if err != nil {
		respondError(w, err)
		return
	}
	id, err := randomID()
	if err != nil {
		respondError(w, err)
		return
	}
	if err := s.conflict(record{Site: Site{ID: id, Domains: in.Domains, Port: in.Port}}); err != nil {
		respondError(w, err)
		return
	}
	s.accept(w, "sites.create", id, func(ctx context.Context, p *jobrunner.Progress) error { return s.create(ctx, id, in, p) })
}

func (s *Service) handlePatch(w http.ResponseWriter, r *http.Request) {
	if err := s.ready(); err != nil {
		respondError(w, err)
		return
	}
	var request struct {
		Confirmed bool      `json:"confirmed"`
		Revision  uint64    `json:"revision"`
		Site      siteInput `json:"site"`
	}
	if err := boundedJSON(w, r, 64<<10, &request); err != nil {
		respondError(w, err)
		return
	}
	if err := requireConfirmed(request.Confirmed); err != nil {
		respondError(w, err)
		return
	}
	old, err := s.revision(r.PathValue("id"), request.Revision)
	if err != nil {
		respondError(w, err)
		return
	}
	in, err := s.validateInput(request.Site)
	if err != nil {
		respondError(w, err)
		return
	}
	if old.Site.TLSEnabled && in.Port == 80 {
		respondError(w, failure(400, "tls_port", "TLS 站点不能使用 80 端口"))
		return
	}
	s.accept(w, "sites.update", old.Site.ID, func(ctx context.Context, p *jobrunner.Progress) error {
		return s.patch(ctx, old.Site.ID, request.Revision, in, p)
	})
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.ready(); err != nil {
		respondError(w, err)
		return
	}
	var request struct {
		Confirmed bool   `json:"confirmed"`
		Revision  uint64 `json:"revision"`
	}
	if err := boundedJSON(w, r, 1024, &request); err != nil {
		respondError(w, err)
		return
	}
	if err := requireConfirmed(request.Confirmed); err != nil {
		respondError(w, err)
		return
	}
	old, err := s.revision(r.PathValue("id"), request.Revision)
	if err != nil {
		respondError(w, err)
		return
	}
	s.accept(w, "sites.delete", old.Site.ID, func(ctx context.Context, p *jobrunner.Progress) error {
		return s.delete(ctx, old.Site.ID, request.Revision, p)
	})
}

func (s *Service) handleTLS(w http.ResponseWriter, r *http.Request) {
	if err := s.ready(); err != nil {
		respondError(w, err)
		return
	}
	var request struct {
		Confirmed   bool   `json:"confirmed"`
		Revision    uint64 `json:"revision"`
		Certificate string `json:"certificate"`
		PrivateKey  string `json:"privateKey"`
	}
	if err := boundedJSON(w, r, 96<<10, &request); err != nil {
		respondError(w, err)
		return
	}
	if err := requireConfirmed(request.Confirmed); err != nil {
		respondError(w, err)
		return
	}
	old, err := s.revision(r.PathValue("id"), request.Revision)
	if err != nil {
		respondError(w, err)
		return
	}
	if old.Site.Port == 80 {
		respondError(w, failure(400, "tls_port", "导入 TLS 前请改用 443 或非面板高位端口"))
		return
	}
	cert, key := []byte(request.Certificate), []byte(request.PrivateKey)
	request.Certificate, request.PrivateKey = "", ""
	if err := validateCertificate(cert, key, old.Site.Domains); err != nil {
		clear(key)
		respondError(w, err)
		return
	}
	revision := request.Revision
	job, err := s.submit("sites.tls", old.Site.ID, func(ctx context.Context, p *jobrunner.Progress) error {
		defer clear(key)
		return s.importTLS(ctx, old.Site.ID, revision, cert, key, p)
	})
	if err != nil {
		clear(key)
		respondError(w, err)
		return
	}
	if s.options.Audit != nil {
		s.options.Audit("sites.tls.queued", old.Site.ID)
	}
	respond(w, 202, map[string]any{"job": job})
}

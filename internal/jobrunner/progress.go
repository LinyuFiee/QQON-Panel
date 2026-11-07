package jobrunner

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (r *Runner) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, request *http.Request) {
		r.mu.Lock()
		tasks := make([]Task, 0, len(r.tasks))
		for i := len(r.tasks) - 1; i >= 0; i-- {
			summary := *r.tasks[i]
			summary.Logs = nil
			// Visible-page polling needs descriptors only. Retrieve the bounded
			// log tail from /api/jobs/{id}, not all 32 tails on every refresh.
			tasks = append(tasks, cloneTask(&summary))
		}
		r.mu.Unlock()
		respond(w, http.StatusOK, map[string]any{"jobs": tasks})
	})
	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, request *http.Request) {
		r.mu.Lock()
		var task *Task
		for _, value := range r.tasks {
			if value.ID == request.PathValue("id") {
				copy := cloneTask(value)
				task = &copy
				break
			}
		}
		r.mu.Unlock()
		if task == nil {
			respond(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "job_not_found", "message": "任务不存在或已超过最近 32 条记录的保留范围。"}})
			return
		}
		respond(w, http.StatusOK, map[string]any{"job": task})
	})
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (p *Progress) Log(line string) {
	if p == nil || p.runner == nil {
		return
	}
	r := p.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.task.Status != "running" {
		return
	}
	// Bound before splitting: even a hostile subprocess's single enormous line
	// cannot create unbounded temporary strings or log entries.
	if len(line) > logLimit {
		line = line[len(line)-logLimit:]
	}
	for len(line) != 0 {
		part := line
		if at := strings.IndexAny(line, "\r\n"); at >= 0 {
			part, line = line[:at], line[at+1:]
		} else {
			line = ""
		}
		if strings.Contains(part, "-----BEGIN ") {
			p.privateBlock = true
		}
		if p.privateBlock {
			if strings.Contains(part, "-----END ") {
				p.privateBlock = false
			}
			part = "[敏感证书或密钥内容已隐藏]"
		}
		part = safeText(part, 4096)
		if part == "" {
			continue
		}
		if len(p.task.Logs) > 0 && part == "[敏感证书或密钥内容已隐藏]" && p.task.Logs[len(p.task.Logs)-1] == part {
			continue
		}
		p.task.Logs = append(p.task.Logs, part)
		// Track JSON-encoded bytes incrementally: escaping can expand a log, and
		// serializing the entire existing tail for every output line is quadratic.
		encoded, _ := json.Marshal(part)
		p.logBytes += len(encoded) + 1
		start := 0
		for p.logBytes+2 > logLimit || len(p.task.Logs)-start > 1024 {
			old, _ := json.Marshal(p.task.Logs[start])
			p.logBytes -= len(old) + 1
			start++
		}
		if start > 0 {
			copy(p.task.Logs, p.task.Logs[start:])
			clear(p.task.Logs[len(p.task.Logs)-start:])
			p.task.Logs = p.task.Logs[:len(p.task.Logs)-start]
		}
	}
	r.dirty = true
}

func safeText(value string, limit int) string {
	// Neither task errors nor command output is a channel for credentials. No
	// command environment, configuration contents or raw argv are logged here.
	if len(value) > limit*2 {
		value = value[:limit*2]
	}
	lower := strings.ToLower(value)
	for _, word := range []string{"password", "passwd", "private key", "privatekey", "private_key", "authorization", "bearer ", "secret=", "token=", "-----begin", "-----end"} {
		if strings.Contains(lower, word) {
			return "[敏感内容已隐藏]"
		}
	}
	var out strings.Builder
	out.Grow(min(len(value), limit))
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		if r == utf8.RuneError && size == 1 || unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			continue
		}
		if out.Len()+utf8.RuneLen(r) > limit {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}

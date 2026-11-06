package machine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"
)

const maxTasks = 32

type dockerTask struct {
	ID         string     `json:"id"`
	Action     string     `json:"action"`
	Target     string     `json:"target"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func confirmedRequest(w http.ResponseWriter, r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return problem(http.StatusUnsupportedMediaType, "json_required", "请求必须为 application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	dec := json.NewDecoder(r.Body)
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return problem(http.StatusBadRequest, "invalid_json", "请求 JSON 无效")
	}
	seen := false
	for dec.More() {
		key, err := dec.Token()
		if err != nil || key != "confirmed" || seen {
			return problem(http.StatusBadRequest, "invalid_json", "请求只能包含一个 confirmed 字段")
		}
		var confirmed bool
		if err := dec.Decode(&confirmed); err != nil || !confirmed {
			return problem(http.StatusBadRequest, "confirmation_required", "必须明确确认 Docker 操作")
		}
		seen = true
	}
	last, err := dec.Token()
	if err != nil || last != json.Delim('}') {
		return problem(http.StatusBadRequest, "invalid_json", "请求 JSON 无效")
	}
	if _, err := dec.Token(); err != io.EOF {
		return problem(http.StatusBadRequest, "invalid_json", "请求 JSON 无效或过大")
	}
	if !seen {
		return problem(http.StatusBadRequest, "confirmation_required", "必须明确确认 Docker 操作")
	}
	return nil
}

func (s *Service) enqueue(action, target string) (dockerTask, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return dockerTask{}, problem(http.StatusInternalServerError, "task_id_failed", "无法创建任务标识")
	}
	task := &dockerTask{ID: hex.EncodeToString(random[:]), Action: action, Target: target, Status: "queued", CreatedAt: time.Now().UTC()}
	s.tasksMu.Lock()
	if s.closing || s.ctx.Err() != nil {
		s.tasksMu.Unlock()
		return dockerTask{}, problem(http.StatusServiceUnavailable, "service_closed", "面板服务正在关闭")
	}
	// Keep the mutex across the nonblocking send so the worker cannot update
	// this task before it is retained and the immutable response is copied.
	select {
	case s.queue <- task:
		for len(s.tasks) >= maxTasks {
			for i, old := range s.tasks {
				if old.Status == "succeeded" || old.Status == "failed" {
					copy(s.tasks[i:], s.tasks[i+1:])
					s.tasks[len(s.tasks)-1] = nil
					s.tasks = s.tasks[:len(s.tasks)-1]
					break
				}
			}
		}
		s.tasks = append(s.tasks, task)
		response := *task
		s.tasksMu.Unlock()
		s.record("docker.queued", "task="+response.ID+" action="+action+" target="+target)
		return response, nil
	default:
		s.tasksMu.Unlock()
		return dockerTask{}, problem(http.StatusTooManyRequests, "task_queue_full", "Docker 任务队列已满，请稍后重试")
	}
}

func (s *Service) handleDockerAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	if !validContainerID(id) {
		writeError(w, problem(http.StatusBadRequest, "invalid_container_id", "必须提供完整的 64 位小写十六进制容器 ID"))
		return
	}
	switch action {
	case "start", "stop", "restart":
	default:
		writeError(w, problem(http.StatusNotFound, "invalid_docker_action", "不支持该 Docker 操作"))
		return
	}
	if err := s.docker.enabled(); err != nil {
		writeError(w, err)
		return
	}
	if !s.docker.writeEnabled {
		writeError(w, problem(http.StatusForbidden, "docker_read_only", "Docker 写操作默认禁用"))
		return
	}
	if err := confirmedRequest(w, r); err != nil {
		writeError(w, err)
		return
	}
	task, err := s.enqueue(action, id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		Task dockerTask `json:"task"`
	}{task})
}

func (s *Service) finishTask(task *dockerTask, err error) {
	s.tasksMu.Lock()
	task.FinishedAt = pointer(time.Now().UTC())
	task.Status = "succeeded"
	if err != nil {
		task.Status = "failed"
		task.Error = "Docker 操作失败"
		var api *apiError
		if errors.As(err, &api) {
			task.Error = api.Message
		}
	}
	id, action, target, status := task.ID, task.Action, task.Target, task.Status
	s.tasksMu.Unlock()
	s.record("docker."+status, "task="+id+" action="+action+" target="+target)
}

func (s *Service) cancelQueued() {
	for {
		select {
		case task := <-s.queue:
			s.finishTask(task, problem(http.StatusServiceUnavailable, "task_cancelled", "面板关闭，任务已取消且未执行"))
		default:
			return
		}
	}
}

func (s *Service) executeTask(task *dockerTask) {
	s.tasksMu.Lock()
	if s.closing || s.ctx.Err() != nil {
		s.tasksMu.Unlock()
		s.finishTask(task, problem(http.StatusServiceUnavailable, "task_cancelled", "面板关闭，任务已取消且未执行"))
		return
	}
	task.Status, task.StartedAt = "running", pointer(time.Now().UTC())
	action, target, id := task.Action, task.Target, task.ID
	s.tasksMu.Unlock()
	s.record("docker.running", "task="+id+" action="+action+" target="+target)
	ctx, cancel := context.WithTimeout(s.ctx, dockerJobTimeout)
	defer cancel()
	query := url.Values{}
	if action == "stop" || action == "restart" {
		query.Set("t", "10")
	}
	res, err := s.docker.request(ctx, http.MethodPost, "/containers/"+target+"/"+action, query)
	if err == nil {
		_, err = readDockerBody(res, 32<<10)
	}
	s.finishTask(task, err)
}

func (s *Service) taskWorker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			s.cancelQueued()
			return
		case task := <-s.queue:
			s.executeTask(task)
		}
	}
}

func (s *Service) handleTasks(w http.ResponseWriter, _ *http.Request) {
	s.tasksMu.Lock()
	tasks := make([]dockerTask, 0, len(s.tasks))
	for i := len(s.tasks) - 1; i >= 0; i-- {
		tasks = append(tasks, *s.tasks[i])
	}
	s.tasksMu.Unlock()
	writeJSON(w, http.StatusOK, struct {
		Tasks []dockerTask `json:"tasks"`
	}{tasks})
}

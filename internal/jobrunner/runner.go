package jobrunner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	queueLimit     = 8
	historyLimit   = 32
	logLimit       = 64 << 10
	maxStateBytes  = 3 << 20
	maxTaskTimeout = 15 * time.Minute
)

// Work runs on the single shared worker. It must observe ctx; request contexts
// must not be captured by callers. Only its descriptor, never the closure, is saved.
type Work func(context.Context, *Progress) error

type Task struct {
	ID         string     `json:"id"`
	Action     string     `json:"action"`
	Target     string     `json:"target"`
	Status     string     `json:"status"`
	Stage      string     `json:"stage"`
	Progress   int        `json:"progress"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Message    string     `json:"message,omitempty"`
	Error      string     `json:"error,omitempty"`
	Logs       []string   `json:"logs"`
}

// Error allows authenticated API callers to distinguish admission failures.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

type pending struct {
	task    *Task
	work    Work
	timeout time.Duration
}

type Runner struct {
	mu         sync.Mutex
	path       string
	tasks      []*Task
	queue      []pending
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	workerDone chan struct{}
	saverDone  chan struct{}
	closedDone chan struct{}
	closeOnce  sync.Once
	closed     bool
	dirty      bool
	storageErr error
	closeErr   error
	release    func()
	// Kept internal so tests can force storage failures without touching host paths.
	persist func([]*Task) error
}

// Progress is valid only during its Work invocation. Updates after termination
// are ignored, and logging never performs a synchronous filesystem write.
type Progress struct {
	runner       *Runner
	task         *Task
	privateBlock bool
	logBytes     int
}

func New(dataDir string) (*Runner, error) {
	path, release, err := prepareStorage(dataDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{path: path, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), workerDone: make(chan struct{}), saverDone: make(chan struct{}), closedDone: make(chan struct{}), release: release}
	r.persist = func(tasks []*Task) error { return saveState(path, tasks) }
	if r.tasks, err = loadState(path); err != nil {
		cancel()
		release()
		return nil, err
	}
	// Never replay partially completed mutations after a crash or process restart.
	now := time.Now().UTC()
	for _, task := range r.tasks {
		if !terminal(task.Status) {
			task.Status, task.Stage, task.FinishedAt = "interrupted", "interrupted", &now
			task.Error = "任务因面板进程重新启动而中断；未自动重试，请核验实际系统状态。"
		}
	}
	if err := r.persist(r.tasks); err != nil {
		cancel()
		release()
		return nil, fmt.Errorf("persist recovered jobs: %w", err)
	}
	go r.worker()
	go r.saver()
	return r, nil
}

func (r *Runner) Submit(action, target string, timeout time.Duration, work Work) (Task, error) {
	if !descriptor(action, 64) || !descriptor(target, 256) || timeout <= 0 || timeout > maxTaskTimeout || work == nil {
		return Task{}, &Error{http.StatusBadRequest, "invalid_job", "任务描述、超时时间或执行器无效。"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.storageErr != nil {
		return Task{}, &Error{http.StatusServiceUnavailable, "jobs_unavailable", "任务执行器已关闭或持久化不可用；未接受操作。"}
	}
	for _, task := range r.tasks {
		if !terminal(task.Status) && task.Action == action && task.Target == target {
			return Task{}, &Error{http.StatusConflict, "job_conflict", "相同操作已有排队或执行中的任务。"}
		}
	}
	if len(r.queue) >= queueLimit {
		return Task{}, &Error{http.StatusTooManyRequests, "queue_full", "任务队列已满（最多 8 个等待任务），请稍后重试。"}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Task{}, &Error{http.StatusServiceUnavailable, "jobs_unavailable", "无法生成任务标识。"}
	}
	task := &Task{ID: hex.EncodeToString(id[:]), Action: action, Target: target, Status: "queued", Stage: "queued", CreatedAt: time.Now().UTC(), Logs: []string{}}
	next := append([]*Task(nil), r.tasks...)
	if len(next) == historyLimit {
		for i, old := range next {
			if terminal(old.Status) {
				next = append(next[:i], next[i+1:]...)
				break
			}
		}
	}
	if len(next) >= historyLimit {
		return Task{}, &Error{http.StatusTooManyRequests, "queue_full", "任务历史尚无可回收的终态记录。"}
	}
	next = append(next, task)
	// Durable admission precedes publication to the worker and to the HTTP client.
	if err := r.persist(next); err != nil {
		r.storageFailureLocked(err)
		return Task{}, &Error{http.StatusServiceUnavailable, "job_storage_failed", "任务未接受：无法安全持久化任务状态。"}
	}
	r.tasks, r.dirty = next, false
	r.queue = append(r.queue, pending{task, work, timeout})
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return cloneTask(task), nil
}

func (r *Runner) worker() {
	defer close(r.workerDone)
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
		}
		for {
			r.mu.Lock()
			if r.closed || r.storageErr != nil || len(r.queue) == 0 {
				r.mu.Unlock()
				break
			}
			item := r.queue[0]
			r.queue[0] = pending{}
			r.queue = r.queue[1:]
			now := time.Now().UTC()
			item.task.Status, item.task.Stage, item.task.StartedAt = "running", "starting", &now
			if err := r.persist(r.tasks); err != nil {
				r.storageFailureLocked(err)
				r.mu.Unlock()
				return
			}
			r.dirty = false
			r.mu.Unlock()
			ctx, cancel := context.WithTimeout(r.ctx, item.timeout)
			err := invoke(ctx, &Progress{runner: r, task: item.task}, item.work)
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			cancel()
			r.mu.Lock()
			if !terminal(item.task.Status) {
				now := time.Now().UTC()
				item.task.FinishedAt = &now
				switch {
				case r.ctx.Err() != nil:
					item.task.Status, item.task.Stage, item.task.Error = "interrupted", "interrupted", "面板关闭或存储故障导致任务中断，请核验实际系统状态。"
				case err != nil:
					item.task.Status, item.task.Stage, item.task.Error = "failed", "failed", safeText(err.Error(), 2048)
				default:
					item.task.Status, item.task.Stage, item.task.Progress = "succeeded", "completed", 100
				}
				if err := r.persist(r.tasks); err != nil {
					// Never publish success if its terminal record is not durable.
					item.task.Status, item.task.Stage = "interrupted", "interrupted"
					item.task.Error = "终态持久化失败，无法确认任务结果；请核验实际系统状态。"
					r.storageFailureLocked(err)
				} else {
					r.dirty = false
				}
			}
			r.mu.Unlock()
		}
	}
}

func invoke(ctx context.Context, progress *Progress, work Work) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("任务执行器发生内部错误；未自动重试。")
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return work(ctx, progress)
}

func (r *Runner) saver() {
	defer close(r.saverDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.mu.Lock()
			if r.dirty && !r.closed && r.storageErr == nil {
				if err := r.persist(r.tasks); err != nil {
					r.storageFailureLocked(err)
				} else {
					r.dirty = false
				}
			}
			r.mu.Unlock()
		}
	}
}

func (r *Runner) storageFailureLocked(err error) {
	r.storageErr = err
	r.cancel()
	now := time.Now().UTC()
	for _, task := range r.tasks {
		if !terminal(task.Status) {
			task.Status, task.Stage, task.FinishedAt = "interrupted", "interrupted", &now
			task.Error = "任务存储不可用；已停止执行，请核验实际系统状态。"
		}
	}
	clear(r.queue)
	r.queue = nil
}

func (r *Runner) Close() error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.cancel()
		now := time.Now().UTC()
		for _, task := range r.tasks {
			if !terminal(task.Status) {
				task.Status, task.Stage, task.FinishedAt = "interrupted", "interrupted", &now
				task.Error = "面板正在关闭，任务已中断；未自动重试。"
			}
		}
		clear(r.queue)
		r.queue = nil
		r.closeErr = r.persist(r.tasks)
		if r.closeErr == nil {
			r.closeErr = r.storageErr
		}
		r.mu.Unlock()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		stopped := true
		for _, done := range []<-chan struct{}{r.workerDone, r.saverDone} {
			select {
			case <-done:
			case <-deadline.C:
				r.closeErr = errors.Join(r.closeErr, errors.New("job work did not observe cancellation within five seconds"))
				stopped = false
			}
			if !stopped {
				break
			}
		}
		// A non-cooperative internal Work cannot permit another writer to this store.
		if stopped {
			r.release()
		}
		close(r.closedDone)
	})
	<-r.closedDone
	return r.closeErr
}

func (p *Progress) Update(stage string, percent int, message string) {
	if p == nil || p.runner == nil {
		return
	}
	r := p.runner
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.task.Status != "running" {
		return
	}
	p.task.Stage, p.task.Progress, p.task.Message = safeText(stage, 64), max(0, min(100, percent)), safeText(message, 1024)
	r.dirty = true
}

func terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "interrupted"
}

func descriptor(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func cloneTask(task *Task) Task {
	copy := *task
	copy.Logs = append([]string{}, task.Logs...)
	if task.StartedAt != nil {
		t := *task.StartedAt
		copy.StartedAt = &t
	}
	if task.FinishedAt != nil {
		t := *task.FinishedAt
		copy.FinishedAt = &t
	}
	return copy
}

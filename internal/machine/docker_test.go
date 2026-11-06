package machine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testContainerID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeDocker(writes bool, f fakeTransport) *dockerClient {
	return &dockerClient{
		life: context.Background(), configured: true, supported: true, writeEnabled: writes,
		readSlots: make(chan struct{}, dockerReadSlots),
		client:    &http.Client{Transport: f, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func dockerResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

func TestDockerFourListsAndStatusCache(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	d := fakeDocker(false, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "docker.local" || r.Method != "GET" {
			t.Errorf("unexpected outgoing request %s %s", r.Method, r.URL)
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > dockerReadTimeout {
			t.Error("unbounded Docker read")
		}
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/version":
			return dockerResponse(200, `{"Version":"27.0.1"}`), nil
		case "/containers/json":
			if r.URL.RawQuery != "all=1" {
				t.Error("container list missing fixed all query")
			}
			return dockerResponse(200, `[{"Id":"`+testContainerID+`","Names":["/fixture"],"Image":"image:tag","State":"running","Status":"Up","Created":1700000000,"Ports":[{"IP":"127.0.0.1","PrivatePort":80,"PublicPort":8080,"Type":"tcp"}]}]`), nil
		case "/images/json":
			return dockerResponse(200, `[{"Id":"sha256:abc","RepoTags":["image:tag"],"Size":100,"Created":1700000000}]`), nil
		case "/volumes":
			return dockerResponse(200, `{"Volumes":[{"Name":"fixture-vol","Driver":"local","Mountpoint":"/fixture"}]}`), nil
		case "/networks":
			return dockerResponse(200, `[{"Id":"abc","Name":"fixture-net","Driver":"bridge","Scope":"local"}]`), nil
		default:
			t.Errorf("unexpected engine path %s", r.URL.Path)
			return dockerResponse(404, "{}"), nil
		}
	})
	s := fixtureService(t, Options{}, d)
	for i := 0; i < 10; i++ {
		status := decodeBody[dockerStatus](t, request(t, s, "GET", "/api/docker", ""))
		if !status.Available || !status.Configured || status.WriteEnabled || status.Version != "27.0.1" {
			t.Fatalf("bad Docker status: %+v", status)
		}
	}
	mu.Lock()
	count := calls["/version"]
	mu.Unlock()
	if count != 1 {
		t.Fatalf("status not cached: %d calls", count)
	}
	for _, name := range []string{"containers", "images", "volumes", "networks"} {
		r := request(t, s, "GET", "/api/docker/"+name, "")
		requireStatus(t, r, 200)
		var body map[string][]json.RawMessage
		if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || len(body[name]) != 1 {
			t.Fatalf("bad %s response: %s", name, r.Body.String())
		}
		if name == "containers" && (!strings.Contains(r.Body.String(), "2023-11-14T22:13:20Z") || !strings.Contains(r.Body.String(), "8080")) {
			t.Fatal("container timestamp or ports missing")
		}
	}
}

func TestDockerReadCapsAndSanitizedFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   fakeTransport
		want int
	}{
		{"too-large", func(*http.Request) (*http.Response, error) {
			return dockerResponse(200, strings.Repeat("x", dockerResponseLimit+1)), nil
		}, 502},
		{"invalid", func(*http.Request) (*http.Response, error) { return dockerResponse(200, "garbage"), nil }, 502},
		{"null-list", func(*http.Request) (*http.Response, error) { return dockerResponse(200, "null"), nil }, 502},
		{"error-body", func(*http.Request) (*http.Response, error) { return dockerResponse(500, "SECRET_DAEMON_ERROR"), nil }, 502},
		{"connection", func(*http.Request) (*http.Response, error) { return nil, errors.New("SECRET_DAEMON_ERROR") }, 502},
		{"redirect", func(*http.Request) (*http.Response, error) {
			res := dockerResponse(302, "")
			res.Header.Set("Location", "https://example.invalid/SECRET_DAEMON_ERROR")
			return res, nil
		}, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureService(t, Options{}, fakeDocker(false, tc.fn))
			r := request(t, s, "GET", "/api/docker/containers", "")
			requireStatus(t, r, tc.want)
			if strings.Contains(r.Body.String(), "SECRET_DAEMON_ERROR") {
				t.Fatal("daemon details leaked")
			}
		})
	}
	s := fixtureService(t, Options{}, fakeDocker(false, func(*http.Request) (*http.Response, error) {
		return dockerResponse(200, "["+strings.Repeat(`{},`, dockerListLimit)+`{}]`), nil
	}))
	requireStatus(t, request(t, s, "GET", "/api/docker/networks", ""), 502)
}

func TestDockerFullIDsAndWriteDefaults(t *testing.T) {
	var calls atomic.Int32
	s := fixtureService(t, Options{}, fakeDocker(false, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return dockerResponse(204, ""), nil
	}))
	for _, id := range []string{"abc", strings.Repeat("g", 64), strings.Repeat("A", 64), testContainerID[:63], testContainerID + "a", "name:tag"} {
		requireStatus(t, request(t, s, "GET", "/api/docker/containers/"+id+"/logs", ""), 400)
		requireStatus(t, request(t, s, "POST", "/api/docker/containers/"+id+"/start", `{"confirmed":true}`), 400)
	}
	requireStatus(t, request(t, s, "POST", "/api/docker/containers/"+testContainerID+"/exec", `{"confirmed":true}`), 404)
	requireStatus(t, request(t, s, "POST", "/api/docker/containers/"+testContainerID+"/start", `{"confirmed":true}`), 403)
	if calls.Load() != 0 {
		t.Fatal("invalid/disabled mutation contacted engine")
	}
	for _, id := range []string{"../" + testContainerID, testContainerID + "?x=1", testContainerID + "/"} {
		if validContainerID(id) {
			t.Fatal("path injection accepted")
		}
	}
}

func dockerFrame(stream byte, data []byte) []byte {
	frame := make([]byte, 8+len(data))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(data)))
	copy(frame[8:], data)
	return frame
}

func TestDockerLogsTTYMultiplexAndLimits(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint(tty), func(t *testing.T) {
			calls := 0
			s := fixtureService(t, Options{}, fakeDocker(false, func(r *http.Request) (*http.Response, error) {
				calls++
				switch r.URL.Path {
				case "/containers/" + testContainerID + "/json":
					return dockerResponse(200, fmt.Sprintf(`{"Config":{"Tty":%t}}`, tty)), nil
				case "/containers/" + testContainerID + "/logs":
					if r.URL.Query().Get("tail") != "200" || r.URL.Query().Get("follow") != "0" || r.URL.Query().Get("stdout") != "1" || r.URL.Query().Get("stderr") != "1" {
						t.Fatal("unsafe log query")
					}
					data := []byte("out\nerr\n")
					if !tty {
						data = append(dockerFrame(1, []byte("out\n")), dockerFrame(2, []byte("err\n"))...)
					}
					return dockerResponse(200, string(data)), nil
				default:
					return dockerResponse(404, ""), nil
				}
			}))
			r := request(t, s, "GET", "/api/docker/containers/"+testContainerID+"/logs", "")
			requireStatus(t, r, 200)
			view := decodeBody[logsView](t, r)
			if view.Text != "out\nerr\n" || view.Truncated || calls != 2 {
				t.Fatalf("invalid log result: %+v calls=%d", view, calls)
			}
		})
	}
	for _, tty := range []bool{false, true} {
		data := bytes.Repeat([]byte("x"), maxDockerLogs+1)
		if !tty {
			data = dockerFrame(1, data)
		}
		view, err := decodeLogs(bytes.NewReader(data), tty)
		if err != nil || !view.Truncated || len(view.Text) != maxDockerLogs {
			t.Fatalf("log cap failed tty=%t len=%d truncated=%t err=%v", tty, len(view.Text), view.Truncated, err)
		}
	}
	for _, data := range [][]byte{[]byte("invalid"), dockerFrame(3, []byte("x")), dockerFrame(1, []byte("x"))[:8]} {
		if _, err := decodeLogs(bytes.NewReader(data), false); err == nil {
			t.Fatal("invalid Docker framing accepted")
		}
	}
	emptyFrames := bytes.Repeat(dockerFrame(1, nil), maxDockerLogFrames+10)
	view, err := decodeLogs(bytes.NewReader(emptyFrames), false)
	if err != nil || !view.Truncated {
		t.Fatal("empty frames bypass work limit")
	}
	view, err = decodeLogs(bytes.NewReader([]byte{0xff, 0, 'x', '\n'}), true)
	if err != nil || strings.ContainsRune(view.Text, 0) {
		t.Fatal("unsafe log control bytes")
	}
}

func TestDockerReadAdmissionAndCloseCancellation(t *testing.T) {
	entered := make(chan struct{}, dockerReadSlots)
	var calls atomic.Int32
	s := fixtureService(t, Options{}, fakeDocker(false, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	mux := http.NewServeMux()
	s.Register(mux)
	var wg sync.WaitGroup
	for i := 0; i < dockerReadSlots; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/docker/images", nil))
		}()
	}
	for i := 0; i < dockerReadSlots; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("read request did not start")
		}
	}
	requireStatus(t, request(t, s, "GET", "/api/docker/images", ""), 429)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel Docker reads")
	}
	if calls.Load() != dockerReadSlots {
		t.Fatal("admission limit did not hold")
	}
}

func waitTaskStatus(t *testing.T, s *Service, id string, status string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.tasksMu.Lock()
		found := false
		for _, task := range s.tasks {
			if task.ID == id && task.Status == status {
				found = true
			}
		}
		s.tasksMu.Unlock()
		if found {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("task %s never reached %s", id, status)
}

func TestTaskQueueSingleWorkerCapAuditAndCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	var active, maxActive, calls atomic.Int32
	s := fixtureService(t, Options{}, fakeDocker(true, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		now := active.Add(1)
		defer active.Add(-1)
		maxActive.Store(max(maxActive.Load(), now))
		if r.Method != "POST" || r.URL.Path != "/containers/"+testContainerID+"/restart" || r.URL.Query().Get("t") != "10" {
			t.Error("unexpected mutation request")
		}
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	var auditMu sync.Mutex
	var audit []string
	s.SetAudit(func(action, detail string) {
		auditMu.Lock()
		audit = append(audit, action+" "+detail)
		auditMu.Unlock()
	})
	path := "/api/docker/containers/" + testContainerID + "/restart"
	for _, body := range []string{`{}`, `{"confirmed":false}`, `{"confirmed":true,"extra":1}`} {
		requireStatus(t, request(t, s, "POST", path, body), 400)
	}
	first := request(t, s, "POST", path, `{"confirmed":true}`)
	requireStatus(t, first, 202)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}
	for i := 0; i < 8; i++ {
		requireStatus(t, request(t, s, "POST", path, `{"confirmed":true}`), 202)
	}
	before := time.Now()
	requireStatus(t, request(t, s, "POST", path, `{"confirmed":true}`), 429)
	if time.Since(before) > time.Second {
		t.Fatal("queue admission blocked")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || maxActive.Load() != 1 {
		t.Fatalf("unexpected worker concurrency/calls: %d/%d", maxActive.Load(), calls.Load())
	}
	s.tasksMu.Lock()
	if len(s.tasks) != 9 {
		t.Error("unexpected task count")
	}
	for _, task := range s.tasks {
		if task.Status != "failed" || task.FinishedAt == nil || task.Error == "" {
			t.Error("Close left pending/running task")
		}
	}
	s.tasksMu.Unlock()
	auditMu.Lock()
	defer auditMu.Unlock()
	if len(audit) < 19 {
		t.Fatalf("missing audit transitions: %d", len(audit))
	}
}

func TestTaskHistoryBoundAndNoMutationRetry(t *testing.T) {
	var calls atomic.Int32
	s := fixtureService(t, Options{}, fakeDocker(true, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return dockerResponse(204, ""), nil
	}))
	var last string
	for i := 0; i < maxTasks+10; i++ {
		r := request(t, s, "POST", "/api/docker/containers/"+testContainerID+"/start", `{"confirmed":true}`)
		requireStatus(t, r, 202)
		task := decodeBody[struct{ Task dockerTask }](t, r).Task
		if task.Status != "queued" || task.CreatedAt.IsZero() || !validTaskID(task.ID) {
			t.Fatalf("bad task acceptance: %+v", task)
		}
		waitTaskStatus(t, s, task.ID, "succeeded")
		last = task.ID
	}
	list := decodeBody[struct{ Tasks []dockerTask }](t, request(t, s, "GET", "/api/tasks", "")).Tasks
	if len(list) != maxTasks || list[0].ID != last || calls.Load() != maxTasks+10 {
		t.Fatalf("history/calls: len=%d calls=%d", len(list), calls.Load())
	}
	for _, task := range list {
		if task.StartedAt == nil || task.FinishedAt == nil || task.Status != "succeeded" {
			t.Fatal("missing task lifecycle timestamps")
		}
	}
	var failedCalls atomic.Int32
	failed := fixtureService(t, Options{}, fakeDocker(true, func(*http.Request) (*http.Response, error) {
		failedCalls.Add(1)
		return nil, errors.New("SECRET_INTERNAL_ENGINE_ERROR")
	}))
	r := request(t, failed, "POST", "/api/docker/containers/"+testContainerID+"/stop", `{"confirmed":true}`)
	requireStatus(t, r, 202)
	task := decodeBody[struct{ Task dockerTask }](t, r).Task
	waitTaskStatus(t, failed, task.ID, "failed")
	if failedCalls.Load() != 1 {
		t.Fatal("failed mutation retried")
	}
	result := request(t, failed, "GET", "/api/tasks", "")
	if strings.Contains(result.Body.String(), "SECRET_INTERNAL_ENGINE_ERROR") {
		t.Fatal("raw engine error leaked in task history")
	}
}

func validTaskID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func TestWindowsDockerExplicitlyUnavailable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows behavior only")
	}
	d, err := newDockerClient(context.Background(), Options{DockerSocket: "/tmp/never-dial.sock", AllowDockerWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	status := d.inspectStatus(context.Background())
	if !status.Configured || status.Available || status.WriteEnabled || status.Reason == "" || d.client != nil {
		t.Fatalf("unsafe Windows Docker behavior: %+v", status)
	}
}

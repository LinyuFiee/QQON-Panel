package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureSampler struct{ calls atomic.Int32 }

func (f *fixtureSampler) sample(now time.Time) systemView {
	f.calls.Add(1)
	return systemView{Platform: "fixture", OS: "fixture", Architecture: "fixture", SampledAt: now.UTC(), Reason: "fixture unavailable"}
}

func fixtureService(t *testing.T, opts Options, docker *dockerClient) *Service {
	t.Helper()
	s, err := newService(opts, &serviceDependencies{
		sampler: &fixtureSampler{}, initialStates: unknownServices("fixture unavailable"), docker: docker, noBackground: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func request(t *testing.T, s *Service, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("non-JSON response %d to %s %s: %s", response.Code, method, target, response.Body.String())
	}
	return response
}

func decodeBody[T any](t *testing.T, r *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(r.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return value
}

func requireStatus(t *testing.T, r *httptest.ResponseRecorder, status int) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("status %d, want %d: %s", r.Code, status, r.Body.String())
	}
}

func TestDefaultCapabilitiesAndJSONErrors(t *testing.T) {
	s := fixtureService(t, Options{}, nil)
	response := request(t, s, "GET", "/api/capabilities", "")
	requireStatus(t, response, 200)
	var caps struct {
		Files  filesCapability
		Docker dockerCapability
	}
	if err := json.Unmarshal(response.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Files.Available || caps.Files.Reason == "" || caps.Docker.Configured || caps.Docker.WriteEnabled || caps.Docker.Reason == "" {
		t.Fatalf("unsafe defaults: %+v", caps)
	}
	for _, path := range []string{"/api/files", "/api/files/preview?path=x", "/api/docker/containers", "/api/docker/images", "/api/docker/volumes", "/api/docker/networks"} {
		r := request(t, s, "GET", path, "")
		requireStatus(t, r, 503)
		var errBody struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(r.Body.Bytes(), &errBody); err != nil || errBody.Error.Code == "" || errBody.Error.Message == "" {
			t.Fatalf("missing structured error %s", r.Body.String())
		}
	}
	status := decodeBody[dockerStatus](t, request(t, s, "GET", "/api/docker", ""))
	if status.Configured || status.Available || status.WriteEnabled || status.Reason == "" {
		t.Fatalf("unsafe Docker default: %+v", status)
	}
	for _, path := range []string{"/api/system", "/api/services", "/api/capabilities", "/api/tasks", "/api/files"} {
		r := request(t, s, "POST", path, "{}")
		requireStatus(t, r, 405)
		if r.Header().Get("Allow") != "GET" {
			t.Fatal("missing Allow header")
		}
	}
}

func TestMetricsCacheAndFiniteRing(t *testing.T) {
	f := &fixtureSampler{}
	s, err := newService(Options{}, &serviceDependencies{sampler: f, noBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 10; i++ {
		request(t, s, "GET", "/api/system", "")
	}
	if f.calls.Load() != 1 {
		t.Fatal("request triggered host sampling")
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxHistory+20; i++ {
		s.storeSample(systemView{SampledAt: base.Add(time.Duration(i) * time.Second), CPUPercent: pointer(float64(i))})
	}
	view := decodeBody[systemView](t, request(t, s, "GET", "/api/system", ""))
	if len(view.History) != maxHistory || !view.History[0].Time.Equal(base.Add(20*time.Second)) || *view.History[maxHistory-1].CPUPercent != 319 {
		t.Fatalf("invalid ring retention: len=%d first=%v", len(view.History), view.History[0].Time)
	}
}

func TestBasicSampleUsesRealProcessRuntime(t *testing.T) {
	now := time.Now()
	view := basicSample(now.Add(-time.Minute), now)
	if view.Panel.HeapBytes == 0 || view.Panel.Goroutines < 1 || view.Panel.UptimeSeconds != 60 {
		t.Fatalf("invalid runtime metrics: %+v", view.Panel)
	}
	if view.CPUPercent != nil || view.Memory.Total != nil || view.Disk.Total != nil || view.UptimeSeconds != nil || view.Panel.RSSBytes != nil {
		t.Fatal("unsupported host metrics invented values")
	}
	if view.Platform != runtime.GOOS {
		t.Fatal("incorrect platform")
	}
}

func TestCloseCancelsServiceLoops(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	s, err := newService(Options{}, &serviceDependencies{
		sampler: &fixtureSampler{},
		services: func(ctx context.Context) servicesView {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			return unknownServices("cancelled")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("service sampler never started")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("Close failed to cancel service query")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(t, s, "GET", "/api/system", ""), 503)
}

func TestSystemctlFixedAllowlistAndBoundedOutput(t *testing.T) {
	calls := 0
	view := queryServices(context.Background(), "/fixture/systemctl", func(ctx context.Context, executable string, args []string) ([]byte, error) {
		calls++
		if executable != "/fixture/systemctl" || len(args) != 6+len(serviceAllowlist) || strings.Join(args[:4], " ") != "show --no-pager --property=Id,ActiveState,LoadState --" {
			t.Fatalf("unexpected command: %s %q", executable, args)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 4*time.Second {
			t.Fatal("missing bounded command deadline")
		}
		for i, name := range serviceAllowlist {
			if args[4+i] != name+".service" {
				t.Fatal("non-allowlisted service")
			}
		}
		if args[len(args)-2] != "php-fpm.service" || args[len(args)-1] != "php[0-9]*-fpm.service" {
			t.Fatal("PHP discovery must use fixed unit patterns")
		}
		return []byte("Id=nginx.service\nActiveState=active\n\nId=mysql.service\nActiveState=inactive\n\nId=docker.service\nActiveState=failed\n\nId=untrusted.service\nActiveState=active\n"), nil
	})
	if calls != 1 || !view.Available || len(view.Services) != 6 || view.Services[0].State != "active" || view.Services[1].State != "inactive" || view.Services[5].State != "failed" || view.Services[2].State != "unknown" {
		t.Fatalf("unexpected services: %+v", view)
	}
	s := fixtureService(t, Options{}, nil)
	s.services = view
	for i := 0; i < 10; i++ {
		requireStatus(t, request(t, s, "GET", "/api/services", ""), 200)
	}
	if calls != 1 {
		t.Fatal("service queries were not cached")
	}
	for _, response := range [][]byte{[]byte("unrecognized"), bytes.Repeat([]byte("x"), serviceOutputLimit+1)} {
		bad := queryServices(context.Background(), "fixture", func(context.Context, string, []string) ([]byte, error) { return response, nil })
		if bad.Available || bad.Reason == "" {
			t.Fatal("invalid output accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	budget := &commandOutputBudget{cancel: cancel}
	stdout := commandOutputWriter{budget: budget, stdout: true}
	stderr := commandOutputWriter{budget: budget}
	if _, err := stdout.Write([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.Write(bytes.Repeat([]byte("x"), serviceOutputLimit)); err == nil || !budget.over || budget.used != serviceOutputLimit || budget.buffer.String() != "123456" || ctx.Err() == nil {
		t.Fatal("combined subprocess output cap or cancellation failed")
	}
}

func TestStrictConfirmedBody(t *testing.T) {
	cases := []struct {
		body string
		ok   bool
	}{
		{`{"confirmed":true}`, true}, {` { "confirmed" : true } `, true},
		{`{}`, false}, {`null`, false}, {`[]`, false}, {`{"confirmed":false}`, false},
		{`{"confirmed":"true"}`, false}, {`{"confirmed":null}`, false},
		{`{"confirmed":true,"confirmed":true}`, false}, {`{"confirmed":true,"extra":1}`, false},
		{`{"Confirmed":true}`, false}, {`{"confirmed":true} {}`, false},
		{`{"confirmed":true}` + strings.Repeat(" ", 1100), false},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			err := confirmedRequest(httptest.NewRecorder(), r)
			if (err == nil) != tc.ok {
				t.Fatalf("body %q: %v", tc.body, err)
			}
		})
	}
}

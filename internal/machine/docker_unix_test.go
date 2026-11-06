//go:build !windows

package machine

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestUnixSocketDockerMockOnly(t *testing.T) {
	// Use a fresh temporary socket. Never probe /var/run/docker.sock, the
	// environment, a Docker CLI, or an actual daemon.
	dir, err := os.MkdirTemp("", "panel-docker-mock-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "engine.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mutations atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Version":"mock-only"}`))
		case "/containers/" + testContainerID + "/start":
			if r.Method != "POST" {
				t.Error("unexpected mutation method")
			}
			mutations.Add(1)
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	})}
	done := make(chan struct{})
	go func() { _ = server.Serve(listener); close(done) }()
	defer func() { _ = server.Close(); <-done }()
	docker, err := newDockerClient(context.Background(), Options{DockerSocket: socket, AllowDockerWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	if docker.transport.Proxy != nil || !docker.transport.DisableKeepAlives || docker.client.CheckRedirect == nil {
		t.Fatal("Docker transport permits proxies, retries, or redirects")
	}
	s := fixtureService(t, Options{}, docker)
	status := decodeBody[dockerStatus](t, request(t, s, "GET", "/api/docker", ""))
	if !status.Available || status.Version != "mock-only" || !status.WriteEnabled {
		t.Fatalf("mock socket unavailable: %+v", status)
	}
	r := request(t, s, "POST", "/api/docker/containers/"+testContainerID+"/start", `{"confirmed":true}`)
	requireStatus(t, r, 202)
	task := decodeBody[struct{ Task dockerTask }](t, r).Task
	waitTaskStatus(t, s, task.ID, "succeeded")
	if mutations.Load() != 1 {
		t.Fatal("mock mutation count mismatch")
	}
}

func TestDockerRejectsNonUnixEndpoints(t *testing.T) {
	for _, socket := range []string{"tcp://127.0.0.1:2375", "http://localhost:2375", "relative.sock", "/tmp/x\n.sock"} {
		if _, err := newDockerClient(context.Background(), Options{DockerSocket: socket}); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", socket)
		}
	}
}

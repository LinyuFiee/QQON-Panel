package sites

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"panel/internal/jobrunner"
)

func TestLoopbackPanelAllowsDifferentBusinessUpstreamPort(t *testing.T) {
	s := &Service{panelHost: "127.0.0.1", options: Options{PanelPort: 53211}}
	if got, err := s.validateUpstream("http://127.0.0.1:53100"); err != nil || got != "http://127.0.0.1:53100" {
		t.Fatalf("ordinary local business port rejected: %s %v", got, err)
	}
	for _, target := range []string{"http://127.0.0.1:53211", "http://localhost:53211", "http://127.0.0.1:8443", "http://127.0.0.1:5173"} {
		if _, err := s.validateUpstream(target); err == nil {
			t.Fatalf("management upstream accepted: %s", target)
		}
	}
}

func TestRestrictedStartupDoesNotOpenSystemSiteState(t *testing.T) {
	data := t.TempDir()
	jobs, err := jobrunner.New(data)
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	// Even an existing inaccessible/corrupt registry is not adopted as permission.
	state := filepath.Join(data, "sites")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "state.json"), []byte("do not load without consent"), 0600); err != nil {
		t.Fatal(err)
	}
	allowed := false
	s, err := newService(Options{DataDir: data, Jobs: jobs, PanelHost: "panel.example", PanelPort: 5173,
		Permission: func() error {
			if allowed {
				return nil
			}
			return errors.New("no system consent")
		},
	}, hostAdapters{goos: "linux", strict: true, webRoot: "/srv/qqon-sites", configRoot: "/etc/nginx/conf.d"})
	if err != nil {
		t.Fatalf("restricted startup failed: %v", err)
	}
	defer s.Close()
	if !errors.Is(s.ready(), errDenied) {
		t.Fatal("restricted startup did not deny writes")
	}
	allowed = true
	if s.ready() == nil {
		t.Fatal("late consent adopted unloaded state without restart")
	}
	if got, _ := os.ReadFile(filepath.Join(state, "state.json")); string(got) != "do not load without consent" {
		t.Fatal("restricted startup modified state")
	}
}

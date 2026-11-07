package sites

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"panel/internal/jobrunner"
)

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

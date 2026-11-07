package machine

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var serviceAllowlist = [...]string{"nginx", "mysql", "mariadb", "redis-server", "redis", "docker"}
var phpServiceName = regexp.MustCompile(`^php(?:[0-9]{1,2}\.[0-9]{1,2})?-fpm\.service$`)

const serviceOutputLimit = 16 << 10

type serviceEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type servicesView struct {
	Available bool           `json:"available"`
	Reason    string         `json:"reason"`
	Services  []serviceEntry `json:"services"`
}

func unknownServices(reason string) servicesView {
	view := servicesView{Reason: reason, Services: make([]serviceEntry, 0, len(serviceAllowlist))}
	for _, name := range serviceAllowlist {
		view.Services = append(view.Services, serviceEntry{ID: name, Name: name, State: "unknown"})
	}
	return view
}

type commandOutputBudget struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	used   int
	over   bool
	cancel context.CancelFunc
}

type commandOutputWriter struct {
	budget *commandOutputBudget
	stdout bool
}

func (w commandOutputWriter) Write(data []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	remaining := serviceOutputLimit - w.budget.used
	n := min(len(data), remaining)
	w.budget.used += n
	if w.stdout {
		_, _ = w.budget.buffer.Write(data[:n])
	}
	if n != len(data) {
		w.budget.over = true
		w.budget.cancel()
		return n, errors.New("output exceeds limit")
	}
	return n, nil
}

type commandRunner func(context.Context, string, []string) ([]byte, error)

func runBoundedCommand(ctx context.Context, executable string, args []string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	// Do not invoke a shell or inherit locale, pager, shell or PATH hooks.
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0"}
	cmd.WaitDelay = 200 * time.Millisecond
	budget := &commandOutputBudget{cancel: cancel}
	cmd.Stdout = commandOutputWriter{budget: budget, stdout: true}
	cmd.Stderr = commandOutputWriter{budget: budget}
	err := cmd.Run()
	if budget.over {
		return nil, errors.New("systemctl output exceeds limit")
	}
	return budget.buffer.Bytes(), err
}

func queryServices(ctx context.Context, executable string, run commandRunner) servicesView {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	args := []string{"show", "--no-pager", "--property=Id,ActiveState,LoadState", "--"}
	for _, name := range serviceAllowlist {
		args = append(args, name+".service")
	}
	// systemctl expands these fixed unit patterns; no shell or user unit name is
	// involved. Only actually loaded PHP units are added to the cached view.
	args = append(args, "php-fpm.service", "php[0-9]*-fpm.service")
	data, err := run(ctx, executable, args)
	if err != nil || len(data) > serviceOutputLimit {
		return unknownServices("无法读取 systemd 服务状态（未运行、超时或无权限）")
	}
	view := unknownServices("")
	byID := make(map[string]int, len(serviceAllowlist))
	for i, name := range serviceAllowlist {
		byID[name+".service"] = i
	}
	found := 0
	id, state, loaded := "", "", ""
	flush := func() {
		if loaded == "loaded" && state != "" && phpServiceName.MatchString(id) && len(view.Services) < len(serviceAllowlist)+16 {
			if _, exists := byID[id]; !exists {
				name := strings.TrimSuffix(id, ".service")
				byID[id] = len(view.Services)
				view.Services = append(view.Services, serviceEntry{ID: name, Name: name, State: "unknown"})
			}
		}
		if idx, ok := byID[id]; ok && state != "" {
			found++
			switch state {
			case "active", "inactive", "failed":
				view.Services[idx].State = state
			default:
				view.Services[idx].State = "unknown"
			}
		}
		id, state, loaded = "", "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "Id="):
			id = strings.TrimPrefix(line, "Id=")
		case strings.HasPrefix(line, "ActiveState="):
			state = strings.TrimPrefix(line, "ActiveState=")
		case strings.HasPrefix(line, "LoadState="):
			loaded = strings.TrimPrefix(line, "LoadState=")
		}
	}
	flush()
	if found == 0 {
		view.Reason = "systemd 未返回可识别的服务状态"
	} else {
		view.Available = true
	}
	return view
}

func (s *Service) servicesLoop(fetch func(context.Context) servicesView) {
	defer s.wg.Done()
	ticker := time.NewTicker(serviceInterval)
	defer ticker.Stop()
	for {
		if s.ctx.Err() != nil {
			return
		}
		view := fetch(s.ctx)
		s.servicesMu.Lock()
		s.services = view
		s.servicesMu.Unlock()
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) handleServices(w http.ResponseWriter, _ *http.Request) {
	s.servicesMu.RLock()
	view := s.services
	view.Services = append([]serviceEntry{}, s.services.Services...)
	s.servicesMu.RUnlock()
	writeJSON(w, http.StatusOK, view)
}

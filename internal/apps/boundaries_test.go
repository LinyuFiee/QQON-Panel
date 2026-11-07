package apps

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRevocationAfterFinalCommandCannotReportSuccess(t *testing.T) {
	h := linuxFixture("dnf")
	var allowed atomic.Bool
	allowed.Store(true)
	h.onCommand = func(invocation) { allowed.Store(false) }
	_, _, mux := setupService(t, h, func() error {
		if !allowed.Load() {
			return errors.New("revoked")
		}
		return nil
	})
	queued := acceptedJob(t, post(t, mux, "/api/apps/nginx/install", `{"confirmed":true}`))
	result := awaitJob(t, mux, queued.ID)
	if result.Status != "failed" {
		t.Fatalf("unverified revoked result %+v", result)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.commands) != 1 || len(h.queries) != 0 {
		t.Fatal("revoked post-command discovery ran")
	}
}

func TestPostOperationCacheContainsVerifiedState(t *testing.T) {
	h := linuxFixture("apt")
	s, _, mux := setupService(t, h, func() error { return nil })
	queued := acceptedJob(t, post(t, mux, "/api/apps/nginx/install", `{"confirmed":true}`))
	if result := awaitJob(t, mux, queued.ID); result.Status != "succeeded" {
		t.Fatal(result)
	}
	h.mu.Lock()
	queries := len(h.queries)
	h.mu.Unlock()
	info, err := s.Capabilities(context.Background())
	if err != nil || !info.NginxInstalled {
		t.Fatalf("post-operation state %+v %v", info, err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queries) != queries {
		t.Fatal("verified post-operation cache was immediately invalidated")
	}
}

func TestFinitePHPScanFailureDoesNotReturnPartialInventory(t *testing.T) {
	h := linuxFixture("apt")
	for i := 0; i < 8193; i++ {
		h.dirs["/usr/lib/systemd/system"] = append(h.dirs["/usr/lib/systemd/system"], fakeEntry("fixture.service"))
	}
	s, _, _ := setupService(t, h, func() error { return nil })
	info, err := s.Capabilities(context.Background())
	if err == nil || info.Available || !strings.Contains(info.Reason, "发现失败") {
		t.Fatalf("oversized inventory accepted %+v %v", info, err)
	}
	if len(h.queries) != 0 {
		t.Fatal("partial discovery continued to packages")
	}
}

func TestMixedStateCannotHideUnknownUnit(t *testing.T) {
	units := map[string]unitInfo{
		"php8.2-fpm.service": {load: "loaded", state: "active"},
		"php8.3-fpm.service": {load: "loaded", state: "inactive"},
		"php8.4-fpm.service": {load: "not-found", state: "inactive"},
	}
	if got := aggregateState([]string{"php8.2-fpm.service", "php8.3-fpm.service", "php8.4-fpm.service"}, units); got != "unknown" {
		t.Fatalf("aggregate %s", got)
	}
}

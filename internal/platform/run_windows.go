//go:build windows

package platform

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows/svc"
)

const serviceStopTimeout = 30 * time.Second

// Run must be called by main, not from a background goroutine. SCM and console
// share the same callback; only the callback can declare its listener ready.
func Run(name string, run func(context.Context, func()) error) error {
	if err := validateRun(name, run); err != nil {
		return err
	}
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return runConsole(run, os.Interrupt)
	}
	handler := &serviceHandler{run: run, stopTimeout: serviceStopTimeout, exit: os.Exit}
	if err := svc.Run(name, handler); err != nil {
		return err
	}
	return handler.err
}

type serviceHandler struct {
	run         func(context.Context, func()) error
	stopTimeout time.Duration
	exit        func(int)
	err         error // Read by Run only after the SCM dispatcher has returned.
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	status := svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: 30000}
	changes <- status
	go func() { done <- h.run(ctx, func() { once.Do(func() { close(ready) }) }) }()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var deadline *time.Timer
	var expired <-chan time.Time
	defer func() {
		if deadline != nil {
			deadline.Stop()
		}
	}()
	stopping := false
	finish := func(err error) (bool, uint32) {
		if stopping && errors.Is(err, context.Canceled) {
			err = nil
		}
		h.err = err
		if err != nil {
			return true, 1
		}
		return false, 0
	}
	stop := func() {
		if stopping {
			return
		}
		stopping = true
		cancel()
		timeout := h.stopTimeout
		if timeout <= 0 {
			timeout = serviceStopTimeout
		}
		deadline = time.NewTimer(timeout)
		expired = deadline.C
		status = svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: uint32(timeout.Milliseconds())}
		changes <- status
	}
	for {
		select {
		case err := <-done:
			// x/sys reports Stopped only after Execute returns: never announce it
			// while run still owns open listeners, files or background workers.
			return finish(err)
		case <-ready:
			ready = nil
			if !stopping {
				status = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
				changes <- status
			}
		case request, ok := <-requests:
			if !ok {
				requests = nil
				stop()
				continue
			}
			switch request.Cmd {
			case svc.Interrogate:
				changes <- status
			case svc.Stop, svc.Shutdown:
				stop()
			}
		case <-ticker.C:
			if status.State == svc.StartPending || status.State == svc.StopPending {
				status.CheckPoint++
				changes <- status
			}
		case <-expired:
			select {
			case err := <-done:
				return finish(err)
			default:
			}
			// A stuck callback cannot be killed safely in Go. Terminate the
			// process with failure rather than falsely reporting graceful Stopped.
			exit := h.exit
			if exit == nil {
				exit = os.Exit
			}
			exit(1)
			expired = nil // Only reached by the non-terminating test hook.
		}
	}
}

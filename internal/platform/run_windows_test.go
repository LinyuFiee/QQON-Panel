//go:build windows

package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func nextStatus(t *testing.T, changes <-chan svc.Status, want svc.State) svc.Status {
	t.Helper()
	select {
	case status := <-changes:
		if status.State != want {
			t.Fatalf("service status = %v, want %v", status.State, want)
		}
		return status
	case <-time.After(2 * time.Second):
		t.Fatal("service status timed out")
		return svc.Status{}
	}
}

type handlerExit struct {
	specific bool
	code     uint32
}

func executeTestService(h *serviceHandler, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) <-chan handlerExit {
	done := make(chan handlerExit, 1)
	go func() {
		specific, code := h.Execute(nil, requests, changes)
		done <- handlerExit{specific, code}
	}()
	return done
}

func TestSCMReadinessAndGracefulStop(t *testing.T) {
	for _, command := range []svc.Cmd{svc.Stop, svc.Shutdown} {
		t.Run(map[svc.Cmd]string{svc.Stop: "stop", svc.Shutdown: "shutdown"}[command], func(t *testing.T) {
			readyGate, cleanup := make(chan struct{}), make(chan struct{})
			cancelled := make(chan struct{})
			requests := make(chan svc.ChangeRequest, 8)
			changes := make(chan svc.Status, 16)
			h := &serviceHandler{stopTimeout: time.Second, run: func(ctx context.Context, ready func()) error {
				<-readyGate
				ready()
				ready()
				<-ctx.Done()
				close(cancelled)
				<-cleanup
				return ctx.Err()
			}}
			done := executeTestService(h, requests, changes)
			start := nextStatus(t, changes, svc.StartPending)
			if start.Accepts != 0 || start.WaitHint == 0 {
				t.Fatal("service accepted controls before listener readiness")
			}
			requests <- svc.ChangeRequest{Cmd: svc.Interrogate}
			nextStatus(t, changes, svc.StartPending)
			close(readyGate)
			running := nextStatus(t, changes, svc.Running)
			if running.Accepts != svc.AcceptStop|svc.AcceptShutdown {
				t.Fatal("missing stop/shutdown controls")
			}
			requests <- svc.ChangeRequest{Cmd: command}
			nextStatus(t, changes, svc.StopPending)
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("STOP did not cancel callback")
			}
			requests <- svc.ChangeRequest{Cmd: svc.Stop}
			requests <- svc.ChangeRequest{Cmd: svc.Interrogate}
			nextStatus(t, changes, svc.StopPending)
			select {
			case <-done:
				t.Fatal("service exited before cleanup completed")
			default:
			}
			close(cleanup)
			select {
			case exit := <-done:
				if exit.code != 0 || exit.specific || h.err != nil {
					t.Fatal("normal stop returned failure")
				}
			case <-time.After(time.Second):
				t.Fatal("service did not finish after cleanup")
			}
		})
	}
}

func TestSCMStartupErrorAndEarlyStop(t *testing.T) {
	want := errors.New("listener failed")
	changes := make(chan svc.Status, 8)
	h := &serviceHandler{run: func(context.Context, func()) error { return want }}
	done := executeTestService(h, make(chan svc.ChangeRequest), changes)
	nextStatus(t, changes, svc.StartPending)
	select {
	case exit := <-done:
		if !exit.specific || exit.code == 0 || !errors.Is(h.err, want) {
			t.Fatal("startup error lost")
		}
	case <-time.After(time.Second):
		t.Fatal("startup error did not exit")
	}
	select {
	case <-changes:
		t.Fatal("failed startup reported Running")
	default:
	}

	requests := make(chan svc.ChangeRequest, 2)
	cleanup := make(chan struct{})
	h = &serviceHandler{run: func(ctx context.Context, ready func()) error {
		<-ctx.Done()
		ready() // A late listener must not resurrect a stopping service.
		<-cleanup
		return nil
	}}
	done = executeTestService(h, requests, changes)
	nextStatus(t, changes, svc.StartPending)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	nextStatus(t, changes, svc.StopPending)
	requests <- svc.ChangeRequest{Cmd: svc.Interrogate}
	nextStatus(t, changes, svc.StopPending)
	close(cleanup)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("early stop did not finish")
	}
	select {
	case status := <-changes:
		if status.State == svc.Running {
			t.Fatal("late ready resurrected service")
		}
	default:
	}
}

func TestSCMStopDeadlineDoesNotFakeStopped(t *testing.T) {
	requests := make(chan svc.ChangeRequest, 2)
	changes := make(chan svc.Status, 8)
	cleanup := make(chan struct{})
	exited := make(chan int, 1)
	h := &serviceHandler{stopTimeout: 20 * time.Millisecond, exit: func(code int) { exited <- code }, run: func(ctx context.Context, ready func()) error {
		ready()
		<-ctx.Done()
		<-cleanup
		return nil
	}}
	done := executeTestService(h, requests, changes)
	nextStatus(t, changes, svc.StartPending)
	nextStatus(t, changes, svc.Running)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	nextStatus(t, changes, svc.StopPending)
	select {
	case code := <-exited:
		if code == 0 {
			t.Fatal("deadline should terminate with failure")
		}
	case <-time.After(time.Second):
		t.Fatal("stop deadline not enforced")
	}
	select {
	case <-done:
		t.Fatal("returned Stopped despite incomplete cleanup")
	default:
	}
	close(cleanup)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("test callback leaked")
	}
}

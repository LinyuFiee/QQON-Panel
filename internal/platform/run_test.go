package platform

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestRunRejectsInvalidArguments(t *testing.T) {
	if err := Run("QQONPanel", nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	for _, name := range []string{"", "bad\x00name"} {
		if err := Run(name, func(context.Context, func()) error { return nil }); err == nil {
			t.Fatal("invalid service name accepted")
		}
	}
}

func TestConsoleReturnsErrorAndReleasesContext(t *testing.T) {
	want := errors.New("listener failed")
	var received context.Context
	err := runConsole(func(ctx context.Context, ready func()) error {
		received = ctx
		if ctx.Err() != nil {
			t.Fatal("console context already cancelled")
		}
		ready()
		ready()
		return want
	}, os.Interrupt)
	if !errors.Is(err, want) || received == nil || received.Err() != context.Canceled {
		t.Fatal("console lost callback error or leaked its signal context")
	}
}

package platform

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
)

func validateRun(name string, run func(context.Context, func()) error) error {
	if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00/\\") || run == nil {
		return errors.New("platform: invalid service name or callback")
	}
	return nil
}

func runConsole(run func(context.Context, func()) error, signals ...os.Signal) error {
	ctx, stop := signal.NotifyContext(context.Background(), signals...)
	defer stop()
	return run(ctx, func() {})
}

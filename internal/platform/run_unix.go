//go:build !windows

package platform

import (
	"context"
	"os"
	"syscall"
)

// Run owns process stop signals; run owns listener readiness and graceful cleanup.
func Run(name string, run func(context.Context, func()) error) error {
	if err := validateRun(name, run); err != nil {
		return err
	}
	return runConsole(run, os.Interrupt, syscall.SIGTERM)
}

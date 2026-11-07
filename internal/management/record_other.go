//go:build !linux && !windows

package management

import "errors"

func runtimeIdentity() (string, bool) { return "unsupported", false }

func readSystemConsent() ([]byte, error) {
	return nil, errors.New("system management is unsupported on this platform")
}

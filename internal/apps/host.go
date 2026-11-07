package apps

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"panel/internal/jobrunner"
)

// host is deliberately private: production callers cannot inject paths, package
// names, environment variables, executable argv, or authority bypasses via Options.
// Windows tests substitute it with an in-memory/fake Linux host.
type host interface {
	platform() string
	executable(string) bool
	readFile(string, int64) ([]byte, error)
	readDir(string, int) ([]fs.DirEntry, error)
	socket(string) bool
	query(context.Context, string, []string) ([]byte, error)
	command(context.Context, string, []string, *jobrunner.Progress) error
}

type realHost struct{ goos string }

func (h realHost) platform() string { return h.goos }
func (h realHost) command(ctx context.Context, executable string, args []string, progress *jobrunner.Progress) error {
	if h.goos != "linux" {
		return errors.New("not_supported: package and service operations require Linux")
	}
	return jobrunner.RunCommand(ctx, executable, args, progress)
}

type tools struct{ manager, packageTool, queryTool, systemctl, nginx string }

func detectTools(h host) tools {
	var result tools
	for _, path := range []string{"/usr/bin/apt-get", "/bin/apt-get"} {
		if h.executable(path) {
			result.manager, result.packageTool = "apt", path
			break
		}
	}
	if result.manager == "" {
		for _, path := range []string{"/usr/bin/dnf", "/bin/dnf"} {
			if h.executable(path) {
				result.manager, result.packageTool = "dnf", path
				break
			}
		}
	}
	queries := []string{"/usr/bin/dpkg-query", "/bin/dpkg-query"}
	if result.manager == "dnf" {
		queries = []string{"/usr/bin/rpm", "/bin/rpm"}
	}
	if result.manager != "" {
		for _, path := range queries {
			if h.executable(path) {
				result.queryTool = path
				break
			}
		}
	}
	for _, path := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		if h.executable(path) {
			result.systemctl = path
			break
		}
	}
	for _, path := range []string{"/usr/sbin/nginx", "/sbin/nginx", "/usr/local/sbin/nginx"} {
		if h.executable(path) {
			result.nginx = path
			break
		}
	}
	return result
}

func query(ctx context.Context, h host, executable string, args ...string) ([]byte, error) {
	if executable == "" {
		return nil, errors.New("required system dependency is missing")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := h.query(ctx, executable, args)
	if len(data) > 64<<10 {
		return nil, errors.New("system query exceeded 64 KiB")
	}
	return data, err
}

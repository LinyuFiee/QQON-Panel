package apps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"
)

var phpUnitPattern = regexp.MustCompile(`^php(?:[5-9]|[1-9][0-9])\.[0-9]{1,2}-fpm\.service$`)
var unitNamePattern = regexp.MustCompile(`^(?:nginx|mariadb|redis|redis-server|php-fpm|php(?:[5-9]|[1-9][0-9])\.[0-9]{1,2}-fpm)\.service$`)

type packageInfo struct {
	installed bool
	version   string
}
type unitInfo struct{ load, state string }

func baseView(reason string) snapshot {
	return snapshot{view: View{Reason: reason, Items: []Item{
		{ID: "nginx", Name: "Nginx", State: "unknown", Description: "HTTP 与反向代理服务；保留已有配置。"},
		{ID: "php-fpm", Name: "PHP-FPM", State: "unknown", Description: "发行版 PHP FastCGI；服务操作针对所有已发现的发行版 PHP-FPM 单元。"},
		{ID: "mariadb", Name: "MariaDB", State: "unknown", Description: "MariaDB 数据库；不重置账户、不删除数据。"},
		{ID: "redis", Name: "Redis", State: "unknown", Description: "Redis 数据服务；保留已有配置与数据。"},
	}, PHPRuntimes: []PHPRuntime{}}, runtime: RuntimeInfo{Reason: reason, PHPRuntimes: []PHPRuntime{}}, units: map[string][]string{}}
}

func (s *Service) state(ctx context.Context, force bool) (snapshot, error) {
	if ctx == nil {
		return baseView("请求上下文无效。"), errors.New("apps: context is required")
	}
	// Authority is checked before even looking for tools, reading system package
	// metadata, or using cached capabilities. Revocation is never cached for 10s.
	if err := s.checkPermission(); err != nil {
		return baseView(err.Error()), nil
	}
	if s.host.platform() != "linux" {
		return baseView("not_supported：软件安装和服务控制仅支持使用 apt/dnf 与 systemd 的 Linux；不会调用 Windows SCM。"), nil
	}
	s.mu.Lock()
	if !force && !s.cached.at.IsZero() && time.Since(s.cached.at) < 10*time.Second {
		value := cloneSnapshot(s.cached)
		s.mu.Unlock()
		return value, nil
	}
	s.mu.Unlock()
	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	if err := ctx.Err(); err != nil {
		return baseView("软件状态查询已取消。"), err
	}
	if err := s.checkPermission(); err != nil {
		return baseView(err.Error()), nil
	}
	s.mu.Lock()
	if !force && !s.cached.at.IsZero() && time.Since(s.cached.at) < 10*time.Second {
		value := cloneSnapshot(s.cached)
		s.mu.Unlock()
		return value, nil
	}
	generation := s.generation
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	value, err := discover(ctx, s.host)
	if err := s.checkPermission(); err != nil {
		return baseView(err.Error()), nil
	}
	value.at = time.Now()
	s.mu.Lock()
	if generation == s.generation {
		s.cached = cloneSnapshot(value)
	}
	s.mu.Unlock()
	return value, err
}

func cloneSnapshot(value snapshot) snapshot {
	value.view.Items = append([]Item{}, value.view.Items...)
	value.view.PHPRuntimes = append([]PHPRuntime{}, value.view.PHPRuntimes...)
	value.runtime.PHPRuntimes = append([]PHPRuntime{}, value.runtime.PHPRuntimes...)
	units := make(map[string][]string, len(value.units))
	for id, names := range value.units {
		units[id] = append([]string{}, names...)
	}
	value.units = units
	return value
}

func discover(ctx context.Context, h host) (snapshot, error) {
	value := baseView("")
	tools := detectTools(h)
	value.view.Manager = tools.manager
	if tools.manager == "" || tools.queryTool == "" || tools.systemctl == "" {
		value.view.Reason = "dependency_unknown：需要受信任的 apt/dnf、dpkg-query/rpm 与 systemctl；当前发行版依赖不可用。"
		value.runtime.Reason = value.view.Reason
		return value, nil
	}
	phpUnits, err := discoverPHPUnits(h, tools.manager)
	if err != nil {
		value.view.Reason, value.runtime.Reason = "PHP-FPM 发行版单元发现失败。", "PHP-FPM 发行版单元发现失败。"
		return value, err
	}
	packages, err := queryPackages(ctx, h, tools, phpUnits)
	if err != nil {
		value.view.Reason, value.runtime.Reason = "无法可靠读取发行版已安装软件版本。", "无法可靠读取发行版已安装软件版本。"
		return value, err
	}
	redisPackage, redisUnit := "redis-server", "redis-server.service"
	if tools.manager == "dnf" {
		redisPackage, redisUnit = "redis", "redis.service"
	}
	value.units["nginx"], value.units["mariadb"], value.units["redis"] = []string{"nginx.service"}, []string{"mariadb.service"}, []string{redisUnit}
	for _, unit := range phpUnits {
		if packages[strings.TrimSuffix(unit, ".service")].installed {
			value.units["php-fpm"] = append(value.units["php-fpm"], unit)
		}
	}
	names := []string{"nginx.service", "mariadb.service", redisUnit}
	names = append(names, value.units["php-fpm"]...)
	units, err := queryUnits(ctx, h, tools.systemctl, names)
	if err != nil {
		value.view.Reason, value.runtime.Reason = "无法可靠读取 systemd 服务状态。", "无法可靠读取 systemd 服务状态。"
		return value, err
	}
	packageByID := map[string]string{"nginx": "nginx", "php-fpm": "php-fpm", "mariadb": "mariadb-server", "redis": redisPackage}
	for i := range value.view.Items {
		item := &value.view.Items[i]
		info := packages[packageByID[item.ID]]
		item.Installed, item.Version = info.installed, info.version
		if item.ID == "php-fpm" {
			versions := []string{}
			for _, unit := range value.units[item.ID] {
				pkg := packages[strings.TrimSuffix(unit, ".service")]
				if pkg.installed {
					item.Installed = true
					versions = append(versions, pkg.version)
				}
			}
			if len(versions) > 0 {
				item.Version = strings.Join(versions, ", ")
			}
		}
		if !item.Installed {
			item.State = "not_installed"
			continue
		}
		item.State = aggregateState(value.units[item.ID], units)
	}
	value.view.Available = true
	value.runtime.Available = true
	value.runtime.NginxInstalled = value.view.Items[0].Installed
	value.runtime.NginxBinary = tools.nginx
	value.runtime.NginxRunning = value.view.Items[0].State == "active"
	if value.runtime.NginxInstalled {
		value.runtime.WebUser, value.runtime.WebGroup = nginxIdentity(h)
		if tools.nginx == "" || value.runtime.WebUser == "" {
			value.runtime.Available, value.runtime.Reason = false, "dependency_unknown：无法确认受信任的 Nginx 可执行文件或非 root 工作进程账户。"
		}
	}
	for _, unit := range value.units["php-fpm"] {
		if units[unit].load != "loaded" || units[unit].state != "active" {
			continue
		}
		if runtime, ok := discoverRuntime(h, unit, packages[strings.TrimSuffix(unit, ".service")].version); ok {
			value.runtime.PHPRuntimes = append(value.runtime.PHPRuntimes, runtime)
		}
	}
	value.view.PHPRuntimes = append([]PHPRuntime{}, value.runtime.PHPRuntimes...)
	return value, nil
}

func discoverPHPUnits(h host, manager string) ([]string, error) {
	seen := map[string]bool{}
	for _, directory := range []string{"/usr/lib/systemd/system", "/lib/systemd/system", "/etc/systemd/system"} {
		entries, err := h.readDir(directory, 8192)
		if err != nil {
			// Missing optional roots are normal; a protected-path or finite-scan
			// failure is not equivalent to an empty runtime inventory.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || (manager == "apt" && !phpUnitPattern.MatchString(name)) || (manager == "dnf" && name != "php-fpm.service") {
				continue
			}
			if _, err := h.readFile(directory+"/"+name, 64<<10); err != nil {
				continue
			}
			seen[name] = true
			if len(seen) > 16 {
				return nil, errors.New("too many PHP-FPM units (maximum 16)")
			}
		}
	}
	units := make([]string, 0, len(seen))
	for unit := range seen {
		units = append(units, unit)
	}
	sort.Strings(units)
	return units, nil
}

func queryPackages(ctx context.Context, h host, tools tools, phpUnits []string) (map[string]packageInfo, error) {
	redis := "redis-server"
	if tools.manager == "dnf" {
		redis = "redis"
	}
	names := []string{"nginx", "php-fpm", "mariadb-server", redis}
	for _, unit := range phpUnits {
		if unit != "php-fpm.service" {
			names = append(names, strings.TrimSuffix(unit, ".service"))
		}
	}
	args := []string{"-W", "-f=${binary:Package}\t${Status}\t${Version}\n", "--"}
	if tools.manager == "dnf" {
		args = []string{"-q", "--queryformat", "%{NAME}\t%{VERSION}-%{RELEASE}\n", "--"}
	}
	data, err := query(ctx, h, tools.queryTool, append(args, names...)...)
	if err != nil && commandExitCode(err) != 1 {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	result := map[string]packageInfo{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 0 {
			continue
		}
		name := strings.SplitN(fields[0], ":", 2)[0]
		if !allowed[name] {
			continue
		}
		if tools.manager == "apt" {
			if len(fields) != 3 {
				return nil, errors.New("invalid dpkg-query record")
			}
			// A held package is still installed. Do not confuse dpkg's desired
			// selection (install/hold/deinstall) with its actual installation state.
			status := strings.Fields(fields[1])
			if len(status) != 3 || status[1] != "ok" || status[2] != "installed" {
				continue
			}
		} else if len(fields) != 2 {
			return nil, errors.New("invalid rpm record")
		}
		version := fields[len(fields)-1]
		if len(version) == 0 || len(version) > 128 || strings.ContainsAny(version, "\x00\r\n") {
			return nil, errors.New("invalid package version")
		}
		result[name] = packageInfo{true, version}
	}
	return result, nil
}

func queryUnits(ctx context.Context, h host, systemctl string, names []string) (map[string]unitInfo, error) {
	for _, unit := range names {
		if !unitNamePattern.MatchString(unit) {
			return nil, errors.New("unit is not allowlisted")
		}
	}
	args := append([]string{"show", "--no-pager", "--property=Id,LoadState,ActiveState", "--"}, names...)
	data, err := query(ctx, h, systemctl, args...)
	if err != nil && commandExitCode(err) != 1 {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	result := map[string]unitInfo{}
	id, info := "", unitInfo{}
	flush := func() {
		if allowed[id] {
			result[id] = info
		}
		id, info = "", unitInfo{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			flush()
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch key {
		case "Id":
			id = value
		case "LoadState":
			info.load = value
		case "ActiveState":
			info.state = value
		}
	}
	flush()
	if len(result) == 0 {
		return nil, errors.New("no recognized systemd states")
	}
	return result, nil
}

func aggregateState(names []string, units map[string]unitInfo) string {
	if len(names) == 0 {
		return "unknown"
	}
	state := ""
	mixed := false
	for _, name := range names {
		unit := units[name]
		if unit.load != "loaded" {
			return "unknown"
		}
		if unit.state != "active" && unit.state != "inactive" && unit.state != "failed" {
			return "unknown"
		}
		if state != "" && state != unit.state {
			mixed = true
		}
		state = unit.state
	}
	if mixed {
		return "mixed"
	}
	return state
}

func commandExitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return -1
}

func findItem(value snapshot, id string) (Item, error) {
	for _, item := range value.view.Items {
		if item.ID == id {
			return item, nil
		}
	}
	return Item{}, fmt.Errorf("unknown app %s", id)
}

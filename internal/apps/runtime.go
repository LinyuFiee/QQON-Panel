package apps

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

var accountPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}\$?$`)

// Parse only the root-managed main config's top-level user directive; do not
// execute nginx -T (which would dump configurations/credentials into task logs).
func nginxIdentity(h host) (string, string) {
	body, err := h.readFile("/etc/nginx/nginx.conf", 128<<10)
	if err != nil {
		return "", ""
	}
	var text strings.Builder
	for _, line := range strings.Split(string(body), "\n") {
		line, _, _ = strings.Cut(line, "#")
		text.WriteString(line)
		text.WriteByte('\n')
	}
	main, _, _ := strings.Cut(text.String(), "{")
	for _, directive := range strings.Split(main, ";") {
		fields := strings.Fields(directive)
		if len(fields) == 0 || fields[0] != "user" {
			continue
		}
		if len(fields) != 2 && len(fields) != 3 {
			return "", ""
		}
		user := fields[1]
		uid, gid, ok := account(h, user)
		if !ok || uid == 0 {
			return "", ""
		}
		group := ""
		if len(fields) == 3 {
			group = fields[2]
		}
		group, groupID, ok := groupAccount(h, group, gid)
		if !ok || groupID == 0 {
			return "", ""
		}
		return user, group
	}
	return "", ""
}

func account(h host, name string) (uint64, uint64, bool) {
	if !accountPattern.MatchString(name) || name == "root" {
		return 0, 0, false
	}
	data, err := h.readFile("/etc/passwd", 512<<10)
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || fields[0] != name {
			continue
		}
		uid, e1 := strconv.ParseUint(fields[2], 10, 32)
		gid, e2 := strconv.ParseUint(fields[3], 10, 32)
		return uid, gid, e1 == nil && e2 == nil && uid != 0
	}
	return 0, 0, false
}

func groupAccount(h host, name string, gid uint64) (string, uint64, bool) {
	if name != "" && (!accountPattern.MatchString(name) || name == "root") {
		return "", 0, false
	}
	data, err := h.readFile("/etc/group", 512<<10)
	if err != nil {
		return "", 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 4 || !accountPattern.MatchString(fields[0]) {
			continue
		}
		id, err := strconv.ParseUint(fields[2], 10, 32)
		if err == nil && id != 0 && (name != "" && fields[0] == name || name == "" && id == gid) {
			return fields[0], id, true
		}
	}
	return "", 0, false
}

func discoverRuntime(h host, unit, version string) (PHPRuntime, bool) {
	id := strings.TrimSuffix(unit, ".service")
	configuration, expected := "", ""
	if unit == "php-fpm.service" {
		configuration, expected = "/etc/php-fpm.d/www.conf", "/run/php-fpm/www.sock"
	} else if phpUnitPattern.MatchString(unit) {
		phpVersion := strings.TrimSuffix(strings.TrimPrefix(id, "php"), "-fpm")
		configuration, expected = "/etc/php/"+phpVersion+"/fpm/pool.d/www.conf", "/run/php/"+id+".sock"
	} else {
		return PHPRuntime{}, false
	}
	body, err := h.readFile(configuration, 128<<10)
	if err != nil {
		return PHPRuntime{}, false
	}
	pool := ""
	values := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			pool = strings.TrimSpace(line)
			continue
		}
		if pool != "[www]" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key != "listen" && key != "user" && key != "group" {
			continue
		}
		if _, exists := values[key]; exists {
			return PHPRuntime{}, false
		}
		value, _, _ = strings.Cut(value, ";")
		values[key] = strings.TrimSpace(value)
	}
	listen := strings.Trim(values["listen"], `"'`)
	if strings.HasPrefix(listen, "/var/run/") {
		listen = "/run/" + strings.TrimPrefix(listen, "/var/run/")
	}
	if listen != expected || path.Clean(listen) != listen {
		return PHPRuntime{}, false
	}
	uid, gid, ok := account(h, values["user"])
	if !ok || uid == 0 {
		return PHPRuntime{}, false
	}
	if _, groupID, ok := groupAccount(h, values["group"], gid); !ok || groupID == 0 {
		return PHPRuntime{}, false
	}
	if !h.socket(expected) {
		return PHPRuntime{}, false
	}
	return PHPRuntime{ID: id, Version: version, Unit: unit, Socket: expected}, true
}

//go:build linux

package machine

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type cpuCounters struct{ total, idle uint64 }
type netCounters struct{ rx, tx uint64 }

type linuxSampler struct {
	started  time.Time
	procRoot string
	osName   string
	statfs   func(string, *syscall.Statfs_t) error
	prevCPU  *cpuCounters
	prevNet  *netCounters
	prevTime time.Time
}

func newHostSampler(started time.Time) hostSampler {
	name := "linux"
	if data, err := readBoundedFile("/etc/os-release", 16<<10); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if value, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				value = strings.TrimSpace(value)
				if decoded, err := strconv.Unquote(value); err == nil {
					value = decoded
				} else {
					value = strings.Trim(value, "'\"")
				}
				if len(value) > 0 && len(value) <= 256 {
					name = value
				}
				break
			}
		}
	}
	return &linuxSampler{started: started, procRoot: "/proc", osName: name, statfs: syscall.Statfs}
}

func readBoundedFile(name string, limit int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("sample unavailable or exceeds limit")
	}
	return data, nil
}

func (p *linuxSampler) read(name string) ([]byte, error) {
	return readBoundedFile(filepath.Join(p.procRoot, name), 512<<10)
}

func parseCPU(data []byte) (cpuCounters, error) {
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuCounters{}, errors.New("invalid cpu sample")
	}
	var c cpuCounters
	// Guest values are already included in user/nice; do not count twice.
	for i := 1; i < len(fields) && i <= 8; i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil || math.MaxUint64-c.total < v {
			return cpuCounters{}, errors.New("invalid cpu sample")
		}
		c.total += v
		if i == 4 || i == 5 {
			c.idle += v
		}
	}
	return c, nil
}

func parseMemory(data []byte) (memoryMetric, error) {
	values := make(map[string]uint64, 2)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:") {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return memoryMetric{}, errors.New("invalid memory sample")
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || v > math.MaxUint64/1024 {
			return memoryMetric{}, errors.New("invalid memory sample")
		}
		values[fields[0]] = v * 1024
	}
	total, hasTotal := values["MemTotal:"]
	available, hasAvailable := values["MemAvailable:"]
	if !hasTotal || !hasAvailable || total == 0 || available > total {
		return memoryMetric{}, errors.New("memory sample unavailable")
	}
	used := total - available
	percent := float64(used) * 100 / float64(total)
	return memoryMetric{&used, &total, &percent}, nil
}

func parseNetwork(data []byte) (netCounters, error) {
	var counters netCounters
	seen := false
	for _, line := range strings.Split(string(data), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			return netCounters{}, errors.New("invalid network sample")
		}
		rx, errRX := strconv.ParseUint(fields[0], 10, 64)
		tx, errTX := strconv.ParseUint(fields[8], 10, 64)
		if errRX != nil || errTX != nil || math.MaxUint64-counters.rx < rx || math.MaxUint64-counters.tx < tx {
			return netCounters{}, errors.New("invalid network sample")
		}
		counters.rx += rx
		counters.tx += tx
		seen = true
	}
	if !seen {
		return netCounters{}, errors.New("no non-loopback network counters")
	}
	return counters, nil
}

func (p *linuxSampler) sample(now time.Time) systemView {
	view := basicSample(p.started, now)
	view.OS = p.osName
	missing := make([]string, 0, 6)
	data, err := p.read("stat")
	if err == nil {
		var cpu cpuCounters
		cpu, err = parseCPU(data)
		if err == nil {
			if p.prevCPU != nil && cpu.total > p.prevCPU.total && cpu.idle >= p.prevCPU.idle {
				total, idle := cpu.total-p.prevCPU.total, cpu.idle-p.prevCPU.idle
				if idle <= total {
					view.CPUPercent = pointer(float64(total-idle) * 100 / float64(total))
				}
			}
			p.prevCPU = &cpu
		}
	}
	if err != nil {
		p.prevCPU = nil
		missing = append(missing, "CPU")
	} else if view.CPUPercent == nil {
		missing = append(missing, "CPU等待有效采样间隔")
	}

	data, err = p.read("meminfo")
	if err == nil {
		view.Memory, err = parseMemory(data)
	}
	if err != nil {
		missing = append(missing, "内存")
	}

	var fs syscall.Statfs_t
	if err := p.statfs("/", &fs); err == nil && fs.Bsize > 0 && fs.Blocks > 0 && fs.Bfree <= fs.Blocks && fs.Blocks <= math.MaxUint64/uint64(fs.Bsize) {
		total := fs.Blocks * uint64(fs.Bsize)
		used := (fs.Blocks - fs.Bfree) * uint64(fs.Bsize)
		view.Disk = memoryMetric{pointer(used), pointer(total), pointer(float64(used) * 100 / float64(total))}
	} else {
		missing = append(missing, "根文件系统")
	}

	data, err = p.read("uptime")
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			seconds, parseErr := strconv.ParseFloat(fields[0], 64)
			if parseErr == nil && seconds >= 0 && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) {
				view.UptimeSeconds = &seconds
			}
		}
	}
	if view.UptimeSeconds == nil {
		missing = append(missing, "运行时间")
	}

	data, err = p.read("net/dev")
	if err == nil {
		var counters netCounters
		counters, err = parseNetwork(data)
		if err == nil {
			elapsed := now.Sub(p.prevTime).Seconds()
			if p.prevNet != nil && elapsed > 0 && counters.rx >= p.prevNet.rx && counters.tx >= p.prevNet.tx {
				view.Network = networkMetric{pointer(float64(counters.rx-p.prevNet.rx) / elapsed), pointer(float64(counters.tx-p.prevNet.tx) / elapsed)}
			}
			p.prevNet = &counters
		}
	}
	if err != nil {
		p.prevNet = nil
		missing = append(missing, "网络")
	} else if view.Network.RX == nil {
		missing = append(missing, "网络等待有效采样间隔")
	}
	p.prevTime = now

	data, err = p.read("self/statm")
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			pages, parseErr := strconv.ParseUint(fields[1], 10, 64)
			pageSize := uint64(os.Getpagesize())
			if parseErr == nil && pages <= math.MaxUint64/pageSize {
				view.Panel.RSSBytes = pointer(pages * pageSize)
			}
		}
	}
	if view.Panel.RSSBytes == nil {
		missing = append(missing, "面板RSS")
	}
	view.Available = view.Memory.Total != nil && p.prevCPU != nil
	if len(missing) > 0 {
		view.Reason = fmt.Sprintf("以下指标暂不可用：%s", strings.Join(missing, "、"))
	}
	return view
}

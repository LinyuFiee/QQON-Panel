//go:build windows

package machine

import (
	"errors"
	"math"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maxWindowsInterfaces = 4096

var (
	metricsKernel32              = windows.NewLazySystemDLL("kernel32.dll")
	getSystemTimes               = metricsKernel32.NewProc("GetSystemTimes")
	globalMemoryStatusEx         = metricsKernel32.NewProc("GlobalMemoryStatusEx")
	getActiveProcessorGroupCount = metricsKernel32.NewProc("GetActiveProcessorGroupCount")
	getProcessMemoryInfo         = metricsKernel32.NewProc("K32GetProcessMemoryInfo")
	getIfTable2                  = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetIfTable2")
	errCPUProcessorGroups        = errors.New("GetSystemTimes only covers the calling processor group")
)

type windowsCPUCounters struct{ idle, kernel, user uint64 }
type windowsNetCounters struct{ rx, tx uint64 }

type windowsMetricsSource struct {
	cpu     func() (windowsCPUCounters, error)
	memory  func() (memoryMetric, error)
	disk    func() (memoryMetric, error)
	network func() (map[uint64]windowsNetCounters, error)
	uptime  func() (*float64, error)
	rss     func() (*uint64, error)
}

type windowsSampler struct {
	started  time.Time
	source   windowsMetricsSource
	prevCPU  *windowsCPUCounters
	prevNet  map[uint64]windowsNetCounters
	prevTime time.Time
}

func newHostSampler(started time.Time) hostSampler {
	return &windowsSampler{started: started, source: windowsMetricsSource{
		cpu: readWindowsCPU, memory: readWindowsMemory, disk: readWindowsDisk,
		network: readWindowsNetwork, uptime: readWindowsUptime, rss: readWindowsRSS,
	}}
}

func (p *windowsSampler) sample(now time.Time) systemView {
	view := basicSample(p.started, now)
	missing := make([]string, 0, 6)
	cpu, err := p.source.cpu()
	if err != nil {
		p.prevCPU = nil
		if errors.Is(err, errCPUProcessorGroups) {
			missing = append(missing, "CPU：GetSystemTimes在多处理器组主机上不能代表整机")
		} else {
			missing = append(missing, "CPU")
		}
	} else {
		view.CPUPercent = windowsCPUPercent(p.prevCPU, cpu)
		p.prevCPU = &cpu
		if view.CPUPercent == nil {
			missing = append(missing, "CPU等待有效采样间隔")
		}
	}
	if metric, err := p.source.memory(); err == nil {
		view.Memory = metric
	} else {
		missing = append(missing, "内存")
	}
	if metric, err := p.source.disk(); err == nil {
		view.Disk = metric
	} else {
		missing = append(missing, "系统卷磁盘")
	}
	if uptime, err := p.source.uptime(); err == nil {
		view.UptimeSeconds = uptime
	} else {
		missing = append(missing, "运行时间")
	}
	counters, err := p.source.network()
	if err != nil {
		p.prevNet = nil
		missing = append(missing, "网络")
	} else {
		view.Network = windowsNetworkRate(p.prevNet, counters, now.Sub(p.prevTime).Seconds())
		p.prevNet = counters
		if view.Network.RX == nil {
			missing = append(missing, "网络等待有效采样间隔或接口变化")
		}
	}
	p.prevTime = now
	if rss, err := p.source.rss(); err == nil {
		view.Panel.RSSBytes = rss
	} else {
		missing = append(missing, "面板RSS")
	}
	view.Available = view.Memory.Total != nil && p.prevCPU != nil
	// Keep the existing JSON fields, but never label the current-drive '/' as
	// the data disk. With no dataDir in this interface we sample the Windows volume.
	view.Reason = "磁盘统计Windows系统卷；网络合计活动非回环接口（含虚拟接口）"
	if len(missing) != 0 {
		view.Reason += "；以下指标暂不可用：" + strings.Join(missing, "、")
	}
	return view
}

func windowsCPUPercent(previous *windowsCPUCounters, current windowsCPUCounters) *float64 {
	if previous == nil || current.kernel < previous.kernel || current.user < previous.user || current.idle < previous.idle {
		return nil
	}
	kernel, user, idle := current.kernel-previous.kernel, current.user-previous.user, current.idle-previous.idle
	if math.MaxUint64-kernel < user || kernel+user == 0 || idle > kernel || current.idle > current.kernel {
		return nil
	}
	// GetSystemTimes kernel time already includes idle time.
	total := kernel + user
	return pointer(float64(total-idle) * 100 / float64(total))
}

func windowsNetworkRate(previous, current map[uint64]windowsNetCounters, seconds float64) networkMetric {
	if len(previous) == 0 || len(previous) != len(current) || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return networkMetric{}
	}
	var rx, tx uint64
	for id, now := range current {
		before, ok := previous[id]
		if !ok || now.rx < before.rx || now.tx < before.tx || math.MaxUint64-rx < now.rx-before.rx || math.MaxUint64-tx < now.tx-before.tx {
			return networkMetric{}
		}
		rx += now.rx - before.rx
		tx += now.tx - before.tx
	}
	rxRate, txRate := float64(rx)/seconds, float64(tx)/seconds
	if math.IsInf(rxRate, 0) || math.IsInf(txRate, 0) {
		return networkMetric{}
	}
	return networkMetric{&rxRate, &txRate}
}

func windowsCapacity(total, free uint64) (memoryMetric, error) {
	if total == 0 || free > total {
		return memoryMetric{}, errors.New("invalid Windows capacity sample")
	}
	used := total - free
	return memoryMetric{&used, &total, pointer(float64(used) * 100 / float64(total))}, nil
}

func windowsCallError(err error) error {
	if err == nil || errors.Is(err, windows.ERROR_SUCCESS) {
		return errors.New("Windows metrics API returned failure")
	}
	return err
}

func readWindowsCPU() (windowsCPUCounters, error) {
	if err := getActiveProcessorGroupCount.Find(); err != nil {
		return windowsCPUCounters{}, err
	}
	groups, _, err := getActiveProcessorGroupCount.Call()
	if groups == 0 {
		return windowsCPUCounters{}, windowsCallError(err)
	}
	if groups != 1 {
		return windowsCPUCounters{}, errCPUProcessorGroups
	}
	if err := getSystemTimes.Find(); err != nil {
		return windowsCPUCounters{}, err
	}
	var idle, kernel, user windows.Filetime
	ok, _, err := getSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if ok == 0 {
		return windowsCPUCounters{}, windowsCallError(err)
	}
	value := func(ft windows.Filetime) uint64 { return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime) }
	counters := windowsCPUCounters{value(idle), value(kernel), value(user)}
	if counters.idle > counters.kernel {
		return windowsCPUCounters{}, errors.New("invalid Windows CPU counters")
	}
	return counters, nil
}

// MEMORYSTATUSEX has a fixed 64-byte layout on supported Windows architectures.
type windowsMemoryStatus struct {
	Length, MemoryLoad                               uint32
	TotalPhys, AvailPhys                             uint64
	TotalPageFile, AvailPageFile                     uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual uint64
}

func readWindowsMemory() (memoryMetric, error) {
	if err := globalMemoryStatusEx.Find(); err != nil {
		return memoryMetric{}, err
	}
	status := windowsMemoryStatus{Length: uint32(unsafe.Sizeof(windowsMemoryStatus{}))}
	ok, _, err := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return memoryMetric{}, windowsCallError(err)
	}
	return windowsCapacity(status.TotalPhys, status.AvailPhys)
}

func readWindowsDisk() (memoryMetric, error) {
	systemDir, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return memoryMetric{}, err
	}
	path, err := windows.UTF16PtrFromString(systemDir)
	if err != nil {
		return memoryMetric{}, err
	}
	var volume [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumePathName(path, &volume[0], uint32(len(volume))); err != nil {
		return memoryMetric{}, err
	}
	// Never block the sampler on an arbitrary network/share path.
	if windows.GetDriveType(&volume[0]) != windows.DRIVE_FIXED {
		return memoryMetric{}, errors.New("Windows system volume is not a local fixed drive")
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(&volume[0], &available, &total, &free); err != nil {
		return memoryMetric{}, err
	}
	return windowsCapacity(total, free)
}

func readWindowsNetwork() (map[uint64]windowsNetCounters, error) {
	if err := getIfTable2.Find(); err != nil {
		return nil, err
	}
	var table *windows.MibIfTable2
	code, _, _ := getIfTable2.Call(uintptr(unsafe.Pointer(&table)))
	if table != nil {
		defer windows.FreeMibTable(unsafe.Pointer(table))
	}
	if code != 0 {
		return nil, windows.Errno(code)
	}
	if table == nil || table.NumEntries == 0 || table.NumEntries > maxWindowsInterfaces {
		return nil, errors.New("Windows interface table unavailable or exceeds limit")
	}
	// The OS owns the allocation; bound the count before constructing a slice.
	return windowsNetworkCounters(unsafe.Slice(&table.Table[0], int(table.NumEntries)))
}

func windowsNetworkCounters(rows []windows.MibIfRow2) (map[uint64]windowsNetCounters, error) {
	if len(rows) > maxWindowsInterfaces {
		return nil, errors.New("Windows interface count exceeds limit")
	}
	result := make(map[uint64]windowsNetCounters)
	for _, row := range rows {
		if row.Type == windows.IF_TYPE_SOFTWARE_LOOPBACK || row.OperStatus != windows.IfOperStatusUp {
			continue
		}
		if _, duplicate := result[row.InterfaceLuid]; duplicate {
			return nil, errors.New("duplicate Windows interface identity")
		}
		result[row.InterfaceLuid] = windowsNetCounters{row.InOctets, row.OutOctets}
	}
	if len(result) == 0 {
		return nil, errors.New("no active non-loopback Windows interfaces")
	}
	return result, nil
}

func readWindowsUptime() (*float64, error) {
	// The official wrapper reads GetTickCount64 (not process uptime).
	seconds := windows.DurationSinceBoot().Seconds()
	if seconds < 0 {
		return nil, errors.New("invalid Windows uptime")
	}
	return &seconds, nil
}

// PROCESS_MEMORY_COUNTERS uses SIZE_T (uintptr) for working-set/page counters.
type windowsProcessMemory struct {
	Size, PageFaultCount                               uint32
	PeakWorkingSetSize, WorkingSetSize                 uintptr
	QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage       uintptr
	QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage uintptr
	PagefileUsage, PeakPagefileUsage                   uintptr
}

func readWindowsRSS() (*uint64, error) {
	if err := getProcessMemoryInfo.Find(); err != nil {
		return nil, err
	}
	status := windowsProcessMemory{Size: uint32(unsafe.Sizeof(windowsProcessMemory{}))}
	ok, _, err := getProcessMemoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&status)), uintptr(status.Size))
	if ok == 0 {
		return nil, windowsCallError(err)
	}
	return pointer(uint64(status.WorkingSetSize)), nil
}

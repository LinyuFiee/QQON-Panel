//go:build windows

package machine

import (
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsSamplingRatesAndSharedSnapshot(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cpu := windowsCPUCounters{idle: 850, kernel: 900, user: 100}
	net := map[uint64]windowsNetCounters{1: {rx: 1000, tx: 2000}}
	calls := 0
	p := &windowsSampler{started: base.Add(-time.Minute), source: windowsMetricsSource{
		cpu:     func() (windowsCPUCounters, error) { calls++; return cpu, nil },
		memory:  func() (memoryMetric, error) { return windowsCapacity(1000, 400) },
		disk:    func() (memoryMetric, error) { return windowsCapacity(409600, 122880) },
		network: func() (map[uint64]windowsNetCounters, error) { return net, nil },
		uptime:  func() (*float64, error) { return pointer(1234.25), nil },
		rss:     func() (*uint64, error) { return pointer(uint64(20480)), nil },
	}}
	first := p.sample(base)
	if !first.Available || first.CPUPercent != nil || first.Network.RX != nil || first.Network.TX != nil {
		t.Fatal("first rates must be null")
	}
	if first.Memory.Percent == nil || *first.Memory.Percent != 60 || first.Disk.Percent == nil || *first.Disk.Percent != 70 || first.Panel.RSSBytes == nil || *first.Panel.RSSBytes != 20480 {
		t.Fatal("incorrect capacities")
	}
	cpu = windowsCPUCounters{idle: 890, kernel: 960, user: 140}
	net = map[uint64]windowsNetCounters{1: {rx: 1400, tx: 2600}}
	second := p.sample(base.Add(2 * time.Second))
	if second.CPUPercent == nil || *second.CPUPercent != 60 || second.Network.RX == nil || *second.Network.RX != 200 || *second.Network.TX != 300 {
		t.Fatal("incorrect Windows rates")
	}
	if strings.Contains(second.Reason, "暂不可用") || !strings.Contains(second.Reason, "系统卷") {
		t.Fatal("missing truthful disk scope")
	}
	s := &Service{}
	s.storeSample(second)
	for range 5 {
		s.handleSystem(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/system", nil))
	}
	if calls != 2 || sampleInterval != 2*time.Second {
		t.Fatal("HTTP polling triggered fresh sampling")
	}
	cpu = windowsCPUCounters{idle: 1, kernel: 2, user: 1}
	net = map[uint64]windowsNetCounters{1: {rx: 1, tx: 1}}
	reset := p.sample(base.Add(4 * time.Second))
	if reset.CPUPercent != nil || reset.Network.RX != nil {
		t.Fatal("counter reset fabricated utilization")
	}
}

func TestWindowsMetricsFailuresStayNullAndRecover(t *testing.T) {
	failure := errors.New("fixture API failure")
	p := &windowsSampler{started: time.Now(), prevCPU: &windowsCPUCounters{}, prevNet: map[uint64]windowsNetCounters{1: {}}, source: windowsMetricsSource{
		cpu:     func() (windowsCPUCounters, error) { return windowsCPUCounters{}, errCPUProcessorGroups },
		memory:  func() (memoryMetric, error) { return memoryMetric{}, failure },
		disk:    func() (memoryMetric, error) { return memoryMetric{}, failure },
		network: func() (map[uint64]windowsNetCounters, error) { return nil, failure },
		uptime:  func() (*float64, error) { return nil, failure },
		rss:     func() (*uint64, error) { return nil, failure },
	}}
	view := p.sample(time.Now())
	if view.Available || view.CPUPercent != nil || view.Memory.Total != nil || view.Disk.Total != nil || view.Network.RX != nil || view.UptimeSeconds != nil || view.Panel.RSSBytes != nil {
		t.Fatal("failed APIs produced fabricated metrics")
	}
	if p.prevCPU != nil || p.prevNet != nil || !strings.Contains(view.Reason, "多处理器组") || view.Panel.HeapBytes == 0 {
		t.Fatal("stale counters or missing actual reason/runtime")
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"cpuPercent":null`, `"rssBytes":null`, `"rxBytesPerSecond":null`, `"uptimeSeconds":null`} {
		if !strings.Contains(string(data), field) {
			t.Fatal("failed metric missing JSON null:", field)
		}
	}
	p.source.cpu = func() (windowsCPUCounters, error) { return windowsCPUCounters{idle: 10, kernel: 20, user: 10}, nil }
	p.source.network = func() (map[uint64]windowsNetCounters, error) {
		return map[uint64]windowsNetCounters{1: {rx: 100, tx: 100}}, nil
	}
	recovered := p.sample(time.Now().Add(2 * time.Second))
	if recovered.CPUPercent != nil || recovered.Network.RX != nil {
		t.Fatal("recovery used stale baseline")
	}
}

func TestWindowsMetricBounds(t *testing.T) {
	for _, pair := range [][2]uint64{{0, 0}, {1, 2}} {
		if _, err := windowsCapacity(pair[0], pair[1]); err == nil {
			t.Fatal("invalid capacity accepted")
		}
	}
	for _, current := range []windowsCPUCounters{{}, {idle: 20, kernel: 10, user: 1}, {idle: 0, kernel: math.MaxUint64, user: 1}} {
		if windowsCPUPercent(&windowsCPUCounters{}, current) != nil {
			t.Fatal("bad CPU delta accepted")
		}
	}
	previous := map[uint64]windowsNetCounters{1: {rx: 100, tx: 100}}
	for _, seconds := range []float64{0, -1, math.Inf(1), math.NaN()} {
		if windowsNetworkRate(previous, previous, seconds).RX != nil {
			t.Fatal("invalid sample interval accepted")
		}
	}
	if windowsNetworkRate(previous, map[uint64]windowsNetCounters{2: {rx: 200, tx: 200}}, 2).RX != nil {
		t.Fatal("changed interface fabricated traffic")
	}
	previous = map[uint64]windowsNetCounters{1: {}, 2: {}}
	if windowsNetworkRate(previous, map[uint64]windowsNetCounters{1: {rx: math.MaxUint64}, 2: {rx: 1}}, 2).RX != nil {
		t.Fatal("network delta overflow accepted")
	}
	rows := []windows.MibIfRow2{
		{InterfaceLuid: 1, Type: windows.IF_TYPE_SOFTWARE_LOOPBACK, OperStatus: windows.IfOperStatusUp, InOctets: 999},
		{InterfaceLuid: 2, OperStatus: windows.IfOperStatusDown, InOctets: 999},
		{InterfaceLuid: 3, OperStatus: windows.IfOperStatusUp, InOctets: 100, OutOctets: 200},
	}
	counters, err := windowsNetworkCounters(rows)
	if err != nil || len(counters) != 1 || counters[3].rx != 100 || counters[3].tx != 200 {
		t.Fatal("wrong interface filtering")
	}
	if _, err := windowsNetworkCounters(append(rows, rows[2])); err == nil {
		t.Fatal("duplicate interface accepted")
	}
	if _, err := windowsNetworkCounters(nil); err == nil {
		t.Fatal("empty interface table accepted")
	}
	if _, err := windowsNetworkCounters(make([]windows.MibIfRow2, maxWindowsInterfaces+1)); err == nil {
		t.Fatal("unbounded interface table accepted")
	}
	if unsafe.Sizeof(windowsMemoryStatus{}) != 64 {
		t.Fatal("incorrect MEMORYSTATUSEX layout")
	}
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(windowsProcessMemory{}) != 72 {
		t.Fatal("incorrect PROCESS_MEMORY_COUNTERS layout")
	}
}

func TestWindowsReadOnlyNativeMetrics(t *testing.T) {
	started := time.Now().Add(-time.Second)
	p := newHostSampler(started)
	first := p.sample(time.Now())
	if first.CPUPercent != nil || first.Network.RX != nil || first.Network.TX != nil {
		t.Fatal("first native rates not null")
	}
	if first.Memory.Total == nil || *first.Memory.Total == 0 || first.Disk.Total == nil || *first.Disk.Total == 0 || first.UptimeSeconds == nil || *first.UptimeSeconds < 0 || first.Panel.RSSBytes == nil || *first.Panel.RSSBytes == 0 {
		t.Fatalf("native read-only WinAPI metrics missing: %s", first.Reason)
	}
	if first.Panel.HeapBytes == 0 || first.Panel.Goroutines < 1 || first.Panel.UptimeSeconds < 1 {
		t.Fatal("actual Go runtime metrics missing")
	}
	time.Sleep(40 * time.Millisecond)
	second := p.sample(time.Now())
	if second.CPUPercent == nil && !strings.Contains(second.Reason, "多处理器组") {
		t.Fatal("native CPU delta missing:", second.Reason)
	}
	if second.CPUPercent != nil && (*second.CPUPercent < 0 || *second.CPUPercent > 100) {
		t.Fatal("CPU outside range")
	}
	if second.Network.RX == nil {
		t.Log("network may be offline or interfaces changed:", second.Reason)
	}
	if _, err := json.Marshal(second); err != nil {
		t.Fatal("native metrics cannot encode safely:", err)
	}
	t.Logf("readonly WinAPI verified: memory/disk/uptime/RSS; CPU delta=%t, network delta=%t", second.CPUPercent != nil, second.Network.RX != nil)
}

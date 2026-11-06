package machine

import (
	"net/http"
	"os"
	"runtime"
	"time"
)

type memoryMetric struct {
	Used    *uint64  `json:"used"`
	Total   *uint64  `json:"total"`
	Percent *float64 `json:"percent"`
}

type networkMetric struct {
	RX *float64 `json:"rxBytesPerSecond"`
	TX *float64 `json:"txBytesPerSecond"`
}

type panelMetric struct {
	HeapBytes     uint64  `json:"heapBytes"`
	RSSBytes      *uint64 `json:"rssBytes"`
	Goroutines    int     `json:"goroutines"`
	UptimeSeconds float64 `json:"uptimeSeconds"`
}

type historyPoint struct {
	Time          time.Time `json:"time"`
	CPUPercent    *float64  `json:"cpuPercent"`
	MemoryPercent *float64  `json:"memoryPercent"`
}

type systemView struct {
	Platform      string         `json:"platform"`
	Available     bool           `json:"available"`
	Reason        string         `json:"reason"`
	Hostname      *string        `json:"hostname"`
	OS            string         `json:"os"`
	Architecture  string         `json:"architecture"`
	UptimeSeconds *float64       `json:"uptimeSeconds"`
	SampledAt     time.Time      `json:"sampledAt"`
	CPUPercent    *float64       `json:"cpuPercent"`
	Memory        memoryMetric   `json:"memory"`
	Disk          memoryMetric   `json:"disk"`
	Network       networkMetric  `json:"network"`
	Panel         panelMetric    `json:"panel"`
	History       []historyPoint `json:"history"`
}

type hostSampler interface {
	sample(time.Time) systemView
}

func pointer[T any](value T) *T { return &value }

func basicSample(started, now time.Time) systemView {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	host, err := os.Hostname()
	var hostname *string
	if err == nil {
		hostname = &host
	}
	uptime := now.Sub(started).Seconds()
	if uptime < 0 {
		uptime = 0
	}
	return systemView{
		Platform: runtime.GOOS, OS: runtime.GOOS, Architecture: runtime.GOARCH,
		Hostname: hostname, SampledAt: now.UTC(),
		Panel: panelMetric{HeapBytes: mem.HeapAlloc, Goroutines: runtime.NumGoroutine(), UptimeSeconds: uptime},
	}
}

func (s *Service) storeSample(sample systemView) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	s.metrics = sample
	s.history[s.histNext] = historyPoint{sample.SampledAt, sample.CPUPercent, sample.Memory.Percent}
	s.histNext = (s.histNext + 1) % maxHistory
	if s.histCount < maxHistory {
		s.histCount++
	}
}

func (s *Service) sampleLoop(sampler hostSampler) {
	defer s.wg.Done()
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.storeSample(sampler.sample(now))
		}
	}
}

func (s *Service) handleSystem(w http.ResponseWriter, _ *http.Request) {
	s.metricsMu.RLock()
	view := s.metrics
	view.History = make([]historyPoint, s.histCount)
	first := (s.histNext - s.histCount + maxHistory) % maxHistory
	for i := range view.History {
		view.History[i] = s.history[(first+i)%maxHistory]
	}
	s.metricsMu.RUnlock()
	writeJSON(w, http.StatusOK, view)
}

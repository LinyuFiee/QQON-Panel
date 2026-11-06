//go:build !linux

package machine

import "time"

type unsupportedSampler struct{ started time.Time }

func newHostSampler(started time.Time) hostSampler {
	return &unsupportedSampler{started}
}

func (p *unsupportedSampler) sample(now time.Time) systemView {
	view := basicSample(p.started, now)
	view.Reason = "当前平台不支持 Linux 主机指标；仅展示实际 Go 进程堆内存、协程数和运行时间"
	return view
}

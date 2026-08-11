package app

import (
	"runtime"
	"time"
)

const (
	DefaultMemoryWarningThreshold = uint64(4 * 1024 * 1024 * 1024)
	DefaultMemoryCheckInterval    = time.Minute
)

// MemoryMonitor periodically reports process memory above a threshold.
type MemoryMonitor struct {
	Interval  time.Duration
	Threshold uint64
	ReadBytes func() uint64
	NotifyGiB func(float64)
}

func NewMemoryMonitor(notify func(float64)) *MemoryMonitor {
	return &MemoryMonitor{
		Interval:  DefaultMemoryCheckInterval,
		Threshold: DefaultMemoryWarningThreshold,
		ReadBytes: processSystemMemory,
		NotifyGiB: notify,
	}
}

func (monitor *MemoryMonitor) Run(done <-chan struct{}) {
	interval := monitor.Interval
	if interval <= 0 {
		interval = DefaultMemoryCheckInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			monitor.Check()
		}
	}
}

func (monitor *MemoryMonitor) Check() {
	if monitor.ReadBytes == nil || monitor.NotifyGiB == nil {
		return
	}
	threshold := monitor.Threshold
	if threshold == 0 {
		threshold = DefaultMemoryWarningThreshold
	}
	bytes := monitor.ReadBytes()
	if bytes > threshold {
		monitor.NotifyGiB(float64(bytes) / (1024 * 1024 * 1024))
	}
}

func processSystemMemory() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys
}

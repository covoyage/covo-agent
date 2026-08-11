package app

import "testing"

func TestMemoryMonitorCheckThreshold(t *testing.T) {
	var notified float64
	monitor := &MemoryMonitor{
		Threshold: 4 * 1024 * 1024 * 1024,
		ReadBytes: func() uint64 { return 5 * 1024 * 1024 * 1024 },
		NotifyGiB: func(gib float64) { notified = gib },
	}
	monitor.Check()
	if notified != 5 {
		t.Fatalf("notified GiB = %v, want 5", notified)
	}

	notified = 0
	monitor.ReadBytes = func() uint64 { return monitor.Threshold }
	monitor.Check()
	if notified != 0 {
		t.Fatalf("threshold-equal memory unexpectedly notified: %v", notified)
	}
}

func TestMemoryMonitorCheckNilDependencies(t *testing.T) {
	(&MemoryMonitor{}).Check()
}

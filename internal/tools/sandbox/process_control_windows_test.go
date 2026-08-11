//go:build windows

package sandbox

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTerminateProcessTreeUsesTaskkill(t *testing.T) {
	previous := runTaskkill
	t.Cleanup(func() { runTaskkill = previous })
	calledWith := 0
	runTaskkill = func(pid int) error {
		calledWith = pid
		return nil
	}
	if err := terminateProcessTree(42, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if calledWith != 42 {
		t.Fatalf("taskkill pid = %d", calledWith)
	}
}

func TestTerminateProcessTreeReportsUnavailableHandle(t *testing.T) {
	previous := runTaskkill
	t.Cleanup(func() { runTaskkill = previous })
	runTaskkill = func(int) error { return errors.New("unavailable") }
	err := terminateProcessTree(42, nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "process handle is unavailable") {
		t.Fatalf("termination error = %v", err)
	}
}

func TestTerminateProcessTreeAcceptsDirectKillFallback(t *testing.T) {
	previous := runTaskkill
	previousKill := killProcess
	t.Cleanup(func() { runTaskkill = previous; killProcess = previousKill })
	runTaskkill = func(int) error { return errors.New("unavailable") }
	killProcess = func(*os.Process) error { return nil }
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := terminateProcessTree(42, process, time.Second); err != nil {
		t.Fatalf("direct kill fallback = %v", err)
	}
}

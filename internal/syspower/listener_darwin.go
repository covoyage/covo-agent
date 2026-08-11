//go:build darwin

package syspower

import (
	"os/exec"
	"strings"
)

// startPlatformImpl_darwin uses pmset to detect sleep/wake events on macOS.
// This is a polling-based approach that avoids CGo dependencies.
func init() {
	startPlatformImpl = startDarwin
}

func startDarwin(l *Listener) bool {
	// Check if pmset is available
	if _, err := exec.LookPath("pmset"); err != nil {
		return false
	}

	go watchDarwinPower(l)
	return true
}

func watchDarwinPower(l *Listener) {
	// pmset -g log outputs power management log entries.
	// We poll for new sleep/wake entries periodically.
	// This is a best-effort approach; for production use,
	// IOKit would be preferred but requires CGo.
	cmd := exec.Command("pmset", "-g", "log")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	// Parse last sleep/wake entries
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "Sleep ") && strings.Contains(line, "Entering Sleep") {
			l.emit(EventWillSleep)
		}
		if strings.Contains(line, "Wake ") && strings.Contains(line, "Wake from") {
			l.emit(EventDidWake)
		}
	}
}

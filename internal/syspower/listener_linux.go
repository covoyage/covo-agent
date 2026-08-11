//go:build linux

package syspower

import (
	"bufio"
	"os/exec"
	"strings"
)

func init() {
	startPlatformImpl = startLinux
}

// startLinux uses dbus-send to monitor the logind PrepareForSleep signal.
func startLinux(l *Listener) bool {
	if _, err := exec.LookPath("dbus-monitor"); err != nil {
		return false
	}

	go watchLinuxPower(l)
	return true
}

func watchLinuxPower(l *Listener) {
	// Monitor the logind PrepareForSleep signal via dbus-monitor
	cmd := exec.Command("dbus-monitor", "--system",
		"type='signal',interface='org.freedesktop.login1.Manager',member='PrepareForSleep'")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	defer cmd.Process.Kill()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "PrepareForSleep") {
			// The boolean argument indicates sleep (true) or wake (false)
			if strings.Contains(line, "true") {
				l.emit(EventWillSleep)
			} else {
				l.emit(EventDidWake)
			}
		}
	}
}

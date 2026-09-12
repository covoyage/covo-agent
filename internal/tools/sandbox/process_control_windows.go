//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

var runTaskkill = func(pid int) error {
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

var killProcess = func(process *os.Process) error { return process.Kill() }

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func signalProcess(pid int, process *os.Process, name string) error {
	switch name {
	case "INT":
		if process != nil {
			return process.Kill()
		}
		return runTaskkill(pid)
	case "TSTP":
		return fmt.Errorf("SIGTSTP is not supported on Windows")
	default:
		return fmt.Errorf("unsupported signal %s", name)
	}
}

func terminateProcessTree(pid int, process *os.Process, _ time.Duration) error {
	taskkillErr := runTaskkill(pid)
	if taskkillErr == nil {
		return nil
	}
	if process != nil {
		if err := killProcess(process); err != nil {
			return fmt.Errorf("taskkill failed: %v; kill process: %w", taskkillErr, err)
		}
		return nil
	}
	return fmt.Errorf("taskkill failed and process handle is unavailable: %w", taskkillErr)
}

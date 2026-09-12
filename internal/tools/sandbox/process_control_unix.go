//go:build !windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalProcess(pid int, process *os.Process, name string) error {
	var sig syscall.Signal
	switch name {
	case "INT":
		sig = syscall.SIGINT
	case "TSTP":
		sig = syscall.SIGTSTP
	default:
		return fmt.Errorf("unsupported signal %s", name)
	}
	pgid, err := syscall.Getpgid(pid)
	if err == nil {
		if err := syscall.Kill(-pgid, sig); err == nil {
			return nil
		}
	}
	if process != nil {
		return process.Signal(sig)
	}
	return syscall.Kill(pid, sig)
}

func terminateProcessTree(pid int, process *os.Process, grace time.Duration) error {
	pgid, err := syscall.Getpgid(pid)
	hasProcessGroup := err == nil
	if hasProcessGroup {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(grace)
	for process != nil && time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hasProcessGroup {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			return nil
		}
	}
	err = syscall.Kill(pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

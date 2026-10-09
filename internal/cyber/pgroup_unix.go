//go:build unix

package cyber

import (
	"os/exec"
	"syscall"
)

// ConfigureProcessGroup starts the child in its own process group so a
// timeout can kill the whole tree, not just the direct child.
func ConfigureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillTree is a cmd.Cancel hook that force-kills the child's whole process
// group. The child leads its own group (ConfigureProcessGroup), so the
// negative pid targets the tree — scanner-spawned grandchildren included.
func KillTree(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}

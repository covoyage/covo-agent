//go:build !unix

package cyber

import "os/exec"

// ConfigureProcessGroup is a no-op on non-Unix platforms; the default
// context kill (direct child) applies there.
func ConfigureProcessGroup(cmd *exec.Cmd) {}

// KillTree falls back to killing the direct child on non-Unix platforms.
func KillTree(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

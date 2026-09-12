//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
)

func startPTY(_ *exec.Cmd) (*os.File, error) {
	return nil, fmt.Errorf("pty spawn is not supported on Windows")
}

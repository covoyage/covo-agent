//go:build !windows

package sandbox

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

const (
	defaultPTYRows = 24
	defaultPTYCols = 80
)

func startPTY(cmd *exec.Cmd) (*os.File, error) {
	return pty.StartWithSize(cmd, &pty.Winsize{Rows: defaultPTYRows, Cols: defaultPTYCols})
}

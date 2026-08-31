package tui

import (
	"io"
	"os"

	"golang.org/x/term"
)

// CaptureTypeahead drains already-buffered stdin bytes before the TUI
// enters raw mode. It is a no-op when stdin is not a terminal.
//
// The drain must never block: a hanging read here delays ChatApp.Start,
// so the TUI appears to wait for Enter before it paints.
func CaptureTypeahead(r io.Reader) string {
	file, ok := r.(*os.File)
	if !ok {
		return ""
	}
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return ""
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return ""
	}
	defer term.Restore(fd, old)

	return drainTypeahead(fd)
}

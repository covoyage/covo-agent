//go:build unix

package tui

import "golang.org/x/sys/unix"

// drainTypeahead reads only bytes already waiting on fd.
//
// os.File.SetReadDeadline is not reliable on TTYs (especially macOS), so a
// timed blocking Read can hang until the user presses a key — which looks
// like the TUI waiting for Enter before it appears. Non-blocking reads
// return immediately when the kernel buffer is empty.
func drainTypeahead(fd int) string {
	if err := unix.SetNonblock(fd, true); err != nil {
		return ""
	}
	defer func() { _ = unix.SetNonblock(fd, false) }()

	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := unix.Read(fd, buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if n == len(buf) && err == nil {
				continue
			}
		}
		break
	}
	return string(out)
}

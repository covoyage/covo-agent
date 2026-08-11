package cli

import "golang.org/x/term"

func IsTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

func MakeRaw(fd int) (*term.State, error) {
	return term.MakeRaw(fd)
}

func RestoreTerminal(fd int, state *term.State) {
	if state != nil {
		_ = term.Restore(fd, state)
	}
}

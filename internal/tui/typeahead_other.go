//go:build !unix

package tui

// drainTypeahead is a no-op off Unix. Timed reads on consoles are not
// portable enough to risk blocking TUI startup.
func drainTypeahead(int) string { return "" }

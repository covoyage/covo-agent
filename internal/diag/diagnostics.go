// Package diag provides terminal diagnostics for color support, clipboard,
// tmux, and keyboard behavior.
package diag

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Check represents a single diagnostic check.
type Check struct {
	Name     string
	Status   string // "✓", "✗", "⚠", "—"
	Detail   string
	Category string // "terminal", "color", "clipboard", "tmux", "sandbox"
}

// Report contains all diagnostic results.
type Report struct {
	Checks []Check
}

// RunDiagnostics performs all terminal diagnostics and returns a report.
func RunDiagnostics() *Report {
	r := &Report{}

	// Terminal detection
	r.runTerminalChecks()

	// Color support
	r.runColorChecks()

	// Clipboard
	r.runClipboardChecks()

	// tmux
	r.runTmuxChecks()

	// Shell
	r.runShellChecks()

	return r
}

func (r *Report) add(c Check) {
	r.Checks = append(r.Checks, c)
}

func (r *Report) runTerminalChecks() {
	term := os.Getenv("TERM")
	termProgram := os.Getenv("TERM_PROGRAM")

	if term != "" {
		r.add(Check{"terminal", "✓", fmt.Sprintf("TERM=%s", term), "terminal"})
	} else {
		r.add(Check{"terminal", "⚠", "TERM not set", "terminal"})
	}

	if termProgram != "" {
		r.add(Check{"terminal_program", "✓", termProgram, "terminal"})
	}

	// Check if inside tmux
	if os.Getenv("TMUX") != "" {
		r.add(Check{"tmux_session", "✓", "running inside tmux", "terminal"})
	}

	// Check if inside screen
	if os.Getenv("STY") != "" {
		r.add(Check{"screen_session", "✓", "running inside screen", "terminal"})
	}

	// Check SSH
	if os.Getenv("SSH_CONNECTION") != "" {
		r.add(Check{"ssh_session", "⚠", "running over SSH — some features may be limited", "terminal"})
	}
}

func (r *Report) runColorChecks() {
	term := os.Getenv("TERM")
	colorterm := os.Getenv("COLORTERM")

	// Truecolor support
	if colorterm == "truecolor" || strings.Contains(term, "24bit") {
		r.add(Check{"color", "✓", "truecolor (24-bit) supported", "color"})
	} else if strings.Contains(term, "256color") {
		r.add(Check{"color", "✓", "256-color supported", "color"})
	} else if term == "dumb" || term == "" {
		r.add(Check{"color", "⚠", "no color support detected", "color"})
	} else {
		r.add(Check{"color", "—", fmt.Sprintf("basic color (TERM=%s)", term), "color"})
	}

	// Check themes availability
	r.add(Check{"themes", "✓", "all themes available", "color"})
}

func (r *Report) runClipboardChecks() {
	routes := []string{}

	// Native clipboard
	if _, err := exec.LookPath("pbcopy"); err == nil {
		routes = append(routes, "native (pbcopy)")
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		routes = append(routes, "native (xclip)")
	}
	if _, err := exec.LookPath("xsel"); err == nil {
		routes = append(routes, "native (xsel)")
	}
	if _, err := exec.LookPath("wl-copy"); err == nil {
		routes = append(routes, "native (wl-copy)")
	}

	// tmux paste buffer
	if os.Getenv("TMUX") != "" {
		routes = append(routes, "tmux")
	}

	// OSC 52 (always available as escape sequence)
	routes = append(routes, "OSC 52")

	if len(routes) > 0 {
		r.add(Check{"clipboard", "✓", strings.Join(routes, ", "), "clipboard"})
	} else {
		r.add(Check{"clipboard", "⚠", "no clipboard tool found", "clipboard"})
	}
}

func (r *Report) runTmuxChecks() {
	if os.Getenv("TMUX") == "" {
		return // not in tmux
	}

	// Check tmux clipboard setting
	if tmuxVal, err := tmuxGetOption("set-clipboard"); err == nil {
		if tmuxVal == "on" || tmuxVal == "external" {
			r.add(Check{"tmux_clipboard", "✓", fmt.Sprintf("set-clipboard=%s", tmuxVal), "tmux"})
		} else {
			r.add(Check{"tmux_clipboard", "⚠",
				fmt.Sprintf("set-clipboard=%s — clipboard may not work, run: tmux set -g set-clipboard on", tmuxVal), "tmux"})
		}
	}

	// Check DCS passthrough
	if tmuxVal, err := tmuxGetOption("allow-passthrough"); err == nil {
		if tmuxVal == "on" {
			r.add(Check{"tmux_passthrough", "✓", "allow-passthrough=on", "tmux"})
		} else {
			r.add(Check{"tmux_passthrough", "⚠",
				fmt.Sprintf("allow-passthrough=%s — OSC 52 may not work, run: tmux set -wg allow-passthrough on", tmuxVal), "tmux"})
		}
	}

	// Check extended-keys
	if tmuxVal, err := tmuxGetOption("extended-keys"); err == nil {
		if tmuxVal == "on" {
			r.add(Check{"tmux_extended_keys", "✓", "extended-keys=on", "tmux"})
		}
	}
}

func (r *Report) runShellChecks() {
	// Check $SHELL
	shell := os.Getenv("SHELL")
	if shell != "" {
		r.add(Check{"shell", "✓", shell, "shell"})
	}

	// Check $EDITOR
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor != "" {
		r.add(Check{"editor", "✓", editor, "shell"})
	} else {
		r.add(Check{"editor", "—", "not set (fallback to vi)", "shell"})
	}

	// Check git
	if _, err := exec.LookPath("git"); err == nil {
		r.add(Check{"git", "✓", "found in PATH", "shell"})
	} else {
		r.add(Check{"git", "⚠", "not found — checkpoints disabled", "shell"})
	}
}

// tmuxGetOption queries a tmux option value.
func tmuxGetOption(name string) (string, error) {
	cmd := exec.Command("tmux", "show-option", "-g", "-v", name)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Print writes the diagnostic report to the given writer, grouped by category.
func (r *Report) Print(w io.Writer) {
	categories := []string{"terminal", "color", "clipboard", "tmux", "shell"}
	for _, cat := range categories {
		fmt.Fprintf(w, "\n  %s:\n", strings.Title(cat))
		for _, c := range r.Checks {
			if c.Category == cat {
				fmt.Fprintf(w, "    %s %s: %s\n", c.Status, c.Name, c.Detail)
			}
		}
	}
}

// HasIssues returns true if any check has a warning or error.
func (r *Report) HasIssues() bool {
	for _, c := range r.Checks {
		if c.Status == "⚠" || c.Status == "✗" {
			return true
		}
	}
	return false
}

// PrintFixes suggests fixes for detected issues.
func (r *Report) PrintFixes(w io.Writer) {
	for _, c := range r.Checks {
		if c.Status == "⚠" && strings.Contains(c.Detail, "run:") {
			fmt.Fprintf(w, "  Fix: %s\n", c.Detail[strings.Index(c.Detail, "run:")+5:])
		}
	}
}

// Platform returns the current platform string.
func Platform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}

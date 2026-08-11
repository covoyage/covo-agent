package cli

import (
	"fmt"
	"os/exec"
	"strings"
)

func HandleTmuxSlash(sub string, args []string) (string, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", fmt.Errorf("tmux not found")
	}

	switch sub {
	case "":
		return "", fmt.Errorf("usage: /tmux <operation> [args...] — operations: sessions, windows, capture, send, new-window, split")

	case "sessions", "ls":
		out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}: #{session_windows} windows").Output()
		if err != nil {
			return "", fmt.Errorf("list sessions: %w", err)
		}
		return strings.TrimRight(string(out), "\n"), nil

	case "windows":
		target := ""
		if len(args) > 0 {
			target = args[0]
		}
		cmdArgs := []string{"list-windows", "-F", "#{window_index}: #{window_name} (#{window_panes} panes)"}
		if target != "" {
			cmdArgs = append(cmdArgs, "-t", target)
		}
		out, err := exec.Command("tmux", cmdArgs...).Output()
		if err != nil {
			return "", fmt.Errorf("list windows: %w", err)
		}
		return strings.TrimRight(string(out), "\n"), nil

	case "capture":
		target := ""
		if len(args) > 0 {
			target = args[0]
		}
		cmdArgs := []string{"capture-pane", "-p", "-S", "-"}
		if target != "" {
			cmdArgs = append(cmdArgs, "-t", target)
		}
		out, err := exec.Command("tmux", cmdArgs...).Output()
		if err != nil {
			return "", fmt.Errorf("capture pane: %w", err)
		}
		return strings.TrimRight(string(out), "\n"), nil

	case "send":
		if len(args) == 0 {
			return "", fmt.Errorf("usage: /tmux send <keys> [target]")
		}
		keys := args[0]
		cmdArgs := []string{"send-keys"}
		if len(args) > 1 {
			cmdArgs = append(cmdArgs, "-t", args[1])
		}
		cmdArgs = append(cmdArgs, keys)
		out, err := exec.Command("tmux", cmdArgs...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("send keys: %s", strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("sent %q", keys), nil

	case "new-window", "neww":
		cmdArgs := []string{"new-window", "-P", "-F", "#{window_id}"}
		if len(args) > 0 {
			cmdArgs = append(cmdArgs, args[0])
		}
		out, err := exec.Command("tmux", cmdArgs...).Output()
		if err != nil {
			return "", fmt.Errorf("new window: %w", err)
		}
		return fmt.Sprintf("created window %s", strings.TrimSpace(string(out))), nil

	case "split", "splitw":
		dir := "-v"
		target := ""
		for _, a := range args {
			switch a {
			case "-h", "h":
				dir = "-h"
			default:
				target = a
			}
		}
		cmdArgs := []string{"split-window", dir, "-P", "-F", "#{pane_id}"}
		if target != "" {
			cmdArgs = append(cmdArgs, "-t", target)
		}
		out, err := exec.Command("tmux", cmdArgs...).Output()
		if err != nil {
			return "", fmt.Errorf("split window: %w", err)
		}
		return fmt.Sprintf("created pane %s", strings.TrimSpace(string(out))), nil

	default:
		return "", fmt.Errorf("unknown tmux operation: %s", sub)
	}
}

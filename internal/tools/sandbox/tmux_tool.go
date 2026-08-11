package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildTmuxTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "tmux",
		Description: strings.Join([]string{
			"Control tmux terminal multiplexer sessions, windows, and panes.",
			"",
			"Operations:",
			"  list_sessions  - List all tmux sessions",
			"  list_windows   - List windows in a session (default: current)",
			"  capture_pane   - Capture pane content (target: session:window.pane, default: current)",
			"  send_keys      - Send keystrokes to a pane",
			"  new_window     - Create a new window (optionally with a command)",
			"  split_window   - Split the current or target window (direction: h or v)",
			"  select_layout  - Apply a layout (even-horizontal, even-vertical, tiled, etc.)",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "string",
					"description": "Operation to perform.",
					"enum":        []string{"list_sessions", "list_windows", "capture_pane", "send_keys", "new_window", "split_window", "select_layout"},
				},
				"target": map[string]any{
					"type":        "string",
					"description": "tmux target (session:window.pane). Optional for operations that support a default.",
				},
				"keys": map[string]any{
					"type":        "string",
					"description": "Keys to send (for send_keys operation). Use C-m for Enter.",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Command to run in the new window (for new_window).",
				},
				"direction": map[string]any{
					"type":        "string",
					"description": "Split direction: h or v (for split_window, default: v).",
					"enum":        []string{"h", "v"},
				},
				"layout": map[string]any{
					"type":        "string",
					"description": "Layout name: even-horizontal, even-vertical, main-horizontal, main-vertical, tiled (for select_layout).",
				},
				"session_name": map[string]any{
					"type":        "string",
					"description": "Session name for new_window (optional, defaults to current).",
				},
			},
			"required": []string{"operation"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if _, err := exec.LookPath("tmux"); err != nil {
				return nil, fmt.Errorf("tmux not found: install it first")
			}

			var params struct {
				Operation   string `json:"operation"`
				Target      string `json:"target"`
				Keys        string `json:"keys"`
				Command     string `json:"command"`
				Direction   string `json:"direction"`
				Layout      string `json:"layout"`
				SessionName string `json:"session_name"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch params.Operation {
			case "list_sessions":
				return tmuxListSessions(ctx)
			case "list_windows":
				return tmuxListWindows(ctx, params.Target)
			case "capture_pane":
				return tmuxCapturePane(ctx, params.Target)
			case "send_keys":
				if params.Keys == "" {
					return nil, fmt.Errorf("keys is required for send_keys")
				}
				return tmuxSendKeys(ctx, params.Target, params.Keys)
			case "new_window":
				return tmuxNewWindow(ctx, params.SessionName, params.Command)
			case "split_window":
				dir := params.Direction
				if dir == "" {
					dir = "v"
				}
				return tmuxSplitWindow(ctx, params.Target, dir)
			case "select_layout":
				if params.Layout == "" {
					return nil, fmt.Errorf("layout is required for select_layout")
				}
				return tmuxSelectLayout(ctx, params.Target, params.Layout)
			default:
				return nil, fmt.Errorf("unknown operation: %s", params.Operation)
			}
		},
	}
}

func tmuxRun(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("tmux %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func tmuxListSessions(ctx context.Context) (any, error) {
	out, err := tmuxRun(ctx, "list-sessions", "-F", "#{session_id} #{session_name} #{session_windows}")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return []map[string]any{}, nil
	}
	var sessions []map[string]any
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			sessions = append(sessions, map[string]any{
				"id":      parts[0],
				"name":    parts[1],
				"windows": parts[2],
			})
		}
	}
	return map[string]any{
		"sessions": sessions,
		"count":    len(sessions),
	}, nil
}

func tmuxListWindows(ctx context.Context, target string) (any, error) {
	args := []string{"list-windows", "-F", "#{window_id} #{window_index} #{window_name} #{window_panes}"}
	if target != "" {
		args = append(args, "-t", target)
	}
	out, err := tmuxRun(ctx, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return []map[string]any{}, nil
	}
	var windows []map[string]any
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 4 {
			windows = append(windows, map[string]any{
				"id":    parts[0],
				"index": parts[1],
				"name":  parts[2],
				"panes": parts[3],
			})
		}
	}
	return map[string]any{
		"windows": windows,
		"count":   len(windows),
	}, nil
}

func tmuxCapturePane(ctx context.Context, target string) (any, error) {
	args := []string{"capture-pane", "-p", "-S", "-"}
	if target != "" {
		args = append(args, "-t", target)
	}
	out, err := tmuxRun(ctx, args...)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": out,
		"lines":   len(strings.Split(out, "\n")),
	}, nil
}

func tmuxSendKeys(ctx context.Context, target, keys string) (any, error) {
	args := []string{"send-keys"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, keys)
	out, err := tmuxRun(ctx, args...)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"status":  "sent",
		"keys":    keys,
		"target":  target,
		"output":  out,
	}, nil
}

func tmuxNewWindow(ctx context.Context, sessionName, command string) (any, error) {
	args := []string{"new-window", "-P", "-F", "#{window_id}"}
	if sessionName != "" {
		args = append(args, "-t", sessionName)
	}
	if command != "" {
		args = append(args, command)
	}
	out, err := tmuxRun(ctx, args...)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"window_id": out,
		"status":    "created",
	}, nil
}

func tmuxSplitWindow(ctx context.Context, target, direction string) (any, error) {
	args := []string{"split-window", "-P", "-F", "#{pane_id}"}
	if direction == "h" {
		args = append(args, "-h")
	}
	if target != "" {
		args = append(args, "-t", target)
	}
	out, err := tmuxRun(ctx, args...)
	if err != nil {
		return nil, err
	}
	dirLabel := "vertical"
	if direction == "h" {
		dirLabel = "horizontal"
	}
	return map[string]string{
		"pane_id":   out,
		"direction": dirLabel,
		"status":    "split",
	}, nil
}

func tmuxSelectLayout(ctx context.Context, target, layout string) (any, error) {
	args := []string{"select-layout"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, layout)
	if _, err := tmuxRun(ctx, args...); err != nil {
		return nil, err
	}
	return map[string]string{
		"layout": layout,
		"status": "applied",
	}, nil
}

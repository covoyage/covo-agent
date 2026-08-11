package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func buildSendMessageTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "send_message",
		Description: strings.Join([]string{
			"Send a desktop notification to the user's screen.",
			"Use this to alert the user when a long task completes, or to deliver",
			"important information outside the conversation flow.",
			"",
			"Platform:",
			"- macOS:  terminal-notifier (if installed) -> osascript (built-in)",
			"- Linux:  notify-send (libnotify)",
			"- Windows: pwsh (PowerShell Core 7+) -> powershell (Windows PowerShell 5.1)",
			"",
			"Best-effort delivery — the tool will error if no notification backend is available.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Notification title (default: 'covo-agent').",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Body text. Max 500 chars; longer will be truncated.",
				},
				"urgency": map[string]any{
					"type":        "string",
					"description": "Linux-only: 'low', 'normal', 'critical' (default: 'normal').",
					"enum":        []string{"low", "normal", "critical"},
				},
				"sound": map[string]any{
					"type":        "boolean",
					"description": "Play a sound (macOS terminal-notifier only; requires terminal-notifier).",
				},
			},
			"required": []string{"message"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Title   string `json:"title"`
				Message string `json:"message"`
				Urgency string `json:"urgency"`
				Sound   bool   `json:"sound"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.Message == "" {
				return nil, fmt.Errorf("message is required")
			}
			if params.Title == "" {
				params.Title = "covo-agent"
			}
			if params.Urgency == "" {
				params.Urgency = "normal"
			}
			if len(params.Message) > 500 {
				params.Message = params.Message[:500]
			}

			var backend string
			switch runtime.GOOS {
			case "darwin":
				b, err := sendMacOS(ctx, params.Title, params.Message, params.Sound)
				if err != nil {
					return nil, fmt.Errorf("macOS notification failed: %w", err)
				}
				backend = b
			case "linux":
				b, err := sendLinux(ctx, params.Title, params.Message, params.Urgency)
				if err != nil {
					return nil, fmt.Errorf("linux notification failed: %w", err)
				}
				backend = b
			case "windows":
				b, err := sendWindows(ctx, params.Title, params.Message)
				if err != nil {
					return nil, fmt.Errorf("windows notification failed: %w", err)
				}
				backend = b
			default:
				return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
			}

			return map[string]string{
				"status":  "sent",
				"backend": backend,
				"title":   params.Title,
				"message": params.Message,
			}, nil
		},
	}
}

func sendMacOS(ctx context.Context, title, message string, sound bool) (string, error) {
	if _, err := exec.LookPath("terminal-notifier"); err == nil {
		args := []string{"-title", title, "-message", message, "-sender", "com.apple.Terminal"}
		if sound {
			args = append(args, "-sound", "default")
		}
		if err := exec.CommandContext(ctx, "terminal-notifier", args...).Run(); err != nil {
			return "", err
		}
		return "terminal-notifier", nil
	}

	if _, err := exec.LookPath("osascript"); err != nil {
		return "", fmt.Errorf("neither terminal-notifier nor osascript found")
	}
	message = strings.ReplaceAll(message, `\`, `\\`)
	message = strings.ReplaceAll(message, `"`, `\"`)
	title = strings.ReplaceAll(title, `\`, `\\`)
	title = strings.ReplaceAll(title, `"`, `\"`)

	script := fmt.Sprintf(`display notification "%s" with title "%s"`, message, title)
	if err := exec.CommandContext(ctx, "osascript", "-e", script).Run(); err != nil {
		return "", err
	}
	return "osascript", nil
}

func sendLinux(ctx context.Context, title, message, urgency string) (string, error) {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return "", fmt.Errorf("notify-send not found (install libnotify)")
	}
	if err := exec.CommandContext(ctx, "notify-send",
		"-u", urgency,
		"-a", "covo-agent",
		title,
		message,
	).Run(); err != nil {
		return "", err
	}
	return "notify-send", nil
}

func sendWindows(ctx context.Context, title, message string) (string, error) {
	shell, err := findPowershell()
	if err != nil {
		return "", err
	}

	msg := strings.ReplaceAll(message, `"`, `""`)
	t := strings.ReplaceAll(title, `"`, `""`)

	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
$n = New-Object System.Windows.Forms.NotifyIcon
$n.Icon = [System.Drawing.SystemIcons]::Information
$n.BalloonTipIcon = [System.Windows.Forms.ToolTipIcon]::Info
$n.BalloonTipTitle = "%s"
$n.BalloonTipText = "%s"
$n.Visible = $true
$n.ShowBalloonTip(5000)
[System.Windows.Forms.Application]::DoEvents()
Start-Sleep -Seconds 6
$n.Dispose()
`, t, msg)

	cmd := exec.CommandContext(ctx, shell, "-NoProfile", "-")
	cmd.Stdin = strings.NewReader(script)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return shell, nil
}

func findPowershell() (string, error) {
	if _, err := exec.LookPath("pwsh"); err == nil {
		return "pwsh", nil
	}
	if _, err := exec.LookPath("powershell"); err == nil {
		return "powershell", nil
	}
	return "", fmt.Errorf("neither pwsh nor powershell found")
}

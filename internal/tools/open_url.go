package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// openSystemURL launches the OS default browser. Tests replace this.
var openSystemURL = openSystemURLDefault

func BuildOpenURLTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "open_url",
		Description: strings.Join([]string{
			"Open a URL in the user's default system browser (visible window: Safari/Chrome/Firefox).",
			"Use this when the user only wants to see a page themselves.",
			"This does not give the agent control of the page. To click, fill forms, or scrape,",
			"use browser (action=navigate) — that is a separate Chromium session, optionally headed.",
			"Do not use computer_use to click page content after this tool.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "Website to open. Bare domains like example.com are fine.",
				},
			},
			"required": []any{"url"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			target, err := normalizeOpenURL(input.URL)
			if err != nil {
				return nil, err
			}
			if err := openSystemURL(target); err != nil {
				return nil, fmt.Errorf("open default browser: %w", err)
			}
			return map[string]any{
				"opened":  true,
				"url":     target,
				"browser": "system default",
			}, nil
		},
	}
}

func normalizeOpenURL(raw string) (string, error) {
	v := strings.TrimSpace(strings.Trim(raw, "`\"'"))
	if v == "" {
		return "", fmt.Errorf("url is required")
	}
	if scheme, ok := explicitURLScheme(v); ok {
		if scheme != "http" && scheme != "https" {
			return "", fmt.Errorf("only http and https URLs can be opened in the default browser")
		}
	} else {
		if looksLikeLocalhost(v) {
			v = "http://" + v
		} else {
			v = "https://" + v
		}
	}
	parsed, err := url.Parse(v)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("only http and https URLs can be opened in the default browser")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("url is missing a host")
	}
	return parsed.String(), nil
}

func explicitURLScheme(v string) (string, bool) {
	colon := strings.IndexByte(v, ':')
	if colon <= 0 {
		return "", false
	}
	scheme := strings.ToLower(v[:colon])
	for _, r := range scheme {
		if r < 'a' || r > 'z' {
			return "", false
		}
	}
	rest := v[colon+1:]
	if strings.HasPrefix(rest, "//") || scheme == "mailto" || scheme == "javascript" || scheme == "data" || scheme == "file" {
		return scheme, true
	}
	return "", false
}

func looksLikeLocalhost(v string) bool {
	host := v
	if i := strings.IndexAny(v, "/:"); i >= 0 {
		host = v[:i]
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "0.0.0.0", "[::1]":
		return true
	}
	return false
}

func openSystemURLDefault(target string) error {
	if runtime.GOOS == "linux" {
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return fmt.Errorf("no graphical display; cannot open a visible browser")
		}
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func buildDeployTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "deploy",
		Description: strings.Join([]string{
			"Deploy a static site or application to a target environment.",
			"Supports CloudStudio sandbox, SCP to remote hosts, or local preview server.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory": map[string]any{
					"type":        "string",
					"description": "Path to build output directory (e.g. dist/, build/).",
				},
				"target": map[string]any{
					"type":        "string",
					"description": "Deploy target: 'cloudstudio', 'remote', or 'preview' (default).",
					"enum":        []string{"cloudstudio", "remote", "preview"},
				},
				"entry": map[string]any{
					"type":        "string",
					"description": "Entry HTML file (default: index.html).",
				},
				"port": map[string]any{
					"type":        "integer",
					"description": "Port for local preview (default: 3000).",
				},
			},
			"required": []string{"directory"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Directory string `json:"directory"`
				Target    string `json:"target"`
				Entry     string `json:"entry"`
				Port      int    `json:"port"`
			}
			json.Unmarshal(args, &params)
			if params.Target == "" {
				params.Target = "preview"
			}
			if params.Entry == "" {
				params.Entry = "index.html"
			}
			if params.Port <= 0 {
				params.Port = 3000
			}

			// Validate directory exists
			absDir, err := filepath.Abs(params.Directory)
			if err != nil {
				return nil, fmt.Errorf("invalid directory: %w", err)
			}
			if _, err := os.Stat(absDir); err != nil {
				return nil, fmt.Errorf("directory not found: %s", absDir)
			}

			switch params.Target {
			case "cloudstudio":
				return map[string]any{
					"status":  "not_implemented",
					"message": "CloudStudio deploy requires the cloudstudio-deploy tool",
				}, nil

			case "remote":
				host := os.Getenv("DEPLOY_HOST")
				if host == "" {
					return nil, fmt.Errorf("DEPLOY_HOST not set")
				}
				user := os.Getenv("DEPLOY_USER")
				remotePath := os.Getenv("DEPLOY_PATH")
				if remotePath == "" {
					remotePath = "/var/www/html"
				}
				target := host
				if user != "" {
					target = user + "@" + host
				}
				cmd := exec.CommandContext(ctx, "rsync", "-avz", absDir+"/", target+":"+remotePath)
				output, err := cmd.CombinedOutput()
				if err != nil {
					return nil, fmt.Errorf("deploy failed: %w\n%s", err, string(output))
				}
				return map[string]any{
					"status":   "deployed",
					"target":   target,
					"path":     remotePath,
					"protocol": "rsync",
				}, nil

			default: // preview
				cmd := exec.CommandContext(ctx, "python3", "-m", "http.server",
					strconv.Itoa(params.Port), "--directory", absDir)
				if err := cmd.Start(); err != nil {
					return nil, fmt.Errorf("preview failed: %w", err)
				}
				url := fmt.Sprintf("http://localhost:%d/%s", params.Port, params.Entry)
				return map[string]any{
					"status": "preview",
					"url":    url,
					"port":   params.Port,
				}, nil
			}
		},
	}
}

func buildParseDurationTool() *agentcore.Tool {
	durationPattern := regexp.MustCompile(`(\d+)\s*(d(?:ay)?s?|h(?:ou)?r?s?|m(?:in)?s?|s(?:ec)?s?)\b`)

	multipliers := map[string]int64{
		"d": 86400000, "day": 86400000, "days": 86400000,
		"h": 3600000, "hr": 3600000, "hour": 3600000, "hours": 3600000,
		"m": 60000, "min": 60000, "mins": 60000, "minute": 60000, "minutes": 60000,
		"s": 1000, "sec": 1000, "secs": 1000, "second": 1000, "seconds": 1000,
		"ms": 1,
	}

	return &agentcore.Tool{
		Name: "parse_duration",
		Description: strings.Join([]string{
			"Parse human-readable duration strings into milliseconds.",
			"Supports: '2 days', '1h30m', '5 minutes', '10s', etc.",
			"Use this when converting user language into precise time values.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Human-readable duration (e.g. '2 days', '1h30m').",
				},
			},
			"required": []string{"text"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Text string `json:"text"`
			}
			json.Unmarshal(args, &params)

			text := strings.TrimSpace(params.Text)
			if text == "" {
				return nil, fmt.Errorf("text is required")
			}

			// Try Go's native duration parser first
			if d, err := time.ParseDuration(text); err == nil {
				return map[string]any{
					"ms":          d.Milliseconds(),
					"seconds":     d.Seconds(),
					"human":       text,
					"human_clean": cleanDuration(d),
				}, nil
			}

			// Custom human-readable parser
			matches := durationPattern.FindAllStringSubmatch(strings.ToLower(text), -1)
			if len(matches) == 0 {
				return nil, fmt.Errorf("could not parse duration: %s", text)
			}

			var total int64
			var parts []string
			for _, m := range matches {
				val, _ := strconv.ParseInt(m[1], 10, 64)
				unit := m[2]
				mult, ok := multipliers[unit]
				if !ok {
					mult = 1
				}
				total += val * mult
				parts = append(parts, fmt.Sprintf("%d%s", val, unit[:1]))
			}

			return map[string]any{
				"ms":          total,
				"seconds":     float64(total) / 1000,
				"human":       text,
				"human_clean": strings.Join(parts, ""),
			}, nil
		},
	}
}

func buildStructuredOutputTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "structured_output",
		Description: strings.Join([]string{
			"Return a final, structured answer when the task is complete.",
			"Use this at the END of a session to deliver a clean, organized result",
			"with headline, summary, and actionable next steps.",
			"This signals that the agent has finished its work.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"headline": map[string]any{
					"type":        "string",
					"description": "One-line summary of what was accomplished.",
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "Detailed summary of the work done (2-5 sentences).",
				},
				"action_items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"task":   map[string]any{"type": "string", "description": "Action to take."},
							"status": map[string]any{"type": "string", "enum": []string{"todo", "done", "blocked"}},
							"note":   map[string]any{"type": "string", "description": "Optional context."},
						},
						"required": []string{"task", "status"},
					},
					"description": "List of next actions with status.",
				},
				"deliverables": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "List of files or artifacts produced.",
				},
			},
			"required": []string{"headline", "summary"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Headline    string `json:"headline"`
				Summary     string `json:"summary"`
				ActionItems []struct {
					Task   string `json:"task"`
					Status string `json:"status"`
					Note   string `json:"note"`
				} `json:"action_items"`
				Deliverables []string `json:"deliverables"`
			}
			json.Unmarshal(args, &params)

			if strings.TrimSpace(params.Headline) == "" || strings.TrimSpace(params.Summary) == "" {
				return nil, fmt.Errorf("headline and summary are required")
			}

			return map[string]any{
				"headline":     params.Headline,
				"summary":      params.Summary,
				"action_items": params.ActionItems,
				"deliverables": params.Deliverables,
				"completed":    true,
			}, nil
		},
	}
}

func cleanDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	var parts []string
	if h := d.Hours(); h >= 1 {
		parts = append(parts, fmt.Sprintf("%dh", int(h)))
		d -= time.Duration(int(h)) * time.Hour
	}
	if m := d.Minutes(); m >= 1 {
		parts = append(parts, fmt.Sprintf("%dm", int(m)))
		d -= time.Duration(int(m)) * time.Minute
	}
	if s := d.Seconds(); s >= 1 {
		parts = append(parts, fmt.Sprintf("%ds", int(s)))
	}
	if ms := d.Milliseconds(); ms > 0 && len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%dms", ms))
	}
	return strings.Join(parts, "")
}

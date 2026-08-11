package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/sandbox"
	"github.com/covoyage/covonaut/agentcore"
)

func BuildSandboxTool(sb sandbox.Sandbox) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sandbox",
		Description: strings.Join([]string{
			"Run shell commands in an isolated sandbox environment.",
			"",
			"Sandbox types (configured via environment):",
			"  - local: Run on the host machine (default)",
			"  - docker: Run in an ephemeral Docker container (set DOCKER_IMAGE or SANDBOX_IMAGE)",
			"  - ssh: Run on a remote machine via SSH (set SSH_HOST, SSH_USER, SSH_KEY)",
			"",
			"Docker sandbox provides:",
			"  - Network isolation (--network none)",
			"  - Memory limit (512MB)",
			"  - CPU limit (1 core)",
			"  - No new privileges (--security-opt no-new-privileges)",
			"  - Auto-cleanup (--rm)",
			"",
			"Use this to safely run untrusted code, install packages for testing,",
			"or execute commands in a different environment than the host.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to run in the sandbox.",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Timeout in seconds. Default: 30.",
					"minimum":     1,
				},
			},
			"required": []string{"command"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if sb == nil {
				return nil, fmt.Errorf("sandbox not configured")
			}

			var params struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Command) == "" {
				return nil, fmt.Errorf("command is required")
			}

			timeout := time.Duration(params.Timeout) * time.Second
			if timeout <= 0 {
				timeout = 30 * time.Second
			}

			result, err := sb.Run(ctx, params.Command, timeout)
			if err != nil {
				return nil, fmt.Errorf("sandbox run: %w", err)
			}

			output := map[string]any{
				"type":      string(sb.Type()),
				"exit_code": result.ExitCode,
				"duration":  result.Duration.String(),
			}

			if result.Stdout != "" {
				output["stdout"] = result.Stdout
			}
			if result.Stderr != "" {
				output["stderr"] = result.Stderr
			}

			return output, nil
		},
	}
}

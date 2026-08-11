package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildRemoteExecTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "remote_exec",
		Description: strings.Join([]string{
			"Execute commands on a remote machine via SSH. Useful for administering",
			"remote servers, deploying applications, or debugging production issues.",
			"",
			"Connection is read from REMOTE_HOST / REMOTE_USER / REMOTE_KEY env vars.",
			"Set REMOTE_HOST=user@host or individually: REMOTE_USER + REMOTE_HOST.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Command to run on the remote machine.",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Working directory on the remote machine (optional).",
				},
			},
			"required": []string{"command"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Command string `json:"command"`
				WorkDir string `json:"workdir"`
			}
			json.Unmarshal(args, &params)
			if strings.TrimSpace(params.Command) == "" {
				return nil, fmt.Errorf("command is required")
			}

			host := os.Getenv("REMOTE_HOST")
			user := os.Getenv("REMOTE_USER")
			keyPath := os.Getenv("REMOTE_KEY")

			if host == "" {
				return nil, fmt.Errorf("REMOTE_HOST not set (use user@host or set REMOTE_USER + REMOTE_HOST)")
			}

			// Build SSH command
			sshArgs := []string{
				"-o", "StrictHostKeyChecking=accept-new",
				"-o", "ConnectTimeout=10",
			}

			if keyPath != "" {
				sshArgs = append(sshArgs, "-i", keyPath)
			}

			target := host
			if user != "" && !strings.Contains(host, "@") {
				target = user + "@" + host
			}
			sshArgs = append(sshArgs, target)

			if params.WorkDir != "" {
				params.Command = fmt.Sprintf("cd %s && %s", params.WorkDir, params.Command)
			}
			sshArgs = append(sshArgs, params.Command)

			cmd := exec.CommandContext(ctx, "ssh", sshArgs...)

			start := time.Now()
			output, err := cmd.CombinedOutput()
			elapsed := time.Since(start)

			result := map[string]any{
				"host":    target,
				"command": params.Command,
				"output":  string(output),
				"elapsed": elapsed.String(),
			}

			if err != nil {
				result["error"] = err.Error()
				if exitErr, ok := err.(*exec.ExitError); ok {
					result["exit_code"] = exitErr.ExitCode()
				}
			}

			return result, nil
		},
	}
}

func BuildAuditReadTool() *agentcore.Tool {
	sensitivePaths := map[string]bool{
		".env": true, ".env.local": true, ".envrc": true,
		"credentials": true, "secrets": true, "secret": true,
		".ssh": true, ".gnupg": true, ".aws": true, ".gcloud": true,
		"id_rsa": true, "id_ed25519": true, "id_ecdsa": true,
		".netrc": true, ".npmrc": true, ".pypirc": true,
		".git-credentials": true, ".docker/config.json": true,
	}

	return &agentcore.Tool{
		Name: "audit_read",
		Description: strings.Join([]string{
			"Read file contents like 'read', but with automatic sensitive-path blocking.",
			"Refuses to read .env, credentials, SSH keys, and cloud config files.",
			"Use this when you need the model to safely browse files without risk of",
			"accidentally exposing secrets.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to read.",
				},
			},
			"required": []string{"file_path"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				FilePath string `json:"file_path"`
			}
			json.Unmarshal(args, &params)

			// Check sensitive paths
			path := strings.ToLower(params.FilePath)
			for _, part := range strings.Split(path, string(os.PathSeparator)) {
				if sensitivePaths[part] {
					return map[string]any{
						"blocked": true,
						"path":    params.FilePath,
						"reason":  fmt.Sprintf("path matches sensitive pattern: %s", part),
					}, nil
				}
			}

			// Delegate to standard read
			data, err := os.ReadFile(params.FilePath)
			if err != nil {
				return nil, fmt.Errorf("audit_read: %w", err)
			}
			return map[string]any{
				"path":    params.FilePath,
				"content": string(data),
				"size":    len(data),
			}, nil
		},
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/codemode"
	"github.com/covoyage/covonaut/agentcore"
)

// BuildRunCodeToolWithRegistry creates the run_code tool with access to
// the agent's tool registry for SDK generation and tool execution.
func BuildRunCodeToolWithRegistry(getTool func(name string) (*agentcore.Tool, bool), toolNames func() []string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "run_code",
		Description: strings.Join([]string{
			"Execute a complete Go program with access to all agent tools.",
			"",
			"The code runs in a sandboxed subprocess. Available tool functions:",
			"  ToolCall(name, args) → (any, error)    — call any tool, returns raw result",
			"  ToolCallMap(name, args) → (map, error)  — call any tool, returns map",
			"  ToolCallStr(name, args) → (string, error) — call any tool, returns string",
			"  ToolCallBytes(name, args) → ([]byte, error) — call any tool, returns bytes",
			"",
			"args is a map[string]any, e.g. map[string]any{\"command\": \"ls -la\"}",
			"",
			"Example (find all TODOs in Go files):",
			"  result, err := ToolCallMap(\"search_files\", map[string]any{",
			"      \"pattern\": \"TODO\",",
			"      \"include\": \"*.go\",",
			"  })",
			"  fmt.Println(result)",
			"",
			"Use this when you need to compose multiple tool calls with control flow,",
			"loops, or intermediate processing — instead of making individual tool calls.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "Go source code to execute. Use ToolCall/ToolCallMap/ToolCallStr to invoke tools.",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Timeout in seconds. Default: 60, max: 300.",
					"minimum":     1,
					"maximum":     300,
				},
			},
			"required": []string{"code"},
		},
		Func: func(runCtx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Code    string `json:"code"`
				Timeout int    `json:"timeout"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Code) == "" {
				return nil, fmt.Errorf("code is required")
			}

			timeout := time.Duration(params.Timeout) * time.Second
			if timeout <= 0 {
				timeout = 60 * time.Second
			}
			if timeout > 300*time.Second {
				timeout = 300 * time.Second
			}

			// Build tool info list from the agent's registry
			names := toolNames()
			defs := make([]agentcore.ToolDefinition, 0, len(names))
			for _, name := range names {
				if t, ok := getTool(name); ok {
					defs = append(defs, t.Definition())
				}
			}
			tools := codemode.ToolsFromDefinitions(defs)

			// Executor that delegates to the agent's tool functions
			executor := func(ctx context.Context, name string, toolArgs json.RawMessage) (any, error) {
				t, ok := getTool(name)
				if !ok {
					return nil, fmt.Errorf("tool %q not found", name)
				}
				return t.Func(ctx, toolArgs)
			}

			result, err := codemode.Execute(runCtx, tools, params.Code, executor, timeout)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"output":    result.Output,
				"exit_code": result.ExitCode,
				"duration":  result.Duration,
				"timed_out": result.TimedOut,
			}, nil
		},
	}
}

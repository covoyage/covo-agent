package externalagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// defaultTimeout bounds a delegated task when the caller does not specify one.
const defaultTimeout = 10 * time.Minute

// BuildExternalAgentTool returns the external_agent tool, which delegates a
// standalone, self-contained task to an external coding agent (Claude Code,
// Codex, or opencode) that runs as its own process.
func BuildExternalAgentTool(r *Registry) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "external_agent",
		Description: "Delegate a standalone, self-contained task to an external coding agent that runs as its own process (Claude Code, Codex, or opencode). " +
			"Use this when a task would benefit from a different model's strengths, or to parallelize independent work. " +
			"The task must be fully self-contained: the external agent cannot see this conversation, so write the complete task, context, constraints, and expected output format into the task text. " +
			"The external agent runs in the working directory (or cwd if given) and can edit files and run commands there; treat its file changes as authoritative. " +
			"The claude provider speaks the Claude Agent SDK control protocol (stream-json) natively and the codex provider speaks the Codex app-server protocol (codex app-server --stdio): both stream the task, answer tool/command approval requests (denying anything not explicitly allowed), and honour cancellation. " +
			"This call blocks until the external agent finishes (or times out) and returns its final text output. " +
			"Requires the chosen CLI to be installed and signed in locally; if no provider is given, an available one is picked automatically. " +
			"Control which providers are exposed with the COVO_EXTERNAL_AGENTS environment variable (default: all).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "The complete, self-contained task for the external agent. Include all context, constraints, and the exact expected output. The external agent cannot see the current conversation.",
				},
				"provider": map[string]any{
					"type":        "string",
					"enum":        []string{"claude", "codex", "opencode"},
					"description": "Which external agent to use. Omit to pick an available one automatically. 'claude' = Claude Code CLI, 'codex' = OpenAI Codex CLI, 'opencode' = opencode CLI.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the external agent (default: the agent's working directory).",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Maximum seconds to wait for the external agent (default: 600).",
				},
				"permission_mode": map[string]any{
					"type":        "string",
					"enum":        []string{"default", "acceptEdits", "plan", "bypassPermissions"},
					"description": "Permission mode for the delegated agent. Claude: default | acceptEdits | plan | bypassPermissions (default: acceptEdits, which auto-approves file edits; plan makes it read-only). Codex: bypassPermissions auto-approves command/file approval requests; otherwise approvals are denied.",
				},
				"allowed_tools": map[string]any{
					"type":        "string",
					"description": "Claude only. Comma-separated tool names to auto-approve without prompting, e.g. 'Bash,Edit,Write'. Read-only tools (Read, Glob, Grep, WebFetch, etc.) are always approved.",
				},
				"disallowed_tools": map[string]any{
					"type":        "string",
					"description": "Claude only. Comma-separated tool names to always deny.",
				},
				"max_turns": map[string]any{
					"type":        "integer",
					"description": "Claude only. Cap on the number of agent turns (default: unlimited).",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Claude or Codex. Model override for the delegated agent, e.g. 'claude-sonnet-4-5' or 'gpt-5.2'. Omit to use the CLI default.",
				},
			},
			"required": []string{"task"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Task            string `json:"task"`
				Provider        string `json:"provider"`
				Cwd             string `json:"cwd"`
				TimeoutSeconds  int    `json:"timeout_seconds"`
				PermissionMode  string `json:"permission_mode"`
				AllowedTools    string `json:"allowed_tools"`
				DisallowedTools string `json:"disallowed_tools"`
				MaxTurns        int    `json:"max_turns"`
				Model           string `json:"model"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Task) == "" {
				return nil, fmt.Errorf("task is required")
			}

			provider, err := selectProvider(r, params.Provider)
			if err != nil {
				return nil, err
			}

			cwd := params.Cwd
			if cwd == "" {
				cwd = r.WorkDir()
			}

			runCtx := ctx
			cancel := func() {}
			timeout := defaultTimeout
			if params.TimeoutSeconds > 0 {
				timeout = time.Duration(params.TimeoutSeconds) * time.Second
			}
			runCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()

			opts := RunOptions{
				Cwd:             cwd,
				PermissionMode:  params.PermissionMode,
				AllowedTools:    splitTools(params.AllowedTools),
				DisallowedTools: splitTools(params.DisallowedTools),
				MaxTurns:        params.MaxTurns,
				Model:           params.Model,
			}

			output, runErr := provider.RunOpt(runCtx, params.Task, cwd, opts)

			result := map[string]any{
				"provider": provider.Name(),
				"task":     params.Task,
				"cwd":      cwd,
			}
			if runErr != nil {
				result["error"] = runErr.Error()
				return result, nil
			}
			result["output"] = output
			return result, nil
		},
	}
}

// selectProvider resolves the requested provider, falling back to the first
// available one when none is requested.
func selectProvider(r *Registry, name string) (Provider, error) {
	if name != "" {
		p, ok := r.Get(name)
		if !ok {
			return nil, fmt.Errorf("external agent %q is not enabled (set COVO_EXTERNAL_AGENTS to include it; enabled: %s)",
				name, strings.Join(enabledNames(r), ", "))
		}
		if reason, ok := p.Available(); !ok {
			return nil, fmt.Errorf("external agent %q unavailable: %s", name, reason)
		}
		return p, nil
	}
	if p, ok := r.Default(); ok {
		return p, nil
	}
	avail := r.Availability()
	if len(avail) == 0 {
		return nil, fmt.Errorf("no external agents are enabled (set COVO_EXTERNAL_AGENTS, e.g. 'all')")
	}
	parts := make([]string, 0, len(avail))
	for _, n := range enabledNames(r) {
		parts = append(parts, fmt.Sprintf("%s: %s", n, avail[n]))
	}
	return nil, fmt.Errorf("no external agent is available right now; %s", strings.Join(parts, "; "))
}

func enabledNames(r *Registry) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for n := range r.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// splitTools parses a comma-separated tool list, trimming whitespace and
// dropping empty entries.
func splitTools(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

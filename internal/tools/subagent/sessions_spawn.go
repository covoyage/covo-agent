package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// SpawnRunner is the function signature for running a spawned session.
type SpawnRunner func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error)

// SpawnResult holds the result of a spawned session.
type SpawnResult struct {
	Output   string   `json:"output"`
	Turns    int      `json:"turns"`
	Toolsets []string `json:"toolsets,omitempty"`
}

// SpawnStore tracks active spawned sessions.
type SpawnStore struct {
	mu       sync.RWMutex
	sessions map[string]*SpawnResult
	counter  int
}

func NewSpawnStore() *SpawnStore {
	return &SpawnStore{
		sessions: make(map[string]*SpawnResult),
	}
}

func (s *SpawnStore) Register(id string, result *SpawnResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = result
}

func (s *SpawnStore) NextID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	return fmt.Sprintf("spawn-%d", s.counter)
}

func (s *SpawnStore) Get(id string) (*SpawnResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.sessions[id]
	return r, ok
}

func BuildSessionsSpawnTool(runner SpawnRunner, subagentRunner *SubagentRunner, parentToolsets func() []string, parentMessages func() []agentcore.Message) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sessions_spawn",
		Description: strings.Join([]string{
			"Spawn a child agent session to execute a subtask autonomously.",
			"The child agent runs with a scoped subset of tools and returns the result.",
			"",
			"Use this for:",
			"- Parallel independent subtasks",
			"- Isolated operations that shouldn't pollute the main conversation",
			"- Long-running searches or analysis",
			"",
			"The child inherits a filtered subset of the parent's tools (no clarify, no nested spawn).",
			"",
			"Context modes:",
			"- 'isolated' (default): child gets no parent context — clean slate.",
			"- 'state': child gets a compact checkpoint summary of the parent's",
			"  recent conversation (last user requests, last assistant action, tools used).",
			"- 'full': child inherits the parent's full conversation messages.",
			"",
			"Parameters:",
			"- task: The subtask description for the child agent.",
			"- capability_mode: Coarse-grained tool access: 'read-only', 'read-write', 'execute', or 'all'.",
			"    Overrides toolsets when set. 'read-only'=filesystem/search/git/web; 'read-write'=+editing;",
			"    'execute'=+shell; 'all'=inherit everything from parent (default).",
			"- toolsets: Explicit toolset names (overrides capability_mode).",
			"- max_turns: Maximum turns for the child (default: 10).",
			"- context_mode: 'isolated' (default), 'state', or 'full'.",
			"- role: 'leaf' (default, cannot delegate) or 'orchestrator' (can further spawn children).",
			"- persona: name or comma-separated names of behavior overlays (e.g. 'reviewer', 'concise,debugger').",
			"    Injects tone/format/focus guidance as a system-reminder. Use 'list' to see available personas.",
			"- isolation: Workspace isolation. 'shared' (default) uses the parent working directory;",
			"    'worktree' runs the child in a detached git worktree so its file edits stay isolated.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "The task description for the child agent to execute.",
				},
				"capability_mode": map[string]any{
					"type":        "string",
					"description": "Coarse-grained tool access filter. 'read-only': filesystem/search/git/web (no mutations); 'read-write': +editing; 'execute': +shell/bash; 'all': inherit everything from parent. Takes precedence over toolsets unless toolsets is explicitly set.",
					"enum":        []string{"read-only", "read-write", "execute", "all"},
				},
				"toolsets": map[string]any{
					"type":        "array",
					"description": "Explicit toolset names the child can use. Overrides capability_mode when set. Default: inherits parent's toolsets (minus dangerous ones).",
					"items": map[string]any{
						"type": "string",
					},
				},
				"max_turns": map[string]any{
					"type":        "integer",
					"description": "Maximum turns for the child agent (default: 10).",
				},
				"context_mode": map[string]any{
					"type":        "string",
					"description": "Context isolation mode: 'isolated' (default, no parent context), 'state' (compact checkpoint summary of parent), or 'full' (inherits parent messages).",
					"enum":        []string{"isolated", "state", "full"},
				},
				"role": map[string]any{
					"type":        "string",
					"description": "Child role: 'leaf' (default, cannot delegate), 'orchestrator' (can spawn children), or agent role names: 'explorer', 'coder', 'reviewer', 'devops', 'architect'.",
					"enum":        []string{"leaf", "orchestrator", "explorer", "coder", "reviewer", "devops", "architect"},
				},
				"provider": map[string]any{
					"type":        "string",
					"description": "Override the LLM provider for this child (e.g. 'openai', 'anthropic'). Use for cost optimization.",
				},
				"persona": map[string]any{
					"type":        "string",
					"description": "Behavior overlay persona name(s). Single name (e.g. 'reviewer') or comma-separated chain (e.g. 'concise,debugger'). Injects tone/format/focus guidance without changing tools.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Override the model for this child (e.g. 'gpt-5.6'). Use for cost/speed optimization.",
				},
				"isolation": map[string]any{
					"type":        "string",
					"description": "Workspace isolation: 'shared' (default, same working directory as parent) or 'worktree' (detached git worktree).",
					"enum":        []string{"shared", "worktree"},
				},
			},
			"required": []string{"task"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Task           string   `json:"task"`
				Toolsets       []string `json:"toolsets"`
				CapabilityMode string   `json:"capability_mode"`
				MaxTurns       int      `json:"max_turns"`
				ContextMode    string   `json:"context_mode"`
				Role           string   `json:"role"`
				Persona        string   `json:"persona"`
				Provider       string   `json:"provider"`
				Model          string   `json:"model"`
				Isolation      string   `json:"isolation"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Task) == "" {
				return nil, fmt.Errorf("task is required")
			}
			if params.MaxTurns <= 0 {
				params.MaxTurns = 10
			}
			if params.ContextMode == "" {
				params.ContextMode = "isolated"
			}
			if params.Role == "" {
				params.Role = "leaf"
			}

			if runner == nil {
				return nil, fmt.Errorf("sessions_spawn: no runner configured")
			}

			// --- Context mode handling ---
			// 'isolated': child gets no parent context (default).
			// 'state': child gets a compact checkpoint summary prepended to the task.
			// 'full': child inherits parent's full messages via context.
			task := params.Task

			// --- Persona injection ---
			// If a persona is specified, resolve it (or the chain) and inject
			// the system-reminder text into the task description.
			if params.Persona != "" {
				names := strings.Split(params.Persona, ",")
				chain, err := ResolvePersonaChain(names)
				if err != nil {
					return nil, fmt.Errorf("persona: %w", err)
				}
				reminder := FormatPersonaChainReminder(chain)
				if reminder != "" {
					task = reminder + "\n\n---\n## Your Task\n" + task
				}
			}

			switch params.ContextMode {
			case "state":
				if parentMessages != nil {
					if summary := SummarizeParentState(parentMessages()); summary != "" {
						task = summary + "\n\n---\n## Your Task\n" + params.Task
					}
				}
			case "full":
				if parentMessages != nil {
					ctx = WithParentMessages(ctx, parentMessages())
				}
			}

			// Resolve toolsets: explicit toolsets > capability_mode > role > parent inherit
			childToolsets := ResolveCapabilityMode(params.CapabilityMode, params.Toolsets)

			// Resolve AgentRole to toolsets if role matches a predefined role
			// (only when neither capability_mode nor explicit toolsets were given)
			if childToolsets == nil {
				if role, ok := GetRole(params.Role); ok {
					if len(role.Toolsets) > 0 {
						childToolsets = role.Toolsets
					}
				}
			}
			if childToolsets == nil && parentToolsets != nil {
				childToolsets = parentToolsets()
			}

			// Determine depth and orchestrator role
			currentDepth := SubagentDepthFromContext(ctx)
			orchestrator := false
			if subagentRunner != nil {
				maxDepth := subagentRunner.cfg.MaxSpawnDepth
				depthAllowed := maxDepth == 0 || currentDepth < maxDepth
				orchestrator = params.Role == "orchestrator" && depthAllowed
			}

			// Apply delegation scoping with role-aware filtering
			if parentToolsets != nil {
				childToolsets = DelegateToolsetsForRole(
					parentToolsets(),
					childToolsets,
					true,         // restrict to parent scope
					orchestrator, // retain delegation if orchestrator
				)
			}

			// Ensure sessions_spawn is not available to leaf children
			if !orchestrator {
				var safeToolsets []string
				for _, ts := range childToolsets {
					if ts != "delegation" {
						safeToolsets = append(safeToolsets, ts)
					}
				}
				childToolsets = safeToolsets
			}

			// Run through SubagentRunner if configured (adds timeout, heartbeat, diagnostics)
			if subagentRunner != nil {
				spawn := runner // capture

				// Pass provider/model overrides via context
				if params.Provider != "" {
					ctx = WithSubagentProvider(ctx, params.Provider)
				}
				if params.Model != "" {
					ctx = WithSubagentModel(ctx, params.Model)
				}
				if params.Isolation != "" {
					ctx = WithSubagentIsolation(ctx, params.Isolation)
				}

				output, err := subagentRunner.Run(ctx, spawn, task, childToolsets, SubagentRunOptions{
					Goal:         params.Task,
					Orchestrator: orchestrator,
					Depth:        currentDepth + 1,
					MaxTurns:     params.MaxTurns,
				})
				if err != nil {
					return map[string]any{
						"status":   "error",
						"error":    err.Error(),
						"task":     params.Task,
						"toolsets": childToolsets,
						"role":     params.Role,
					}, nil
				}
				return map[string]any{
					"status":   "completed",
					"output":   output,
					"toolsets": childToolsets,
					"task":     params.Task,
					"role":     params.Role,
				}, nil
			}

			// Fallback: raw SpawnRunner without safety wrapper
			var safeToolsets []string
			for _, ts := range childToolsets {
				if ts != "delegation" {
					safeToolsets = append(safeToolsets, ts)
				}
			}
			if params.Isolation != "" {
				ctx = WithSubagentIsolation(ctx, params.Isolation)
			}

			output, err := runner(ctx, task, safeToolsets, params.MaxTurns)
			if err != nil {
				return map[string]any{
					"status": "error",
					"error":  err.Error(),
					"task":   params.Task,
				}, nil
			}

			return map[string]any{
				"status":   "completed",
				"output":   output,
				"toolsets": safeToolsets,
				"task":     params.Task,
			}, nil
		},
	}
}

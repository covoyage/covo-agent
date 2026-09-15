package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

// SpawnCompleteFunc is called when a background child finishes.
type SpawnCompleteFunc func(childID, task, output string, err error)

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

// flexBool accepts JSON booleans and their stringified forms ("true",
// "false", "1", "0") — models frequently stringify flag arguments, and a
// strict unmarshal would fail the whole spawn for a cosmetic type mismatch.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	v := strings.Trim(strings.TrimSpace(string(data)), `"`)
	switch v {
	case "true", "1":
		*b = true
	case "false", "0", "null", "":
		*b = false
	default:
		return fmt.Errorf("invalid boolean value %q", v)
	}
	return nil
}

// flexInt accepts JSON numbers and numeric strings ("10") for the same
// reason as flexBool.
type flexInt int

func (i *flexInt) UnmarshalJSON(data []byte) error {
	v := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if v == "" || v == "null" {
		*i = 0
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("invalid integer value %q", v)
	}
	*i = flexInt(n)
	return nil
}

func BuildSessionsSpawnTool(runner SpawnRunner, subagentRunner *SubagentRunner, parentToolsets func() []string, parentMessages func() []agentcore.Message, registry *SubagentRegistry, onComplete SpawnCompleteFunc) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sessions_spawn",
		Description: strings.Join([]string{
			"Spawn a child agent session that works on a subtask independently and reports back.",
			"",
			buildSpawnRoster(subagentRunner),
			"",
			"Usage notes:",
			"- When the child is done, its result is returned in the tool result (for background",
			"  spawns, retrieve it with sessions_wait). You MUST incorporate the results into your",
			"  work based on the requirements BEFORE concluding.",
			"- background: defaults to TRUE — the call returns immediately with a child id and",
			"  the child runs concurrently with your work; results are delivered to you on",
			"  completion. Set background=false only to block until one child finishes.",
			"- Subagents receive the project's AGENTS.md. If the task depends on specific conventions",
			"  (build rules, testing patterns, output formats), include them directly in the task.",
			"- Children start without this conversation (context_mode 'isolated'): give absolute",
			"  paths, the exact question to answer, and the expected report format. Never refer to",
			"  \"as discussed above\" inside a child task.",
			"- The child inherits a filtered subset of the parent's tools: no clarify, and — unless",
			"  role='orchestrator' — the whole delegation family (spawn/wait/fork/close/steer tools)",
			"  is physically removed, so leaf children cannot delegate further.",
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
			"- max_turns: Optional turn budget for the child. Omit for no limit — the child runs",
			"    until it judges the task complete (guardrails still apply). Set it only to bound",
			"    cost on well-scoped tasks.",
			"- context_mode: 'isolated' (default), 'state', 'full', or 'turns:N' (inherit only the",
			"    most recent N parent messages, e.g. 'turns:5').",
			"- role: 'leaf' (default, cannot delegate) or 'orchestrator' (can further spawn children).",
			"- persona: name or comma-separated names of behavior overlays (e.g. 'reviewer', 'concise,debugger').",
			"    Injects tone/format/focus guidance as a system-reminder. Use 'list' to see available personas.",
			"- isolation: Workspace isolation. 'shared' (default) uses the parent working directory;",
			"    'worktree' runs the child in a detached git worktree so its file edits stay isolated.",
			"- background: If true, return immediately with status=started and an id. Default false.",
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
					"description": "Optional turn budget for the child. Omit for no limit — the child runs until it judges the task complete. Set only to bound cost.",
				},
				"context_mode": map[string]any{
					"type":        "string",
					"description": "Context mode: 'isolated' (default, no parent context), 'state' (compact checkpoint summary of parent), 'full' (inherits parent messages), or 'turns:N' (inherits only the most recent N parent messages, e.g. 'turns:5').",
					"enum":        []string{"isolated", "state", "full", "turns:N"},
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
					"background": map[string]any{
						"type":        "boolean",
						"description": "If true, start the child and return immediately. Pair with sessions_yield.",
					},
				},
			},
			"required": []string{"task"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Task           string    `json:"task"`
				Toolsets       []string  `json:"toolsets"`
				CapabilityMode string    `json:"capability_mode"`
				MaxTurns       flexInt   `json:"max_turns"`
				ContextMode    string    `json:"context_mode"`
				Role           string    `json:"role"`
				Background     *flexBool `json:"background"`
				Persona        string    `json:"persona"`
				Provider       string    `json:"provider"`
				Model          string    `json:"model"`
				Isolation      string    `json:"isolation"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Task) == "" {
				return nil, fmt.Errorf("task is required")
			}
			// background defaults to TRUE: spawns return
			// immediately and children run concurrently — sequential tool
			// calls must not serialize children. Explicit false blocks.
			background := params.Background == nil || bool(*params.Background)
			// MaxTurns <= 0 means "no turn budget" — passed through as-is;
			// the child runs until it judges the task complete.
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
			// 'turns:N': child inherits only the most recent N injectable parent messages.
			mode, turnsN, err := normalizeContextMode(params.ContextMode)
			if err != nil {
				return nil, err
			}
			task := params.Task

			// --- Persona injection ---
			// If a persona is specified, resolve it (or the chain) and inject
			// the system-reminder text into the task description.
			var personaWarnings []string
			applyPersonaWarnings := func(out map[string]any) map[string]any {
				if len(personaWarnings) > 0 {
					out["persona_warnings"] = personaWarnings
				}
				return out
			}
			if params.Persona != "" {
				names := strings.Split(params.Persona, ",")
				chain, warnings, err := ResolvePersonaChainLenient(names)
				if err != nil {
					return nil, fmt.Errorf("persona: %w", err)
				}
				personaWarnings = warnings
				reminder := FormatPersonaChainReminder(chain)
				if reminder != "" {
					task = reminder + "\n\n---\n## Your Task\n" + task
				}
			}

			switch mode {
			case "state":
				if parentMessages != nil {
					if summary := SummarizeParentState(parentMessages()); summary != "" {
						task = summary + "\n\n---\n## Your Task\n" + params.Task
					}
				}
			case "full", "turns":
				if parentMessages != nil {
					ctx = WithParentMessages(ctx, parentMessages())
					if turnsN > 0 {
						ctx = WithParentMessageTurns(ctx, turnsN)
					}
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

			// Physically strip the delegation tool family from leaf children:
			// recursive spawn/steer/close tools are removed from the child's
			// toolset override, so a leaf cannot delegate even if it tries.
			if !orchestrator {
				childToolsets = StripDelegationTools(childToolsets)
			}
			if background && registry == nil {
				// No registry (headless/test contexts): fall back to a
				// blocking run rather than failing the spawn.
				background = false
			}
			if background {
				if runner == nil {
					return nil, fmt.Errorf("sessions_spawn: no runner configured")
				}
				if params.Provider != "" {
					ctx = WithSubagentProvider(ctx, params.Provider)
				}
				if params.Model != "" {
					ctx = WithSubagentModel(ctx, params.Model)
				}
				if params.Isolation != "" {
					ctx = WithSubagentIsolation(ctx, params.Isolation)
				}
				runCtx, id := registry.StartBackground(ctx, params.Task, currentDepth+1)
				// Carry the registry id so the spawn runner registers the
				// transcript view under the same id the tool block shows.
				runCtx = WithSubagentViewRef(runCtx, id)
				var timeoutCancel context.CancelFunc
				if subagentRunner != nil && subagentRunner.cfg.DefaultTimeout > 0 {
					runCtx, timeoutCancel = context.WithTimeout(runCtx, subagentRunner.cfg.DefaultTimeout)
				}
				spawnTask := task
				spawnToolsets := append([]string(nil), childToolsets...)
				maxTurns := params.MaxTurns
				safego.SafeGo(func() {
					if timeoutCancel != nil {
						defer timeoutCancel()
					}
					// Panic-safe completion: a child runner panic must not
					// leak a "running" registry entry or skip the completion
					// notification.
					defer func() {
						if r := recover(); r != nil {
							registry.Complete(id, true)
							if onComplete != nil {
								onComplete(id, params.Task, "", fmt.Errorf("subagent panicked: %v", r))
							}
						}
					}()
					output, err := runner(runCtx, spawnTask, spawnToolsets, int(maxTurns))
					registry.Complete(id, err != nil)
					if onComplete != nil {
						onComplete(id, params.Task, output, err)
					}
				}, nil)
				return applyPersonaWarnings(map[string]any{
					"status":   "started",
					"id":       id,
					"task":     params.Task,
					"toolsets": childToolsets,
					"role":     params.Role,
				}), nil
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
					MaxTurns:     int(params.MaxTurns),
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
				return applyPersonaWarnings(map[string]any{
					"status":   "completed",
					"output":   output,
					"toolsets": childToolsets,
					"task":     params.Task,
					"role":     params.Role,
				}), nil
			}

			// Fallback: raw SpawnRunner without safety wrapper
			var safeToolsets []string
			for _, ts := range childToolsets {
				if ts != "clarify" {
					safeToolsets = append(safeToolsets, ts)
				}
			}
			if params.Isolation != "" {
				ctx = WithSubagentIsolation(ctx, params.Isolation)
			}

			output, err := runner(ctx, task, safeToolsets, int(params.MaxTurns))
			if err != nil {
				return applyPersonaWarnings(map[string]any{
					"status": "error",
					"error":  err.Error(),
					"task":   params.Task,
				}), nil
			}

			return applyPersonaWarnings(map[string]any{
				"status":   "completed",
				"output":   output,
				"toolsets": safeToolsets,
				"task":     params.Task,
			}), nil
		},
	}
}

// buildSpawnRoster renders the live delegation roster into the tool
// description: available personas and roles, spawn depth, and timeout. The
// tool is rebuilt with each agent instance, so the roster reflects what is
// actually spawnable ("visible == callable") instead of a static guess.
func buildSpawnRoster(subagentRunner *SubagentRunner) string {
	var b strings.Builder
	b.WriteString("Roster for this session:")

	var personas []string
	for _, p := range ListPersonas() {
		personas = append(personas, p.Name)
	}
	if len(personas) > 0 {
		b.WriteString("\n- Personas (persona param): " + strings.Join(personas, ", "))
	}

	var roles []string
	for _, r := range GetRoles() {
		roles = append(roles, r.Name)
	}
	if len(roles) > 0 {
		b.WriteString("\n- Preset roles (persona param, set toolsets): " + strings.Join(roles, ", "))
	}

	if subagentRunner != nil && subagentRunner.cfg.MaxSpawnDepth > 0 {
		b.WriteString(fmt.Sprintf("\n- Max spawn depth: %d (delegation tools are physically removed from children at or beyond this depth)",
			subagentRunner.cfg.MaxSpawnDepth))
	} else {
		b.WriteString("\n- Max spawn depth: 0 (flat — delegation tools are physically removed from all children)")
	}
	if subagentRunner != nil && subagentRunner.cfg.DefaultTimeout > 0 {
		b.WriteString(fmt.Sprintf("\n- Per-child timeout: %s (a foreground wait that exceeds it moves the child to the background)",
			subagentRunner.cfg.DefaultTimeout))
	}

	return b.String()
}

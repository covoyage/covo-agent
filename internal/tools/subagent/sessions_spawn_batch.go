package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/safego"
)

// BatchTask describes a single task in a batch spawn.
type BatchTask struct {
	ID             string   `json:"id"`
	Task           string   `json:"task"`
	Toolsets       []string `json:"toolsets,omitempty"`
	CapabilityMode string   `json:"capability_mode,omitempty"` // read-only, read-write, execute, all
	MaxTurns       int      `json:"max_turns,omitempty"`
	Role           string   `json:"role,omitempty"`         // "leaf" or "orchestrator"
	Provider       string   `json:"provider,omitempty"`     // override LLM provider
	Model          string   `json:"model,omitempty"`        // override model
	ContextMode    string   `json:"context_mode,omitempty"` // "isolated" (default), "state", "full"
}

// BatchResult holds the result of one task in a batch spawn.
type BatchResult struct {
	ID       string   `json:"id"`
	Output   string   `json:"output"`
	Error    string   `json:"error,omitempty"`
	Status   string   `json:"status"` // "completed" or "failed"
	Role     string   `json:"role,omitempty"`
	Toolsets []string `json:"toolsets,omitempty"`
}

func BuildSessionsSpawnBatchTool(runner SpawnRunner, subagentRunner *SubagentRunner, parentToolsets func() []string, parentMessages func() []agentcore.Message) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sessions_spawn_batch",
		Description: strings.Join([]string{
			"Spawn MULTIPLE child agent sessions in PARALLEL to execute independent subtasks.",
			"Each child runs with a scoped subset of tools and returns its result.",
			"",
			"Use this for:",
			"- Exploring multiple approaches simultaneously",
			"- Researching several independent topics in parallel",
			"- Running batch analysis tasks concurrently",
			"",
			"The maximum parallelism is 5 by default (configurable via max_parallel).",
			"Children cannot spawn further sessions unless role='orchestrator'.",
			"",
			"Each task supports an optional 'role' field:",
			"  - 'leaf' (default): Cannot delegate further.",
			"  - 'orchestrator': Can spawn grandchildren (subject to max_spawn_depth).",
			"",
			"Each task supports an optional 'context_mode' field:",
			"  - 'isolated' (default): No parent context — clean slate.",
			"  - 'state': Compact checkpoint summary of parent's recent conversation.",
			"  - 'full': Inherits the parent's full conversation messages.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tasks": map[string]any{
					"type":        "array",
					"description": "List of tasks to execute in parallel. Each task has: id (unique), task (description), optional toolsets, max_turns, and role.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "Unique identifier for this task (used to correlate results).",
							},
							"task": map[string]any{
								"type":        "string",
								"description": "The task description for the child agent.",
							},
							"capability_mode": map[string]any{
								"type":        "string",
								"description": "Coarse-grained tool access: 'read-only', 'read-write', 'execute', 'all'. Overrides toolsets.",
								"enum":        []string{"read-only", "read-write", "execute", "all"},
							},
							"toolsets": map[string]any{
								"type":        "array",
								"description": "Toolset names the child can use. Default: inherits parent's toolsets.",
								"items":       map[string]any{"type": "string"},
							},
							"max_turns": map[string]any{
								"type":        "integer",
								"description": "Maximum turns for this child agent (default: 10).",
							},
							"role": map[string]any{
								"type":        "string",
								"description": "'leaf' (default) or 'orchestrator' (can spawn grandchildren). Subject to max_spawn_depth.",
								"enum":        []string{"leaf", "orchestrator"},
							},
							"provider": map[string]any{
								"type":        "string",
								"description": "Override LLM provider for this child (e.g. 'openai', 'anthropic').",
							},
							"model": map[string]any{
								"type":        "string",
								"description": "Override model for this child (e.g. 'gpt-5.6').",
							},
							"context_mode": map[string]any{
								"type":        "string",
								"description": "Context isolation mode: 'isolated' (default, no parent context), 'state' (compact checkpoint summary of parent), or 'full' (inherits parent messages).",
								"enum":        []string{"isolated", "state", "full"},
							},
						},
						"required": []string{"id", "task"},
					},
					"minItems": 1,
				},
				"max_parallel": map[string]any{
					"type":        "integer",
					"description": "Maximum number of children to run in parallel (default: 5).",
				},
			},
			"required": []string{"tasks"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Tasks       []BatchTask `json:"tasks"`
				MaxParallel int         `json:"max_parallel"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if len(params.Tasks) == 0 {
				return nil, fmt.Errorf("at least one task is required")
			}
			if params.MaxParallel <= 0 {
				params.MaxParallel = 5
			}

			if runner == nil {
				return nil, fmt.Errorf("sessions_spawn_batch: no runner configured")
			}

			// Validate uniqueness of task IDs
			seen := make(map[string]bool, len(params.Tasks))
			for _, t := range params.Tasks {
				if t.ID == "" {
					return nil, fmt.Errorf("each task must have a unique id")
				}
				if seen[t.ID] {
					return nil, fmt.Errorf("duplicate task id: %s", t.ID)
				}
				seen[t.ID] = true
				if strings.TrimSpace(t.Task) == "" {
					return nil, fmt.Errorf("task %q has empty description", t.ID)
				}
			}

			// Determine depth and orchestrator settings
			currentDepth := SubagentDepthFromContext(ctx)
			maxDepth := 0
			if subagentRunner != nil {
				maxDepth = subagentRunner.cfg.MaxSpawnDepth
			}

			results := make([]BatchResult, len(params.Tasks))
			var mu sync.Mutex
			sem := make(chan struct{}, params.MaxParallel)
			var wg sync.WaitGroup

			for i, task := range params.Tasks {
				wg.Add(1)
				idx, t := i, task
				safego.SafeGo(func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()

					childToolsets := ResolveCapabilityMode(t.CapabilityMode, t.Toolsets)
					if childToolsets == nil && parentToolsets != nil {
						childToolsets = parentToolsets()
					}

					// Determine role
					orchestrator := false
					depthAllowed := maxDepth == 0 || currentDepth < maxDepth
					if t.Role == "orchestrator" && depthAllowed {
						orchestrator = true
					}

					if parentToolsets != nil {
						childToolsets = DelegateToolsetsForRole(
							parentToolsets(),
							childToolsets,
							true,
							orchestrator,
						)
					}

					if !orchestrator {
						var safeToolsets []string
						for _, ts := range childToolsets {
							if ts != "delegation" {
								safeToolsets = append(safeToolsets, ts)
							}
						}
						childToolsets = safeToolsets
					}

					maxT := t.MaxTurns
					if maxT <= 0 {
						maxT = 10
					}

					// --- Context mode handling ---
					// 'isolated': child gets no parent context (default).
					// 'state': child gets a compact checkpoint summary prepended to the task.
					// 'full': child inherits parent's full messages via context.
					taskText := t.Task
					childCtx := ctx
					contextMode := t.ContextMode
					if contextMode == "" {
						contextMode = "isolated"
					}
					switch contextMode {
					case "state":
						if parentMessages != nil {
							if summary := SummarizeParentState(parentMessages()); summary != "" {
								taskText = summary + "\n\n---\n## Your Task\n" + t.Task
							}
						}
					case "full":
						if parentMessages != nil {
							childCtx = WithParentMessages(childCtx, parentMessages())
						}
					}

					// Run through SubagentRunner if configured
					var output string
					var runErr error
					if subagentRunner != nil {
						spawn := runner
						if t.Provider != "" {
							childCtx = WithSubagentProvider(childCtx, t.Provider)
						}
						if t.Model != "" {
							childCtx = WithSubagentModel(childCtx, t.Model)
						}
						output, runErr = subagentRunner.Run(childCtx, spawn, taskText, childToolsets, SubagentRunOptions{
							Goal:         t.Task,
							ChildIndex:   idx,
							ChildCount:   len(params.Tasks),
							Orchestrator: orchestrator,
							Depth:        currentDepth + 1,
							MaxTurns:     maxT,
						})
					} else {
						output, runErr = runner(childCtx, taskText, childToolsets, maxT)
					}

					r := BatchResult{
						ID:       t.ID,
						Status:   "completed",
						Output:   output,
						Role:     t.Role,
						Toolsets: childToolsets,
					}
					if runErr != nil {
						r.Status = "failed"
						r.Error = runErr.Error()
					}

					mu.Lock()
					results[idx] = r
					mu.Unlock()
				}, nil)
			}

			wg.Wait()

			completed := 0
			failed := 0
			for _, r := range results {
				if r.Status == "completed" {
					completed++
				} else {
					failed++
				}
			}

			return map[string]any{
				"status":    "done",
				"completed": completed,
				"failed":    failed,
				"total":     len(results),
				"results":   results,
			}, nil
		},
	}
}

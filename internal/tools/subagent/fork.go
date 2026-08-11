package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/covoyage/covonaut/agentcore"
)

// ForkContext provides full parent conversation history to a child agent.
// Unlike sessions_spawn (isolated context), fork inherits everything.
type ForkContext struct {
	ParentID    string `json:"parent_id"`
	ParentCWD   string `json:"parent_cwd"`
	ParentModel string `json:"parent_model,omitempty"`
	History     string `json:"history"` // Full conversation history
}

// ForkGuard prevents recursive fork storms.
type ForkGuard struct {
	maxDepth int32
	counter  atomic.Int32
}

func NewForkGuard(maxDepth int) *ForkGuard {
	return &ForkGuard{maxDepth: int32(maxDepth)}
}

func (g *ForkGuard) Acquire(parentDepth int) error {
	if parentDepth >= int(g.maxDepth) {
		return fmt.Errorf("fork depth %d exceeds max %d", parentDepth, g.maxDepth)
	}
	g.counter.Add(1)
	return nil
}

func (g *ForkGuard) Release() {
	g.counter.Add(-1)
}

func (g *ForkGuard) Active() int32 {
	return g.counter.Load()
}

func truncateMax(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max > 3 {
		return s[:max-3] + "..."
	}
	return s[:max]
}

func BuildSessionsForkTool(runner SpawnRunner, registry *SubagentRegistry, guard *ForkGuard) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sessions_fork",
		Description: strings.Join([]string{
			"Fork the current conversation into a subagent that inherits the FULL",
			"parent conversation history. Unlike sessions_spawn (fresh context), this",
			"gives the child agent complete context including all prior tool outputs,",
			"decisions, and context.",
			"",
			"Use when the subagent needs deep understanding of the work done so far,",
			"not just a one-line task description.",
			"Fork depth is limited to prevent infinite recursion.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "Task for the forked agent.",
				},
				"history": map[string]any{
					"type":        "string",
					"description": "Full conversation history to inherit.",
				},
				"depth": map[string]any{
					"type":        "integer",
					"description": "Current fork depth (parent passes its depth).",
				},
				"toolsets": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Toolset names for forked agent.",
				},
			},
			"required": []string{"task", "history"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Task     string   `json:"task"`
				History  string   `json:"history"`
				Depth    int      `json:"depth"`
				Toolsets []string `json:"toolsets"`
			}
			json.Unmarshal(args, &params)

			if err := guard.Acquire(params.Depth); err != nil {
				return nil, err
			}
			defer guard.Release()

			// Prepend history context to the task
			fullTask := fmt.Sprintf("%s\n\n<inherited_conversation>\n%s\n</inherited_conversation>",
				params.Task, params.History)

			subID := registry.Start(params.Task, params.Depth+1)

			output, err := runner(ctx, fullTask, params.Toolsets, 0)
			if err != nil {
				registry.Complete(subID, true)
				return nil, fmt.Errorf("fork failed: %w", err)
			}

			registry.Complete(subID, false)
			return map[string]any{
				"forked": true,
				"depth":  params.Depth + 1,
				"output": truncateMax(output, 2000),
				"active": guard.Active(),
			}, nil
		},
	}
}

package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func BuildForkSnapshot(messages []agentcore.Message) ([]agentcore.Message, error) {
	var snapshot []agentcore.Message
	pending := map[string]bool{}
	batchStart := -1
	for _, message := range messages {
		if message.Role == agentcore.RoleSystem {
			continue
		}
		if message.Role != agentcore.RoleTool && len(pending) != 0 {
			return nil, fmt.Errorf("fork history contains an unfinished tool batch before another message")
		}
		if message.Role == agentcore.RoleTool {
			if !pending[message.ToolCallID] {
				return nil, fmt.Errorf("fork history contains an unmatched tool result")
			}
			delete(pending, message.ToolCallID)
		}
		if len(message.ToolCalls) > 0 {
			if message.Role != agentcore.RoleAssistant {
				return nil, fmt.Errorf("fork tool calls must belong to assistant messages")
			}
			batchStart = len(snapshot)
			for _, call := range message.ToolCalls {
				if call.ID == "" || pending[call.ID] {
					return nil, fmt.Errorf("fork history contains invalid tool call ids")
				}
				pending[call.ID] = true
			}
		}
		data, err := json.Marshal(message)
		if err != nil {
			return nil, err
		}
		var copied agentcore.Message
		if err := json.Unmarshal(data, &copied); err != nil {
			return nil, err
		}
		copied.ID, copied.InvocationID = "", ""
		copied.Metadata = map[string]any{"inherited_context": true}
		snapshot = append(snapshot, copied)
	}
	if len(pending) > 0 {
		snapshot = snapshot[:batchStart]
	}
	return snapshot, nil
}

func BuildSessionsForkTool(runner SpawnRunner, registry *SubagentRegistry, guard *ForkGuard, providers ...func() []agentcore.Message) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sessions_fork",
		Description: strings.Join([]string{
			"Fork the runtime conversation snapshot into a subagent, preserving completed tool call/result pairs.",
			"The current unfinished tool batch is excluded. Historical approvals and process handles are not inherited.",
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
				"toolsets": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Toolset names for forked agent.",
				},
				"role": map[string]any{
					"type":        "string",
					"description": "Optional spawn role: leaf, orchestrator, explorer, coder, reviewer, devops, architect, recon, verifier.",
					"enum":        []string{"leaf", "orchestrator", "explorer", "coder", "reviewer", "devops", "architect", "recon", "verifier"},
				},
			},
			"required":             []string{"task"},
			"additionalProperties": false,
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Task     string   `json:"task"`
				Toolsets []string `json:"toolsets"`
				Role     string   `json:"role"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(args)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&params); err != nil {
				return nil, err
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return nil, fmt.Errorf("fork expects one JSON object")
			}
			if strings.TrimSpace(params.Task) == "" || runner == nil || guard == nil || len(providers) == 0 || providers[0] == nil {
				return nil, fmt.Errorf("fork requires a task, runner, guard, and runtime history provider")
			}
			if strings.EqualFold(strings.TrimSpace(params.Role), "parent") {
				return nil, fmt.Errorf("parent is reserved for the top-level agent")
			}
			depth := SubagentDepthFromContext(ctx)

			if err := guard.Acquire(depth); err != nil {
				return nil, err
			}
			defer guard.Release()
			snapshot, err := BuildForkSnapshot(providers[0]())
			if err != nil {
				return nil, err
			}
			ctx = WithStructuredFork(ctx, snapshot)
			ctx = WithSubagentDepth(ctx, depth+1)

			task := params.Task
			toolsets := params.Toolsets
			ctx, task, toolsets = ApplySpawnRole(ctx, task, params.Role, toolsets)

			fullTask := task + "\n\nThe preceding messages are inherited historical context. Follow your own role and task; prior approvals and live tool/process handles are not inherited."

			var subID string
			if registry != nil {
				subID = registry.Start(params.Task, depth+1)
			}

			output, err := runner(ctx, fullTask, toolsets, 0)
			if err != nil {
				if registry != nil && subID != "" {
					registry.Complete(subID, true)
				}
				return nil, fmt.Errorf("fork failed: %w", err)
			}

			if registry != nil && subID != "" {
				registry.Complete(subID, false)
			}
			return map[string]any{
				"forked": true,
				"depth":  depth + 1,
				"output": truncateMax(output, 2000),
				"active": guard.Active(),
			}, nil
		},
	}
}

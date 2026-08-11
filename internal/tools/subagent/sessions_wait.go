package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildSessionsWaitTool(registry *SubagentRegistry) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sessions_wait",
		Description: strings.Join([]string{
			"Wait for one or more sub-agents to complete.",
			"Blocks until all specified agents finish or timeout.",
			"Omit agent_ids to wait for all active agents.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Agent IDs to wait for. Empty = all active agents.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Max wait time in seconds (default: 300).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				AgentIDs       []string `json:"agent_ids"`
				TimeoutSeconds int      `json:"timeout_seconds"`
			}
			json.Unmarshal(args, &params)
			if params.TimeoutSeconds <= 0 {
				params.TimeoutSeconds = 300
			}

			deadline := time.After(time.Duration(params.TimeoutSeconds) * time.Second)
			tick := time.NewTicker(500 * time.Millisecond)
			defer tick.Stop()

			agentIDs := params.AgentIDs
			if len(agentIDs) == 0 {
				agentIDs = registry.ActiveIDs()
				if len(agentIDs) == 0 {
					return map[string]any{"message": "no active agents"}, nil
				}
			}

			for {
				select {
				case <-deadline:
					return map[string]any{
						"timed_out": true,
						"remaining": registry.ActiveIDs(),
					}, nil
				case <-tick.C:
					allDone := true
					for _, id := range agentIDs {
						r, ok := registry.Get(id)
						if !ok || (r.Status != "completed" && r.Status != "failed" && r.Status != "interrupted") {
							allDone = false
							break
						}
					}
					if allDone {
						return map[string]any{"all_completed": true, "waited_for": len(agentIDs)}, nil
					}
				case <-ctx.Done():
					return nil, fmt.Errorf("cancelled")
				}
			}
		},
	}
}

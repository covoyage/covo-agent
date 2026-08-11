package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildInterruptAgentTool(registry *SubagentRegistry) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "interrupt_agent",
		Description: strings.Join([]string{
			"Interrupt a running sub-agent by cancelling its context.",
			"Use this to stop a sub-agent that is taking too long, going off-track,",
			"or when the parent has obtained the information it needs.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Sub-agent ID to interrupt (from dashboard or spawn result).",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Reason for interruption (logged).",
				},
			},
			"required": []string{"agent_id"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				AgentID string `json:"agent_id"`
				Reason  string `json:"reason"`
			}
			json.Unmarshal(args, &params)

			interrupted := registry.Interrupt(params.AgentID)
			if !interrupted {
				return nil, fmt.Errorf("agent %s not found or not running", params.AgentID)
			}

			return map[string]any{
				"interrupted": true,
				"agent_id":    params.AgentID,
				"reason":      params.Reason,
			}, nil
		},
	}
}

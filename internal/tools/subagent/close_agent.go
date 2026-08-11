package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildCloseAgentTool(registry *SubagentRegistry) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "close_agent",
		Description: strings.Join([]string{
			"Explicitly close a completed or interrupted sub-agent.",
			"This removes it from the active tracking in the dashboard.",
			"Only works on agents that are no longer running.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Sub-agent ID to close.",
				},
			},
			"required": []string{"agent_id"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				AgentID string `json:"agent_id"`
			}
			json.Unmarshal(args, &params)

			closed := registry.Close(params.AgentID)
			return map[string]any{
				"closed":   closed,
				"agent_id": params.AgentID,
			}, nil
		},
	}
}

func BuildSendInputTool(registry *SubagentRegistry) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "send_input",
		Description: strings.Join([]string{
			"Send a message to a running sub-agent while it's executing.",
			"The message is delivered as additional context for the sub-agent",
			"to consider in its current or next turn.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Sub-agent ID to send input to.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Message to send to the sub-agent.",
				},
			},
			"required": []string{"agent_id", "message"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				AgentID string `json:"agent_id"`
				Message string `json:"message"`
			}
			json.Unmarshal(args, &params)

			delivered := registry.SendInput(params.AgentID, params.Message)
			if !delivered {
				return nil, fmt.Errorf("agent %s not running", params.AgentID)
			}

			return &agentcore.DualToolOutput{
				ForLLM:  fmt.Sprintf("Sent message to sub-agent %s: %s", params.AgentID, params.Message),
				ForUser: fmt.Sprintf("📨 Sent to %s", params.AgentID),
				Silent:  true,
			}, nil
		},
	}
}

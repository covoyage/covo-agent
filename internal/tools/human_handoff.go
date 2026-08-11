package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// HandoffCallback is a function that presents a message to the user and
// blocks until the user responds. Returns the user's response text.
// This is called synchronously from the tool's Func, so the agent will
// block until the callback returns.
type HandoffCallback func(ctx context.Context, message string) (string, error)

// HandoffResult is the structured result returned by the human_handoff tool.
type HandoffResult struct {
	Response string `json:"response"`
}

func buildHumanHandoffTool(callback HandoffCallback) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "human_handoff",
		Description: strings.Join([]string{
			"Pause and wait for the user to provide input or instructions.",
			"Use this when you need the user to make a decision, provide",
			"information you don't have, or review something before continuing.",
			"",
			"The agent blocks until the user responds. Use 'clarify' instead",
			"when you only want to ask a question without blocking.",
			"",
			"The user's full response is returned as the tool result.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to show the user. Explain what you need from them clearly.",
				},
			},
			"required": []string{"message"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Message) == "" {
				return nil, fmt.Errorf("message is required")
			}

			if callback == nil {
				return nil, fmt.Errorf("human_handoff: no callback configured (UI layer must provide one)")
			}

			response, err := callback(ctx, params.Message)
			if err != nil {
				return nil, fmt.Errorf("human_handoff failed: %w", err)
			}

			return HandoffResult{Response: response}, nil
		},
	}
}

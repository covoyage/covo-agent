package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func buildReactionTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "reaction",
		Description: strings.Join([]string{
			"Show an emoji reaction in this conversation UI.",
			"This does not post to Slack, Discord, or any other gateway.",
			"Use it as lightweight local feedback: 👍 done, 👀 reviewing, ⏳ in progress.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"emoji": map[string]any{
					"type":        "string",
					"description": "Emoji to react with (e.g. '👍', '✅', '🚀').",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Optional context message for the reaction.",
				},
			},
			"required": []string{"emoji"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Emoji   string `json:"emoji"`
				Message string `json:"message"`
			}
			json.Unmarshal(args, &params)

			emoji := strings.TrimSpace(params.Emoji)
			if emoji == "" {
				return nil, fmt.Errorf("emoji is required")
			}

			var llm, user string
			if params.Message != "" {
				llm = fmt.Sprintf("Reacted %s with message: %s", emoji, params.Message)
				user = emoji
			} else {
				llm = fmt.Sprintf("Reacted %s", emoji)
				user = emoji
			}

			return &agentcore.DualToolOutput{
				ForLLM:  llm,
				ForUser: user,
			}, nil
		},
	}
}

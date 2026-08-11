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
			"React to a message with an emoji on the connected gateway channel.",
			"Use this to acknowledge messages, indicate status, or provide visual feedback.",
			"Common reactions: 👍 (agree/done), 👀 (reviewing), 🚀 (deploying),",
			"✅ (verified), ❌ (failed), ⏳ (in progress), 🎉 (celebrate).",
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

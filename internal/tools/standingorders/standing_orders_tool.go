package standingorders

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// BuildStandingOrdersTool creates the standing_orders tool.
func BuildStandingOrdersTool(store *StandingOrdersStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "standing_orders",
		Description: "Manage persistent behavioral instructions that are injected into every session. " +
			"Use 'add' to create a new standing order, 'remove' to delete one by ID, 'list' to view all, " +
			"and 'clear' to remove all standing orders. " +
			"Standing orders are useful for setting preferred behaviors that should apply across all conversations. " +
			"For example: \"Always use tabs for indentation\" or \"Prefer Python 3 over Python 2.\"",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "The action to perform: 'add', 'remove', 'list', or 'clear'.",
					"enum":        []string{"add", "remove", "list", "clear"},
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The standing order content (required for 'add', ignored otherwise).",
				},
				"id": map[string]any{
					"type":        "string",
					"description": "The standing order ID (required for 'remove', ignored otherwise).",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action  string `json:"action"`
				Content string `json:"content,omitempty"`
				ID      string `json:"id,omitempty"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch strings.ToLower(params.Action) {
			case "add":
				if strings.TrimSpace(params.Content) == "" {
					return nil, fmt.Errorf("content is required for 'add' action")
				}
				order, err := store.Add(params.Content)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"success": true,
					"id":      order.ID,
					"message": fmt.Sprintf("Added standing order %q", order.Content),
				}, nil

			case "remove":
				if params.ID == "" {
					return nil, fmt.Errorf("id is required for 'remove' action")
				}
				if err := store.Remove(params.ID); err != nil {
					return nil, err
				}
				return map[string]any{
					"success": true,
					"message": fmt.Sprintf("Removed standing order %s", params.ID),
				}, nil

			case "list":
				orders := store.ToToolItems()
				if len(orders) == 0 {
					return map[string]any{
						"success": true,
						"orders":  []StandingOrdersToolItem{},
						"message": "No standing orders.",
					}, nil
				}
				return map[string]any{
					"success": true,
					"orders":  orders,
					"count":   len(orders),
				}, nil

			case "clear":
				if err := store.Clear(); err != nil {
					return nil, err
				}
				return map[string]any{
					"success": true,
					"message": "Cleared all standing orders.",
				}, nil

			default:
				return nil, fmt.Errorf("unknown action %q (valid: add, remove, list, clear)", params.Action)
			}
		},
	}
}

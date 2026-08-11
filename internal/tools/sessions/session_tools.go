package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// YieldFunc is a callback triggered by sessions_yield to pause the current turn.
type YieldFunc func()

func BuildSessionsYieldTool(yieldFn YieldFunc) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "sessions_yield",
		Description: "End the current turn after spawning subagents. The agent will wait for subagent results to arrive before the next turn. Call this when you've started parallel subagents and want to pause instead of advancing further.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if yieldFn != nil {
				yieldFn()
			}
			return map[string]any{
				"status": "yielded",
				"note":   "Turn ended. Results from subagents will arrive in the next turn.",
			}, nil
		},
	}
}

// SessionHistoryProvider provides access to session history.
type SessionHistoryProvider func(ctx context.Context, limit int, includeTools bool) ([]map[string]any, error)

func BuildSessionsHistoryTool(provider SessionHistoryProvider) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sessions_history",
		Description: strings.Join([]string{
			"Read bounded, redacted conversation history from past sessions.",
			"Use this to review what was discussed, decisions made, or code changed in prior sessions.",
			"Results are truncated at 4000 characters per message.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of messages to return (default: 20).",
				},
				"include_tools": map[string]any{
					"type":        "boolean",
					"description": "Include tool call messages (default: false).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Limit        int  `json:"limit"`
				IncludeTools bool `json:"include_tools"`
			}
			json.Unmarshal(args, &params)
			if params.Limit <= 0 {
				params.Limit = 20
			}
			if provider != nil {
				msgs, err := provider(ctx, params.Limit, params.IncludeTools)
				if err != nil {
					return nil, fmt.Errorf("history: %w", err)
				}
				return map[string]any{
					"messages": msgs,
					"count":    len(msgs),
				}, nil
			}
			return map[string]any{"messages": []any{}, "note": "no session store configured"}, nil
		},
	}
}

func BuildSessionsSendTool(sendFn func(ctx context.Context, target, msg string) (string, error)) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "sessions_send",
		Description: strings.Join([]string{
			"Send a message to another session or notification channel.",
			"Used by subagents to report completion, or to coordinate between parallel agents.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{
					"type":        "string",
					"description": "Target session ID or routing key.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "The message to send.",
				},
			},
			"required": []string{"message"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Target  string `json:"target"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Message) == "" {
				return nil, fmt.Errorf("message is required")
			}
			if sendFn != nil {
				status, err := sendFn(ctx, params.Target, params.Message)
				if err != nil {
					return nil, fmt.Errorf("send: %w", err)
				}
				return map[string]any{"status": "sent", "detail": status}, nil
			}
			return map[string]any{"status": "acknowledged", "note": "message recorded"}, nil
		},
	}
}

// SessionLister lists sessions with metadata.
type SessionLister func() []map[string]any

func BuildSessionsListTool(lister SessionLister) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "sessions_list",
		Description: "List recent sessions with title, start time, and preview. Use this to find past conversations to resume or reference.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum sessions to return (default: 20).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Limit int `json:"limit"`
			}
			json.Unmarshal(args, &params)
			if params.Limit <= 0 {
				params.Limit = 20
			}
			if lister != nil {
				sessions := lister()
				if params.Limit < len(sessions) {
					sessions = sessions[:params.Limit]
				}
				return map[string]any{
					"sessions": sessions,
					"count":    len(sessions),
				}, nil
			}
			return map[string]any{"sessions": []any{}, "note": "no session store configured"}, nil
		},
	}
}

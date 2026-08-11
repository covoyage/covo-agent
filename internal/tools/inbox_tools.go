package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/inbox"
	"github.com/covoyage/covonaut/agentcore"
)

// buildInboxSendTool builds the inbox_send tool — allows any agent (typically
// a sub-agent) to asynchronously notify a recipient session by persistent
// queue. Survives parent process restart: message is delivered on next drain.
func buildInboxSendTool(store *inbox.Store, sessionIDFn func() string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "inbox_send",
		Description: strings.Join([]string{
			"Send an asynchronous message to another session's persistent inbox.",
			"",
			"Use this to notify a parent session when a background sub-agent completes,",
			"or to deliver results without requiring the recipient to be actively waiting.",
			"Messages persist across process restarts — they are delivered when the",
			"recipient next calls inbox_check or its inbox is auto-drained.",
			"",
			"Typical flow: a sub-agent spawned via sessions_spawn runs to completion,",
			"then calls inbox_send with to_session_id = parent's session id to deliver",
			"its result. The parent picks it up on its next turn.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to_session_id": map[string]any{
					"type":        "string",
					"description": "Recipient session ID. The message will be queued for this session.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Message body to deliver. For sub-agent completion, include a concise summary of what was accomplished and any key outputs.",
				},
			},
			"required": []string{"to_session_id", "message"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if store == nil {
				return nil, fmt.Errorf("inbox store is unavailable (initialization failed)")
			}
			var params struct {
				ToSessionID string `json:"to_session_id"`
				Message     string `json:"message"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.ToSessionID == "" {
				return nil, fmt.Errorf("to_session_id is required")
			}
			if params.Message == "" {
				return nil, fmt.Errorf("message is required")
			}
			fromSession := sessionIDFn()
			id, err := store.Send(params.ToSessionID, fromSession, params.Message)
			if err != nil {
				return nil, fmt.Errorf("inbox_send: %w", err)
			}
			return map[string]any{
				"status":          "sent",
				"message_id":      id,
				"to_session_id":   params.ToSessionID,
				"from_session_id": fromSession,
			}, nil
		},
	}
}

// buildInboxCheckTool builds the inbox_check tool — drains and returns pending
// messages for the current session.
func buildInboxCheckTool(store *inbox.Store, sessionIDFn func() string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "inbox_check",
		Description: strings.Join([]string{
			"Check and drain the current session's persistent inbox.",
			"",
			"Returns all pending messages addressed to this session and marks them delivered.",
			"Use this to pick up asynchronous notifications from sub-agents or other sessions.",
			"Returns an empty list if no messages are pending.",
			"",
			"Messages survive process restarts, so it is safe to call this at any time,",
			"including after a crash recovery.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"peek_only": map[string]any{
					"type":        "boolean",
					"description": "If true, return pending messages without marking them drained (default: false).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if store == nil {
				return nil, fmt.Errorf("inbox store is unavailable (initialization failed)")
			}
			var params struct {
				PeekOnly bool `json:"peek_only"`
			}
			_ = json.Unmarshal(args, &params)
			sessionID := sessionIDFn()
			if sessionID == "" {
				return nil, fmt.Errorf("inbox_check: no active session")
			}
			var msgs []inbox.Message
			var err error
			if params.PeekOnly {
				msgs, err = store.Peek(sessionID)
			} else {
				msgs, err = store.Drain(sessionID)
			}
			if err != nil {
				return nil, fmt.Errorf("inbox_check: %w", err)
			}
			out := make([]map[string]any, len(msgs))
			for i, m := range msgs {
				out[i] = map[string]any{
					"id":                m.ID,
					"sender_session_id": m.SenderSession,
					"message":           m.Message,
					"created_at":        m.CreatedAt,
				}
			}
			return map[string]any{
				"session_id": sessionID,
				"messages":   out,
				"count":      len(out),
				"drained":    !params.PeekOnly,
			}, nil
		},
	}
}

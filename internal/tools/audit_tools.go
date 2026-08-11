package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/audit"
	"github.com/covoyage/covonaut/agentcore"
)

// buildAuditQueryTool builds the audit_query tool — queries the persistent
// audit log of tool calls and lifecycle events. Useful for reviewing what
// happened in this or previous sessions, debugging agent behavior, and
// compliance/forensics.
func buildAuditQueryTool(store *audit.Store, sessionIDFn func() string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "audit_query",
		Description: strings.Join([]string{
			"Query the persistent audit log of agent tool calls and lifecycle events.",
			"",
			"The audit log records every tool:start / tool:end event with session id,",
			"tool name, agent id, and event data. Use this to review past activity,",
			"debug unexpected behavior, or verify what operations were performed.",
			"",
			"Filters are all optional. With no filters, returns the most recent entries",
			"for the current session (or all sessions if session_id is empty).",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"event_type": map[string]any{
					"type":        "string",
					"description": "Filter by event type (e.g. 'tool:start', 'tool:end', 'session:start'). Empty = all types.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session id. Empty = all sessions. If omitted, defaults to the current session.",
				},
				"tool_name": map[string]any{
					"type":        "string",
					"description": "Filter by tool name (e.g. 'edit_block', 'bash'). Empty = all tools.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of entries to return (default: 50, max: 1000).",
				},
				"since_minutes": map[string]any{
					"type":        "integer",
					"description": "Only return entries from the last N minutes. Empty = no time filter.",
				},
				"count_only": map[string]any{
					"type":        "boolean",
					"description": "If true, return only the count of matching entries (default: false).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if store == nil {
				return nil, fmt.Errorf("audit store is unavailable (initialization failed)")
			}
			var params struct {
				EventType    string `json:"event_type"`
				SessionID    string `json:"session_id"`
				ToolName     string `json:"tool_name"`
				Limit        int    `json:"limit"`
				SinceMinutes int    `json:"since_minutes"`
				CountOnly    bool   `json:"count_only"`
			}
			_ = json.Unmarshal(args, &params)

			// Default to current session if session_id is empty and no
			// explicit empty-string was requested.
			if params.SessionID == "" && !hasKey(args, "session_id") {
				params.SessionID = sessionIDFn()
			}

			f := audit.QueryFilter{
				EventType: params.EventType,
				SessionID: params.SessionID,
				ToolName:  params.ToolName,
				Limit:     params.Limit,
			}
			if params.SinceMinutes > 0 {
				f.Since = time.Now().Add(-time.Duration(params.SinceMinutes) * time.Minute)
			}

			if params.CountOnly {
				n, err := store.Count(f)
				if err != nil {
					return nil, fmt.Errorf("audit_query count: %w", err)
				}
				return map[string]any{
					"count": n,
					"filter": map[string]any{
						"event_type": params.EventType,
						"session_id": params.SessionID,
						"tool_name":  params.ToolName,
					},
				}, nil
			}

			entries, err := store.Query(f)
			if err != nil {
				return nil, fmt.Errorf("audit_query: %w", err)
			}
			out := make([]map[string]any, len(entries))
			for i, e := range entries {
				entry := map[string]any{
					"id":         e.ID,
					"event_type": e.EventType,
					"session_id": e.SessionID,
					"tool_name":  e.ToolName,
					"agent_id":   e.AgentID,
					"created_at": e.CreatedAt,
				}
				if e.Data != "" {
					entry["data"] = e.Data
				}
				out[i] = entry
			}
			return map[string]any{
				"entries": out,
				"count":   len(out),
				"filter": map[string]any{
					"event_type": params.EventType,
					"session_id": params.SessionID,
					"tool_name":  params.ToolName,
				},
			}, nil
		},
	}
}

// hasKey returns true if the JSON object contains the given key.
func hasKey(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

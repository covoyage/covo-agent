package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/session"
)

const (
	msgContentMaxLen = 2000
	defaultLimit     = 50
	maxLimit         = 200
	minLimit         = 1
)

// SessionToolProvider registers session/messaging MCP tools on a Server.
type SessionToolProvider struct {
	store   *session.FileStore
	gateway *gatewayClient
}

// NewSessionToolProvider creates a provider backed by the given sessions directory.
func NewSessionToolProvider(sessionsDir string) (*SessionToolProvider, error) {
	store, err := session.NewFileStore(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("init session store: %w", err)
	}
	return &SessionToolProvider{store: store, gateway: newGatewayClient("")}, nil
}

// SetGatewaySocket overrides the gateway IPC socket path (defaults to
// ~/.covo-agent/gateway.sock).
func (p *SessionToolProvider) SetGatewaySocket(path string) {
	p.gateway = newGatewayClient(path)
}

// RegisterTools installs all 10 session/messaging tools onto the server.
func (p *SessionToolProvider) RegisterTools(s *Server) {
	// 1. conversations_list
	s.RegisterTool(mcpToolSchema{
		Name:        "conversations_list",
		Description: "List all conversations (sessions) with optional platform, search, and limit filters.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"platform": map[string]any{"type": "string", "description": "Optional platform filter"},
				"limit":    map[string]any{"type": "integer", "description": "Maximum number of results (default 50, max 200)", "default": defaultLimit},
				"search":   map[string]any{"type": "string", "description": "Optional search term to filter session names"},
			},
		},
	}, p.conversationsList)

	// 2. conversation_get
	s.RegisterTool(mcpToolSchema{
		Name:        "conversation_get",
		Description: "Get detailed information about a single conversation.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session_key"},
			"properties": map[string]any{
				"session_key": map[string]any{"type": "string", "description": "Session ID (timestamp_counter format)"},
			},
		},
	}, p.conversationGet)

	// 3. messages_read
	s.RegisterTool(mcpToolSchema{
		Name:        "messages_read",
		Description: "Read recent messages from a conversation (user/assistant only).",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session_key"},
			"properties": map[string]any{
				"session_key": map[string]any{"type": "string", "description": "Session ID"},
				"limit":       map[string]any{"type": "integer", "description": "Maximum number of messages (default 50, max 200)", "default": defaultLimit},
			},
		},
	}, p.messagesRead)

	// 4. attachments_fetch
	s.RegisterTool(mcpToolSchema{
		Name:        "attachments_fetch",
		Description: "Fetch attachments (media, images, tool calls) for a specific message.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session_key", "message_id"},
			"properties": map[string]any{
				"session_key": map[string]any{"type": "string", "description": "Session ID"},
				"message_id":  map[string]any{"type": "string", "description": "Message ID within the session"},
			},
		},
	}, p.attachmentsFetch)

	// 5. messages_send
	s.RegisterTool(mcpToolSchema{
		Name:        "messages_send",
		Description: "Send a message to a platform conversation through the running covo-agent gateway.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"target", "message"},
			"properties": map[string]any{
				"target":  map[string]any{"type": "string", "description": "Target in \"platform:chat_id\" format"},
				"message": map[string]any{"type": "string", "description": "Message text to send"},
			},
		},
	}, p.messagesSend)

	// 6. channels_list
	s.RegisterTool(mcpToolSchema{
		Name:        "channels_list",
		Description: "List known channels extracted from session contexts. Limited in pure MCP serve mode.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"platform": map[string]any{"type": "string", "description": "Optional platform filter"},
			},
		},
	}, p.channelsList)

	// 7. events_poll
	s.RegisterTool(mcpToolSchema{
		Name:        "events_poll",
		Description: "Poll for new gateway events (messages, replies, pairing, permission requests).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"after_cursor": map[string]any{"type": "integer", "description": "Return events after this cursor"},
				"session_key":  map[string]any{"type": "string", "description": "Optional session filter"},
				"limit":        map[string]any{"type": "integer", "description": "Maximum number of events", "default": defaultLimit},
			},
		},
	}, p.eventsPoll)

	// 8. events_wait
	s.RegisterTool(mcpToolSchema{
		Name:        "events_wait",
		Description: "Block waiting for new gateway events (long poll).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"after_cursor": map[string]any{"type": "integer", "description": "Return events after this cursor"},
				"session_key":  map[string]any{"type": "string", "description": "Optional session filter"},
				"timeout_ms":   map[string]any{"type": "integer", "description": "Timeout in milliseconds", "default": 30000},
			},
		},
	}, p.eventsWait)

	// 9. permissions_list_open
	s.RegisterTool(mcpToolSchema{
		Name:        "permissions_list_open",
		Description: "List pending gateway permission requests awaiting a decision.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}, p.permissionsListOpen)

	// 10. permissions_respond
	s.RegisterTool(mcpToolSchema{
		Name:        "permissions_respond",
		Description: "Respond to a pending gateway permission request.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"id", "decision"},
			"properties": map[string]any{
				"id":       map[string]any{"type": "string", "description": "Permission request ID"},
				"decision": map[string]any{"type": "string", "description": "Decision: allow-once, allow-always, or deny"},
			},
		},
	}, p.permissionsRespond)
}

// conversationsList implements the conversations_list tool.
func (p *SessionToolProvider) conversationsList(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		Platform *string `json:"platform"`
		Limit    *int    `json:"limit"`
		Search   *string `json:"search"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	limit := defaultLimit
	if params.Limit != nil {
		limit = *params.Limit
		if limit < minLimit {
			limit = minLimit
		}
		if limit > maxLimit {
			limit = maxLimit
		}
	}

	searchTerm := ""
	if params.Search != nil {
		searchTerm = strings.ToLower(*params.Search)
	}

	list, err := p.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	// Sort by UpdatedAt descending
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})

	type convResult struct {
		SessionKey   string `json:"session_key"`
		DisplayName  string `json:"display_name"`
		Platform     string `json:"platform"`
		MessageCount int64  `json:"message_count"`
		UpdatedAt    string `json:"updated_at"`
		CreatedAt    string `json:"created_at"`
	}

	platformFilter := ""
	if params.Platform != nil {
		platformFilter = strings.ToLower(*params.Platform)
	}

	results := make([]convResult, 0, limit)
	for _, info := range list {
		if searchTerm != "" {
			name := strings.ToLower(info.Name)
			label := strings.ToLower(info.Label)
			if !strings.Contains(name, searchTerm) && !strings.Contains(label, searchTerm) {
				continue
			}
		}

		// Open session to extract platform from message metadata.
		mgr, err := p.store.Open(ctx, info.ID)
		if err != nil {
			continue
		}
		platform := extractPlatform(mgr)

		if platformFilter != "" && strings.ToLower(platform) != platformFilter {
			continue
		}

		displayName := info.Name
		if info.Label != "" {
			displayName = info.Label
		}

		results = append(results, convResult{
			SessionKey:   info.ID,
			DisplayName:  displayName,
			Platform:     platform,
			MessageCount: info.MessageCount,
			UpdatedAt:    info.UpdatedAt.Format(time.RFC3339),
			CreatedAt:    info.CreatedAt.Format(time.RFC3339),
		})

		if len(results) >= limit {
			break
		}
	}

	return results, nil
}

// conversationGet implements the conversation_get tool.
func (p *SessionToolProvider) conversationGet(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		SessionKey string `json:"session_key"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.SessionKey == "" {
		return nil, fmt.Errorf("session_key is required")
	}

	has, err := p.store.Has(ctx, params.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("check session: %w", err)
	}
	if !has {
		return nil, fmt.Errorf("session not found: %s", params.SessionKey)
	}

	mgr, err := p.store.Open(ctx, params.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}

	info := mgr.Info()

	// Extract channel info from the session context
	channels := extractChannels(mgr)

	return map[string]any{
		"session_key":    info.ID,
		"display_name":   resolveDisplayName(info),
		"platform":       extractPlatform(mgr),
		"message_count":  info.MessageCount,
		"channels":       channels,
		"created_at":     info.CreatedAt.Format(time.RFC3339),
		"updated_at":     info.UpdatedAt.Format(time.RFC3339),
		"cwd":            info.Cwd,
		"parent_session": info.ParentSession,
	}, nil
}

// messagesRead implements the messages_read tool.
func (p *SessionToolProvider) messagesRead(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		SessionKey string `json:"session_key"`
		Limit      *int   `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.SessionKey == "" {
		return nil, fmt.Errorf("session_key is required")
	}

	limit := defaultLimit
	if params.Limit != nil {
		limit = *params.Limit
		if limit < minLimit {
			limit = minLimit
		}
		if limit > maxLimit {
			limit = maxLimit
		}
	}

	mgr, err := p.store.Open(ctx, params.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}

	messages := mgr.MessagesOnPath()

	type msgResult struct {
		ID        string `json:"id"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp,omitempty"`
		Name      string `json:"name,omitempty"`
	}

	results := make([]msgResult, 0, limit)
	for i := len(messages) - 1; i >= 0 && len(results) < limit; i-- {
		msg := messages[i]
		// Filter to user/assistant only
		if msg.Role != agentcore.RoleUser && msg.Role != agentcore.RoleAssistant {
			continue
		}
		// Skip compaction and branch summary messages
		if msg.Type == "compaction_summary" || msg.Type == "branch_summary" {
			continue
		}
		content := truncateContent(flattenContent(msg), msgContentMaxLen)
		results = append(results, msgResult{
			ID:        msg.ID,
			Role:      string(msg.Role),
			Content:   content,
			Timestamp: extractTimestamp(msg),
			Name:      msg.Name,
		})
	}

	return results, nil
}

// attachmentsFetch implements the attachments_fetch tool.
func (p *SessionToolProvider) attachmentsFetch(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		SessionKey string `json:"session_key"`
		MessageID  string `json:"message_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.SessionKey == "" {
		return nil, fmt.Errorf("session_key is required")
	}
	if params.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}

	mgr, err := p.store.Open(ctx, params.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}

	messages := mgr.MessagesOnPath()
	var targetMsg *agentcore.Message
	for i := range messages {
		if messages[i].ID == params.MessageID {
			targetMsg = &messages[i]
			break
		}
	}
	if targetMsg == nil {
		return nil, fmt.Errorf("message not found: %s", params.MessageID)
	}

	type attachment struct {
		Type       string `json:"type"`
		MediaType  string `json:"media_type,omitempty"`
		Name       string `json:"name,omitempty"`
		URL        string `json:"url,omitempty"`
		ToolCallID string `json:"tool_call_id,omitempty"`
		Arguments  string `json:"arguments,omitempty"`
		Text       string `json:"text,omitempty"`
		Signature  string `json:"signature,omitempty"`
	}

	var attachments []attachment
	for _, bl := range targetMsg.Blocks {
		switch bl.Kind {
		case agentcore.BlockKindImage:
			attachments = append(attachments, attachment{
				Type:      "MEDIA",
				MediaType: bl.MediaType,
				URL:       bl.URL,
			})
		case agentcore.BlockKindToolCall:
			attachments = append(attachments, attachment{
				Type:       "TOOL_CALL",
				Name:       bl.Name,
				ToolCallID: bl.ToolCallID,
				Arguments:  bl.Arguments,
			})
		case agentcore.BlockKindThinking:
			attachments = append(attachments, attachment{
				Type:      "THINKING",
				Text:      truncateContent(bl.Text, msgContentMaxLen),
				Signature: bl.Signature,
			})
		}
	}

	// Also look at tool calls on the message itself
	for _, tc := range targetMsg.ToolCalls {
		attachments = append(attachments, attachment{
			Type:       "TOOL_CALL",
			Name:       tc.Name,
			ToolCallID: tc.ID,
			Arguments:  tc.Arguments,
		})
	}

	return map[string]any{
		"message_id":  params.MessageID,
		"session_key": params.SessionKey,
		"attachments": attachments,
	}, nil
}

// messagesSend sends an outbound message through the running gateway's IPC
// API. When no gateway is listening the tool reports an actionable offline
// status instead of pretending success.
func (p *SessionToolProvider) messagesSend(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		Target  string `json:"target"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parse arguments: %w", err)
	}
	platform, channelID, err := parseSendTarget(params.Target)
	if err != nil {
		return nil, err
	}
	return p.gateway.Send(ctx, platform, channelID, params.Message)
}

// parseSendTarget splits a "platform:chat_id" target. With a single
// configured gateway platform the platform part may be omitted.
func parseSendTarget(target string) (platform, channelID string, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("target must be \"platform:chat_id\" (or \"chat_id\" when a single platform is configured)")
	}
	if i := strings.Index(target, ":"); i > 0 {
		return target[:i], target[i+1:], nil
	}
	return "", target, nil
}

// eventsPoll returns gateway events after the given cursor (no waiting).
func (p *SessionToolProvider) eventsPoll(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		AfterCursor uint64 `json:"after_cursor"`
		SessionKey  string `json:"session_key"`
		Limit       int    `json:"limit"`
	}
	_ = json.Unmarshal(args, &params)
	result, err := p.gateway.Events(ctx, params.AfterCursor, 0)
	if err != nil {
		return nil, err
	}
	return applyEventFilter(result, params.SessionKey, params.Limit), nil
}

// eventsWait long-polls for gateway events, blocking up to timeout_ms.
func (p *SessionToolProvider) eventsWait(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		AfterCursor uint64 `json:"after_cursor"`
		SessionKey  string `json:"session_key"`
		TimeoutMS   int    `json:"timeout_ms"`
	}
	_ = json.Unmarshal(args, &params)
	if params.TimeoutMS <= 0 {
		params.TimeoutMS = 30_000
	}
	result, err := p.gateway.Events(ctx, params.AfterCursor, params.TimeoutMS/1000)
	if err != nil {
		return nil, err
	}
	return applyEventFilter(result, params.SessionKey, 0), nil
}

// applyEventFilter narrows events to an optional session key and applies a
// limit, keeping the newest events when truncating.
func applyEventFilter(result map[string]any, sessionKey string, limit int) map[string]any {
	events, _ := result["events"].([]any)
	if sessionKey == "" && limit <= 0 {
		return result
	}
	var filtered []any
	for _, e := range events {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if sessionKey != "" {
			sk, _ := m["session_key"].(string)
			if sk != "" && sk != sessionKey {
				continue
			}
		}
		filtered = append(filtered, m)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	if filtered == nil {
		filtered = []any{}
	}
	out := make(map[string]any, len(result))
	for k, v := range result {
		out[k] = v
	}
	out["events"] = filtered
	return out
}

// permissionsListOpen lists pending gateway permission requests.
func (p *SessionToolProvider) permissionsListOpen(ctx context.Context, _ json.RawMessage) (any, error) {
	return p.gateway.PermissionsOpen(ctx)
}

// permissionsRespond delivers a decision for a pending permission request.
// decision: allow-once | allow-always | deny (once/session/always/deny are
// accepted as aliases).
func (p *SessionToolProvider) permissionsRespond(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("parse arguments: %w", err)
	}
	choice, err := normalizePermissionDecision(params.Decision)
	if err != nil {
		return nil, err
	}
	return p.gateway.PermissionRespond(ctx, params.ID, choice)
}

func normalizePermissionDecision(decision string) (string, error) {
	switch strings.TrimSpace(decision) {
	case "allow-once", "once":
		return "once", nil
	case "session":
		return "session", nil
	case "allow-always", "always":
		return "always", nil
	case "deny":
		return "deny", nil
	default:
		return "", fmt.Errorf("invalid decision %q: use allow-once, session, allow-always, or deny", decision)
	}
}

// channelsList implements the channels_list tool.
func (p *SessionToolProvider) channelsList(ctx context.Context, args json.RawMessage) (any, error) {
	var params struct {
		Platform *string `json:"platform"`
	}
	_ = json.Unmarshal(args, &params) // optional params, ignore errors

	list, err := p.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	type channelResult struct {
		Platform     string `json:"platform"`
		ChannelID    string `json:"channel_id"`
		SessionKey   string `json:"session_key"`
		DisplayName  string `json:"display_name"`
		MessageCount int64  `json:"message_count"`
		UpdatedAt    string `json:"updated_at"`
	}

	seen := make(map[string]bool)
	var results []channelResult

	for _, info := range list {
		mgr, err := p.store.Open(ctx, info.ID)
		if err != nil {
			continue
		}
		chs := extractChannels(mgr)
		for _, ch := range chs {
			key := ch.Platform + ":" + ch.ChatID
			if seen[key] {
				continue
			}
			seen[key] = true

			if params.Platform != nil && *params.Platform != "" && ch.Platform != *params.Platform {
				continue
			}

			results = append(results, channelResult{
				Platform:     ch.Platform,
				ChannelID:    ch.ChatID,
				SessionKey:   info.ID,
				DisplayName:  resolveDisplayName(info),
				MessageCount: info.MessageCount,
				UpdatedAt:    info.UpdatedAt.Format(time.RFC3339),
			})
		}
	}

	if results == nil {
		results = []channelResult{}
	}

	return results, nil
}

// ---- helpers ----

type channelInfo struct {
	Platform string `json:"platform"`
	ChatID   string `json:"chat_id"`
}

func extractChannels(mgr *session.Manager) []channelInfo {
	// In pure MCP serve mode, channel info is extracted from session messages.
	// Look for system messages or metadata messages that contain channel context.
	var channels []channelInfo
	seen := make(map[string]bool)
	messages := mgr.MessagesOnPath()
	for _, msg := range messages {
		if msg.Metadata == nil {
			continue
		}
		plat, _ := msg.Metadata["platform"].(string)
		chatID, _ := msg.Metadata["chat_id"].(string)
		if plat == "" && chatID == "" {
			continue
		}
		key := plat + ":" + chatID
		if seen[key] {
			continue
		}
		seen[key] = true
		channels = append(channels, channelInfo{Platform: plat, ChatID: chatID})
	}
	return channels
}

func extractPlatform(mgr *session.Manager) string {
	// Platform is stored in message metadata (set by gateway/plugin integrations).
	// Scan messages on the root→leaf path for the first non-empty platform.
	for _, msg := range mgr.MessagesOnPath() {
		if msg.Metadata == nil {
			continue
		}
		if plat, ok := msg.Metadata["platform"].(string); ok && plat != "" {
			return plat
		}
	}
	return ""
}

func resolveDisplayName(info session.Info) string {
	if info.Label != "" {
		return info.Label
	}
	if info.Name != "" {
		return info.Name
	}
	return info.ID
}

func flattenContent(msg agentcore.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	// Reconstruct from blocks
	var parts []string
	for _, bl := range msg.Blocks {
		if bl.Kind == agentcore.BlockKindText {
			parts = append(parts, bl.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func truncateContent(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... [truncated]"
}

func extractTimestamp(msg agentcore.Message) string {
	if msg.Metadata != nil {
		if ts, ok := msg.Metadata["timestamp"]; ok {
			if s, ok := ts.(string); ok {
				return s
			}
		}
	}
	return ""
}

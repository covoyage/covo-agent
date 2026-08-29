package shared

import (
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// toolResultCollapseWidth mirrors the live transcript: replayed tool results
// longer than this start collapsed (click or ctrl+r to expand).
const toolResultCollapseWidth = 160

func ChatMessageFromAgentMessage(msg agentcore.Message) chat.ChatMessage {
	role := chat.RoleSystem
	switch msg.Role {
	case agentcore.RoleUser:
		role = chat.RoleUser
	case agentcore.RoleAssistant:
		role = chat.RoleAssistant
	case agentcore.RoleTool:
		role = chat.RoleTool
	case agentcore.RoleSystem:
		role = chat.RoleSystem
	}
	text := msg.Content
	if text == "" && len(msg.ToolCalls) > 0 {
		text = i18n.T("statusline.tool_calls", "count", fmt.Sprintf("%d", len(msg.ToolCalls)))
	}
	return chat.ChatMessage{
		Role: role,
		Text: text,
	}
}

// restoreMessages converts a session snapshot into structured transcript
// messages. Tool results keep their identity: tool name, argument preview,
// and the full result body (collapsed when long), matching what the live
// transcript showed.
func restoreMessages(msgs []agentcore.Message) []chat.ChatMessage {
	toolCalls := map[string]agentcore.ToolCall{}
	for _, msg := range msgs {
		for _, call := range msg.ToolCalls {
			toolCalls[call.ID] = call
		}
	}

	out := make([]chat.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		switch msg.Role {
		case agentcore.RoleTool:
			cm := chat.ChatMessage{Role: chat.RoleTool, Text: msg.Content}
			cm.Meta = msg.Name
			if call, ok := toolCalls[msg.ToolCallID]; ok {
				cm.Meta = call.Name
				cm.ArgPreview = chat.ToolArgPreview(call.Arguments, 48)
			}
			if msg.Content != "" {
				cm.Collapsed = core.VisibleWidth(msg.Content) > toolResultCollapseWidth
			}
			out = append(out, cm)
		default:
			cm := ChatMessageFromAgentMessage(msg)
			if cm.Text == "" {
				continue
			}
			out = append(out, cm)
		}
	}
	return out
}

// RestoreChatHistory clears the app's chat history and re-appends messages
// from a session snapshot, skipping internal system messages (agent identity,
// rules, memory guidance, etc.) that are not meant for display.
func RestoreChatHistory(app *chat.ChatApp, msgs []agentcore.Message) {
	app.History().Clear()
	for _, msg := range restoreMessages(msgs) {
		app.History().Append(msg)
	}
}

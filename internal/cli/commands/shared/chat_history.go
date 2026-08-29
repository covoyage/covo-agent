package shared

import (
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	"github.com/covoyage/covo-agent/internal/i18n"
)

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

// RestoreChatHistory clears the app's chat history and re-appends messages
// from a session snapshot, skipping internal system messages (agent identity,
// rules, memory guidance, etc.) that are not meant for display.
func RestoreChatHistory(app *chat.ChatApp, msgs []agentcore.Message) {
	app.History().Clear()
	for _, msg := range msgs {
		if msg.Role == agentcore.RoleSystem {
			continue
		}
		app.History().Append(ChatMessageFromAgentMessage(msg))
	}
}

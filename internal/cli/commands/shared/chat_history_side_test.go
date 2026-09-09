package shared

import (
	"testing"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"
)

func TestChatMessageFromAgentMessageSideMeta(t *testing.T) {
	named := ChatMessageFromAgentMessage(agentcore.Message{
		Role:    agentcore.RoleUser,
		Content: "btw q",
		Name:    "side",
	})
	if named.Meta != "side" {
		t.Fatalf("name side: meta=%q", named.Meta)
	}
	flagged := ChatMessageFromAgentMessage(agentcore.Message{
		Role:     agentcore.RoleAssistant,
		Content:  "btw a",
		Metadata: map[string]any{"side": true},
	})
	if flagged.Meta != "side" {
		t.Fatalf("metadata side: meta=%q", flagged.Meta)
	}
	plain := ChatMessageFromAgentMessage(agentcore.Message{
		Role:    agentcore.RoleUser,
		Content: "hello",
	})
	if plain.Meta == "side" {
		t.Fatal("plain message should not be side")
	}
}

func TestRestoreChatHistoryKeepsSideMeta(t *testing.T) {
	app := chat.NewChatApp(chat.ChatAppConfig{})
	RestoreChatHistory(app, []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "main"},
		{Role: agentcore.RoleUser, Content: "side q", Name: "side"},
		{Role: agentcore.RoleAssistant, Content: "side a", Name: "side"},
	})
	msgs := app.History().Messages()
	if len(msgs) != 3 {
		t.Fatalf("len=%d", len(msgs))
	}
	if msgs[1].Meta != "side" || msgs[2].Meta != "side" {
		t.Fatalf("side meta lost: %+v", msgs)
	}
	if msgs[0].Meta == "side" {
		t.Fatal("main message marked side")
	}
}

package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
)

func TestHistorySearchIndexFind(t *testing.T) {
	idx := NewHistorySearchIndex()
	idx.Sync([]chat.ChatMessage{
		{Text: "alpha\nbeta"},
		{Text: "ALPHA again"},
	})
	matches := idx.Find("alpha")
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}
	if matches[0].MsgIndex != 0 || matches[0].LineIndex != 0 {
		t.Fatalf("first match = %+v", matches[0])
	}
	if matches[1].MsgIndex != 1 {
		t.Fatalf("second match = %+v", matches[1])
	}
	if idx.Find("   ") != nil {
		t.Fatal("blank query should return nil")
	}
}

func TestHistorySearchOverlayNextPrev(t *testing.T) {
	history := chat.NewChatHistory()
	history.Append(chat.ChatMessage{Role: chat.RoleUser, Text: "one alpha"})
	history.Append(chat.ChatMessage{Role: chat.RoleAssistant, Text: "two alpha"})
	overlay := NewHistorySearchOverlay(history, nil, nil)
	overlay.appendQuery("alpha")
	if overlay.MatchCount() != 2 {
		t.Fatalf("match count = %d, want 2", overlay.MatchCount())
	}
	first, ok := overlay.Current()
	if !ok || first.MsgIndex != 0 {
		t.Fatalf("current = %+v ok=%v", first, ok)
	}
	if !overlay.Next() {
		t.Fatal("next failed")
	}
	second, _ := overlay.Current()
	if second.MsgIndex != 1 {
		t.Fatalf("next current = %+v", second)
	}
	if !overlay.Prev() {
		t.Fatal("prev failed")
	}
	back, _ := overlay.Current()
	if back.MsgIndex != 0 {
		t.Fatalf("prev current = %+v", back)
	}
}

func TestHistorySearchOverlayKeys(t *testing.T) {
	closed := false
	overlay := NewHistorySearchOverlay(nil, nil, func() { closed = true })
	overlay.Update(core.KeyMsg{Data: "a"})
	overlay.Update(core.KeyMsg{Data: "\x1b"})
	if overlay.Query() != "a" {
		t.Fatalf("query = %q", overlay.Query())
	}
	if !closed {
		t.Fatal("escape should close overlay")
	}
}

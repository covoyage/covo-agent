package tui

import (
	"testing"
	"time"
)

func TestSuggestionsManagerActivatesHotkey(t *testing.T) {
	submitted := make(chan string, 1)
	manager := NewSuggestionsManager(func(text string) { submitted <- text })
	manager.mu.Lock()
	manager.active = suggestionTexts()
	want := manager.active[1].Text
	manager.mu.Unlock()

	if !manager.HandleHotkey("\x1b[50;5u") { // ctrl+2 (Kitty keyboard protocol)
		t.Fatal("ctrl+2 did not activate a suggestion")
	}
	select {
	case got := <-submitted:
		if got != want {
			t.Fatalf("submitted %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for suggestion submission")
	}
	if manager.HasActive() {
		t.Fatal("suggestions remained active after submission")
	}
}

func TestSuggestionsManagerRejectsInactiveHotkey(t *testing.T) {
	manager := NewSuggestionsManager(nil)
	if manager.HandleHotkey("\x1f") { // ctrl+/
		t.Fatal("unrelated hotkey was handled")
	}
	if manager.HandleHotkey("\x1b[50;5u") { // ctrl+2, but no active suggestions
		t.Fatal("inactive suggestion was handled")
	}
}

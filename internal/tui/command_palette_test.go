package tui

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestCommandPaletteItems(t *testing.T) {
	items := CommandPaletteItems([]core.Suggestion{
		{InsertText: "help", Label: "/help", Description: "show help"},
		{InsertText: "help", Label: "dup"},
	}, []PaletteAction{
		{ID: "search", Label: "Search", Description: "find"},
		{ID: "", Label: "skip"},
	})
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Value != "action:search" {
		t.Fatalf("first value = %q, want action:search", items[0].Value)
	}
	if items[1].Value != "slash:help" {
		t.Fatalf("second value = %q, want slash:help", items[1].Value)
	}
}

func TestDefaultPaletteActions(t *testing.T) {
	actions := DefaultPaletteActions()
	if len(actions) == 0 {
		t.Fatal("expected default palette actions")
	}
	seen := map[string]bool{}
	for _, action := range actions {
		if action.ID == "" {
			t.Fatal("empty action id")
		}
		if seen[action.ID] {
			t.Fatalf("duplicate action %s", action.ID)
		}
		seen[action.ID] = true
	}
	for _, id := range []string{"help", "dashboard", "search", "queue"} {
		if !seen[id] {
			t.Fatalf("missing action %s", id)
		}
	}
}

func TestNewCommandPaletteStartsSearching(t *testing.T) {
	picker := NewCommandPalette(CommandPaletteItems(nil, DefaultPaletteActions()), nil, nil)
	lines := picker.Render(80)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Search:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("palette should start in search mode, got %q", lines)
	}
}

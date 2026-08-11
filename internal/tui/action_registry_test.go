package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/terminal"
)

func TestActionRegistry_RegisterAndLookup(t *testing.T) {
	r := NewActionRegistry()
	r.Register(ActionDef{
		ID:          ActionQuit,
		Label:       "quit",
		Description: "Quit application",
		DefaultKeys: []terminal.KeyID{"ctrl+q"},
		Category:    CatEssentials,
		Context:     WhenAlways,
	})

	if !r.Matches("\x11", ActionQuit) { // 0x11 = Ctrl+Q
		t.Fatal("raw Ctrl+Q should match ActionQuit")
	}

	// Lookup should find it in any context
	id := r.Lookup("\x11", WhenAlways)
	if id != ActionQuit {
		t.Errorf("expected ActionQuit, got %s", id)
	}
}

func TestActionRegistry_ContextFiltering(t *testing.T) {
	r := NewActionRegistry()
	r.Register(ActionDef{
		ID:          ActionOpenSessions,
		Label:       "sessions",
		DefaultKeys: []terminal.KeyID{"ctrl+o"},
		Category:    CatPanels,
		Context:     WhenAgentScreen,
	})
	r.Register(ActionDef{
		ID:          ActionQuit,
		Label:       "quit",
		DefaultKeys: []terminal.KeyID{"ctrl+q"},
		Category:    CatEssentials,
		Context:     WhenAlways,
	})

	// ctrl+q should match in any context (WhenAlways)
	if id := r.Lookup("\x11", WhenPromptFocused); id != ActionQuit {
		t.Errorf("WhenAlways should match in any context, got %s", id)
	}

	// ctrl+o should only match in WhenAgentScreen
	if id := r.Lookup("\x0f", WhenAgentScreen); id != ActionOpenSessions {
		t.Errorf("expected ActionOpenSessions in WhenAgentScreen, got %s", id)
	}
	// ctrl+o should NOT match in WhenPromptFocused
	if id := r.Lookup("\x0f", WhenPromptFocused); id != "" {
		t.Errorf("WhenAgentScreen action should not match in WhenPromptFocused, got %s", id)
	}
}

func TestActionRegistry_Hints(t *testing.T) {
	r := NewActionRegistry()
	r.Register(ActionDef{
		ID:          ActionQuit,
		Label:       "quit",
		DefaultKeys: []terminal.KeyID{"ctrl+q"},
		Category:    CatEssentials,
		Context:     WhenAlways,
	})
	r.Register(ActionDef{
		ID:          ActionOpenSessions,
		Label:       "sessions",
		DefaultKeys: []terminal.KeyID{"ctrl+o"},
		Category:    CatPanels,
		Context:     WhenAgentScreen,
	})

	// In WhenAlways context, only quit should appear
	hints := r.Hints(WhenPromptFocused)
	if len(hints) != 1 || hints[0].ID != ActionQuit {
		t.Errorf("expected 1 hint (quit) in WhenPromptFocused, got %d", len(hints))
	}

	// In WhenAgentScreen, both should appear
	hints = r.Hints(WhenAgentScreen)
	if len(hints) != 2 {
		t.Errorf("expected 2 hints in WhenAgentScreen, got %d", len(hints))
	}
}

func TestActionRegistry_All(t *testing.T) {
	r := NewActionRegistry()
	r.Register(ActionDef{ID: ActionQuit, Label: "quit", DefaultKeys: []terminal.KeyID{"ctrl+q"}, Category: CatEssentials, Context: WhenAlways})
	r.Register(ActionDef{ID: ActionHelp, Label: "help", DefaultKeys: []terminal.KeyID{"ctrl+/"}, Category: CatEssentials, Context: WhenAlways})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
	if all[0].ID != ActionQuit || all[1].ID != ActionHelp {
		t.Error("order not preserved")
	}
}

func TestActionRegistry_SearchCheatsheet(t *testing.T) {
	r := NewActionRegistry()
	r.Register(ActionDef{ID: ActionQuit, Label: "quit", Description: "Quit the application", DefaultKeys: []terminal.KeyID{"ctrl+q"}, Category: CatEssentials, Context: WhenAlways, LongHelp: "Press twice to quit"})
	r.Register(ActionDef{ID: ActionHelp, Label: "help", Description: "Show shortcuts", DefaultKeys: []terminal.KeyID{"ctrl+/"}, Category: CatEssentials, Context: WhenAlways})

	results := r.SearchCheatsheet("quit")
	if len(results) != 1 || results[0].ID != ActionQuit {
		t.Errorf("expected 1 result matching 'quit', got %d", len(results))
	}

	results = r.SearchCheatsheet("")
	if len(results) != 2 {
		t.Errorf("empty query should return all, got %d", len(results))
	}
}

func TestDefaultActions(t *testing.T) {
	ctx := &TerminalContext{Brand: BrandITerm2}
	actions := DefaultActions(ctx)

	// Should have at least quit, help, interrupt
	found := make(map[ActionId]bool)
	for _, a := range actions {
		found[a.ID] = true
	}
	if !found[ActionQuit] {
		t.Error("missing ActionQuit")
	}
	if !found[ActionHelp] {
		t.Error("missing ActionHelp")
	}
	// ActionInterrupt (Ctrl+C) is intentionally NOT in DefaultActions —
	// it's handled by the editor/chat layer, not HotkeyRouter.
	if found[ActionInterrupt] {
		t.Error("ActionInterrupt should NOT be in DefaultActions (would swallow Ctrl+C)")
	}
}

func TestDefaultActions_VSCodeAdaptive(t *testing.T) {
	// VS Code family should have ctrl+d as first quit key
	vsctx := &TerminalContext{Brand: BrandVSCode, TERMProgram: "vscode"}
	actions := DefaultActions(vsctx)

	var quitDef ActionDef
	for _, a := range actions {
		if a.ID == ActionQuit {
			quitDef = a
			break
		}
	}
	if len(quitDef.DefaultKeys) == 0 {
		t.Fatal("no quit keys")
	}
	if quitDef.DefaultKeys[0] != "ctrl+d" {
		t.Errorf("VS Code should prefer ctrl+d, got %s", quitDef.DefaultKeys[0])
	}
}

func TestFormatKeyDisplay(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ctrl+q", "Ctrl+Q"},
		{"ctrl+/", "Ctrl+/"},
		{"enter", "Enter"},
		{"shift+enter", "Shift+Enter"},
		{"ctrl+shift+p", "Ctrl+Shift+P"},
	}
	for _, tt := range tests {
		got := formatKeyDisplay(tt.input)
		if got != tt.want {
			t.Errorf("formatKeyDisplay(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestActionRegistry_AttachKeybindingsManager(t *testing.T) {
	r := NewActionRegistry()
	r.Register(ActionDef{
		ID:          ActionQuit,
		Label:       "quit",
		DefaultKeys: []terminal.KeyID{"ctrl+q"},
		Description: "Quit",
		Category:    CatEssentials,
		Context:     WhenAlways,
	})

	km := terminal.NewKeybindingsManager(nil)
	r.AttachKeybindingsManager(km)

	def := km.Definition(string(ActionQuit))
	if def.Description != "Quit" {
		t.Errorf("expected synced definition, got %q", def.Description)
	}
}

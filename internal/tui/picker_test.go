package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestPicker_BasicNavigation(t *testing.T) {
	p := NewPicker(PickerConfig{Title: "Test", PageSize: 3})
	p.SetItems([]PickerItem{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Beta"},
		{Value: "c", Label: "Gamma"},
	})

	if len(p.Items()) != 3 {
		t.Fatalf("expected 3 items, got %d", len(p.Items()))
	}

	// Initial selection should be 0
	if p.SelectedIndex() != 0 {
		t.Errorf("expected initial selection 0, got %d", p.SelectedIndex())
	}

	// Simulate down key (Esc sequence for down: \x1b[B)
	p.Update(core.KeyMsg{Data: "\x1b[B"})
	if p.SelectedIndex() != 1 {
		t.Errorf("after down, expected 1, got %d", p.SelectedIndex())
	}

	// Down again
	p.Update(core.KeyMsg{Data: "\x1b[B"})
	if p.SelectedIndex() != 2 {
		t.Errorf("after 2x down, expected 2, got %d", p.SelectedIndex())
	}

	// Down with wrap
	p.Update(core.KeyMsg{Data: "\x1b[B"})
	if p.SelectedIndex() != 0 {
		t.Errorf("after wrap, expected 0, got %d", p.SelectedIndex())
	}
}

func TestPicker_Search(t *testing.T) {
	p := NewPicker(PickerConfig{
		Title:     "Test",
		PageSize:  10,
		Searchable: true,
	})
	p.SetItems([]PickerItem{
		{Value: "openai", Label: "OpenAI"},
		{Value: "anthropic", Label: "Anthropic"},
		{Value: "google", Label: "Google"},
	})

	// Enter search mode (/)
	p.Update(core.KeyMsg{Data: "/"})

	// Type "open"
	p.Update(core.KeyMsg{Data: "o"})
	p.Update(core.KeyMsg{Data: "p"})
	p.Update(core.KeyMsg{Data: "e"})
	p.Update(core.KeyMsg{Data: "n"})

	items := p.Items()
	if len(items) != 1 {
		t.Fatalf("expected 1 filtered item, got %d", len(items))
	}
	if items[0].Value != "openai" {
		t.Errorf("expected openai, got %s", items[0].Value)
	}

	// Exit search (Esc)
	p.Update(core.KeyMsg{Data: "\x1b"})
	if len(p.Items()) != 3 {
		t.Errorf("after cancel search, expected 3 items, got %d", len(p.Items()))
	}
}

func TestPicker_OnSelect(t *testing.T) {
	p := NewPicker(PickerConfig{Title: "Test", PageSize: 3})
	p.SetItems([]PickerItem{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Beta"},
	})

	var selected PickerItem
	p.OnSelect(func(item PickerItem) {
		selected = item
	})

	// Press Enter
	p.Update(core.KeyMsg{Data: "\r"})

	if selected.Value != "a" {
		t.Errorf("expected 'a' selected, got '%s'", selected.Value)
	}
}

func TestPicker_OnCancel(t *testing.T) {
	p := NewPicker(PickerConfig{Title: "Test", PageSize: 3})
	p.SetItems([]PickerItem{
		{Value: "a", Label: "Alpha"},
	})

	called := false
	p.OnCancel(func() {
		called = true
	})

	// Press Esc
	p.Update(core.KeyMsg{Data: "\x1b"})

	if !called {
		t.Error("expected onCancel to be called")
	}
}

func TestPicker_Pagination(t *testing.T) {
	items := make([]PickerItem, 25)
	for i := range items {
		items[i] = PickerItem{Value: string(rune('a' + i)), Label: string(rune('a' + i))}
	}

	p := NewPicker(PickerConfig{Title: "Test", PageSize: 5})
	p.SetItems(items)

	// Should start at offset 0
	if p.SelectedIndex() != 0 {
		t.Error("expected initial 0")
	}

	// Move down 5 times (to last in page)
	for i := 0; i < 5; i++ {
		p.Update(core.KeyMsg{Data: "\x1b[B"})
	}

	// Should have scrolled
	if p.SelectedIndex() != 5 {
		t.Errorf("expected index 5, got %d", p.SelectedIndex())
	}
}

func TestPicker_Render(t *testing.T) {
	p := NewPicker(PickerConfig{Title: "Test Picker", PageSize: 3})
	p.SetItems([]PickerItem{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Beta"},
	})

	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected non-empty render")
	}

	// Should contain the title
	found := false
	for _, line := range lines {
		if containsVisible(line, "Test Picker") {
			found = true
			break
		}
	}
	if !found {
		t.Error("render should contain title")
	}
}

func TestPicker_SetSelected(t *testing.T) {
	p := NewPicker(PickerConfig{Title: "Test", PageSize: 3})
	p.SetItems([]PickerItem{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Beta"},
		{Value: "c", Label: "Gamma"},
	})

	p.SetSelected(2)
	if p.SelectedIndex() != 2 {
		t.Errorf("expected 2, got %d", p.SelectedIndex())
	}
}

// Helper: check if rendered string contains visible text (ignoring ANSI)
func containsVisible(s, substr string) bool {
	// Strip ANSI codes
	clean := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			// skip until 'm'
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		clean += string(s[i])
	}
	return len(clean) >= len(substr) && (clean == substr || indexOf(clean, substr) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

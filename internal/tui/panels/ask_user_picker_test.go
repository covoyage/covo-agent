package panels

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestAskUserPickerNumericShortcut(t *testing.T) {
	var got string
	picker := NewAskUserPicker("pick", []string{"A", "B", "C"}, func(answer string) { got = answer }, nil)
	picker.Update(core.KeyMsg{Data: "2"})
	if got != "B" {
		t.Fatalf("choice = %q, want %q", got, "B")
	}
}

func TestAskUserPickerNavigation(t *testing.T) {
	var got string
	picker := NewAskUserPicker("pick", []string{"A", "B", "C"}, func(answer string) { got = answer }, nil)
	picker.Update(core.KeyMsg{Data: "\x1b[B"}) // down arrow
	picker.Update(core.KeyMsg{Data: "\r"})     // enter
	if got != "B" {
		t.Fatalf("choice = %q, want %q", got, "B")
	}
}

func TestAskUserPickerWrap(t *testing.T) {
	var got string
	picker := NewAskUserPicker("pick", []string{"A", "B"}, func(answer string) { got = answer }, nil)
	picker.Update(core.KeyMsg{Data: "\x1b[A"}) // up arrow wraps to last
	picker.Update(core.KeyMsg{Data: "\r"})
	if got != "B" {
		t.Fatalf("wrapped choice = %q, want %q", got, "B")
	}
}

func TestAskUserPickerOutOfRangeShortcutIgnored(t *testing.T) {
	called := false
	picker := NewAskUserPicker("pick", []string{"A"}, func(answer string) { called = true }, nil)
	picker.Update(core.KeyMsg{Data: "9"})
	if called {
		t.Fatal("out-of-range numeric shortcut should not confirm")
	}
}

func TestAskUserPickerCancel(t *testing.T) {
	cancelled := false
	picker := NewAskUserPicker("pick", []string{"A"}, nil, func() { cancelled = true })
	picker.Update(core.KeyMsg{Data: "\x1b"})
	if !cancelled {
		t.Fatal("escape did not cancel ask_user picker")
	}
}

func TestAskUserPickerRenderEmptyOptions(t *testing.T) {
	picker := NewAskUserPicker("pick", nil, nil, nil)
	lines := picker.Render(60)
	if len(lines) < 4 {
		t.Fatalf("render produced %d lines, want >= 4", len(lines))
	}
}

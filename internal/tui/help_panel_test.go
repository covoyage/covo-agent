package tui

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
)

func testHelpPanel() *HelpPanel {
	km := terminal.NewKeybindingsManager(terminal.DefaultKeybindings())
	km.Register("app.help", terminal.KeybindingDef{DefaultKeys: []terminal.KeyID{"ctrl+/"}, Description: "Toggle keybindings help"})
	km.Register("app.quit", terminal.KeybindingDef{DefaultKeys: []terminal.KeyID{"ctrl+q"}, Description: "Quit application"})
	km.Register("slash.model", terminal.KeybindingDef{Description: "Open interactive provider/model picker"})
	return NewHelpPanel("Keybindings", km, 20)
}

func sendKey(p *HelpPanel, data string) {
	p.Update(core.KeyMsg{Data: data})
}

func sendText(p *HelpPanel, s string) {
	for _, r := range s {
		sendKey(p, string(r))
	}
}

func TestHelpPanel_FilterNarrowsRows(t *testing.T) {
	p := testHelpPanel()

	sendKey(p, "/")
	if !p.filtering {
		t.Fatal("expected filtering mode after '/'")
	}
	sendText(p, "model")
	if p.query != "model" {
		t.Fatalf("expected query 'model', got %q", p.query)
	}
	if len(p.filtered) != 1 || !strings.Contains(p.filtered[0].desc, "model picker") {
		t.Fatalf("expected 1 filtered row matching 'model', got %d", len(p.filtered))
	}

	rendered := strings.Join(p.Render(60), "\n")
	if !strings.Contains(rendered, "/ model") {
		t.Errorf("render should show the filter input, got:\n%s", rendered)
	}
}

func TestHelpPanel_FilterNoMatches(t *testing.T) {
	p := testHelpPanel()

	sendKey(p, "/")
	sendText(p, "zzzz")
	if len(p.filtered) != 0 {
		t.Fatalf("expected no matches, got %d", len(p.filtered))
	}
	rendered := strings.Join(p.Render(60), "\n")
	if !strings.Contains(rendered, "no matches") {
		t.Errorf("render should show 'no matches', got:\n%s", rendered)
	}
}

func TestHelpPanel_EscExitsFilterThenCloses(t *testing.T) {
	p := testHelpPanel()

	sendKey(p, "/")
	if !p.filtering {
		t.Fatal("expected filtering mode")
	}
	if !p.HandleEsc() {
		t.Fatal("first Esc should be consumed by the filter")
	}
	if p.filtering || p.query != "" {
		t.Fatalf("expected filter to reset, filtering=%v query=%q", p.filtering, p.query)
	}
	if p.HandleEsc() {
		t.Fatal("second Esc should not be consumed (panel closes)")
	}
}

func TestHelpPanel_FilterBackspace(t *testing.T) {
	p := testHelpPanel()

	sendKey(p, "/")
	sendText(p, "mo")
	sendKey(p, "\x7f")
	if p.query != "m" {
		t.Fatalf("expected query 'm' after backspace, got %q", p.query)
	}
}

func TestHelpPanel_FilterChineseInput(t *testing.T) {
	p := testHelpPanel()

	sendKey(p, "/")
	sendText(p, "模式")
	if p.query != "模式" {
		t.Fatalf("expected query '模式', got %q", p.query)
	}
	sendKey(p, "\x7f")
	if p.query != "模" {
		t.Fatalf("expected query '模' after backspace, got %q", p.query)
	}
}

func TestHelpPanel_FilterPaste(t *testing.T) {
	p := testHelpPanel()

	sendKey(p, "/")
	sendKey(p, "c")
	p.Update(core.PasteMsg{Text: "模型"})
	if p.query != "c模型" {
		t.Fatalf("expected query 'c模型', got %q", p.query)
	}
	if !p.filtering {
		t.Fatal("paste should keep filter mode active")
	}
}

func TestHelpPanel_FilterBackspaceEmpty(t *testing.T) {
	p := testHelpPanel()

	sendKey(p, "/")
	sendKey(p, "\x7f")
	if p.query != "" {
		t.Fatalf("expected empty query after backspace, got %q", p.query)
	}
}

func TestHelpPanel_ScrollClamped(t *testing.T) {
	p := testHelpPanel()
	for i := 0; i < 1000; i++ {
		sendKey(p, "down")
	}
	if p.offset > countBodyLines(p.rows) {
		t.Fatalf("offset %d exceeds body %d", p.offset, countBodyLines(p.rows))
	}
}

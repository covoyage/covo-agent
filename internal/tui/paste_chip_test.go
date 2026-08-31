package tui

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/theme"
)

func TestShouldChipPaste(t *testing.T) {
	if ShouldChipPaste(strings.Repeat("a", LargePasteThreshold-1)) {
		t.Fatal("short single-line paste should not chip")
	}
	if !ShouldChipPaste(strings.Repeat("a", LargePasteThreshold)) {
		t.Fatal("large paste should chip")
	}
	if ShouldChipPaste("hello\n") {
		t.Fatal("trailing newline alone should not chip")
	}
	if !ShouldChipPaste("a\nb") {
		t.Fatal("multiline paste should chip")
	}
}

func TestPasteStoreExpand(t *testing.T) {
	store := NewPasteStore()
	blob := strings.Repeat("hello\n", 20)
	chip := store.Store(blob)
	if chip != "[Pasted ~20 lines]" {
		t.Fatalf("chip = %q", chip)
	}
	got := store.Expand("before " + chip + " after")
	if got != "before "+blob+" after" {
		t.Fatalf("expand = %q", got)
	}
}

func TestPasteStoreDisambiguatesDuplicateChips(t *testing.T) {
	store := NewPasteStore()
	first := store.Store("a\nb")
	second := store.Store("c\nd")
	if first != "[Pasted ~2 lines]" {
		t.Fatalf("first chip = %q", first)
	}
	if second != "[Pasted ~2 lines #2]" {
		t.Fatalf("second chip = %q", second)
	}
	got := store.Expand(first + " " + second)
	if got != "a\nb c\nd" {
		t.Fatalf("expand = %q", got)
	}
}

func TestStylePasteChips(t *testing.T) {
	in := "prefix [Pasted ~6 lines] suffix"
	got := StylePasteChips(in)
	if !strings.Contains(got, "[Pasted ~6 lines]") {
		t.Fatalf("styled = %q", got)
	}
	if StylePasteChips("plain") != "plain" {
		t.Fatal("non-chip text should pass through")
	}
}

func TestPasteChipStyleUsesThemeWarning(t *testing.T) {
	theme.ForceColor(true)
	prev := theme.CurrentPalette()
	t.Cleanup(func() {
		if prev != nil && prev.Semantic != nil {
			theme.SyncPaletteGlobals(prev.Semantic, prev.Mode)
		}
	})
	theme.SyncPaletteGlobals(&theme.SemanticTheme{
		Warning: "#112233",
		Accent:  "#ff00ff",
		Text:    "#eeeeee",
	}, theme.ColorModeTruecolor)

	st := pasteChipStyle(theme.CurrentPalette())
	got := st.Render("[Pasted ~2 lines]")
	if !strings.Contains(got, "48;2;17;34;51") {
		t.Fatalf("expected warning background, got %q", got)
	}
	if strings.Contains(got, "48;2;255;0;255") {
		t.Fatalf("should not fall back to accent when warning is set, got %q", got)
	}
}

func TestPasteStoreNil(t *testing.T) {
	var store *PasteStore
	if store.Expand("x") != "x" {
		t.Fatal("nil expand should pass through")
	}
	if store.Store("blob") != "blob" {
		t.Fatal("nil store should return original text")
	}
}

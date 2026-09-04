package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/theme"
)

func TestShouldChipPaste(t *testing.T) {
	if ShouldChipPaste(strings.Repeat("a", ChipMinRunes)) {
		t.Fatal("150-rune single line should not chip")
	}
	if !ShouldChipPaste(strings.Repeat("a", ChipMinRunes+1)) {
		t.Fatal("151-rune paste should chip")
	}
	if ShouldChipPaste("hello\n") {
		t.Fatal("trailing newline alone should not chip")
	}
	if ShouldChipPaste("a\nb") {
		t.Fatal("two-line paste should stay as text")
	}
	if !ShouldChipPaste("a\nb\nc") {
		t.Fatal("three-line paste should chip")
	}
	if !ShouldChipPaste(strings.Repeat("a", LargePasteThreshold)) {
		t.Fatal("large paste should chip")
	}
}

func TestFileRefFromPaste(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	got, ok := FileRefFromPaste(file+"\n", dir)
	if !ok || got != "@file:notes.txt" {
		t.Fatalf("file ref = %q ok=%v", got, ok)
	}
	got, ok = FileRefFromPaste(dir, dir)
	if !ok || got != "@folder:." {
		t.Fatalf("dir ref = %q ok=%v", got, ok)
	}
	if _, ok := FileRefFromPaste("https://example.com/a.go", dir); ok {
		t.Fatal("url should not become a file ref")
	}
	if _, ok := FileRefFromPaste("not-a-path", dir); ok {
		t.Fatal("bare word should not become a file ref")
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

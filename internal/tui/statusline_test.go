package tui

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

func newTestStatusline() *StatusLineManager {
	m := NewStatusLineManager()
	m.SetRenderFn("mode", func(p *theme.Palette) string { return "MODE" })
	m.SetRenderFn("git-branch", func(p *theme.Palette) string { return "GIT" })
	m.SetRenderFn("context-used", func(p *theme.Palette) string { return "CTX" })
	m.SetRenderFn("activity", func(p *theme.Palette) string {
		return "ACT " + strings.Repeat("x", 60)
	})
	m.SetRenderFn("shortcuts", func(p *theme.Palette) string { return "SHORTCUTS" })
	return m
}

// TestBuildLinePriorityFit verifies that a long low-priority segment
// (activity) is dropped before the higher-priority head segments, and the
// line never overflows.
func TestBuildLinePriorityFit(t *testing.T) {
	m := newTestStatusline()
	line := m.BuildLine(theme.CurrentPalette(), 40)
	if !strings.Contains(line, "MODE") || !strings.Contains(line, "CTX") {
		t.Fatalf("high-priority segments must survive, got %q", line)
	}
	if strings.Contains(line, "ACT xxx") {
		t.Fatalf("expected over-long activity dropped, got %q", line)
	}
	if w := core.VisibleWidth(line); int64(w) > 40 {
		t.Fatalf("line overflows: width %d > 40: %q", w, line)
	}
}

// TestBuildLineDropsShortcutsFirst verifies that on overflow the shortcuts
// segment (lowest priority) is dropped before higher-priority segments.
func TestBuildLineDropsShortcutsFirst(t *testing.T) {
	m := newTestStatusline()
	m.SetRenderFn("activity", func(p *theme.Palette) string { return "ACT" })
	line := m.BuildLine(theme.CurrentPalette(), 20)
	// MODE GIT CTX ACT = 4+3+3+3 + 3 separators(9) + leading space = 21 > 20
	// → shortcuts dropped first, but the four content segments still overflow
	// → the next lowest (activity, pri 5) is dropped → MODE GIT CTX remain.
	if !strings.Contains(line, "MODE") || !strings.Contains(line, "CTX") {
		t.Fatalf("expected head segments kept, got %q", line)
	}
	if strings.Contains(line, "SHORTCUTS") {
		t.Fatalf("expected shortcuts dropped first, got %q", line)
	}
	if w := core.VisibleWidth(line); int64(w) > 20 {
		t.Fatalf("line overflows: width %d > 20: %q", w, line)
	}
}

// TestBuildLineSingleSegmentTruncates verifies the last-segment-standing
// fallback truncates instead of overflowing.
func TestBuildLineSingleSegmentTruncates(t *testing.T) {
	m := NewStatusLineManager()
	m.SetRenderFn("activity", func(p *theme.Palette) string {
		return "SEG " + strings.Repeat("y", 80)
	})
	line := m.BuildLine(theme.CurrentPalette(), 20)
	if w := core.VisibleWidth(line); int64(w) > 20 {
		t.Fatalf("line overflows: width %d > 20: %q", w, line)
	}
	if !strings.Contains(line, "SEG") {
		t.Fatalf("expected truncated head preserved, got %q", line)
	}
}

package tui

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/theme"
)

// TestStickyFooterSubagentsLine verifies the live sub-agent strip: rendered
// only while children are running, hidden otherwise, truncated to width.
func TestStickyFooterSubagentsLine(t *testing.T) {
	footer := NewStickyFooter()

	// No provider / no live children: no line.
	if got := footer.Render(120); strings.Contains(strings.Join(got, "\n"), "live agents") {
		t.Fatalf("expected no subagent line without live children, got %v", got)
	}

	footer.SetSubagentsProvider(func() []FooterSubagent {
		return []FooterSubagent{
			{ID: "sub-1", Task: "analyze repo", Status: "running", Live: true},
			{ID: "sub-2", Task: "write tests", Status: "running", Live: true},
			{ID: "sub-0", Task: "done task", Status: "completed", Live: false},
		}
	})
	lines := footer.Render(120)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "2 live agents") {
		t.Fatalf("expected '2 live agents' in footer, got %v", lines)
	}
	if !strings.Contains(joined, "analyze repo") {
		t.Fatalf("expected latest live task preview, got %v", lines)
	}
	if strings.Contains(joined, "done task") {
		t.Fatalf("completed children must not render in the live strip, got %v", lines)
	}

	// Narrow terminal: line is truncated to width.
	for _, l := range footer.Render(30) {
		if w := core.VisibleWidth(l); int64(w) > 30 {
			t.Fatalf("footer line overflows 30 cols: %q (%d)", l, w)
		}
	}
	_ = theme.CurrentPalette
}

// TestStickyFooterSubagentsSingular verifies "1 live agent" grammar and that
// a persona-injected task (<system-reminder> preamble + "## Your Task")
// previews the REAL task text.
func TestStickyFooterSubagentsSingular(t *testing.T) {
	footer := NewStickyFooter()
	footer.SetSubagentsProvider(func() []FooterSubagent {
		return []FooterSubagent{
			{ID: "sub-9", Task: "<system-reminder>\n## Persona: architect\n...\n</system-reminder>\n\n---\n## Your Task\n对比两个项目的功能", Status: "running", Live: true},
		}
	})
	joined := strings.Join(footer.Render(160), "\n")
	if !strings.Contains(joined, "1 live agent ·") || strings.Contains(joined, "1 live agents") {
		t.Fatalf("expected singular '1 live agent', got %v", joined)
	}
	if !strings.Contains(joined, "对比两个项目的功能") {
		t.Fatalf("expected real task after reminder block, got %v", joined)
	}
	if strings.Contains(joined, "<system-reminder>") {
		t.Fatalf("reminder tag leaked into preview: %v", joined)
	}
}

// TestSubagentTaskPreviewSkipReminder covers the preview helper directly.
func TestSubagentTaskPreviewSkipReminder(t *testing.T) {
	task := "<system-reminder>\nnoise\n</system-reminder>\n\n---\n## Your Task\ncompare A and B"
	if got := SubagentTaskPreview(task, 0); got != "compare A and B" {
		t.Fatalf("got %q", got)
	}
	if got := SubagentTaskPreview("plain task", 5); got != "plain..." {
		t.Fatalf("got %q", got)
	}
}

// TestStickyFooterBgTasksOnly verifies the strip shows the bg-task count
// alone when no sub-agents are running.
func TestStickyFooterBgTasksOnly(t *testing.T) {
	footer := NewStickyFooter()
	footer.SetBgTaskCount(2)
	joined := strings.Join(footer.Render(120), "\n")
	if !strings.Contains(joined, "⚙ 2 bg tasks") {
		t.Fatalf("expected bg task strip, got %v", joined)
	}
	if strings.Contains(joined, "live agent") {
		t.Fatalf("unexpected live agent segment, got %v", joined)
	}
}

// TestStickyFooterAgentsAndBgCombined verifies both segments share the strip.
func TestStickyFooterAgentsAndBgCombined(t *testing.T) {
	footer := NewStickyFooter()
	footer.SetSubagentsProvider(func() []FooterSubagent {
		return []FooterSubagent{{ID: "sub-1", Task: "analyze", Status: "running", Live: true}}
	})
	footer.SetBgTaskCount(1)
	joined := strings.Join(footer.Render(200), "\n")
	if !strings.Contains(joined, "1 live agent") || !strings.Contains(joined, "⚙ 1 bg task") {
		t.Fatalf("expected combined strip, got %v", joined)
	}
}

// TestStickyFooterBgSingular verifies singular grammar for one bg task.
func TestStickyFooterBgSingular(t *testing.T) {
	footer := NewStickyFooter()
	footer.SetBgTaskCount(1)
	joined := strings.Join(footer.Render(120), "\n")
	if !strings.Contains(joined, "⚙ 1 bg task ·") || strings.Contains(joined, "bg tasks") {
		t.Fatalf("expected singular bg task, got %v", joined)
	}
}

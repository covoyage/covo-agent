package tui

import (
	"reflect"
	"strings"
	"testing"
)

func TestStickyFooterSnapshot(t *testing.T) {
	footer := NewStickyFooter()
	footer.SetMode("code")
	footer.SetGitBranch("main")
	footer.SetContextUsage("ctx: 50%")
	footer.SetContextWarn(true)
	footer.SetShortcuts("ctrl+/ help")
	footer.SetBgTaskCount(2)

	got := footer.Snapshot()
	want := FooterSnapshot{
		GitBranch:   "main",
		ContextUsed: "ctx: 50%",
		ContextWarn: true,
		Shortcuts:   "ctrl+/ help",
		BgTaskCount: 2,
		Mode:        "code",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot() = %+v, want %+v", got, want)
	}
}

func TestStickyFooterRendersPlan(t *testing.T) {
	footer := NewStickyFooter()
	footer.SetPlanStore(func() []PlanItem {
		return []PlanItem{
			{Step: "inspect", Status: "completed"},
			{Step: "repair session owner", Status: "in_progress"},
			{Step: "verify", Status: "pending"},
		}
	})
	lines := footer.Render(80)
	if len(lines) == 0 || !strings.Contains(lines[0], "PLAN 1/3") || !strings.Contains(lines[0], "repair session owner") {
		t.Fatalf("lines=%v", lines)
	}
}

func TestFilterActiveTodos(t *testing.T) {
	items := []TodoItem{
		{ID: "1", Status: "pending"},
		{ID: "2", Status: "in_progress"},
		{ID: "3", Status: "completed"},
		{ID: "4", Status: "cancelled"},
	}
	got := filterActive(items)
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Fatalf("filterActive() = %+v", got)
	}
}

func TestStatusLineManagerToggle(t *testing.T) {
	manager := NewStatusLineManager()
	before := manager.EnabledIDs()
	if len(before) == 0 {
		t.Fatal("status line has no enabled items")
	}

	manager.Toggle("mode")
	for _, id := range manager.EnabledIDs() {
		if id == "mode" {
			t.Fatal("mode remained enabled after toggle")
		}
	}
	manager.Toggle("mode")
	found := false
	for _, id := range manager.EnabledIDs() {
		found = found || id == "mode"
	}
	if !found {
		t.Fatal("mode was not re-enabled after second toggle")
	}
}

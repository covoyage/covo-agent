package tui

import (
	"strings"
	"testing"
	"time"
)

func TestDashboardPickerItems(t *testing.T) {
	items := DashboardPickerItems(DashboardData{
		CurrentID: "abcdef123456",
		Busy:      true,
		MsgCount:  4,
		Tasks: []DashboardTask{{
			ID:        "task-1",
			Input:     "review pr",
			Status:    "running",
			StartedAt: time.Now().Add(-time.Second),
		}},
		Sessions: []DashboardSession{{
			ID:        "abcdef123456",
			Name:      "main",
			IsCurrent: true,
			MsgCount:  4,
			Status:    "Working",
		}},
	})
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if !strings.HasPrefix(items[0].Value, "current:") {
		t.Fatalf("first = %+v", items[0])
	}
	if items[1].Value != "task:task-1" {
		t.Fatalf("task = %+v", items[1])
	}
	if items[2].Value != "session:abcdef123456" {
		t.Fatalf("session = %+v", items[2])
	}
	if !strings.Contains(items[2].Label, "main") {
		t.Fatalf("session label = %q", items[2].Label)
	}
}

// TestDashboardPickerItemsSubagentSortFilter verifies the sort/filter options
// and the depth-based tree indentation of sub-agent rows.
func TestDashboardPickerItemsSubagentSortFilter(t *testing.T) {
	base := time.Now()
	data := DashboardData{Subagents: []DashboardSubagent{
		{ID: "sub-1", Task: "root task", Status: "completed", Depth: 1,
			StartedAt: base.Add(-10 * time.Minute), EndedAt: base.Add(-8 * time.Minute)},
		{ID: "sub-2", Task: "slow child", Status: "running", Depth: 2, StartedAt: base.Add(-5 * time.Minute), Live: true},
		{ID: "sub-3", Task: "failed child", Status: "failed", Depth: 2,
			StartedAt: base.Add(-4 * time.Minute), EndedAt: base.Add(-1 * time.Minute)},
	}}

	// Spawn order keeps chronology and indents by depth.
	items := DashboardPickerItems(data, DashboardListOptions{})
	var labels []string
	for _, it := range items {
		if strings.HasPrefix(it.Value, "subagent:") {
			labels = append(labels, it.Label)
		}
	}
	if len(labels) != 3 {
		t.Fatalf("expected 3 subagent rows, got %v", labels)
	}
	if !strings.Contains(labels[1], "└ sub-2") {
		t.Fatalf("expected depth-2 child indented, got %q", labels[1])
	}

	subValues := func(items []PickerItem) []string {
		var out []string
		for _, it := range items {
			if strings.HasPrefix(it.Value, "subagent:") {
				out = append(out, it.Value)
			}
		}
		return out
	}

	// Duration sort: slowest first → sub-2 (5m running) before sub-3 (3m).
	subs := subValues(DashboardPickerItems(data, DashboardListOptions{SubagentSort: DashboardSortDuration}))
	if len(subs) != 3 || !strings.Contains(subs[0], "sub-2") {
		t.Fatalf("expected slowest first, got %v", subs)
	}

	// Status sort: running before failed before completed.
	subs = subValues(DashboardPickerItems(data, DashboardListOptions{SubagentSort: DashboardSortStatus}))
	if len(subs) != 3 || !strings.Contains(subs[0], "sub-2") || !strings.Contains(subs[1], "sub-3") {
		t.Fatalf("expected running→failed→completed order, got %v", subs)
	}

	// Running filter keeps only the live child.
	subs = subValues(DashboardPickerItems(data, DashboardListOptions{SubagentFilter: DashboardFilterRunning}))
	if len(subs) != 1 || !strings.Contains(subs[0], "sub-2") {
		t.Fatalf("expected only running sub-2, got %v", subs)
	}
}

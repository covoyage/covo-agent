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

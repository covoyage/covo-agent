package agent

import (
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/kanban"
	toolsplanning "github.com/covoyage/covo-agent/internal/tools/planning"
	"github.com/covoyage/covonaut/agentcore"
)

func TestStopGateDecision(t *testing.T) {
	pending := toolsplanning.TodoItem{ID: "1", Content: "write tests", Status: toolsplanning.TodoPending}
	inProgress := toolsplanning.TodoItem{ID: "2", Content: "refactor", Status: toolsplanning.TodoInProgress}
	done := toolsplanning.TodoItem{ID: "3", Content: "done thing", Status: toolsplanning.TodoCompleted}
	cancelled := toolsplanning.TodoItem{ID: "4", Content: "dropped", Status: toolsplanning.TodoCancelled}

	tests := []struct {
		name         string
		items        []toolsplanning.TodoItem
		reentryCount int
		maxReentry   int
		wantReentry  bool
	}{
		{"no todos", nil, 0, 3, false},
		{"all done", []toolsplanning.TodoItem{done, cancelled}, 0, 3, false},
		{"one pending", []toolsplanning.TodoItem{pending, done}, 0, 3, true},
		{"one in_progress", []toolsplanning.TodoItem{inProgress}, 1, 3, true},
		{"cap reached", []toolsplanning.TodoItem{pending}, 3, 3, false},
		{"cap exceeded", []toolsplanning.TodoItem{pending, inProgress}, 5, 3, false},
		{"last allowed attempt", []toolsplanning.TodoItem{pending}, 2, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reentry, msg := stopGateDecision(tt.items, tt.reentryCount, tt.maxReentry)
			if reentry != tt.wantReentry {
				t.Fatalf("reentry = %v, want %v", reentry, tt.wantReentry)
			}
			if reentry {
				if msg == "" {
					t.Error("expected a non-empty nudge message")
				}
				// The message should list the unfinished item content.
				if !strings.Contains(msg, "write tests") && !strings.Contains(msg, "refactor") {
					t.Errorf("nudge message should list incomplete items, got: %q", msg)
				}
				// Completed/cancelled items must not be listed.
				if strings.Contains(msg, "done thing") || strings.Contains(msg, "dropped") {
					t.Errorf("nudge message must not list finished items, got: %q", msg)
				}
			}
		})
	}
}

func TestStopGateReentryProgression(t *testing.T) {
	items := []toolsplanning.TodoItem{{ID: "1", Content: "x", Status: toolsplanning.TodoPending}}
	count := 0
	max := 3
	nudges := 0
	for i := 0; i < 10; i++ {
		reentry, _ := stopGateDecision(items, count, max)
		if !reentry {
			break
		}
		nudges++
		count++
	}
	if nudges != max {
		t.Errorf("expected exactly %d nudges before the gate gives up, got %d", max, nudges)
	}
}

func TestParseJudgeVerdict(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantSat   bool
		reasonHas string
	}{
		{"satisfied", "VERDICT: SATISFIED — all tests pass and feature works", true, "all tests pass"},
		{"not satisfied underscore", "VERDICT: NOT_SATISFIED — tests still failing", false, "tests still failing"},
		{"not satisfied spaced", "VERDICT: NOT SATISFIED - the build is broken", false, "the build is broken"},
		{"garbled fails open", "the model rambled without a verdict", true, ""},
		{"lowercase satisfied", "verdict: satisfied — done", true, "done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sat, reason := parseJudgeVerdict(tt.in)
			if sat != tt.wantSat {
				t.Fatalf("satisfied = %v, want %v (in=%q)", sat, tt.wantSat, tt.in)
			}
			if tt.reasonHas != "" && !strings.Contains(reason, tt.reasonHas) {
				t.Errorf("reason %q should contain %q", reason, tt.reasonHas)
			}
		})
	}
}

func TestBuildGoalTranscript(t *testing.T) {
	msgs := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "first"},
		{Role: agentcore.RoleAssistant, Content: "second"},
		{Role: agentcore.RoleAssistant, Content: ""}, // skipped
		{Role: agentcore.RoleAssistant, Content: "third"},
	}
	out := buildGoalTranscript(msgs, 10000)
	// Chronological order, empties skipped.
	if !strings.Contains(out, "first") || !strings.Contains(out, "third") {
		t.Fatalf("transcript missing content: %q", out)
	}
	if strings.Index(out, "first") > strings.Index(out, "third") {
		t.Errorf("transcript not in chronological order: %q", out)
	}
}

func TestBuildGoalTranscriptCap(t *testing.T) {
	big := strings.Repeat("x", 2000)
	msgs := []agentcore.Message{
		{Role: agentcore.RoleAssistant, Content: big},
		{Role: agentcore.RoleAssistant, Content: big},
		{Role: agentcore.RoleAssistant, Content: big},
	}
	out := buildGoalTranscript(msgs, 1500)
	// At least the most recent message is kept, but the cap bounds total size.
	if out == "" {
		t.Fatal("expected at least one message kept")
	}
	if len(out) > 4000 {
		t.Errorf("transcript exceeded reasonable cap: %d chars", len(out))
	}
}

func TestBuildKanbanReentry(t *testing.T) {
	tasks := []*kanban.Task{
		{ID: "T-001", Title: "write tests", Status: kanban.StatusInProgress},
		{ID: "T-002", Title: "refactor module", Status: kanban.StatusTodo},
	}
	msg := buildKanbanReentry(tasks, 1, 3)

	// Should list all incomplete tasks with their IDs and titles.
	if !strings.Contains(msg, "write tests") || !strings.Contains(msg, "refactor module") {
		t.Errorf("should list all incomplete tasks, got: %q", msg)
	}
	if !strings.Contains(msg, "T-001") || !strings.Contains(msg, "T-002") {
		t.Errorf("should list task IDs, got: %q", msg)
	}
	// Should show task status.
	if !strings.Contains(msg, "in_progress") || !strings.Contains(msg, "todo") {
		t.Errorf("should show task status, got: %q", msg)
	}
	// Should include attempt/max counter.
	if !strings.Contains(msg, "1/3") {
		t.Errorf("should include attempt/max counter, got: %q", msg)
	}

	// Empty list should not panic and should still carry the counter.
	empty := buildKanbanReentry(nil, 2, 3)
	if !strings.Contains(empty, "2/3") {
		t.Errorf("should include counter even with no tasks, got: %q", empty)
	}
}

package agent

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestSubagentViewRegistryLifecycle(t *testing.T) {
	r := NewSubagentViewRegistry()

	calls := 0
	id := r.Register("analyze the codebase", 1, func() []agentcore.Message {
		calls++
		return []agentcore.Message{{Role: "user", Content: "live"}}
	})

	if !strings.HasPrefix(id, "agent-") {
		t.Fatalf("unexpected display id %q", id)
	}

	entry, ok := r.Get(id)
	if !ok {
		t.Fatal("entry not found")
	}
	if !entry.Live() || entry.Status() != "running" {
		t.Fatalf("expected live entry, got %q", entry.Status())
	}
	if entry.Task() != "analyze the codebase" || entry.Depth() != 1 {
		t.Fatalf("unexpected task/depth: %q/%d", entry.Task(), entry.Depth())
	}

	// Live messages come from the closure while running.
	if msgs := entry.Messages(); len(msgs) != 1 || msgs[0].Content != "live" {
		t.Fatalf("expected live messages, got %v", msgs)
	}
	if calls != 1 {
		t.Fatalf("expected live closure to be called once, got %d", calls)
	}

	// MarkFinished captures the snapshot and freezes it.
	r.MarkFinished(id, "completed")
	if entry.Live() || entry.Status() != "completed" {
		t.Fatalf("expected completed entry, got %q", entry.Status())
	}
	if entry.EndedAt().IsZero() {
		t.Fatal("expected ended_at to be set")
	}

	// After completion the snapshot is stable (closure no longer called).
	if msgs := entry.Messages(); len(msgs) != 1 || msgs[0].Content != "live" {
		t.Fatalf("expected frozen snapshot, got %v", msgs)
	}
	if calls != 2 { // once during live check, once for the snapshot capture
		t.Fatalf("expected closure reuse for snapshot, got %d calls", calls)
	}
}

func TestSubagentViewRegistryOrderAndFailure(t *testing.T) {
	r := NewSubagentViewRegistry()
	idA := r.Register("task a", 1, nil)
	idB := r.Register("task b", 2, nil)

	entries := r.List()
	if len(entries) != 2 || entries[0].ID() != idA || entries[1].ID() != idB {
		t.Fatalf("expected spawn order [a b], got %v", entries)
	}

	r.MarkFinished(idB, "interrupted")
	// Unknown status maps to failed; interrupted is not accepted by design
	// (spawn closure only reports completed/failed).
	if got := entries[1].Status(); got != "failed" {
		t.Fatalf("expected failed for unknown status, got %q", got)
	}
	if entries[1].Live() {
		t.Fatal("finished entry must not be live")
	}
}

// TestSubagentViewRegistryReregisterSameID verifies that re-registering the
// same view id (SubagentRunner reruns reuse it) does not duplicate the entry
// in List() — the footer count would otherwise inflate.
func TestSubagentViewRegistryReregisterSameID(t *testing.T) {
	r := NewSubagentViewRegistry()
	id := r.Register("task v1", 1, nil)
	r.RegisterWithID(id, "task v2 (rerun)", 1, nil)

	entries := r.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after re-registration, got %d", len(entries))
	}
	if entries[0].Task() != "task v2 (rerun)" {
		t.Fatalf("expected latest task, got %q", entries[0].Task())
	}
	if entries[0].ID() != id {
		t.Fatalf("expected id preserved, got %q", entries[0].ID())
	}
}

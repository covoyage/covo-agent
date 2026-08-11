package fileops

import (
	"testing"
)

func TestLcsTable(t *testing.T) {
	a := []string{"a", "b", "c"}
	b := []string{"a", "c", "d"}
	dp := lcsTable(a, b)
	if dp[3][3] != 2 {
		t.Fatalf("expected LCS length 2 (a, c), got %d", dp[3][3])
	}
	if dp[0][0] != 0 {
		t.Fatalf("expected dp[0][0] = 0, got %d", dp[0][0])
	}
}

func TestComputeEdits_Identical(t *testing.T) {
	before := []string{"line1", "line2", "line3"}
	after := []string{"line1", "line2", "line3"}
	edits := computeEdits(before, after)
	if len(edits) != 0 {
		t.Fatalf("expected 0 edits for identical input, got %d", len(edits))
	}
}

func TestComputeEdits_Insert(t *testing.T) {
	before := []string{"line1", "line3"}
	after := []string{"line1", "line2", "line3"}
	edits := computeEdits(before, after)

	if len(edits) == 0 {
		t.Fatal("expected at least 1 edit")
	}
	hasInsert := false
	for _, e := range edits {
		if e.Type == "insert" {
			hasInsert = true
			break
		}
	}
	if !hasInsert {
		t.Errorf("expected at least one insert edit, got %+v", edits)
	}
}

func TestComputeEdits_Delete(t *testing.T) {
	before := []string{"line1", "line2", "line3"}
	after := []string{"line1", "line3"}
	edits := computeEdits(before, after)
	hasDelete := false
	for _, e := range edits {
		if e.Type == "delete" {
			hasDelete = true
			break
		}
	}
	if !hasDelete {
		t.Errorf("expected at least one delete edit, got %+v", edits)
	}
}

func TestComputeEdits_Empty(t *testing.T) {
	edits := computeEdits(nil, []string{"a", "b"})
	if len(edits) != 2 {
		t.Fatalf("expected 2 edits for insert from empty, got %d", len(edits))
	}
	for _, e := range edits {
		if e.Type != "insert" {
			t.Errorf("expected all insert edits, got %s", e.Type)
		}
	}

	edits = computeEdits([]string{"a", "b"}, nil)
	for _, e := range edits {
		if e.Type != "delete" {
			t.Errorf("expected all delete edits, got %s", e.Type)
		}
	}
}

func TestCountChanges(t *testing.T) {
	edits := []editOp{
		{Type: "insert"},
		{Type: "delete"},
		{Type: "insert"},
		{Type: "replace"},
	}
	add, del := countChanges(edits)
	if add != 3 {
		t.Errorf("expected 3 additions (2 insert + 1 replace), got %d", add)
	}
	if del != 2 {
		t.Errorf("expected 2 deletions (1 delete + 1 replace), got %d", del)
	}
}

func TestCountHunks(t *testing.T) {
	edits := []editOp{
		{Line: 1, Type: "insert"},
		{Line: 10, Type: "delete"},
	}
	n := countHunks(edits, 3)
	if n <= 0 || n > 2 {
		t.Errorf("expected 1-2 hunks for separated edits, got %d", n)
	}
}

func TestApplyUnifiedPatch(t *testing.T) {
	input := []string{"a", "b", "c", "d"}
	// Patch without ---/+++ headers (applyUnifiedPatch does not handle them)
	patch := []string{
		"@@ -2,2 +2,2 @@",
		" b",
		"-c",
		"+C",
		" d",
	}
	result, err := applyUnifiedPatch(input, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(result), result)
	}
	if result[2] != "C" {
		t.Errorf("expected line 3 to be 'C', got %q", result[2])
	}
}

func TestApplyUnifiedPatch_NoChanges(t *testing.T) {
	input := []string{"a", "b"}
	result, err := applyUnifiedPatch(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
}

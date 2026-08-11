package diff

import (
	"strings"
	"testing"
)

func TestUnified_NoChange(t *testing.T) {
	got := Unified("hello\nworld", "hello\nworld", "test.go", 3, 100)
	if got != "" {
		t.Errorf("expected empty diff for identical text, got %q", got)
	}
}

func TestUnified_PureInsertion(t *testing.T) {
	old := "A\nB\nC"
	new := "A\nX\nB\nC"
	got := Unified(old, new, "test.go", 1, 100)

	if !strings.Contains(got, "+X") {
		t.Errorf("diff should contain +X, got:\n%s", got)
	}
	// Should NOT mark B or C as removed (the old naive algorithm did this).
	if strings.Contains(got, "-B") {
		t.Errorf("diff should NOT contain -B (pure insertion), got:\n%s", got)
	}
	if strings.Contains(got, "-C") {
		t.Errorf("diff should NOT contain -C (pure insertion), got:\n%s", got)
	}
}

func TestUnified_PureDeletion(t *testing.T) {
	old := "A\nX\nB\nC"
	new := "A\nB\nC"
	got := Unified(old, new, "test.go", 1, 100)

	if !strings.Contains(got, "-X") {
		t.Errorf("diff should contain -X, got:\n%s", got)
	}
	// Should NOT mark B or C as added (the old naive algorithm did this).
	if strings.Contains(got, "+B") {
		t.Errorf("diff should NOT contain +B (pure deletion), got:\n%s", got)
	}
	if strings.Contains(got, "+C") {
		t.Errorf("diff should NOT contain +C (pure deletion), got:\n%s", got)
	}
}

func TestUnified_Modification(t *testing.T) {
	old := "line1\nline2\nline3"
	new := "line1\nMODIFIED\nline3"
	got := Unified(old, new, "test.go", 1, 100)

	if !strings.Contains(got, "-line2") {
		t.Errorf("diff should contain -line2, got:\n%s", got)
	}
	if !strings.Contains(got, "+MODIFIED") {
		t.Errorf("diff should contain +MODIFIED, got:\n%s", got)
	}
	// Context line should be present.
	if !strings.Contains(got, " line1") {
		t.Errorf("diff should contain context ' line1', got:\n%s", got)
	}
}

func TestUnified_NewFile(t *testing.T) {
	got := Unified("", "new content\nline2", "new.go", 3, 100)
	if got == "" {
		t.Fatal("expected non-empty diff for new file")
	}
	if !strings.Contains(got, "+new content") {
		t.Errorf("diff should contain +new content, got:\n%s", got)
	}
	if !strings.Contains(got, "+line2") {
		t.Errorf("diff should contain +line2, got:\n%s", got)
	}
}

func TestUnified_DeleteAll(t *testing.T) {
	got := Unified("content\nto delete", "", "del.go", 3, 100)
	if got == "" {
		t.Fatal("expected non-empty diff for deleted file")
	}
	if !strings.Contains(got, "-content") {
		t.Errorf("diff should contain -content, got:\n%s", got)
	}
	if !strings.Contains(got, "-to delete") {
		t.Errorf("diff should contain -to delete, got:\n%s", got)
	}
}

func TestUnified_Truncation(t *testing.T) {
	// Generate a large diff.
	var oldLines, newLines []string
	for i := 0; i < 100; i++ {
		oldLines = append(oldLines, "old"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		newLines = append(newLines, "new"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	old := strings.Join(oldLines, "\n")
	new := strings.Join(newLines, "\n")

	got := Unified(old, new, "big.go", 3, 20)
	if !strings.Contains(got, "truncated") {
		t.Errorf("diff should be truncated, got length %d", len(got))
	}
}

func TestUnified_MultipleHunks(t *testing.T) {
	// Changes far apart should produce separate hunks.
	old := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10"
	new := "line1\nCHANGED2\nline3\nline4\nline5\nline6\nline7\nline8\nCHANGED9\nline10"
	got := Unified(old, new, "multi.go", 1, 200)

	// Should have two @@ hunk headers.
	hunkCount := strings.Count(got, "@@")
	if hunkCount < 2 {
		t.Errorf("expected at least 2 hunks for distant changes, got %d:\n%s", hunkCount, got)
	}
}

func TestUnified_EmptyBoth(t *testing.T) {
	got := Unified("", "", "empty.go", 3, 100)
	if got != "" {
		t.Errorf("expected empty diff for both empty, got %q", got)
	}
}

func TestLcsDiff_PureInsertion(t *testing.T) {
	old := []string{"A", "B", "C"}
	new := []string{"A", "X", "B", "C"}
	diffs := lcsDiff(old, new)

	// Should be: context A, add X, context B, context C
	if len(diffs) != 4 {
		t.Fatalf("expected 4 diff lines, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Kind != KindContext || diffs[0].Content != "A" {
		t.Errorf("line 0: expected context A, got %+v", diffs[0])
	}
	if diffs[1].Kind != KindAdd || diffs[1].Content != "X" {
		t.Errorf("line 1: expected add X, got %+v", diffs[1])
	}
	if diffs[2].Kind != KindContext || diffs[2].Content != "B" {
		t.Errorf("line 2: expected context B, got %+v", diffs[2])
	}
	if diffs[3].Kind != KindContext || diffs[3].Content != "C" {
		t.Errorf("line 3: expected context C, got %+v", diffs[3])
	}
}

func TestLcsDiff_PureDeletion(t *testing.T) {
	old := []string{"A", "X", "B", "C"}
	new := []string{"A", "B", "C"}
	diffs := lcsDiff(old, new)

	// Should be: context A, del X, context B, context C
	if len(diffs) != 4 {
		t.Fatalf("expected 4 diff lines, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Kind != KindContext || diffs[0].Content != "A" {
		t.Errorf("line 0: expected context A, got %+v", diffs[0])
	}
	if diffs[1].Kind != KindDel || diffs[1].Content != "X" {
		t.Errorf("line 1: expected del X, got %+v", diffs[1])
	}
	if diffs[2].Kind != KindContext || diffs[2].Content != "B" {
		t.Errorf("line 2: expected context B, got %+v", diffs[2])
	}
	if diffs[3].Kind != KindContext || diffs[3].Content != "C" {
		t.Errorf("line 3: expected context C, got %+v", diffs[3])
	}
}

func TestSplitLines_EmptyString(t *testing.T) {
	got := splitLines("")
	if got != nil {
		t.Errorf("expected nil for empty string, got %v", got)
	}
}

func TestSplitLines_NoTrailingNewline(t *testing.T) {
	got := splitLines("a\nb\nc")
	if len(got) != 3 {
		t.Errorf("expected 3 lines, got %d: %v", len(got), got)
	}
}

func TestSplitLines_TrailingNewline(t *testing.T) {
	got := splitLines("a\nb\nc\n")
	if len(got) != 3 {
		t.Errorf("expected 3 lines (trailing empty trimmed), got %d: %v", len(got), got)
	}
}

package agent

import (
	"testing"
)

func TestComputeProposedContent_WriteFile(t *testing.T) {
	args := map[string]interface{}{"content": "new content"}
	got, ok := computeProposedContent("write_file", args, "old content")
	if !ok {
		t.Fatal("expected ok=true for write_file")
	}
	if got != "new content" {
		t.Errorf("write_file: got %q, want %q", got, "new content")
	}
}

func TestComputeProposedContent_Write(t *testing.T) {
	args := map[string]interface{}{"content": "via write alias"}
	got, ok := computeProposedContent("write", args, "old")
	if !ok {
		t.Fatal("expected ok=true for write alias")
	}
	if got != "via write alias" {
		t.Errorf("write: got %q, want %q", got, "via write alias")
	}
}

func TestComputeProposedContent_EditBlock_Single(t *testing.T) {
	old := "line1\nold middle\nline3"
	args := map[string]interface{}{
		"path": "test.go",
		"old":  "old middle",
		"new":  "new middle",
	}
	got, ok := computeProposedContent("edit_block", args, old)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "line1\nnew middle\nline3"
	if got != want {
		t.Errorf("edit_block single: got %q, want %q", got, want)
	}
}

func TestComputeProposedContent_EditBlock_EditsArray(t *testing.T) {
	old := "alpha\nbeta\ngamma"
	args := map[string]interface{}{
		"path": "test.go",
		"edits": []interface{}{
			map[string]interface{}{"oldText": "alpha", "newText": "ALPHA"},
			map[string]interface{}{"oldText": "beta", "newText": "BETA"},
		},
	}
	got, ok := computeProposedContent("edit_block", args, old)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "ALPHA\nBETA\ngamma"
	if got != want {
		t.Errorf("edit_block array: got %q, want %q", got, want)
	}
}

func TestComputeProposedContent_Edit(t *testing.T) {
	old := "func foo() {\n\treturn nil\n}"
	args := map[string]interface{}{
		"file_path": "test.go",
		"old_text":  "return nil",
		"new_text":  "return err",
	}
	got, ok := computeProposedContent("edit", args, old)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "func foo() {\n\treturn err\n}"
	if got != want {
		t.Errorf("edit: got %q, want %q", got, want)
	}
}

func TestComputeProposedContent_Edit_NoMatch(t *testing.T) {
	args := map[string]interface{}{
		"file_path": "test.go",
		"old_text":  "nonexistent",
		"new_text":  "replacement",
	}
	// When oldText doesn't match, Replace returns the original string unchanged.
	got, ok := computeProposedContent("edit", args, "original content")
	if !ok {
		t.Fatal("expected ok=true even when no match (Replace is a no-op)")
	}
	if got != "original content" {
		t.Errorf("edit no-match: got %q, want %q", got, "original content")
	}
}

func TestComputeProposedContent_AppendFile(t *testing.T) {
	args := map[string]interface{}{"content": "\nappended line"}
	got, ok := computeProposedContent("append_file", args, "original")
	if !ok {
		t.Fatal("expected ok=true for append_file")
	}
	want := "original\nappended line"
	if got != want {
		t.Errorf("append_file: got %q, want %q", got, want)
	}
}

func TestComputeProposedContent_AppendFile_EmptyContent(t *testing.T) {
	args := map[string]interface{}{"content": ""}
	_, ok := computeProposedContent("append_file", args, "original")
	if ok {
		t.Error("expected ok=false for append_file with empty content")
	}
}

func TestComputeProposedContent_UnknownTool(t *testing.T) {
	_, ok := computeProposedContent("unknown_tool", map[string]interface{}{}, "")
	if ok {
		t.Error("expected ok=false for unknown tool")
	}
}

func TestGenerateSimpleDiff_NoChange(t *testing.T) {
	got := generateSimpleDiff("same", "same", "file.go")
	if got != "" {
		t.Errorf("expected empty diff for identical content, got %q", got)
	}
}

func TestGenerateSimpleDiff_BasicChange(t *testing.T) {
	old := "line1\nline2\nline3"
	new := "line1\nMODIFIED\nline3"
	got := generateSimpleDiff(old, new, "test.go")
	if got == "" {
		t.Fatal("expected non-empty diff")
	}
	if !contains(got, "-line2") {
		t.Errorf("diff should contain removed line2, got:\n%s", got)
	}
	if !contains(got, "+MODIFIED") {
		t.Errorf("diff should contain added MODIFIED, got:\n%s", got)
	}
}

func TestGenerateSimpleDiff_NewFile(t *testing.T) {
	got := generateSimpleDiff("", "new content", "new.go")
	if got == "" {
		t.Fatal("expected non-empty diff for new file")
	}
	if !contains(got, "+new content") {
		t.Errorf("diff should contain added content, got:\n%s", got)
	}
}

// TestGenerateSimpleDiff_PureInsertion verifies that inserting a single line
// in the middle of a file produces a minimal diff (just the +line), NOT a
// noisy diff that marks all subsequent lines as removed and re-added.
// This was the critical bug in the old naive forward-scan algorithm.
func TestGenerateSimpleDiff_PureInsertion(t *testing.T) {
	old := "A\nB\nC"
	new := "A\nX\nB\nC"
	got := generateSimpleDiff(old, new, "test.go")
	if got == "" {
		t.Fatal("expected non-empty diff for insertion")
	}
	if !contains(got, "+X") {
		t.Errorf("diff should contain +X, got:\n%s", got)
	}
	// The old algorithm would mark B and C as removed and re-added.
	// The LCS algorithm should NOT do this.
	if contains(got, "-B") {
		t.Errorf("diff should NOT contain -B (pure insertion), got:\n%s", got)
	}
	if contains(got, "-C") {
		t.Errorf("diff should NOT contain -C (pure insertion), got:\n%s", got)
	}
}

// TestGenerateSimpleDiff_PureDeletion verifies that deleting a single line
// produces a minimal diff (just the -line), NOT a noisy diff.
func TestGenerateSimpleDiff_PureDeletion(t *testing.T) {
	old := "A\nX\nB\nC"
	new := "A\nB\nC"
	got := generateSimpleDiff(old, new, "test.go")
	if got == "" {
		t.Fatal("expected non-empty diff for deletion")
	}
	if !contains(got, "-X") {
		t.Errorf("diff should contain -X, got:\n%s", got)
	}
	// The old algorithm would mark B as added (when it was already there).
	if contains(got, "+B") {
		t.Errorf("diff should NOT contain +B (pure deletion), got:\n%s", got)
	}
}

func TestDiffApprovalTools_CoversAllSimulatableTools(t *testing.T) {
	// Every tool in diffApprovalTools must be supported by computeProposedContent.
	for toolName := range diffApprovalTools {
		switch toolName {
		case "write_file", "write", "edit_block", "edit", "append_file":
			// supported
		default:
			t.Errorf("tool %q is in diffApprovalTools but not in computeProposedContent", toolName)
		}
	}
}

// contains is a simple substring check.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

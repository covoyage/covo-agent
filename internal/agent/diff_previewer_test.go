package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractPatchFilePaths(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: a.go\n*** Add File: b.go\n*** Delete File: c.go\n*** Move File: old.go -> new.go\n*** End Patch"
	arguments, err := json.Marshal(map[string]string{"patch": patch})
	if err != nil {
		t.Fatal(err)
	}
	got := extractPatchFilePaths(string(arguments))
	want := []string{"a.go", "b.go", "c.go", "old.go"}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("paths = %v, want %v", got, want)
		}
	}
}

func TestDiffPreviewerGeneratesUnifiedDiff(t *testing.T) {
	workingDir := t.TempDir()
	path := filepath.Join(workingDir, "sample.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previewer := &diffPreviewer{workingDir: workingDir}
	context := previewer.captureOldContent("write_file", `{"path":"sample.txt"}`)
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	previews := previewer.generateDiffs(context, "")
	if len(previews) != 1 {
		t.Fatalf("preview count = %d, want 1", len(previews))
	}
	if previews[0].Path != "sample.txt" {
		t.Fatalf("preview path = %q", previews[0].Path)
	}
	if !strings.Contains(previews[0].Unified, "-before") || !strings.Contains(previews[0].Unified, "+after") {
		t.Fatalf("unexpected unified diff: %q", previews[0].Unified)
	}
}

func TestDiffFromToolResult(t *testing.T) {
	preview, ok := diffFromToolResult("edit_block", `{"path":"sample.go","diff":"-old\n+new"}`)
	if !ok {
		t.Fatal("edit_block result did not produce a diff")
	}
	if preview.Path != "sample.go" || preview.Unified != "-old\n+new" {
		t.Fatalf("preview = %+v", preview)
	}
	if _, ok := diffFromToolResult("write_file", `{}`); ok {
		t.Fatal("unsupported tool produced a result diff")
	}
}

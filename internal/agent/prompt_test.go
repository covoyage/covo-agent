package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadContextFilesIncludesInstructionFilesAndAncestors(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "pkg", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("root instruction"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root agents"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "COVO.md"), []byte("nested covo"), 0o644); err != nil {
		t.Fatal(err)
	}

	pb := NewPromptBuilder(nil, nested)
	files := pb.loadContextFiles()
	got := map[string]string{}
	for _, f := range files {
		got[f.Name] = f.Content
	}
	if !strings.Contains(got["COVO.md"], "nested covo") {
		t.Fatalf("missing nested COVO.md: %+v", got)
	}
	if !contextFileContains(got, "CLAUDE.md", "root instruction") {
		t.Fatalf("missing ancestor CLAUDE.md: %+v", got)
	}
	if !contextFileContains(got, "AGENTS.md", "root agents") {
		t.Fatalf("missing ancestor AGENTS.md: %+v", got)
	}
}

func TestLoadContextFilesPrefersCloserFile(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("outer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("inner"), 0o644); err != nil {
		t.Fatal(err)
	}

	pb := NewPromptBuilder(nil, nested)
	files := pb.loadContextFiles()
	var agents string
	for _, f := range files {
		if strings.EqualFold(filepath.Base(f.Name), "AGENTS.md") {
			agents = f.Content
			break
		}
	}
	if !strings.Contains(agents, "inner") {
		t.Fatalf("expected closer AGENTS.md, got %q", agents)
	}
	if strings.Contains(agents, "outer") {
		t.Fatalf("should not load both AGENTS.md copies, got %q", agents)
	}
}

func contextFileContains(got map[string]string, name, snippet string) bool {
	for k, v := range got {
		if strings.EqualFold(filepath.Base(k), name) && strings.Contains(v, snippet) {
			return true
		}
	}
	return false
}

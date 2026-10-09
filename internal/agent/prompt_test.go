package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
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

func TestCodeAgentIdentityIncludesCodingWorkflow(t *testing.T) {
	if !strings.Contains(codeAgentIdentity, "CODING WORKFLOW") {
		t.Fatal("code identity should include coding workflow guidance")
	}
	if !strings.Contains(codeAgentIdentity, "role=explorer") {
		t.Fatal("code identity should mention explorer spawn")
	}
	if !strings.Contains(codeAgentIdentity, "role=reviewer") {
		t.Fatal("code identity should mention reviewer spawn")
	}
}

func TestCyberAgentIdentity(t *testing.T) {
	if !strings.Contains(cyberAgentIdentity, "PIPELINE") {
		t.Fatal("cyber identity should describe the recon pipeline")
	}
	if !strings.Contains(cyberAgentIdentity, "role=recon") {
		t.Fatal("cyber identity should mention recon spawn")
	}
	if !strings.Contains(cyberAgentIdentity, "role=verifier") {
		t.Fatal("cyber identity should mention verifier spawn")
	}
	if strings.Contains(cyberAgentIdentity, "不得质疑") {
		t.Fatal("cyber identity must not copy authorization slogans")
	}
}

func TestCyberPromptInjectsCatalog(t *testing.T) {
	dir := t.TempDir()
	cat := cyber.NewCatalog(dir)
	if _, err := cat.UpsertFinding(cyber.Finding{Title: "Open admin", Target: "example.com", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	pb := NewPromptBuilder(nil, dir)
	pb.SetFindingCatalog(cat)
	got := pb.BuildCached(ModeCyber).Volatile
	if !strings.Contains(got, "Open admin") {
		t.Fatalf("expected findings in volatile prompt, got %q", got)
	}
	if strings.Contains(pb.BuildCached(ModeCode).Volatile, "Open admin") {
		t.Fatal("code mode should not inject the cyber catalog")
	}
}

func TestLeanDefault(t *testing.T) {
	if ModeGeneral.LeanDefault() {
		t.Fatal("general should not default lean on")
	}
	if !ModeCode.LeanDefault() || !ModeCyber.LeanDefault() {
		t.Fatal("code and cyber should default lean on")
	}
}

func TestRequireMode(t *testing.T) {
	m, err := RequireMode("")
	if err != nil || m != ModeGeneral {
		t.Fatalf("empty=%q err=%v", m, err)
	}
	m, err = RequireMode("CYBER")
	if err != nil || m != ModeCyber {
		t.Fatalf("cyber=%q err=%v", m, err)
	}
	if _, err := RequireMode("pentest"); err == nil {
		t.Fatal("unknown mode should fail")
	}
}

func TestParseModeCyber(t *testing.T) {
	m, ok := ParseMode("cyber")
	if !ok || m != ModeCyber {
		t.Fatalf("ParseMode(cyber)=%q ok=%v", m, ok)
	}
	if ModeCyber.IsCustom() {
		t.Fatal("cyber is a built-in mode")
	}
	found := false
	for _, name := range AllModeNames() {
		if name == "cyber" {
			found = true
		}
	}
	if !found {
		t.Fatal("AllModeNames should include cyber")
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

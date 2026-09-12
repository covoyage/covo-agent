package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/covoyage/covonaut/skill"
)

func writeSkillMD(t *testing.T, dir, name, body string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFilterLoadedSkills_DisabledAndMissingBinary(t *testing.T) {
	root := t.TempDir()
	keepPath := writeSkillMD(t, root, "keep", `---
name: keep
description: always available
---
body`)
	gonePath := writeSkillMD(t, root, "gone", `---
name: gone
description: user disabled
---
body`)
	missingBin := writeSkillMD(t, root, "needs-bin", `---
name: needs-bin
description: requires a missing binary
prerequisites:
  commands: [definitely-not-a-real-covo-binary]
---
body`)
	wrongOS := writeSkillMD(t, root, "mac-only", `---
name: mac-only
description: macos only
platforms: [macos]
---
body`)

	skills := []skill.Skill{
		{Name: "keep", FilePath: keepPath},
		{Name: "gone", FilePath: gonePath},
		{Name: "needs-bin", FilePath: missingBin},
		{Name: "mac-only", FilePath: wrongOS},
	}

	got := filterLoadedSkills(skills, []string{"gone"}, "linux")
	if len(got) != 1 || got[0].Name != "keep" {
		t.Fatalf("got %+v, want only keep", got)
	}
}

func TestFilterLoadedSkills_PresentBinaryKept(t *testing.T) {
	root := t.TempDir()
	path := writeSkillMD(t, root, "has-go", `---
name: has-go
description: needs go
prerequisites:
  commands: [go]
---
body`)
	got := filterLoadedSkills([]skill.Skill{{Name: "has-go", FilePath: path}}, nil, runtime.GOOS)
	if len(got) != 1 {
		t.Fatalf("expected go-gated skill to remain, got %d", len(got))
	}
}

func TestFilterLoadedSkills_EnvVarsAlias(t *testing.T) {
	root := t.TempDir()
	path := writeSkillMD(t, root, "needs-key", `---
name: needs-key
description: requires env var alias
prerequisites:
  env_vars: [COVO_TEST_MISSING_SKILL_ENV]
---
body`)
	got := filterLoadedSkills([]skill.Skill{{Name: "needs-key", FilePath: path}}, nil, runtime.GOOS)
	if len(got) != 0 {
		t.Fatalf("env_vars should gate the skill, got %d", len(got))
	}
}

func TestPlatformMatches(t *testing.T) {
	if !platformMatches([]string{"macos"}, "darwin") {
		t.Fatal("macos should match darwin")
	}
	if platformMatches([]string{"linux"}, "darwin") {
		t.Fatal("linux should not match darwin")
	}
	if !platformMatches(nil, "linux") {
		t.Fatal("empty platforms should match")
	}
}

func TestInspectSkillCatalog_SourceAndReasons(t *testing.T) {
	root := t.TempDir()
	keepPath := writeSkillMD(t, root, "keep", `---
name: keep
description: always available
---
body`)
	gonePath := writeSkillMD(t, root, "gone", `---
name: gone
description: user disabled
---
body`)
	missingBin := writeSkillMD(t, root, "needs-bin", `---
name: needs-bin
description: requires a missing binary
prerequisites:
  commands: [definitely-not-a-real-covo-binary]
---
body`)
	entries := inspectSkillCatalog([]skill.Skill{
		{Name: "keep", FilePath: keepPath},
		{Name: "gone", FilePath: gonePath},
		{Name: "needs-bin", FilePath: missingBin},
	}, []string{"gone"}, "linux", root)
	byName := map[string]SkillCatalogEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if !byName["keep"].Kept || byName["keep"].Source != "extra" {
		t.Fatalf("keep = %+v", byName["keep"])
	}
	if byName["gone"].Kept || byName["gone"].Reason != "disabled" {
		t.Fatalf("gone = %+v", byName["gone"])
	}
	if byName["needs-bin"].Kept || byName["needs-bin"].Reason != "missing definitely-not-a-real-covo-binary" {
		t.Fatalf("needs-bin = %+v", byName["needs-bin"])
	}
}

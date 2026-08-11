package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillManagerPatchNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skill-manager-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(filepath.Join(skillsDir, "weather"), 0755)

	content := `---
name: weather
description: 查询天气
---

# 查询天气

## 步骤

1. 使用 curl 查询天气 API
2. 解析返回的天气数据
`
	err = os.WriteFile(filepath.Join(skillsDir, "weather", "SKILL.md"), []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewSkillManager(skillsDir, nil)

	// Test with non-existent string
	err = mgr.Patch("weather", "使用 httpx 查询天气 API", "使用 requests 查询天气 API")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "searched:") {
		t.Errorf("expected error to contain 'searched:', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "closest match:") {
		t.Errorf("expected error to contain 'closest match:', got: %s", errMsg)
	}
}

func TestSkillManagerPatchSuccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skill-manager-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(filepath.Join(skillsDir, "weather"), 0755)

	content := `---
name: weather
description: 查询天气
---

# 查询天气

## 步骤

1. 使用 curl 查询天气 API
2. 解析返回的天气数据
`
	err = os.WriteFile(filepath.Join(skillsDir, "weather", "SKILL.md"), []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewSkillManager(skillsDir, nil)

	// Test with existing string
	err = mgr.Patch("weather", "使用 curl 查询天气 API", "使用 httpx 查询天气 API")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the change
	data, err := os.ReadFile(filepath.Join(skillsDir, "weather", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "使用 httpx 查询天气 API") {
		t.Errorf("expected file to contain new string, got: %s", string(data))
	}
}

// TestSkillManagerCategoryNestedSkill verifies that a skill stored under a
// category subdirectory (skillsDir/<category>/<name>) can be resolved by its
// bare name — the layout used by bundled skills like github/codebase-inspection.
func TestSkillManagerCategoryNestedSkill(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	nested := filepath.Join(skillsDir, "github", "codebase-inspection")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: codebase-inspection
description: inspect a codebase
---

# Codebase Inspection

original body
`
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewSkillManager(skillsDir, nil)

	// Read by bare name must find the category-nested skill.
	got, err := mgr.Read("codebase-inspection")
	if err != nil {
		t.Fatalf("Read by bare name failed: %v", err)
	}
	if !strings.Contains(got, "name: codebase-inspection") {
		t.Errorf("unexpected content: %s", got)
	}

	// Patch by bare name must also resolve and edit the nested file.
	if err := mgr.Patch("codebase-inspection", "original body", "patched body"); err != nil {
		t.Fatalf("Patch by bare name failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(nested, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "patched body") {
		t.Errorf("patch did not apply to nested skill: %s", string(data))
	}

	// A genuinely missing skill still errors.
	if _, err := mgr.Read("does-not-exist"); err == nil {
		t.Error("expected error reading non-existent skill")
	}
}

func TestSkillManagerRejectsUnsafeSkillNames(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skill-manager-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, "skills")
	usage := NewSkillUsageTracker(skillsDir)
	mgr := NewSkillManager(skillsDir, usage)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../escape", "nested/name", "/absolute", ".archive", "bad name"} {
		t.Run(name, func(t *testing.T) {
			if _, err := mgr.Create(name, "description", "body"); err == nil {
				t.Fatal("expected invalid skill name error")
			}
		})
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "escape")); !os.IsNotExist(err) {
		t.Fatalf("unsafe skill name created path outside skills dir: %v", err)
	}
}

func TestSkillManagerRejectsUnsafeRelativePaths(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skill-manager-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, "skills")
	usage := NewSkillUsageTracker(skillsDir)
	mgr := NewSkillManager(skillsDir, usage)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create("weather", "description", "body"); err != nil {
		t.Fatal(err)
	}

	for _, relPath := range []string{"../escape.txt", "references/../../escape.txt", "/tmp/escape.txt", "."} {
		t.Run(relPath, func(t *testing.T) {
			if err := mgr.WriteFile("weather", relPath, "content"); err == nil {
				t.Fatal("expected unsafe relative path error")
			}
		})
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("unsafe relative path created file outside skills dir: %v", err)
	}
}

func TestSkillManagerSupportsCategoryQualifiedDuplicateNames(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), "skills")
	for _, entry := range []struct {
		id   string
		body string
	}{
		{id: "deploy", body: "flat body"},
		{id: "alpha/deploy", body: "alpha body"},
		{id: "beta/deploy", body: "beta body"},
	} {
		dir := filepath.Join(skillsDir, filepath.FromSlash(entry.id))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: deploy\ndescription: test\n---\n\n" + entry.body + "\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	usage := NewSkillUsageTracker(skillsDir)
	mgr := NewSkillManager(skillsDir, usage)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	if content, err := mgr.Read("deploy"); err != nil || !strings.Contains(content, "flat body") {
		t.Fatalf("bare read = %q, %v", content, err)
	}
	if content, err := mgr.Read("alpha/deploy"); err != nil || !strings.Contains(content, "alpha body") {
		t.Fatalf("qualified read = %q, %v", content, err)
	}
	if err := mgr.Edit("beta/deploy", "updated beta"); err != nil {
		t.Fatal(err)
	}
	if content, err := mgr.Read("beta/deploy"); err != nil || !strings.Contains(content, "updated beta") {
		t.Fatalf("qualified edit = %q, %v", content, err)
	}
	if err := mgr.Delete("alpha/deploy"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "beta", "deploy", "SKILL.md")); err != nil {
		t.Fatalf("deleting alpha affected beta: %v", err)
	}
}

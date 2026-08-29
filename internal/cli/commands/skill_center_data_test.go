package commands

import (
	"path/filepath"
	"testing"

	"github.com/covoyage/covo-agent/internal/evolution"
	covoskill "github.com/covoyage/covonaut/skill"
)

func TestBuildSkillCenterDataUsesRuntimeInventoryAndStableIDs(t *testing.T) {
	dir := t.TempDir()
	firstDir := filepath.Join(dir, "alpha", "deploy")
	secondDir := filepath.Join(dir, "beta", "deploy")
	usage := evolution.NewSkillUsageTracker(dir)
	if err := usage.ReconcileSkills(map[string]evolution.SkillRegistration{
		"alpha/deploy": {Name: "deploy", Provenance: "bundled"},
		"beta/deploy":  {Name: "deploy", Provenance: "installed"},
	}); err != nil {
		t.Fatal(err)
	}
	usage.RecordView("beta/deploy")

	items, paths := buildSkillCenterData(
		[]covoskill.Skill{
			{Name: "deploy", BaseDir: firstDir, FilePath: filepath.Join(firstDir, "SKILL.md")},
			{Name: "deploy", BaseDir: secondDir, FilePath: filepath.Join(secondDir, "SKILL.md")},
		},
		[]evolution.SkillInfo{
			{ID: "alpha/deploy", SkillFrontmatter: evolution.SkillFrontmatter{Name: "deploy"}, Dir: firstDir, Category: "alpha"},
			{ID: "beta/deploy", SkillFrontmatter: evolution.SkillFrontmatter{Name: "deploy"}, Dir: secondDir, Category: "beta"},
		},
		usage,
	)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	if items[0].Name != "alpha/deploy" || items[1].Name != "beta/deploy" {
		t.Fatalf("duplicate display names = %+v", items)
	}
	if items[1].UseCount != 1 || items[1].Provenance != "installed" {
		t.Fatalf("usage overlay = %+v", items[1])
	}
	if paths["alpha/deploy"] != filepath.Join(firstDir, "SKILL.md") {
		t.Fatalf("skill path = %q", paths["alpha/deploy"])
	}
}

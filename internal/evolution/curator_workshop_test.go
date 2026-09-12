package evolution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProposeOrCreateSkill_WritesProposalNotSkill(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	usage := NewSkillUsageTracker(skillsDir)
	mgr := NewSkillManager(skillsDir, usage)
	store := NewWorkshopStore(home)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}

	c := NewCurator(skillsDir, usage, CuratorConfig{}, nil)
	c.SetSkillManager(mgr)
	c.SetWorkshopStore(store)

	err := c.proposeOrCreateSkill(ExtractionCandidate{
		Name:        "session-review",
		Description: "Use when reviewing a session",
		Body:        "# Session Review\n\nDo the thing.",
		Confidence:  0.9,
	})
	if err != nil {
		t.Fatal(err)
	}

	pending := store.ListPending()
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if pending[0].SkillName != "session-review" {
		t.Fatalf("skill name = %q", pending[0].SkillName)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "session-review", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("expected no live skill until the proposal is applied")
	}
}

func TestProposeOrCreateSkill_WithoutWorkshopCreatesSkill(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	usage := NewSkillUsageTracker(skillsDir)
	mgr := NewSkillManager(skillsDir, usage)

	c := NewCurator(skillsDir, usage, CuratorConfig{}, nil)
	c.SetSkillManager(mgr)

	err := c.proposeOrCreateSkill(ExtractionCandidate{
		Name:        "session-review",
		Description: "Use when reviewing a session",
		Body:        "# Session Review\n\nDo the thing.",
		Confidence:  0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "session-review", "SKILL.md")); err != nil {
		t.Fatalf("expected live skill when workshop is unset: %v", err)
	}
}

package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkshopRecoversAlreadyArchivedExternalSkill(t *testing.T) {
	home := t.TempDir()
	manager := NewSkillManager(filepath.Join(home, "skills"), nil)
	if err := os.MkdirAll(manager.skillsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create("sample", "description", "body"); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(home, "extra")
	if err := os.MkdirAll(filepath.Join(extra, "category"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(manager.skillsDir, "sample"), filepath.Join(extra, "category", "sample")); err != nil {
		t.Fatal(err)
	}
	manager.extraRoots = []string{extra}
	store := NewWorkshopStore(home)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	if err := store.BindSkillManager(manager); err != nil {
		t.Fatal(err)
	}
	p, err := store.Create(ProposalDelete, "sample", "", "reason")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.preparePlan(*p)
	if err != nil {
		t.Fatal(err)
	}
	store.items[0].Plan, store.items[0].Status = plan, ProposalApplying
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(plan.Archive), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Dir(p.TargetPath), plan.Archive); err != nil {
		t.Fatal(err)
	}
	restarted := NewWorkshopStore(home)
	if err := restarted.Init(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.BindSkillManager(manager); err != nil {
		t.Fatal(err)
	}
	if restarted.List()[0].Status != ProposalApplied {
		t.Fatal("archived external skill not recovered")
	}
}

func TestWorkshopRejectsCorruptStateAndCopiesPlans(t *testing.T) {
	store := NewWorkshopStore(t.TempDir())
	if err := os.WriteFile(store.filePath, []byte("[broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err == nil {
		t.Fatal("corrupt state silently discarded")
	}
	store.items = []SkillProposal{{ID: "sample", Plan: &SkillApplyPlan{Content: "original"}}}
	listed := store.List()
	listed[0].Plan.Content = "changed"
	proposal, err := store.Get("sample")
	if err != nil {
		t.Fatal(err)
	}
	proposal.Plan.Content = "changed again"
	if store.items[0].Plan.Content != "original" {
		t.Fatal("plan snapshot shares mutable state")
	}
}

func TestWorkshopCommitConflictAndRecovery(t *testing.T) {
	home := t.TempDir()
	manager := NewSkillManager(filepath.Join(home, "skills"), nil)
	if err := os.MkdirAll(manager.skillsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create("sample", "sample description", "original"); err != nil {
		t.Fatal(err)
	}
	store := NewWorkshopStore(home)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	if err := store.BindSkillManager(manager); err != nil {
		t.Fatal(err)
	}
	proposal, err := store.Create(ProposalUpdate, "sample", "replacement", "reason")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Edit("sample", "human edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplySkill(proposal.ID); err == nil {
		t.Fatal("intervening edit overwritten")
	}
	store.items = nil
	proposal, err = store.Create(ProposalUpdate, "sample", "replacement", "reason")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.preparePlan(*proposal)
	if err != nil {
		t.Fatal(err)
	}
	store.items[0].Plan, store.items[0].Status = plan, ProposalApplying
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if err := atomicWorkshopWrite(proposal.TargetPath, []byte(plan.Content)); err != nil {
		t.Fatal(err)
	}
	restarted := NewWorkshopStore(home)
	if err := restarted.Init(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.BindSkillManager(manager); err != nil {
		t.Fatal(err)
	}
	if restarted.List()[0].Status != ProposalApplied {
		t.Fatal("published plan not recovered")
	}
	content, err := manager.Read("sample")
	if err != nil || !strings.Contains(content, "replacement") {
		t.Fatalf("content=%q error=%v", content, err)
	}
}

func TestWorkshopRecoversUnpublishedCreateAndArchive(t *testing.T) {
	for _, kind := range []SkillProposalKind{ProposalCreate, ProposalDelete} {
		t.Run(string(kind), func(t *testing.T) {
			home := t.TempDir()
			manager := NewSkillManager(filepath.Join(home, "skills"), nil)
			if err := os.MkdirAll(manager.skillsDir, 0700); err != nil {
				t.Fatal(err)
			}
			if kind == ProposalDelete {
				if _, err := manager.Create("sample", "description", "body"); err != nil {
					t.Fatal(err)
				}
			}
			store := NewWorkshopStore(home)
			if err := store.Init(); err != nil {
				t.Fatal(err)
			}
			if err := store.BindSkillManager(manager); err != nil {
				t.Fatal(err)
			}
			proposal, err := store.Create(kind, "sample", "body", "description")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := store.preparePlan(*proposal)
			if err != nil {
				t.Fatal(err)
			}
			store.items[0].Plan, store.items[0].Status = plan, ProposalApplying
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			restarted := NewWorkshopStore(home)
			if err := restarted.Init(); err != nil {
				t.Fatal(err)
			}
			if err := restarted.BindSkillManager(manager); err != nil {
				t.Fatal(err)
			}
			if restarted.List()[0].Status != ProposalApplied {
				t.Fatal("plan not recovered")
			}
			if kind == ProposalCreate {
				if _, err := manager.Read("sample"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := os.Stat(plan.Archive); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

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

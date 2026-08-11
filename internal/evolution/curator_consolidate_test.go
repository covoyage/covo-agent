package evolution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestSkill(t *testing.T, skillsDir, name, description string) {
	t.Helper()
	dir := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func newTestCuratorForConsolidation(t *testing.T) (*Curator, *SkillUsageTracker, string) {
	t.Helper()
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}

	usage := NewSkillUsageTracker(skillsDir)
	mgr := NewSkillManager(skillsDir, usage)

	c := NewCurator(skillsDir, usage, CuratorConfig{}, nil)
	c.SetSkillManager(mgr)

	return c, usage, skillsDir
}

func TestConsolidateSkillsRequiresSkillManager(t *testing.T) {
	usage := NewSkillUsageTracker(t.TempDir())
	c := NewCurator("", usage, CuratorConfig{}, nil)

	_, err := c.ConsolidateSkills(context.Background(), func(ctx context.Context, sys, user string) (string, error) {
		return `{"clusters":[]}`, nil
	})
	if err == nil {
		t.Fatal("expected error when skill manager is not linked")
	}
}

func TestConsolidateSkillsRequiresLLM(t *testing.T) {
	c, _, _ := newTestCuratorForConsolidation(t)
	_, err := c.ConsolidateSkills(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when llmCall is nil")
	}
}

func TestConsolidateSkillsSkipsWithFewerThanTwoCandidates(t *testing.T) {
	c, usage, skillsDir := newTestCuratorForConsolidation(t)
	writeTestSkill(t, skillsDir, "deploy-to-staging", "Deploy the app to staging")
	if err := usage.RegisterSkill("deploy-to-staging", "agent-created"); err != nil {
		t.Fatal(err)
	}

	called := false
	report, err := c.ConsolidateSkills(context.Background(), func(ctx context.Context, sys, user string) (string, error) {
		called = true
		return `{"clusters":[]}`, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("LLM should not be called with fewer than 2 candidate skills")
	}
	if report.TotalSkills != 1 {
		t.Fatalf("expected TotalSkills=1, got %d", report.TotalSkills)
	}
}

func TestConsolidateSkillsReturnsClustersFromLLM(t *testing.T) {
	c, usage, skillsDir := newTestCuratorForConsolidation(t)
	writeTestSkill(t, skillsDir, "deploy-to-staging", "Deploy the app to the staging environment")
	writeTestSkill(t, skillsDir, "deploy-staging-env", "Push a build to staging")
	writeTestSkill(t, skillsDir, "unrelated-skill", "Query the weather API")
	for _, name := range []string{"deploy-to-staging", "deploy-staging-env", "unrelated-skill"} {
		if err := usage.RegisterSkill(name, "agent-created"); err != nil {
			t.Fatal(err)
		}
	}

	var capturedPrompt string
	report, err := c.ConsolidateSkills(context.Background(), func(ctx context.Context, sys, user string) (string, error) {
		capturedPrompt = user
		return "```json\n" + `{"clusters":[{"skills":["deploy-to-staging","deploy-staging-env"],"reason":"both deploy to staging","suggestion":"merge into deploy-staging"}]}` + "\n```", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalSkills != 3 {
		t.Fatalf("expected TotalSkills=3, got %d", report.TotalSkills)
	}
	if len(report.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d: %+v", len(report.Clusters), report.Clusters)
	}
	cluster := report.Clusters[0]
	if len(cluster.Skills) != 2 || cluster.Skills[0] != "deploy-to-staging" || cluster.Skills[1] != "deploy-staging-env" {
		t.Fatalf("unexpected cluster skills: %+v", cluster.Skills)
	}
	if cluster.Suggestion != "merge into deploy-staging" {
		t.Fatalf("unexpected suggestion: %q", cluster.Suggestion)
	}
	if !strings.Contains(capturedPrompt, "deploy-to-staging") || !strings.Contains(capturedPrompt, "unrelated-skill") {
		t.Fatalf("expected prompt to list all candidate skills, got: %s", capturedPrompt)
	}
}

func TestConsolidateSkillsExcludesArchivedSkills(t *testing.T) {
	c, usage, skillsDir := newTestCuratorForConsolidation(t)
	writeTestSkill(t, skillsDir, "skill-a", "Does a thing")
	writeTestSkill(t, skillsDir, "skill-b", "Does another thing")
	if err := usage.RegisterSkill("skill-a", "agent-created"); err != nil {
		t.Fatal(err)
	}
	if err := usage.RegisterSkill("skill-b", "agent-created"); err != nil {
		t.Fatal(err)
	}
	if err := usage.SetState("skill-b", StateArchived); err != nil {
		t.Fatal(err)
	}

	called := false
	report, err := c.ConsolidateSkills(context.Background(), func(ctx context.Context, sys, user string) (string, error) {
		called = true
		return `{"clusters":[]}`, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("archived skill should reduce candidates below 2, so LLM should not be called")
	}
	if report.TotalSkills != 1 {
		t.Fatalf("expected only 1 non-archived candidate, got %d", report.TotalSkills)
	}
}

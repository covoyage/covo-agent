package evolution

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCuratorDoesNotAgeBundledSkills(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), "skills")
	usage := NewSkillUsageTracker(skillsDir)
	if err := usage.ReconcileSkills(map[string]SkillRegistration{
		"bundled-skill": {Name: "bundled-skill", Provenance: "bundled"},
	}); err != nil {
		t.Fatal(err)
	}
	usage.mu.Lock()
	usage.records["bundled-skill"].LastActivityAt = time.Now().Add(-365 * 24 * time.Hour).UTC().Format(time.RFC3339)
	usage.mu.Unlock()

	curator := NewCurator(skillsDir, usage, CuratorConfig{StaleAfterDays: 1, ArchiveAfterDays: 2}, nil)
	curator.run()

	record, ok := usage.GetRecord("bundled-skill")
	if !ok {
		t.Fatal("bundled record missing")
	}
	if record.State != StateActive {
		t.Fatalf("bundled skill state = %q, want active", record.State)
	}
}

func TestCuratorUsesBundledManifestWhenLegacyProvenanceIsWrong(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(skillsDir, map[string]string{"category/skill": "hash"}); err != nil {
		t.Fatal(err)
	}
	usage := NewSkillUsageTracker(skillsDir)
	if err := usage.ReconcileSkills(map[string]SkillRegistration{
		"category/skill": {Name: "skill", Provenance: "legacy"},
	}); err != nil {
		t.Fatal(err)
	}
	usage.mu.Lock()
	usage.records["category/skill"].LastActivityAt = time.Now().Add(-365 * 24 * time.Hour).UTC().Format(time.RFC3339)
	usage.mu.Unlock()
	curator := NewCurator(skillsDir, usage, CuratorConfig{StaleAfterDays: 1}, nil)
	curator.run()
	record, _ := usage.GetRecord("category/skill")
	if record.State != StateActive {
		t.Fatalf("manifest-bundled skill state = %q", record.State)
	}
}

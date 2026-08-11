package evolution

import (
	"path/filepath"
	"testing"
)

func TestReconcileSkillsMigratesLegacyBareNameToFlatID(t *testing.T) {
	usage := NewSkillUsageTracker(filepath.Join(t.TempDir(), "skills"))
	if err := usage.RegisterSkill("deploy", "legacy"); err != nil {
		t.Fatal(err)
	}
	usage.RecordView("deploy")
	if err := usage.ReconcileSkills(map[string]SkillRegistration{
		"ops/deploy": {Name: "deploy", Provenance: "bundled"},
		"deploy":     {Name: "deploy", Provenance: "agent-created"},
	}); err != nil {
		t.Fatal(err)
	}
	flat, ok := usage.GetRecord("deploy")
	if !ok || flat.ID != "deploy" || flat.UseCount != 1 || flat.Provenance != "legacy" {
		t.Fatalf("flat migrated record = %+v, %v", flat, ok)
	}
	nested, ok := usage.GetRecord("ops/deploy")
	if !ok || nested.ID != "ops/deploy" || nested.UseCount != 0 {
		t.Fatalf("nested record = %+v, %v", nested, ok)
	}
}

func TestReconcileSkillsMigratesAmbiguousLegacyNameDeterministically(t *testing.T) {
	usage := NewSkillUsageTracker(filepath.Join(t.TempDir(), "skills"))
	if err := usage.RegisterSkill("deploy", "legacy"); err != nil {
		t.Fatal(err)
	}
	usage.RecordView("deploy")
	if err := usage.ReconcileSkills(map[string]SkillRegistration{
		"zeta/deploy":  {Name: "deploy", Provenance: "installed"},
		"alpha/deploy": {Name: "deploy", Provenance: "bundled"},
	}); err != nil {
		t.Fatal(err)
	}
	alpha, ok := usage.GetRecord("alpha/deploy")
	if !ok || alpha.UseCount != 1 || alpha.Provenance != "legacy" {
		t.Fatalf("canonical migrated record = %+v, %v", alpha, ok)
	}
	zeta, ok := usage.GetRecord("zeta/deploy")
	if !ok || zeta.UseCount != 0 || zeta.Provenance != "installed" {
		t.Fatalf("secondary record = %+v, %v", zeta, ok)
	}
}

package evolution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackBundledSkills(t *testing.T) {
	root, cleanup, err := unpackBundledSkills()
	if err != nil {
		t.Fatalf("unpackBundledSkills: %v", err)
	}
	defer cleanup()

	// The unpacked root must be a real directory.
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("unpacked skills root not a dir: %v", err)
	}

	// A known bundled skill should be present (category-nested).
	known := filepath.Join(root, "github", "codebase-inspection", "SKILL.md")
	if _, err := os.Stat(known); err != nil {
		t.Errorf("expected embedded skill at %s: %v", known, err)
	}

	// At least a few category directories should have been unpacked.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs < 3 {
		t.Errorf("expected several skill categories unpacked, got %d", dirs)
	}
}

func TestUnpackBundledSkillsCleanup(t *testing.T) {
	root, cleanup, err := unpackBundledSkills()
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove unpacked dir: %v", err)
	}
}

// TestInitSyncsEmbeddedSkills verifies that a SkillManager pointed at a fresh
// (empty) user dir gets bundled skills, exercising the embed fallback path when
// no on-disk bundled dir resolves to that location.
func TestInitSyncsEmbeddedSkills(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	mgr := NewSkillManager(skillsDir, NewSkillUsageTracker(skillsDir))
	if err := mgr.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// After Init the user skills dir should contain bundled categories.
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".archive" {
			count++
		}
	}
	if count == 0 {
		t.Error("expected bundled skills synced into user dir, found none")
	}

	allSkills, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	uniqueIDs := make(map[string]bool)
	for _, skill := range allSkills {
		uniqueIDs[skill.ID] = true
	}
	records := mgr.usage.AllRecords()
	if len(records) != len(uniqueIDs) {
		t.Fatalf("usage records = %d, unique installed skills = %d", len(records), len(uniqueIDs))
	}
	if len(records) < 10 {
		t.Fatalf("expected bundled skill catalog, got only %d records", len(records))
	}
	for _, record := range records {
		if record.Provenance != "bundled" {
			t.Fatalf("skill %q provenance = %q, want bundled", record.Name, record.Provenance)
		}
	}
	if content, err := mgr.Read("codebase-inspection"); err != nil || content == "" {
		t.Fatalf("read nested bundled skill: content=%d bytes err=%v", len(content), err)
	}
}

func TestUsageGetRecordReturnsCopy(t *testing.T) {
	usage := NewSkillUsageTracker(filepath.Join(t.TempDir(), "skills"))
	if err := usage.ReconcileSkills(map[string]SkillRegistration{
		"category/example": {Name: "example", Provenance: "bundled"},
	}); err != nil {
		t.Fatal(err)
	}
	record, ok := usage.GetRecord("category/example")
	if !ok {
		t.Fatal("record missing")
	}
	record.State = StateArchived
	again, _ := usage.GetRecord("category/example")
	if again.State != StateActive {
		t.Fatalf("internal record mutated through returned pointer: %+v", again)
	}
}

func TestInitSkillUsageReconciliationIsIdempotentAndPreservesRecords(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	usage := NewSkillUsageTracker(skillsDir)
	if err := usage.RegisterSkill("codebase-inspection", "existing"); err != nil {
		t.Fatal(err)
	}
	usage.RecordView("codebase-inspection")
	mgr := NewSkillManager(skillsDir, usage)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	firstCount := len(usage.AllRecords())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	if secondCount := len(usage.AllRecords()); secondCount != firstCount {
		t.Fatalf("second init records = %d, want %d", secondCount, firstCount)
	}
	record, ok := usage.GetRecord("codebase-inspection")
	if !ok {
		t.Fatal("existing record missing")
	}
	if record.Provenance != "existing" || record.UseCount != 1 {
		t.Fatalf("existing record changed: %+v", record)
	}
}

func TestCreateKeepsFlatAndBundledSameNameRecordsSeparate(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), "skills")
	usage := NewSkillUsageTracker(skillsDir)
	mgr := NewSkillManager(skillsDir, usage)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	before, ok := usage.GetRecord("weather")
	if !ok || before.Provenance != "bundled" {
		t.Fatalf("bundled weather record = %+v, %v", before, ok)
	}
	usage.RecordView("weather")
	if _, err := mgr.Create("weather", "custom weather", "custom body"); err != nil {
		t.Fatal(err)
	}
	flat, ok := usage.GetRecord("weather")
	if !ok || flat.Provenance != "agent-created" {
		t.Fatalf("flat weather record = %+v, %v", flat, ok)
	}
	bundled, ok := usage.GetRecord("productivity/weather")
	if !ok || bundled.Provenance != "bundled" || bundled.UseCount != 1 {
		t.Fatalf("bundled weather record = %+v, %v", bundled, ok)
	}
}

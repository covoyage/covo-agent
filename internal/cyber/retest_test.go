package cyber

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageCleanupDecodedReferencesAndCorruptLedger(t *testing.T) {
	work := t.TempDir()
	catalog := NewCatalog(work)
	spill, err := writeSpill(work, "escape", "keep")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(spill, old, old); err != nil {
		t.Fatal(err)
	}
	encoded := ""
	for _, character := range filepath.Base(spill) {
		encoded += fmt.Sprintf("\\u%04x", character)
	}
	if err := os.WriteFile(filepath.Join(catalog.dir, "external.json"), []byte(`{"evidence":"`+encoded+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := catalog.CleanStorage(1, "", false)
	if err != nil || preview.ReclaimableBytes != 0 {
		t.Fatal("escaped reference was not protected", preview, err)
	}
	if err := os.WriteFile(filepath.Join(catalog.dir, "external.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.CleanStorage(1, preview.Revision, true); err == nil {
		t.Fatal("corrupt evidence accepted")
	}
	if _, err := os.Stat(spill); err != nil {
		t.Fatal("evidence removed on failure", err)
	}
}

func TestStorageCleanupProtectsReferencesActivityAndPreview(t *testing.T) {
	work := t.TempDir()
	catalog := NewCatalog(work)
	keep, err := writeSpill(work, "keep", "evidence")
	if err != nil {
		t.Fatal(err)
	}
	remove, err := writeSpill(work, "remove", "unreferenced")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	for _, path := range []string{keep, remove} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := catalog.UpsertFinding(Finding{Title: "candidate", Target: "example.test", Evidence: keep}); err != nil {
		t.Fatal(err)
	}
	preview, err := catalog.CleanStorage(1, "", false)
	if err != nil || preview.ReclaimableBytes != int64(len("unreferenced")) {
		t.Fatal(preview, err)
	}
	if _, err := catalog.CleanStorage(1, "stale", true); err == nil {
		t.Fatal("stale preview accepted")
	}
	result, err := catalog.CleanStorage(1, preview.Revision, true)
	if err != nil || result.Removed != 1 {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(remove); !os.IsNotExist(err) {
		t.Fatal("expired output retained")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("referenced evidence removed", err)
	}
	if _, err := catalog.EnsureRuntime("local", "run", work); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catalog.dir, "scan_report.md"), []byte("report"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(catalog.dir, "scan_report.md"), old, old); err != nil {
		t.Fatal(err)
	}
	preview, err = catalog.CleanStorage(1, "", false)
	if err != nil || preview.ReclaimableBytes != 0 {
		t.Fatal("active storage not protected", err)
	}
}

func TestRetestKeepsFindingSnapshotAndTerminalState(t *testing.T) {
	catalog := NewCatalog(t.TempDir())
	finding, err := catalog.UpsertFinding(Finding{Title: "candidate", Target: "https://example.test", Evidence: "operator notes"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := catalog.BeginRetest(finding.ID, "source-session")
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.BeginRetest(finding.ID, "source-session")
	if err != nil || second.ID == first.ID {
		t.Fatal("retests not independent", err)
	}
	if _, err := catalog.FinishRetest(first.ID, "child-1", "cancelled", "partial evidence"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.FinishRetest(first.ID, "child-1", "completed", "late completion"); err == nil {
		t.Fatal("cancelled retest overwritten")
	}
	findings, err := catalog.ListFindings()
	if err != nil || findings[0].Evidence != "operator notes" {
		t.Fatal("source finding mutated", err)
	}
	records, err := catalog.ListRetests()
	if err != nil || records[0].Finding.Evidence != "operator notes" || records[1].Status != "running" {
		t.Fatal("snapshot lost", err)
	}
}

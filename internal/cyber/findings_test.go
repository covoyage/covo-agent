package cyber

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Two Catalog instances model the parent agent and a spawned subagent: each
// builds its own Extension over the same workDir, so per-catalog state cannot
// be what serializes the ledger.
func TestCatalogUpsertsSerializeAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	const writers, perWriter = 2, 25

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c := NewCatalog(dir)
			for i := 0; i < perWriter; i++ {
				if _, err := c.UpsertFinding(Finding{
					Title:    fmt.Sprintf("writer %d finding %d", w, i),
					Target:   fmt.Sprintf("10.0.%d.%d", w, i),
					Severity: "high",
				}); err != nil {
					t.Errorf("writer %d upsert failed: %v", w, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	items, err := NewCatalog(dir).ListFindings()
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(items) != writers*perWriter {
		t.Fatalf("findings = %d, want %d (updates lost)", len(items), writers*perWriter)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".covo", "cyber"))
	if err != nil {
		t.Fatalf("read ledger dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestCatalogFilesRemainPrivate(t *testing.T) {
	dir := t.TempDir()
	catalog := NewCatalog(dir)
	if _, err := catalog.RecordExchange(Exchange{URL: "https://app.test", ReqHeaders: "Cookie: secret"}); err != nil {
		t.Fatal(err)
	}
	cyberDir := filepath.Join(dir, ".covo", "cyber")
	path := filepath.Join(cyberDir, "exchanges.json")
	for _, item := range []struct {
		path string
		want os.FileMode
	}{{cyberDir, 0o700}, {path, 0o600}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Mode().Perm() != item.want {
			t.Fatalf("new %s mode: %v err=%v, want %v", item.path, info.Mode().Perm(), err, item.want)
		}
	}
	if err := os.Chmod(cyberDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := catalog.EnsurePrivateStorage(); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path string
		want os.FileMode
	}{{cyberDir, 0o700}, {path, 0o600}} {
		info, err := os.Stat(item.path)
		if err != nil || info.Mode().Perm() != item.want {
			t.Fatalf("mode of %s: %v err=%v, want %v", item.path, info.Mode().Perm(), err, item.want)
		}
	}
}

func TestCatalogMigratesPrivateOutputs(t *testing.T) {
	dir := t.TempDir()
	outputs := SpillDir(dir)
	if err := os.MkdirAll(outputs, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(outputs, "legacy.txt")
	if err := os.WriteFile(path, []byte("private evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewCatalog(dir).EnsurePrivateStorage(); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{outputs, 0o700}, {path, 0o600}} {
		info, err := os.Stat(item.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != item.mode {
			t.Errorf("legacy mode = %v, want %v", info.Mode().Perm(), item.mode)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(outputs, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := NewCatalog(dir).EnsurePrivateStorage(); err == nil {
		t.Fatal("migration accepted nested symbolic link")
	}
}

func TestCatalogRejectsEscapingStorageLinks(t *testing.T) {
	for _, location := range []string{".covo", ".covo/cyber", ".covo/cyber/findings.json"} {
		t.Run(location, func(t *testing.T) {
			dir := t.TempDir()
			external := t.TempDir()
			if err := os.Chmod(external, 0o755); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(external, "findings.json")
			original := []byte(`[{"id":"outside","title":"private","target":"app.test"}]`)
			if err := os.WriteFile(outside, original, 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(dir, location)
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			target := external
			if strings.HasSuffix(location, ".json") {
				target = outside
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			catalog := NewCatalog(dir)
			if _, err := catalog.ListFindings(); err == nil {
				t.Error("read accepted escaping storage link")
			}
			if err := catalog.EnsurePrivateStorage(); err == nil {
				t.Error("initialization accepted escaping storage link")
			}
			if _, err := catalog.UpsertFinding(Finding{Title: "new", Target: "app.test"}); err == nil {
				t.Error("write accepted escaping storage link")
			}
			data, err := os.ReadFile(outside)
			if err != nil || string(data) != string(original) {
				t.Fatalf("outside contents changed: %q, %v", data, err)
			}
			for _, item := range []struct {
				path string
				mode os.FileMode
			}{{external, 0o755}, {outside, 0o644}} {
				info, err := os.Stat(item.path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != item.mode {
					t.Errorf("outside mode changed: %s = %v", item.path, info.Mode().Perm())
				}
			}
		})
	}
}

func TestCatalogUpsertReplacesByID(t *testing.T) {
	c := NewCatalog(t.TempDir())
	first, err := c.UpsertFinding(Finding{Title: "open relay", Target: "smtp.example.com", Severity: "medium"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := c.UpsertFinding(Finding{ID: first.ID, Title: "open relay", Target: "smtp.example.com", Severity: "critical", Status: "verified", PoC: "nc smtp.example.com 25"}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	items, err := c.ListFindings()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("findings = %d, want 1", len(items))
	}
	if items[0].Severity != "critical" || items[0].Status != StatusConfirmed {
		t.Fatalf("update not applied: %+v", items[0])
	}
}

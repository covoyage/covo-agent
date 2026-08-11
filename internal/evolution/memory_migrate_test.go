package evolution

import (
	"path/filepath"
	"testing"
)

func TestMigrateMemoryProviderFileToSQLite(t *testing.T) {
	dir := t.TempDir()

	src := NewFileMemoryProvider(filepath.Join(dir, "src"))
	if err := src.Init(); err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	dst := NewSQLiteMemoryProvider(filepath.Join(dir, "dst.db"))
	if err := dst.Init(); err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	if err := src.Add(MemoryAgent, "agent entry 1"); err != nil {
		t.Fatal(err)
	}
	if err := src.Add(MemoryAgent, "agent entry 2"); err != nil {
		t.Fatal(err)
	}
	if err := src.Add(MemoryUser, "user entry 1"); err != nil {
		t.Fatal(err)
	}

	if err := MigrateMemoryProvider(src, dst); err != nil {
		t.Fatalf("MigrateMemoryProvider: %v", err)
	}

	agentEntries, err := dst.Read(MemoryAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(agentEntries) != 2 {
		t.Fatalf("expected 2 agent entries, got %d", len(agentEntries))
	}

	userEntries, err := dst.Read(MemoryUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(userEntries) != 1 {
		t.Fatalf("expected 1 user entry, got %d", len(userEntries))
	}
	if userEntries[0].Content != "user entry 1" {
		t.Fatalf("expected 'user entry 1', got %q", userEntries[0].Content)
	}
}

func TestMigrateMemoryProviderSQLiteToFile(t *testing.T) {
	dir := t.TempDir()

	src := NewSQLiteMemoryProvider(filepath.Join(dir, "src.db"))
	if err := src.Init(); err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	dst := NewFileMemoryProvider(filepath.Join(dir, "dst"))
	if err := dst.Init(); err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	if err := src.Add(MemoryAgent, "agent entry"); err != nil {
		t.Fatal(err)
	}
	if err := src.Add(MemoryUser, "user entry"); err != nil {
		t.Fatal(err)
	}

	if err := MigrateMemoryProvider(src, dst); err != nil {
		t.Fatalf("MigrateMemoryProvider: %v", err)
	}

	if snap := dst.Snapshot(MemoryAgent); snap != "agent entry\n" {
		t.Fatalf("unexpected agent snapshot: %q", snap)
	}
	if snap := dst.Snapshot(MemoryUser); snap != "user entry\n" {
		t.Fatalf("unexpected user snapshot: %q", snap)
	}
}

func TestMigrateMemoryProviderEmptySource(t *testing.T) {
	dir := t.TempDir()

	src := NewFileMemoryProvider(filepath.Join(dir, "src"))
	if err := src.Init(); err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	dst := NewSQLiteMemoryProvider(filepath.Join(dir, "dst.db"))
	if err := dst.Init(); err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	if err := MigrateMemoryProvider(src, dst); err != nil {
		t.Fatalf("MigrateMemoryProvider (empty): %v", err)
	}

	entries, err := dst.Read(MemoryAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after empty migration, got %d", len(entries))
	}
}

func TestMigrateMemoryProviderSelfMigration(t *testing.T) {
	dir := t.TempDir()

	dbPath := filepath.Join(dir, "test.db")
	p := NewSQLiteMemoryProvider(dbPath)
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.Add(MemoryAgent, "entry"); err != nil {
		t.Fatal(err)
	}
	// Also verify the helper doesn't interfere with existing data on re-init:
	p2 := NewSQLiteMemoryProvider(dbPath)
	if err := p2.Init(); err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if err := MigrateMemoryProvider(p, p2); err != nil {
		t.Fatalf("self migrate: %v", err)
	}

	entries, err := p2.Read(MemoryAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 { // original + readded
		t.Fatalf("expected 2 entries (original + migrated copy), got %d", len(entries))
	}
}

func TestMemoryProviderNamesRegistered(t *testing.T) {
	names := MemoryProviderNames()
	got := make(map[string]bool)
	for _, n := range names {
		got[n] = true
	}
	if !got["file"] {
		t.Error("file not registered")
	}
	if !got["sqlite"] {
		t.Error("sqlite not registered")
	}
}

func TestNewMemorySystemWithProvider(t *testing.T) {
	dir := t.TempDir()
	p := NewFileMemoryProvider(filepath.Join(dir, "memories"))
	ms := NewMemorySystemWithProvider(p)
	if ms.Provider() != p {
		t.Fatal("NewMemorySystemWithProvider did not store the provider")
	}
}

func TestMemorySystemSetProviderSwap(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemorySystem(dir)

	newP := NewSQLiteMemoryProvider(filepath.Join(dir, "test.db"))
	ms.SetProvider(newP)

	if ms.Provider() != newP {
		t.Fatal("SetProvider did not swap")
	}
}

func TestMemorySystemSetProviderToNil(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemorySystem(dir)
	// Set to nil should close old but not crash
	ms.SetProvider(nil)
	if ms.Provider() != nil {
		t.Fatal("expected nil provider")
	}
}

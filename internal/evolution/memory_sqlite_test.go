package evolution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteMemoryProvider(t *testing.T) {
	dir, err := os.MkdirTemp("", "sqlite_mem_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	provider := NewSQLiteMemoryProvider(filepath.Join(dir, "test.db"))
	if err := provider.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer provider.Close()

	if provider.Name() != "sqlite" {
		t.Fatalf("Name() = %q, want %q", provider.Name(), "sqlite")
	}

	if err := provider.Add(MemoryAgent, "hello world"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := provider.Add(MemoryAgent, "second entry"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := provider.Add(MemoryUser, "user pref"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	entries, err := provider.Read(MemoryAgent)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Read returned %d entries, want 2", len(entries))
	}

	if snap := provider.Snapshot(MemoryAgent); snap == "" {
		t.Fatal("Snapshot is empty")
	}

	if err := provider.Replace(MemoryAgent, "hello", "HELLO REPLACED"); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if err := provider.Remove(MemoryUser, "pref"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	userEntries, _ := provider.Read(MemoryUser)
	if len(userEntries) != 0 {
		t.Fatalf("expected 0 user entries after remove, got %d", len(userEntries))
	}
}

func TestSQLiteMemoryProviderEmptyReplaceError(t *testing.T) {
	dir, err := os.MkdirTemp("", "sqlite_mem_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	provider := NewSQLiteMemoryProvider(filepath.Join(dir, "test.db"))
	if err := provider.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer provider.Close()

	if err := provider.Replace(MemoryAgent, "nonexistent", "new"); err == nil {
		t.Fatal("expected error on replace nonexistent")
	}
}

func TestSQLiteMemoryProviderEmptyRemoveError(t *testing.T) {
	dir, err := os.MkdirTemp("", "sqlite_mem_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	provider := NewSQLiteMemoryProvider(filepath.Join(dir, "test.db"))
	if err := provider.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer provider.Close()

	if err := provider.Remove(MemoryAgent, "nonexistent"); err == nil {
		t.Fatal("expected error on remove nonexistent")
	}
}

func TestSQLiteMemoryProviderEmptyAdd(t *testing.T) {
	dir, err := os.MkdirTemp("", "sqlite_mem_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	provider := NewSQLiteMemoryProvider(filepath.Join(dir, "test.db"))
	if err := provider.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer provider.Close()

	if err := provider.Add(MemoryAgent, ""); err != nil {
		t.Fatalf("Add empty: %v", err)
	}
	if err := provider.Add(MemoryAgent, "  "); err != nil {
		t.Fatalf("Add whitespace: %v", err)
	}

	entries, err := provider.Read(MemoryAgent)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestMemoryProviderRegistry(t *testing.T) {
	names := MemoryProviderNames()
	if len(names) < 2 {
		t.Fatalf("expected at least 2 providers (file, sqlite), got %v", names)
	}

	factory, ok := GetMemoryProvider("file")
	if !ok {
		t.Fatal("file provider not registered")
	}
	if factory == nil {
		t.Fatal("file factory is nil")
	}

	factory, ok = GetMemoryProvider("sqlite")
	if !ok {
		t.Fatal("sqlite provider not registered")
	}
	if factory == nil {
		t.Fatal("sqlite factory is nil")
	}
}

func TestNewMemorySystemFromEnv(t *testing.T) {
	t.Run("default (no env)", func(t *testing.T) {
		os.Unsetenv("COVO_MEMORY_PROVIDER")
		ms := NewMemorySystem(t.TempDir())
		if ms.Provider().Name() != "file" {
			t.Fatalf("expected file, got %q", ms.Provider().Name())
		}
	})

	t.Run("sqlite via env", func(t *testing.T) {
		t.Setenv("COVO_MEMORY_PROVIDER", "sqlite")
		ms := NewMemorySystem(t.TempDir())
		if err := ms.Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if ms.Provider().Name() != "sqlite" {
			t.Fatalf("expected sqlite, got %q", ms.Provider().Name())
		}
	})

	t.Run("unknown provider falls back to file", func(t *testing.T) {
		t.Setenv("COVO_MEMORY_PROVIDER", "nonexistent")
		ms := NewMemorySystem(t.TempDir())
		if ms.Provider().Name() != "file" {
			t.Fatalf("expected file fallback, got %q", ms.Provider().Name())
		}
	})
}

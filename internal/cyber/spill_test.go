package cyber

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundWithSpillShortCircuit(t *testing.T) {
	if got := BoundWithSpill(t.TempDir(), "test", "hello", MaxStdoutBytes); got != "hello" {
		t.Fatalf("short output should pass through, got %q", got)
	}
}

func TestBoundWithSpillWritesFullText(t *testing.T) {
	workDir := t.TempDir()
	full := strings.Repeat("A", MaxStdoutBytes*3)

	got := BoundWithSpill(workDir, "nmap-10.0.0.8/24-stdout", full, MaxStdoutBytes)
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("notice missing: %q", got[:min(len(got), 200)])
	}
	if !strings.Contains(got, ".covo/cyber/outputs/") {
		t.Fatalf("spill path missing from notice: %q", got[:min(len(got), 300)])
	}

	entries, err := os.ReadDir(SpillDir(workDir))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one spill file, err=%v entries=%d", err, len(entries))
	}
	data, err := os.ReadFile(filepath.Join(SpillDir(workDir), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != MaxStdoutBytes*3 {
		t.Fatalf("spilled file lost bytes: %d", len(data))
	}
}

func TestBoundWithSpillFailureFallback(t *testing.T) {
	// workDir points at a regular file so MkdirAll fails; must not panic
	// and must still return truncated content.
	file := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := BoundWithSpill(file, "test", strings.Repeat("A", 100), 10)
	if !strings.Contains(got, "spill failed") {
		t.Fatalf("expected spill-failure notice, got %q", got)
	}
}

func TestSpillPrivateUniqueAndRejectsLinks(t *testing.T) {
	workDir := t.TempDir()
	first, err := writeSpill(workDir, "same", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeSpill(workDir, "same", "second")
	if err != nil || first == second {
		t.Fatalf("spill paths collide: %q %q, %v", first, second, err)
	}
	for _, path := range []string{first, second, SpillDir(workDir)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("mode of %s = %v, want %v", path, info.Mode().Perm(), want)
		}
	}
	external := t.TempDir()
	if err := os.Rename(SpillDir(workDir), SpillDir(workDir)+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, SpillDir(workDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSpill(workDir, "secret", "private"); err == nil {
		t.Fatal("spill accepted symbolic link")
	}
	entries, err := os.ReadDir(external)
	if err != nil || len(entries) != 0 {
		t.Fatalf("spill wrote outside storage: %v, %v", entries, err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

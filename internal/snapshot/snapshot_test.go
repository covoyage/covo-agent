package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	workDir := t.TempDir()
	dataDir := t.TempDir()
	s, err := NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !s.Enabled() {
		t.Skip("snapshot service not enabled (git not available)")
	}
	return s
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestService_Track(t *testing.T) {
	s := newTestService(t)
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "hello")

	hash, err := s.Track()
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
}

func TestService_TrackIdempotent(t *testing.T) {
	s := newTestService(t)
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "hello")

	hash1, _ := s.Track()
	hash2, _ := s.Track()
	if hash1 != hash2 {
		t.Fatalf("same content should produce same hash: %s vs %s", hash1, hash2)
	}
}

func TestService_Patch(t *testing.T) {
	s := newTestService(t)
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "original")

	hash1, _ := s.Track()

	// Modify file
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "modified")
	// Add new file
	writeFile(t, filepath.Join(s.workDir, "b.txt"), "new file")

	_, _ = s.Track() // refresh index

	patch, err := s.Patch(hash1)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if len(patch.Files) != 2 {
		t.Fatalf("expected 2 changed files, got %d: %v", len(patch.Files), patch.Files)
	}
}

func TestService_Revert(t *testing.T) {
	s := newTestService(t)
	original := "original content"
	writeFile(t, filepath.Join(s.workDir, "a.txt"), original)

	hash1, _ := s.Track()

	// Modify the file
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "modified content")
	// Add a new file that didn't exist at snapshot time
	writeFile(t, filepath.Join(s.workDir, "b.txt"), "should be removed")

	_, _ = s.Track()

	// Revert to hash1
	patches := []Patch{{Hash: hash1, Files: []string{"a.txt", "b.txt"}}}
	if err := s.Revert(patches); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	// a.txt should be back to original
	if got := readFile(t, filepath.Join(s.workDir, "a.txt")); got != original {
		t.Fatalf("a.txt not reverted: got %q, want %q", got, original)
	}
	// b.txt should be removed (didn't exist at snapshot)
	if _, err := os.Stat(filepath.Join(s.workDir, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("b.txt should have been removed, but exists")
	}
}

func TestService_Restore(t *testing.T) {
	s := newTestService(t)
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "version 1")
	writeFile(t, filepath.Join(s.workDir, "b.txt"), "file b")

	hash1, _ := s.Track()

	// Make changes
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "version 2")
	os.Remove(filepath.Join(s.workDir, "b.txt"))
	writeFile(t, filepath.Join(s.workDir, "c.txt"), "file c")

	_, _ = s.Track()

	// Full restore to hash1
	if err := s.Restore(hash1); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// a.txt should be version 1
	if got := readFile(t, filepath.Join(s.workDir, "a.txt")); got != "version 1" {
		t.Fatalf("a.txt not restored: got %q", got)
	}
	// b.txt should exist again
	if got := readFile(t, filepath.Join(s.workDir, "b.txt")); got != "file b" {
		t.Fatalf("b.txt not restored: got %q", got)
	}
}

func TestService_Diff(t *testing.T) {
	s := newTestService(t)
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "line1\nline2\n")

	hash1, _ := s.Track()

	writeFile(t, filepath.Join(s.workDir, "a.txt"), "line1\nline2 modified\nline3\n")
	_, _ = s.Track()

	diff, err := s.Diff(hash1)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !contains(diff, "line2 modified") {
		t.Fatalf("diff should contain 'line2 modified':\n%s", diff)
	}
}

func TestService_ListFiles(t *testing.T) {
	s := newTestService(t)
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "a")
	writeFile(t, filepath.Join(s.workDir, "sub", "b.txt"), "b")

	hash, _ := s.Track()

	files, err := s.ListFiles(hash)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestService_PersistenceAcrossInstances(t *testing.T) {
	workDir := t.TempDir()
	dataDir := t.TempDir()

	// First instance: create a snapshot
	s1, err := NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService s1: %v", err)
	}
	if !s1.Enabled() {
		t.Skip("snapshot service not enabled")
	}
	writeFile(t, filepath.Join(workDir, "a.txt"), "original")
	hash1, _ := s1.Track()

	// Modify file
	writeFile(t, filepath.Join(workDir, "a.txt"), "modified")

	// Second instance: should see the same git objects
	s2, err := NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService s2: %v", err)
	}
	if !s2.Enabled() {
		t.Fatal("s2 should be enabled")
	}

	// Revert using the hash from s1
	if err := s2.Revert([]Patch{{Hash: hash1, Files: []string{"a.txt"}}}); err != nil {
		t.Fatalf("Revert via s2: %v", err)
	}
	if got := readFile(t, filepath.Join(workDir, "a.txt")); got != "original" {
		t.Fatalf("file not reverted via second instance: got %q", got)
	}
}

func TestService_NoOpWhenDisabled(t *testing.T) {
	// Create a service with an invalid workDir to force disable
	s := &Service{
		gitDir:  "/nonexistent/path/that/does/not/exist",
		workDir: "/nonexistent",
		enabled: false,
	}
	hash, err := s.Track()
	if err != nil {
		t.Fatalf("Track on disabled should not error: %v", err)
	}
	if hash != "" {
		t.Fatalf("disabled Track should return empty hash, got %q", hash)
	}
	if err := s.Revert(nil); err != nil {
		t.Fatalf("Revert on disabled should not error: %v", err)
	}
}

func TestService_ContentAddressedDedup(t *testing.T) {
	s := newTestService(t)
	// Same content in different files should produce same blobs (git dedup)
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "same content")
	hash1, _ := s.Track()

	writeFile(t, filepath.Join(s.workDir, "b.txt"), "same content")
	hash2, _ := s.Track()

	// Hashes differ (different tree structure) but the blob is shared
	if hash1 == hash2 {
		t.Fatalf("different tree states should have different hashes")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

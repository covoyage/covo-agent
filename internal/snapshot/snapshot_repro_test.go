package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

// TestService_TrackManyFiles reproduces a report where a workspace with
// thousands of small files snapshotted as the empty tree.
func TestService_TrackManyFilesReal(t *testing.T) {
	workDir := t.TempDir()
	dataDir := t.TempDir()
	for i := 0; i < 2400; i++ {
		p := filepath.Join(workDir, "f"+string(rune('a'+i/1000))+string(rune('a'+(i/100)%10))+string(rune('a'+(i/10)%10))+string(rune('a'+i%10))+".txt")
		if err := os.WriteFile(p, []byte("content"+string(rune('a'+i%26))), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !s.Enabled() {
		t.Skip("git not available")
	}
	hash, err := s.Track()
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	files, err := s.ListFiles(hash)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 2400 {
		t.Fatalf("want 2400 files in snapshot, got %d (hash=%s)", len(files), hash)
	}
}

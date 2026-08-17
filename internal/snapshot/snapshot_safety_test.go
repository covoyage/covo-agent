package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestService_ExcludesDataDirWhenInsideWorkDir covers the nested layout:
// the work dir contains the data dir, so the object store itself lives
// inside the work tree. Track() must exclude it.
func TestService_ExcludesDataDirWhenInsideWorkDir(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	dataDir := filepath.Join(workDir, ".covo-agent")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workDir, "a.txt"), "hello")

	s, err := NewService(workDir, dataDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !s.Enabled() {
		t.Skip("git not available")
	}

	// Simulate the store growing between snapshots (as it would in real use).
	storeObjects := filepath.Join(dataDir, "snapshot", hashPath(workDir), "objects")
	writeFile(t, filepath.Join(storeObjects, "aa", "blob1"), strings.Repeat("x", 100))

	hash1, err := s.Track()
	if err != nil {
		t.Fatalf("Track 1: %v", err)
	}
	files1, err := s.ListFiles(hash1)
	if err != nil {
		t.Fatalf("ListFiles 1: %v", err)
	}
	for _, f := range files1 {
		if strings.HasPrefix(f, ".covo-agent/") {
			t.Fatalf("snapshot must not contain data dir paths, found %q (all: %v)", f, files1)
		}
	}

	// Grow the store again and re-track: the tree hash must not change due
	// to store growth, and no store paths may appear.
	writeFile(t, filepath.Join(storeObjects, "bb", "blob2"), strings.Repeat("y", 100))
	hash2, err := s.Track()
	if err != nil {
		t.Fatalf("Track 2: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("object store growth must not change the snapshot tree: %s vs %s", hash1, hash2)
	}
}

// TestService_ExcludesBigFiles verifies the per-file size limit: a file over
// maxFileBytes must not be staged into snapshots.
func TestService_ExcludesBigFiles(t *testing.T) {
	workDir := t.TempDir()
	dataDir := t.TempDir()
	writeFile(t, filepath.Join(workDir, "small.txt"), "small")
	big := make([]byte, 3*512*1024) // 1.5 MiB, over the 1 MiB limit below
	for i := range big {
		big[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(workDir, "ota_firmware.img"), big, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("COVO_SNAPSHOT_MAX_FILE_MB", "1")
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
	found := false
	for _, f := range files {
		if f == "small.txt" {
			found = true
		}
		if f == "ota_firmware.img" {
			t.Fatalf("oversized file must be excluded from snapshots, got files %v", files)
		}
	}
	if !found {
		t.Fatalf("small file should still be tracked, got files %v", files)
	}
}

// TestService_GCPrunesUnreachable verifies that objects referenced only by
// pruned snapshots are removed from the store, while retained ones survive.
func TestService_GCPrunesUnreachable(t *testing.T) {
	s := newTestService(t)
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "version 1")
	hash1, err := s.Track()
	if err != nil {
		t.Fatalf("Track 1: %v", err)
	}
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "version 2")
	hash2, err := s.Track()
	if err != nil {
		t.Fatalf("Track 2: %v", err)
	}
	// Advance to a third state so hash2 is neither retained nor the
	// currently-staged index state (the index is a gc reachability root and
	// must never be collected).
	writeFile(t, filepath.Join(s.workDir, "a.txt"), "version 3")
	hash3, err := s.Track()
	if err != nil {
		t.Fatalf("Track 3: %v", err)
	}

	if err := s.GC([]string{hash1, hash3}); err != nil {
		t.Fatalf("GC: %v", err)
	}

	// hash1 and hash3 must survive GC.
	for _, h := range []string{hash1, hash3} {
		if _, err := s.git("cat-file", "-e", h+"^{tree}"); err != nil {
			t.Fatalf("retained hash %s should survive GC: %v", h, err)
		}
	}
	// hash2 must have been pruned (it is not retained and not staged).
	if _, err := s.git("cat-file", "-e", hash2+"^{tree}"); err == nil {
		t.Fatal("pruned hash should not survive GC")
	}
}

// TestService_RevertSkipsStorePaths verifies that revert refuses to touch
// snapshot-store paths even when a legacy patch entry lists them.
func TestService_RevertSkipsStorePaths(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	dataDir := filepath.Join(workDir, ".covo-agent")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workDir, "a.txt"), "current")

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

	storeFile := filepath.Join(dataDir, "snapshot", hashPath(workDir), "objects", "zz", "keepme")
	writeFile(t, storeFile, "live object data")

	// A legacy patch that pretends the store file should be removed.
	_, err = os.Stat(storeFile)
	if err != nil {
		t.Fatalf("store file missing: %v", err)
	}
	// Compute the store-relative path as seen from workDir.
	rel, err := filepath.Rel(workDir, storeFile)
	if err != nil {
		t.Fatal(err)
	}
	patches := []Patch{{Hash: hash, Files: []string{rel, "a.txt"}}}
	if err := s.Revert(patches); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	// The store file must still exist; revert must not delete it.
	if _, err := os.Stat(storeFile); err != nil {
		t.Fatalf("revert must not delete snapshot store files: %v", err)
	}
	// a.txt did not exist in the snapshot-time index? It did (it was tracked),
	// so it should be restored to "current" — unchanged content check.
	if got := readFile(t, filepath.Join(s.workDir, "a.txt")); got != "current" {
		t.Fatalf("a.txt should be restored to snapshot state, got %q", got)
	}
}

// TestMaxFileBytesFromEnv covers env parsing of the size limit.
func TestMaxFileBytesFromEnv(t *testing.T) {
	cases := []struct {
		env  string
		want int64
	}{
		{"", defaultMaxFileBytes},
		{"2", 2 << 20},
		{"0", 0},
		{"-5", defaultMaxFileBytes},
		{"bogus", defaultMaxFileBytes},
	}
	for _, c := range cases {
		t.Setenv("COVO_SNAPSHOT_MAX_FILE_MB", c.env)
		if got := maxFileBytesFromEnv(); got != c.want {
			t.Errorf("env %q: got %d, want %d", c.env, got, c.want)
		}
	}
}

// TestAnchorPattern covers gitignore escaping.
func TestAnchorPattern(t *testing.T) {
	if got := anchorPattern("a/b.txt"); got != "/a/b.txt" {
		t.Errorf("anchorPattern = %q, want /a/b.txt", got)
	}
	if got := anchorPattern("weird[name]/file"); got != "/weird\\[name\\]/file" {
		t.Errorf("anchorPattern escaping failed: %q", got)
	}
}

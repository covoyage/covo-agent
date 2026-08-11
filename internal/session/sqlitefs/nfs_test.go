package sqlitefs

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalMode_String(t *testing.T) {
	if ModeWAL.String() != "WAL" {
		t.Error("bad string")
	}
	if ModeTruncate.String() != "TRUNCATE" {
		t.Error("bad string")
	}
}

func TestJournalMode_DSNParam(t *testing.T) {
	if ModeWAL.DSNParam() != "WAL" {
		t.Error("bad DSN param")
	}
	if ModeTruncate.DSNParam() != "TRUNCATE" {
		t.Error("bad DSN param")
	}
}

func TestDetectNFS_LocalTempDir(t *testing.T) {
	dir := t.TempDir()
	// Temp dirs are on local filesystem — should not be NFS
	// (unless the test machine itself is on NFS, which is unusual)
	result := DetectNFS(dir)
	// We can't assert false definitively (CI might be on NFS),
	// but we can at least verify it doesn't crash
	_ = result
}

func TestEffectiveDBPath_Local(t *testing.T) {
	dir := t.TempDir()
	path := EffectiveDBPath(dir)
	expected := filepath.Join(dir, "sessions.db")
	if path != expected {
		// If on NFS, path would include hostname — skip in that case
		if !DetectNFS(dir) {
			t.Errorf("expected %q, got %q", expected, path)
		}
	}
}

func TestEffectiveDBPath_NFS(t *testing.T) {
	// Simulate NFS by testing the per-host path logic
	// We can't create a real NFS mount in tests, but we can verify
	// the hostname-based path format
	dir := "/net/server/home/user/.covo-agent"
	if !DetectNFS(dir) {
		t.Skip("not on NFS — can't test NFS path")
	}
	path := EffectiveDBPath(dir)
	if !strings.Contains(path, "sessions-") {
		t.Error("expected hostname-based path on NFS")
	}
	if !strings.HasSuffix(path, ".db") {
		t.Error("expected .db suffix")
	}
}

func TestEffectiveJournalMode_Local(t *testing.T) {
	dir := t.TempDir()
	mode := EffectiveJournalMode(dir)
	if !DetectNFS(dir) {
		if mode != ModeWAL {
			t.Errorf("expected WAL on local filesystem, got %s", mode)
		}
	}
}

func TestBuildDSN(t *testing.T) {
	dir := t.TempDir()
	dsn := BuildDSN(dir)

	if !strings.HasPrefix(dsn, "file:") {
		t.Error("expected file: prefix")
	}
	if !strings.Contains(dsn, "_busy_timeout=5000") {
		t.Error("expected busy_timeout in DSN")
	}

	if !DetectNFS(dir) {
		if !strings.Contains(dsn, "_journal_mode=WAL") {
			t.Error("expected WAL mode on local filesystem")
		}
	} else {
		if !strings.Contains(dsn, "_journal_mode=TRUNCATE") {
			t.Error("expected TRUNCATE mode on NFS")
		}
	}
}

func TestBuildDSN_ContainsPath(t *testing.T) {
	dir := t.TempDir()
	dsn := BuildDSN(dir)
	if !strings.Contains(dsn, dir) {
		t.Errorf("expected DSN to contain dir %q, got %q", dir, dsn)
	}
}

func TestIsNetworkMount(t *testing.T) {
	dir := t.TempDir()
	// Should not panic
	result := IsNetworkMount(dir)
	_ = result
}

func TestHostname(t *testing.T) {
	h := Hostname()
	if h == "" {
		t.Error("expected non-empty hostname")
	}
	// Should be sanitized
	for _, c := range h {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_') {
			t.Errorf("hostname contains unsanitized character: %c", c)
		}
	}
}

func TestDetectNFSLinux_MockMounts(t *testing.T) {
	// This test only runs on Linux
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	// We can't mock /proc/mounts, but we can verify the function
	// doesn't crash on a normal path
	_ = detectNFSLinux(t.TempDir())
}

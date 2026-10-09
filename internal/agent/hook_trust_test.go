package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookTrustStoreDigestChangeInvalidates(t *testing.T) {
	dir := t.TempDir()
	store := NewHookTrustStore(dir)
	work := filepath.Join(dir, "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	d1 := HookFileDigest([]byte(`{"hooks":[{"event":"PreToolUse","command":"true"}]}`))
	d2 := HookFileDigest([]byte(`{"hooks":[{"event":"PreToolUse","command":"false"}]}`))
	if store.Matches(work, d1) {
		t.Fatal("untrusted workspace should not match")
	}
	if err := store.Grant(work, d1); err != nil {
		t.Fatal(err)
	}
	if !store.Matches(work, d1) {
		t.Fatal("granted digest should match")
	}
	if store.Matches(work, d2) {
		t.Fatal("changed digest must not match previous grant")
	}
	reloaded := NewHookTrustStore(dir)
	if !reloaded.Matches(work, d1) {
		t.Fatal("grant should persist")
	}
}

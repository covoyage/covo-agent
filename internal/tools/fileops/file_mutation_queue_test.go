package fileops

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

func TestFileMutexKeyResolvesMissingLeafThroughAlias(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skip(err)
	}
	for _, suffix := range []string{"new.txt", "new-dir/new.txt"} {
		if getFileMutexKey(filepath.Join(real, suffix)) != getFileMutexKey(filepath.Join(alias, suffix)) {
			t.Fatalf("alias has separate lock: %s", suffix)
		}
	}
}

func TestMultiPathLocksDeduplicateAndOrder(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		var workers sync.WaitGroup
		for _, paths := range [][]string{{first, second, first}, {second, first}} {
			workers.Add(1)
			go func(paths []string) {
				defer workers.Done()
				_ = WithFileMutationLocks(paths, func() error { return nil })
			}(paths)
		}
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("multi-path locks deadlocked")
	}
}

func TestResolveReadPathFindsDecomposedFilename(t *testing.T) {
	dir := t.TempDir()
	nfcName := "caf\u00e9.txt"
	nfdName := norm.NFD.String(nfcName)
	if err := os.WriteFile(filepath.Join(dir, nfdName), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}

	resolved := resolveReadPath(nfcName, dir)
	content, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", resolved, err)
	}
	if string(content) != "ok" {
		t.Fatalf("content = %q", content)
	}
}

package fileops

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/unicode/norm"
)

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

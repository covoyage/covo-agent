package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeACPFS struct {
	readErr  error
	writeErr error
	content  []byte
	written  map[string][]byte
}

func (f *fakeACPFS) ReadTextFile(path string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.content, nil
}

func (f *fakeACPFS) WriteTextFile(path string, content []byte) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.written == nil {
		f.written = map[string][]byte{}
	}
	f.written[path] = content
	return nil
}

func TestSwappableFSDefaultsToLocal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSwappableFS()
	b, err := s.ReadFile(p)
	if err != nil || string(b) != "local" {
		t.Fatalf("expected local read, got %q err=%v", b, err)
	}
}

func TestSwappableFSRoutesToACP(t *testing.T) {
	s := newSwappableFS()
	s.set(&acpFileBackend{fs: &fakeACPFS{content: []byte("from editor")}, local: localFileBackend{}})

	b, err := s.ReadFile("/whatever.go")
	if err != nil || string(b) != "from editor" {
		t.Fatalf("expected editor content, got %q err=%v", b, err)
	}
}

func TestACPBackendFallsBackOnReadError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	be := &acpFileBackend{fs: &fakeACPFS{readErr: errors.New("binary/unsupported")}, local: localFileBackend{}}
	b, err := be.ReadFile(p)
	if err != nil || string(b) != "disk" {
		t.Fatalf("expected local fallback, got %q err=%v", b, err)
	}
}

func TestACPBackendWriteRoutesToEditor(t *testing.T) {
	fs := &fakeACPFS{}
	be := &acpFileBackend{fs: fs, local: localFileBackend{}}
	if err := be.WriteFile("/x/y.go", []byte("content")); err != nil {
		t.Fatal(err)
	}
	if string(fs.written["/x/y.go"]) != "content" {
		t.Errorf("write did not route to editor: %v", fs.written)
	}
}

func TestSetFileBackendNilRevertsToLocal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	ca := &CovoAgent{fsOps: newSwappableFS()}
	ca.SetFileBackend(&fakeACPFS{content: []byte("editor")})
	if b, _ := ca.fsOps.ReadFile(p); string(b) != "editor" {
		t.Fatalf("expected editor, got %q", b)
	}
	ca.SetFileBackend(nil)
	if b, _ := ca.fsOps.ReadFile(p); string(b) != "disk" {
		t.Fatalf("expected local after nil revert, got %q", b)
	}
}

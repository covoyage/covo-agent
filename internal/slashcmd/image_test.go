package slashcmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseImageArgs(t *testing.T) {
	tests := []struct {
		input   string
		path    string
		message string
		wantErr bool
	}{
		{input: "/image", wantErr: true},
		{input: "/image screenshot.png", path: "screenshot.png"},
		{input: "/image ./ui.png why is this button offset", path: "./ui.png", message: "why is this button offset"},
		{input: `/image "my shot.png" look here`, path: "my shot.png", message: "look here"},
		{input: `/image 'my shot.png'`, path: "my shot.png"},
		{input: `/image "open`, wantErr: true},
	}
	for _, tt := range tests {
		path, message, err := parseImageArgs(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%q: expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tt.input, err)
			continue
		}
		if path != tt.path || message != tt.message {
			t.Errorf("%q: path=%q message=%q, want %q %q", tt.input, path, message, tt.path, tt.message)
		}
	}
}

func TestResolveImagePath(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(png, []byte("not-a-real-png"), 0o644); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveImagePath("shot.png", dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(png)
	if err != nil {
		want = png
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	if _, err := resolveImagePath("notes.txt", dir); err == nil {
		t.Fatal("expected unsupported type")
	}
	if _, err := resolveImagePath("missing.png", dir); err == nil {
		t.Fatal("expected missing file")
	}
}

func TestHandleImageAttachesAndSubmits(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(png, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotPath, gotMsg string
	sctx := &SlashContext{
		Input:   "/image shot.png why is this offset",
		Runtime: RuntimeDependencies{WorkingDir: dir},
		UI: UIDependencies{
			AttachImage: func(path, message string) error {
				gotPath, gotMsg = path, message
				return nil
			},
		},
	}
	if !handleImage(sctx, []string{"image", "shot.png", "why", "is", "this", "offset"}) {
		t.Fatal("expected handled")
	}
	want, err := filepath.EvalSymlinks(png)
	if err != nil {
		want = png
	}
	if gotPath != want {
		t.Fatalf("path=%q want %q", gotPath, want)
	}
	if gotMsg != "why is this offset" {
		t.Fatalf("message=%q", gotMsg)
	}
}

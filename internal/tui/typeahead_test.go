package tui

import (
	"bytes"
	"os"
	"testing"
)

func TestCaptureTypeaheadNonFile(t *testing.T) {
	if got := CaptureTypeahead(bytes.NewBufferString("hello")); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestCaptureTypeaheadNonTTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	if _, err := w.WriteString("typed"); err != nil {
		t.Fatal(err)
	}
	if got := CaptureTypeahead(r); got != "" {
		t.Fatalf("non-tty capture = %q, want empty", got)
	}
}

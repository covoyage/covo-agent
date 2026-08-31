//go:build unix

package tui

import (
	"os"
	"testing"
	"time"
)

func TestDrainTypeaheadReadsBufferedBytes(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	if _, err := w.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	if got := drainTypeahead(int(r.Fd())); got != "hello" {
		t.Fatalf("drainTypeahead = %q, want %q", got, "hello")
	}
}

func TestDrainTypeaheadEmptyDoesNotBlock(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	done := make(chan string, 1)
	go func() { done <- drainTypeahead(int(r.Fd())) }()

	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("empty drain = %q, want empty", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("drainTypeahead blocked on an empty fd")
	}
}

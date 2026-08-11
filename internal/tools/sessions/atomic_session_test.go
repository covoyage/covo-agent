package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAtomicSessionWriterAppendLineEmptyIsNoop(t *testing.T) {
	writer := NewAtomicSessionWriter(t.TempDir())
	if err := writer.AppendLine("session", ""); err != nil {
		t.Fatalf("AppendLine empty: %v", err)
	}
	lines, err := writer.Read("session")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 {
		t.Fatalf("lines = %v, want empty", lines)
	}
}

func TestAtomicSessionWriterDoesNotEscapeDirectory(t *testing.T) {
	dir := t.TempDir()
	writer := NewAtomicSessionWriter(dir)
	if err := writer.Append("../outside", map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "outside.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed: %v", err)
	}
	lines, err := writer.Read("../outside")
	if err != nil || len(lines) != 1 {
		t.Fatalf("Read traversal-like ID = %v, %v", lines, err)
	}
}

func TestAtomicSessionWriterConcurrentSessions(t *testing.T) {
	writer := NewAtomicSessionWriter(t.TempDir())
	const sessions = 8
	const entries = 10
	var waitGroup sync.WaitGroup
	for session := 0; session < sessions; session++ {
		sessionID := fmt.Sprintf("session-%d", session)
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for entry := 0; entry < entries; entry++ {
				if err := writer.Append(sessionID, map[string]int{"entry": entry}); err != nil {
					t.Errorf("Append(%s): %v", sessionID, err)
					return
				}
			}
		}()
	}
	waitGroup.Wait()
	for session := 0; session < sessions; session++ {
		lines, err := writer.Read(fmt.Sprintf("session-%d", session))
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != entries {
			t.Fatalf("session %d lines = %d, want %d", session, len(lines), entries)
		}
	}
}

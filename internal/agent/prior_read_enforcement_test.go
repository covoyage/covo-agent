package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestPriorReadBeforeHook(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.go")
	if err := os.WriteFile(existing, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "new.go")

	mkArgs := func(t *testing.T, path string) json.RawMessage {
		t.Helper()
		b, err := json.Marshal(map[string]any{"path": path})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("new file is exempt", func(t *testing.T) {
		hook := NewReadTracker().PriorReadBeforeHook()
		err := hook(context.Background(), &agentcore.HookContext{
			ToolName:  "write_file",
			Arguments: mkArgs(t, missing),
		})
		if err != nil {
			t.Errorf("expected new-file write to be allowed, got: %v", err)
		}
	})

	t.Run("existing unread file is blocked", func(t *testing.T) {
		hook := NewReadTracker().PriorReadBeforeHook()
		err := hook(context.Background(), &agentcore.HookContext{
			ToolName:  "write_file",
			Arguments: mkArgs(t, existing),
		})
		if err == nil {
			t.Error("expected existing unread file write to be blocked")
		}
	})

	t.Run("existing file allowed after read", func(t *testing.T) {
		tracker := NewReadTracker()
		tracker.RecordRead(existing)
		hook := tracker.PriorReadBeforeHook()
		err := hook(context.Background(), &agentcore.HookContext{
			ToolName:  "write_file",
			Arguments: mkArgs(t, existing),
		})
		if err != nil {
			t.Errorf("expected read file write to be allowed, got: %v", err)
		}
	})

	t.Run("non-write tool is ignored", func(t *testing.T) {
		hook := NewReadTracker().PriorReadBeforeHook()
		err := hook(context.Background(), &agentcore.HookContext{
			ToolName:  "read",
			Arguments: mkArgs(t, existing),
		})
		if err != nil {
			t.Errorf("expected non-write tool to be ignored, got: %v", err)
		}
	})
}

func TestShellWritePriorReadViolation(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.go")
	if err := os.WriteFile(existing, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		command     string
		read        bool // pre-record a read of existing
		wantBlocked bool
	}{
		{"overwrite existing unread via >", "echo hi > existing.go", false, true},
		{"heredoc overwrite existing unread", "cat > existing.go <<'EOF'\nx\nEOF", false, true},
		{"tee overwrite existing unread", "echo hi | tee existing.go", false, true},
		{"overwrite existing after read", "echo hi > existing.go", true, false},
		{"append to existing unread is allowed", "echo hi >> existing.go", false, false},
		{"tee append is allowed", "echo hi | tee -a existing.go", false, false},
		{"new file creation is exempt", "echo hi > brand_new.go", false, false},
		{"redirect to /dev/null ignored", "make build > /dev/null 2>&1", false, false},
		{"stderr dup ignored", "go test ./... 2>&1", false, false},
		{"read-only command no target", "grep -r foo existing.go", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewReadTracker()
			if tt.read {
				tracker.RecordRead(existing)
			}
			_, blocked := tracker.ShellWritePriorReadViolation(tt.command, dir)
			if blocked != tt.wantBlocked {
				t.Errorf("ShellWritePriorReadViolation(%q) blocked = %v, want %v",
					tt.command, blocked, tt.wantBlocked)
			}
		})
	}
}

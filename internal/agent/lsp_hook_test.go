package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/covoyage/covo-agent/internal/lsp"
	"github.com/covoyage/covonaut/agentcore"
)

// --- lspBeforeHook tests ---

func TestLspBeforeHook_NilManager(t *testing.T) {
	ca := &CovoAgent{lspManager: nil}
	hook := ca.lspBeforeHook()
	hc := &agentcore.HookContext{
		ToolName:  "edit",
		Arguments: json.RawMessage(`{"file_path":"test.go"}`),
	}
	// Must not panic.
	if err := hook(context.Background(), hc); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestLspBeforeHook_NoOpWhenInactive(t *testing.T) {
	ca := &CovoAgent{lspManager: lsp.NewManager(lsp.ManagerConfig{Enabled: false})}
	hook := ca.lspBeforeHook()
	hc := &agentcore.HookContext{
		ToolName:  "edit",
		Arguments: json.RawMessage(`{"file_path":"test.go"}`),
	}
	if err := hook(context.Background(), hc); err != nil {
		t.Errorf("expected nil error when inactive, got %v", err)
	}
}

func TestLspBeforeHook_NoOpForReadOnlyTool(t *testing.T) {
	ca := &CovoAgent{lspManager: lsp.NewManager(lsp.ManagerConfig{Enabled: true})}
	hook := ca.lspBeforeHook()
	hc := &agentcore.HookContext{
		ToolName:  "read",
		Arguments: json.RawMessage(`{"path":"test.go"}`),
	}
	if err := hook(context.Background(), hc); err != nil {
		t.Errorf("expected nil error for read-only tool, got %v", err)
	}
}

func TestLspBeforeHook_NoOpForUnknownTool(t *testing.T) {
	ca := &CovoAgent{lspManager: lsp.NewManager(lsp.ManagerConfig{Enabled: true})}
	hook := ca.lspBeforeHook()
	hc := &agentcore.HookContext{
		ToolName:  "web_search",
		Arguments: json.RawMessage(`{"query":"test"}`),
	}
	if err := hook(context.Background(), hc); err != nil {
		t.Errorf("expected nil error for unknown tool, got %v", err)
	}
}

func TestLspBeforeHook_EditToolNoCrash(t *testing.T) {
	// Even with LSP enabled and an edit tool, the hook should not crash.
	// SnapshotBaseline will be a no-op because no LSP client is running
	// for the temp file's workspace.
	ca := &CovoAgent{lspManager: lsp.NewManager(lsp.ManagerConfig{Enabled: true})}
	hook := ca.lspBeforeHook()
	hc := &agentcore.HookContext{
		ToolName:  "edit",
		Arguments: json.RawMessage(`{"file_path":"/tmp/nonexistent_test_file.go"}`),
	}
	if err := hook(context.Background(), hc); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// --- lspAfterToolCall tests ---

func TestLspAfterToolCall_NilManager(t *testing.T) {
	ca := &CovoAgent{lspManager: nil}
	hook := ca.lspAfterToolCall()
	tc := agentcore.ToolCall{Name: "edit", Arguments: `{"file_path":"test.go"}`}
	result := &agentcore.ToolResult{Result: "ok"}
	// Must not panic, must return nil (no modification).
	modified := hook(context.Background(), tc, result)
	if modified != nil {
		t.Errorf("expected nil when manager is nil, got %+v", modified)
	}
}

func TestLspAfterToolCall_NoOpWhenInactive(t *testing.T) {
	ca := &CovoAgent{lspManager: lsp.NewManager(lsp.ManagerConfig{Enabled: false})}
	hook := ca.lspAfterToolCall()
	tc := agentcore.ToolCall{Name: "edit", Arguments: `{"file_path":"test.go"}`}
	result := &agentcore.ToolResult{Result: "ok"}
	modified := hook(context.Background(), tc, result)
	if modified != nil {
		t.Errorf("expected nil when inactive, got %+v", modified)
	}
}

func TestLspAfterToolCall_NoOpForReadOnlyTool(t *testing.T) {
	ca := &CovoAgent{lspManager: lsp.NewManager(lsp.ManagerConfig{Enabled: true})}
	hook := ca.lspAfterToolCall()
	tc := agentcore.ToolCall{Name: "read", Arguments: `{"path":"test.go"}`}
	result := &agentcore.ToolResult{Result: "file contents"}
	modified := hook(context.Background(), tc, result)
	if modified != nil {
		t.Errorf("expected nil for read-only tool, got %+v", modified)
	}
}

func TestLspAfterToolCall_NoOpWhenResultHasError(t *testing.T) {
	ca := &CovoAgent{lspManager: lsp.NewManager(lsp.ManagerConfig{Enabled: true})}
	hook := ca.lspAfterToolCall()
	tc := agentcore.ToolCall{Name: "edit", Arguments: `{"file_path":"test.go"}`}
	result := &agentcore.ToolResult{Result: "", Err: context.DeadlineExceeded}
	modified := hook(context.Background(), tc, result)
	if modified != nil {
		t.Errorf("expected nil when result has error, got %+v", modified)
	}
}

func TestLspAfterToolCall_NoOpWhenNoDiagnostics(t *testing.T) {
	// Without a real LSP server, GetNewDiagnostics returns nil.
	// The hook should return nil (no modification to the result).
	ca := &CovoAgent{lspManager: lsp.NewManager(lsp.ManagerConfig{Enabled: true})}
	hook := ca.lspAfterToolCall()
	tc := agentcore.ToolCall{Name: "edit", Arguments: `{"file_path":"/tmp/nonexistent_test_file.go"}`}
	result := &agentcore.ToolResult{Result: "edit applied"}
	modified := hook(context.Background(), tc, result)
	if modified != nil {
		t.Errorf("expected nil when no diagnostics, got %+v", modified)
	}
}

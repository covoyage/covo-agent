package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// runNav invokes the code_navigate tool with the given raw args and returns
// (result, error). A nil mgr makes the tool report "LSP not active" before
// touching the filesystem, which is enough to exercise parameter validation.
func runNav(t *testing.T, args map[string]any) (any, error) {
	t.Helper()
	e := &lspNavExtension{} // mgr == nil → inactive branch
	tool := e.navTool()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return tool.Func(context.Background(), raw)
}

func TestLSPNavTool_DiagnosticsDoesNotRequireLine(t *testing.T) {
	// diagnostics is file-level; omitting line must not error at validation.
	got, err := runNav(t, map[string]any{"action": "diagnostics", "path": "foo.go"})
	if err != nil {
		t.Fatalf("diagnostics without line should not error, got: %v", err)
	}
	s, ok := got.(string)
	if !ok || !strings.Contains(s, "not active") {
		t.Fatalf("expected inactive notice, got %v", got)
	}
}

func TestLSPNavTool_OtherActionsRequireLine(t *testing.T) {
	for _, action := range []string{"definition", "references", "hover"} {
		_, err := runNav(t, map[string]any{"action": action, "path": "foo.go"})
		if err == nil {
			t.Errorf("%s without line should error", action)
		} else if !strings.Contains(err.Error(), "line is required") {
			t.Errorf("%s: unexpected error %v", action, err)
		}
	}
}

func TestLSPNavTool_EmptyPathErrors(t *testing.T) {
	if _, err := runNav(t, map[string]any{"action": "diagnostics", "path": ""}); err == nil {
		t.Fatal("empty path should error")
	}
}

func TestLSPNavTool_DiagnosticsAcceptsLine(t *testing.T) {
	// Passing a line for diagnostics is harmless (ignored) — must not error.
	got, err := runNav(t, map[string]any{"action": "diagnostics", "path": "foo.go", "line": 5})
	if err != nil {
		t.Fatalf("diagnostics with line should not error, got: %v", err)
	}
	if !strings.Contains(got.(string), "not active") {
		t.Fatalf("expected inactive notice, got %v", got)
	}
}

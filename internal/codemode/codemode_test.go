package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestGenerateSDK_SingleTool(t *testing.T) {
	tools := []ToolInfo{
		{Name: "search_files", Description: "Search for files", Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
				"include": map[string]any{"type": "string"},
			},
			"required": []any{"pattern"},
		}},
	}
	code := `result, err := ToolCallMap("search_files", map[string]any{"pattern": "TODO"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(result)`

	sdk := GenerateSDK(tools, code)

	// Should contain package declaration
	if !strings.Contains(sdk, "package main") {
		t.Error("missing package main")
	}
	// Should contain the tool function
	if !strings.Contains(sdk, "func SearchFiles(") {
		t.Error("missing SearchFiles function")
	}
	// Should contain the SDK helpers
	if !strings.Contains(sdk, "func ToolCall(") {
		t.Error("missing ToolCall helper")
	}
	// Should contain user code
	if !strings.Contains(sdk, `ToolCallMap("search_files"`) {
		t.Error("missing user code")
	}
	// Should initialize scanner in main
	if !strings.Contains(sdk, "_scanner = bufio.NewScanner") {
		t.Error("missing scanner init")
	}
}

func TestToolsFromDefinitions(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "web_fetch", Description: "Fetch a URL", Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string"},
			},
			"required": []any{"url"},
		}},
	}
	tools := ToolsFromDefinitions(defs)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "web_fetch" {
		t.Errorf("expected web_fetch, got %s", tools[0].Name)
	}
}

func TestToolNameToGoFunc(t *testing.T) {
	tests := []struct{ in, want string }{
		{"web_fetch", "WebFetch"},
		{"sandbox", "Sandbox"},
		{"search_files", "SearchFiles"},
		{"computer_use", "ComputerUse"},
		{"run_code", "RunCode"},
	}
	for _, tt := range tests {
		got := toolNameToGoFunc(tt.in)
		if got != tt.want {
			t.Errorf("toolNameToGoFunc(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExecute_BasicProgram(t *testing.T) {
	tools := []ToolInfo{
		{Name: "greet", Description: "Say hello", Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}},
	}
	executor := func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		return "hello from tool", nil
	}
	result, err := Execute(context.Background(), tools, `result, err := ToolCallStr("greet", map[string]any{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("got:", result)`, executor, 0)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Output, "got: hello from tool") {
		t.Errorf("unexpected output: %s", result.Output)
	}
}

func TestExecute_CompilationError(t *testing.T) {
	_, err := Execute(context.Background(), nil, `this is not valid go code !!!`, nil, 0)
	if err == nil {
		t.Fatal("expected error for invalid code")
	}
	if !strings.Contains(err.Error(), "compilation failed") {
		t.Errorf("expected compilation error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "fix the code") {
		t.Errorf("error should include fix guidance, got: %v", err)
	}
}

func TestExecute_MultipleToolCalls(t *testing.T) {
	tools := []ToolInfo{
		{Name: "alpha", Description: "Tool A", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
		{Name: "beta", Description: "Tool B", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
	}
	callCount := 0
	executor := func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		callCount++
		if name == "alpha" {
			return "result-alpha", nil
		}
		return "result-beta", nil
	}
	code := `r1, err := ToolCallStr("alpha", map[string]any{})
	if err != nil { fmt.Println("err1:", err); return }
	r2, err := ToolCallStr("beta", map[string]any{})
	if err != nil { fmt.Println("err2:", err); return }
	fmt.Println(r1, r2)`
	result, err := Execute(context.Background(), tools, code, executor, 0)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Output, "result-alpha result-beta") {
		t.Errorf("unexpected output: %s", result.Output)
	}
	if callCount != 2 {
		t.Errorf("expected 2 tool calls, got %d", callCount)
	}
}

func TestExecute_ToolError(t *testing.T) {
	tools := []ToolInfo{
		{Name: "fail_tool", Description: "Always fails", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{},
		}},
	}
	executor := func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		return nil, fmt.Errorf("permission denied")
	}
	code := `_, err := ToolCallStr("fail_tool", map[string]any{})
	if err != nil {
		fmt.Println("caught:", err)
	} else {
		fmt.Println("should have failed")
	}`
	result, err := Execute(context.Background(), tools, code, executor, 0)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result.Output, "caught: permission denied") {
		t.Errorf("expected error propagation, got: %s", result.Output)
	}
}

func TestExecute_RuntimeError(t *testing.T) {
	tools := []ToolInfo{}
	code := `panic("oops")`
	result, err := Execute(context.Background(), tools, code, nil, 0)
	if err == nil {
		t.Fatal("expected error for runtime panic")
	}
	if result == nil {
		t.Fatal("expected result even on error")
	}
	if result.ExitCode == 0 {
		t.Errorf("exit code should be non-zero, got %d", result.ExitCode)
	}
}

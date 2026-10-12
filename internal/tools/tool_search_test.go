package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covo-agent/internal/evolution"
	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

func TestHideCyberOnlyTools(t *testing.T) {
	defs := []agentcore.ToolDefinition{
		{Name: "read"},
		{Name: "scan"},
		{Name: "finding_upsert"},
	}
	hidden := hideCyberOnlyTools(defs, false)
	for _, td := range hidden {
		if td.Name == "scan" || td.Name == "finding_upsert" {
			t.Fatalf("cyber-only tool %q leaked to tool_search outside cyber mode", td.Name)
		}
	}
	shown := hideCyberOnlyTools(defs, true)
	if len(shown) != 3 {
		t.Fatalf("cyber mode should keep all tools, got %d", len(shown))
	}
}

func TestCyberDelegationRequiresExplicitRole(t *testing.T) {
	for _, test := range []struct {
		cyberMode         bool
		role, contextRole string
	}{
		{false, "", ""},
		{true, "parent", "recon"},
		{true, "recon", "parent"},
	} {
		cyberMode := test.cyberMode
		home := t.TempDir()
		extension := NewExtension(ExtensionConfig{
			HomeDir: home, SessionsDir: filepath.Join(home, "sessions"), WorkDir: home, CyberMode: cyberMode, SubagentRole: test.role,
		})
		t.Cleanup(func() { _ = extension.Dispose() })
		workshop := evolution.NewWorkshopStore(home)
		if err := workshop.Init(); err != nil {
			t.Fatal(err)
		}
		extension.SetWorkshopStore(workshop)
		core := agentcore.New(agentcore.Config{})
		t.Cleanup(core.Close)
		if err := extension.Init(context.Background(), core); err != nil {
			t.Fatal(err)
		}
		seen := 0
		for _, tool := range extension.tools {
			if cyberMode && tool.Name == "scan" {
				ctx := toolssubagent.WithSubagentRole(context.Background(), test.contextRole)
				result, err := tool.Func(ctx, json.RawMessage(`{"recipe":"list"}`))
				if test.role == "parent" && err == nil {
					t.Fatalf("parent inherited recon context and ran scan: %+v", result)
				}
				if test.role == "recon" && err != nil {
					t.Fatalf("fixed recon role could not run scan with stale context: %v", err)
				}
			}
			if tool.Name != "sessions_spawn" && tool.Name != "sessions_spawn_batch" {
				continue
			}
			seen++
			schema := tool.Parameters
			if tool.Name == "sessions_spawn_batch" {
				schema = schema["properties"].(map[string]any)["tasks"].(map[string]any)["items"].(map[string]any)
			}
			requiredRole := false
			for _, field := range schema["required"].([]string) {
				requiredRole = requiredRole || field == "role"
			}
			if requiredRole != cyberMode {
				t.Fatalf("%s cyber=%v required role=%v", tool.Name, cyberMode, requiredRole)
			}
			if cyberMode && !strings.Contains(tool.Description, "leaf has no hands-on tools") {
				t.Fatalf("missing Cyber delegation guidance: %s", tool.Description)
			}
		}
		if seen != 2 {
			t.Fatalf("expected both delegation tools, got %d", seen)
		}
	}
}

func TestToolCallRejectsSelf(t *testing.T) {
	ag := agentcore.New(agentcore.Config{})
	tool := buildToolCallTool(ag)
	raw, _ := json.Marshal(map[string]any{
		"name":      "tool_call",
		"arguments": map[string]any{},
	})
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	if result["status"] != "invalid_request" || !strings.Contains(result["error"].(string), "cannot invoke itself") {
		t.Fatalf("expected recoverable self-call rejection, got %#v", out)
	}
}

func TestToolCallMissingNameIsRecoverable(t *testing.T) {
	ag := agentcore.New(agentcore.Config{})
	out, err := buildToolCallTool(ag).Func(context.Background(), json.RawMessage(`{"arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	if result["status"] != "invalid_request" || !strings.Contains(result["error"].(string), "name is required") {
		t.Fatalf("unexpected result %#v", out)
	}
}

func TestToolSearchOmitsDiscoveryMetaTools(t *testing.T) {
	tool := buildToolSearchTool(func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{
			{Name: "tool_call", Description: "invoke tool"},
			{Name: "tool_describe", Description: "describe tool"},
			{Name: "scan", Description: "scan target with tool"},
		}
	})
	out, err := tool.Func(context.Background(), json.RawMessage(`{"query":"tool"}`))
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	matches := result["matches"].([]map[string]string)
	if len(matches) != 1 || matches[0]["name"] != "scan" {
		t.Fatalf("meta tools leaked into search: %#v", matches)
	}
}

func TestCyberRoleVisibilityAppliesToSearchAndCall(t *testing.T) {
	called := false
	ag := agentcore.New(agentcore.Config{})
	ag.RegisterTools(&agentcore.Tool{
		Name: "scan",
		Func: func(context.Context, json.RawMessage) (any, error) {
			called = true
			return "ok", nil
		},
	})
	visibility := func(ctx context.Context, name string) bool {
		return cyber.AllowOrchestratorTool(toolssubagent.SubagentRoleFromContext(ctx), name)
	}
	defs := func() []agentcore.ToolDefinition {
		return []agentcore.ToolDefinition{{Name: "scan", Description: "scan target"}}
	}
	search := buildToolSearchTool(defs, visibility)
	parentResult, err := search.Func(context.Background(), json.RawMessage(`{"query":"scan"}`))
	if err != nil {
		t.Fatal(err)
	}
	if matches := parentResult.(map[string]any)["matches"].([]map[string]string); len(matches) != 0 {
		t.Fatalf("parent discovered hands-on tool: %#v", matches)
	}

	call := buildToolCallTool(ag, visibility)
	parentCall, err := call.Func(context.Background(), json.RawMessage(`{"name":"scan","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if parentCall.(map[string]any)["status"] != "unavailable" || called {
		t.Fatalf("parent invoked hands-on tool: %#v called=%v", parentCall, called)
	}

	reconCtx := toolssubagent.WithSubagentRole(context.Background(), "recon")
	reconResult, err := search.Func(reconCtx, json.RawMessage(`{"query":"scan"}`))
	if err != nil {
		t.Fatal(err)
	}
	if matches := reconResult.(map[string]any)["matches"].([]map[string]string); len(matches) != 1 {
		t.Fatalf("recon could not discover scan: %#v", matches)
	}
	if _, err := call.Func(reconCtx, json.RawMessage(`{"name":"scan","arguments":{}}`)); err != nil || !called {
		t.Fatalf("recon could not invoke scan: err=%v called=%v", err, called)
	}
}

func TestToolCallRunsInvokeToolHooks(t *testing.T) {
	var seen []string
	ag := agentcore.New(agentcore.Config{
		ExecutionConfig: agentcore.ExecutionConfig{
			GlobalBefore: []agentcore.BeforeHook{
				func(_ context.Context, hc *agentcore.HookContext) error {
					seen = append(seen, hc.ToolName)
					return nil
				},
			},
		},
	})
	echo := &agentcore.Tool{
		Name: "echo",
		Func: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "pong", nil
		},
	}
	ag.RegisterTools(echo)
	if _, err := echo.Func(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("direct Func must not run hooks, seen %v", seen)
	}

	call := buildToolCallTool(ag)
	raw, _ := json.Marshal(map[string]any{
		"name":      "echo",
		"arguments": map[string]any{},
	})
	out, err := call.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["status"] != "ok" {
		t.Fatalf("unexpected result %#v", out)
	}
	if len(seen) != 1 || seen[0] != "echo" {
		t.Fatalf("InvokeTool should run hooks, seen %v", seen)
	}
}

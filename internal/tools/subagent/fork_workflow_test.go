package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestSessionsFork_ReconRole(t *testing.T) {
	var capturedTask string
	var capturedCtx context.Context
	var capturedToolsets []string
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capturedTask = task
		capturedCtx = ctx
		capturedToolsets = append([]string(nil), toolsets...)
		return "ok", nil
	}
	tool := BuildSessionsForkTool(runner, NewSubagentRegistry(), NewForkGuard(4), func() []agentcore.Message {
		return []agentcore.Message{{Role: agentcore.RoleUser, Content: "prior notes"}}
	})
	args, _ := json.Marshal(map[string]any{
		"task":     "map example.com",
		"role":     "recon",
		"toolsets": []string{},
	})
	if _, err := tool.Func(context.Background(), args); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if SubagentRoleFromContext(capturedCtx) != "recon" {
		t.Fatalf("role=%q", SubagentRoleFromContext(capturedCtx))
	}
	if !strings.Contains(capturedTask, "reconnaissance specialist") {
		t.Fatalf("task missing recon prompt: %q", capturedTask)
	}
	if !IsStructuredFork(capturedCtx) || ParentMessagesFromContext(capturedCtx)[0].Content != "prior notes" {
		t.Fatalf("runtime history snapshot missing")
	}
	found := false
	for _, name := range capturedToolsets {
		if name == "cyber" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("toolsets=%v, want cyber", capturedToolsets)
	}
}

func TestForkSnapshotPreservesCompletedPairs(t *testing.T) {
	parent := []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: "parent-only policy"},
		{Role: agentcore.RoleUser, Content: "task", Metadata: map[string]any{"approval": "granted"}},
		{Role: agentcore.RoleAssistant, ToolCalls: []agentcore.ToolCall{{ID: "read-1", Name: "read", Arguments: `{"path":"a.go"}`}}},
		{Role: agentcore.RoleTool, ToolCallID: "read-1", Content: "file contents"},
		{Role: agentcore.RoleAssistant, ToolCalls: []agentcore.ToolCall{{ID: "fork-1", Name: "sessions_fork"}}},
	}
	snapshot, err := BuildForkSnapshot(parent)
	if err != nil || len(snapshot) != 3 || snapshot[2].ToolCallID != "read-1" {
		t.Fatalf("snapshot: %+v, %v", snapshot, err)
	}
	if snapshot[0].Metadata["approval"] != nil {
		t.Fatal("historical approval inherited")
	}
	snapshot[1].ToolCalls[0].Arguments = "changed"
	if parent[2].ToolCalls[0].Arguments != `{"path":"a.go"}` {
		t.Fatal("snapshot aliases parent tool calls")
	}
	if _, err := BuildForkSnapshot([]agentcore.Message{{Role: agentcore.RoleTool, ToolCallID: "orphan"}}); err == nil {
		t.Fatal("orphan result accepted")
	}
	if _, err := BuildForkSnapshot([]agentcore.Message{parent[2], {Role: agentcore.RoleUser, Content: "next"}}); err == nil {
		t.Fatal("incomplete interior batch accepted")
	}
}

func TestSessionsForkRuntimeDepthAndHistory(t *testing.T) {
	calls := 0
	runner := func(ctx context.Context, task string, tools []string, turns int) (string, error) {
		calls++
		if SubagentDepthFromContext(ctx) != 2 || !IsStructuredFork(ctx) {
			t.Fatal("runtime fork metadata lost")
		}
		return "ok", nil
	}
	tool := BuildSessionsForkTool(runner, nil, NewForkGuard(2), func() []agentcore.Message {
		return []agentcore.Message{{Role: agentcore.RoleUser, Content: "actual history"}}
	})
	ctx := WithSubagentDepth(context.Background(), 1)
	if _, err := tool.Func(ctx, json.RawMessage(`{"task":"child"}`)); err != nil {
		t.Fatal(err)
	}
	for _, args := range []string{`{"task":"child","history":"forged"}`, `{"task":"child","depth":0}`, `{"task":"child","role":"parent"}`} {
		if _, err := tool.Func(ctx, json.RawMessage(args)); err == nil {
			t.Fatalf("forged fork accepted: %s", args)
		}
	}
	if _, err := tool.Func(WithSubagentDepth(ctx, 2), json.RawMessage(`{"task":"child"}`)); err == nil {
		t.Fatal("runtime depth bypassed")
	}
	if calls != 1 {
		t.Fatalf("runner calls=%d", calls)
	}
}

func TestStructuredForkClearsTurnsLimitAndDoesNotLeakToSpawn(t *testing.T) {
	ctx := WithParentMessageTurns(context.Background(), 1)
	ctx = WithStructuredFork(ctx, []agentcore.Message{{Role: agentcore.RoleUser, Content: "history"}})
	if !IsStructuredFork(ctx) || ParentMessageTurnsFromContext(ctx) != 0 {
		t.Fatal("structured snapshot could be truncated by inherited turns limit")
	}
	spawnCtx := WithParentMessages(ctx, nil)
	if IsStructuredFork(spawnCtx) {
		t.Fatal("ordinary spawn inherited structured-fork marker")
	}
}

func TestWorkflow_ReconRole(t *testing.T) {
	var capturedTask string
	var capturedCtx context.Context
	var capturedToolsets []string
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capturedTask = task
		capturedCtx = ctx
		capturedToolsets = append([]string(nil), toolsets...)
		return "ok", nil
	}
	tool := BuildWorkflowTool(runner, NewSubagentRegistry())
	args, _ := json.Marshal(map[string]any{
		"phases": []map[string]any{
			{"label": "recon", "task": "map example.com", "role": "recon"},
		},
	})
	if _, err := tool.Func(context.Background(), args); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if SubagentRoleFromContext(capturedCtx) != "recon" {
		t.Fatalf("role=%q", SubagentRoleFromContext(capturedCtx))
	}
	if !strings.Contains(capturedTask, "reconnaissance specialist") {
		t.Fatalf("task missing recon prompt: %q", capturedTask)
	}
	found := false
	for _, name := range capturedToolsets {
		if name == "cyber" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("toolsets=%v, want cyber", capturedToolsets)
	}
}

func TestAdvancedWorkflow_ReconRole(t *testing.T) {
	var capturedTask string
	var capturedCtx context.Context
	var capturedToolsets []string
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capturedTask = task
		capturedCtx = ctx
		capturedToolsets = append([]string(nil), toolsets...)
		return "ok", nil
	}
	tool := BuildAdvancedWorkflowTool(runner, NewSubagentRegistry(), t.TempDir())
	args, _ := json.Marshal(map[string]any{
		"name": "recon",
		"phases": []map[string]any{
			{"id": "p1", "prompt": "map example.com", "role": "recon"},
		},
	})
	if _, err := tool.Func(context.Background(), args); err != nil {
		t.Fatalf("advanced workflow: %v", err)
	}
	if SubagentRoleFromContext(capturedCtx) != "recon" {
		t.Fatalf("role=%q", SubagentRoleFromContext(capturedCtx))
	}
	if !strings.Contains(capturedTask, "reconnaissance specialist") {
		t.Fatalf("task missing recon prompt: %q", capturedTask)
	}
	found := false
	for _, name := range capturedToolsets {
		if name == "cyber" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("toolsets=%v, want cyber", capturedToolsets)
	}
}

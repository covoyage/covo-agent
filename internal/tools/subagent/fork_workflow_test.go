package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
	tool := BuildSessionsForkTool(runner, NewSubagentRegistry(), NewForkGuard(4))
	args, _ := json.Marshal(map[string]any{
		"task":     "map example.com",
		"history":  "prior notes",
		"role":     "recon",
		"depth":    0,
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
	if !strings.Contains(capturedTask, "prior notes") {
		t.Fatalf("task missing inherited history: %q", capturedTask)
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

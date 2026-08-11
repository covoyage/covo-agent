package agent

import (
	"context"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

func TestExecutionPhase_Valid(t *testing.T) {
	if !PhasePlan.Valid() {
		t.Error("PhasePlan should be valid")
	}
	if !PhaseAct.Valid() {
		t.Error("PhaseAct should be valid")
	}
	invalid := ExecutionPhase("invalid")
	if invalid.Valid() {
		t.Error("invalid phase should not be valid")
	}
}

func TestExecutionPhase_String(t *testing.T) {
	if PhasePlan.String() != "plan" {
		t.Errorf("PhasePlan.String() = %q, want %q", PhasePlan.String(), "plan")
	}
	if PhaseAct.String() != "act" {
		t.Errorf("PhaseAct.String() = %q, want %q", PhaseAct.String(), "act")
	}
}

func TestCovoAgent_ExecutionPhase_Default(t *testing.T) {
	ca := &CovoAgent{}
	if ca.IsPlanMode() {
		t.Error("default execution phase should not be Plan mode")
	}
}

func TestCovoAgent_SetExecutionPhase(t *testing.T) {
	ca := &CovoAgent{}

	ca.SetExecutionPhase(PhasePlan)
	if !ca.IsPlanMode() {
		t.Error("after SetExecutionPhase(PhasePlan), IsPlanMode should be true")
	}
	if ca.ExecutionPhase() != PhasePlan {
		t.Errorf("ExecutionPhase = %q, want %q", ca.ExecutionPhase(), PhasePlan)
	}

	ca.SetExecutionPhase(PhaseAct)
	if ca.IsPlanMode() {
		t.Error("after SetExecutionPhase(PhaseAct), IsPlanMode should be false")
	}
}

func TestPlanModeAllowedTools_ContainsReadonlyTools(t *testing.T) {
	required := []string{"read", "grep", "glob", "ls", "exit_plan_mode", "todo", "update_plan"}
	for _, name := range required {
		if !PlanModeAllowedTools[name] {
			t.Errorf("PlanModeAllowedTools should contain %q", name)
		}
	}
}

func TestPlanModeAllowedTools_ExcludesMutatingTools(t *testing.T) {
	// These are mutating tools that should NOT be in the allowed set.
	blocked := []string{"write_file", "edit", "edit_block", "bash", "execute_command"}
	for _, name := range blocked {
		if PlanModeAllowedTools[name] {
			t.Errorf("PlanModeAllowedTools should NOT contain %q (mutating tool)", name)
		}
	}
}

func TestPlanModeGateBeforeHook_BlocksInPlanMode(t *testing.T) {
	ca := &CovoAgent{}
	ca.SetExecutionPhase(PhasePlan)

	hook := ca.planModeGateBeforeHook()

	// Read-only tool should pass.
	err := hook(context.Background(), &agentcore.HookContext{ToolName: "read"})
	if err != nil {
		t.Errorf("read tool should be allowed in Plan mode, got error: %v", err)
	}

	// Mutating tool should be blocked.
	err = hook(context.Background(), &agentcore.HookContext{ToolName: "write_file"})
	if err == nil {
		t.Error("write_file tool should be blocked in Plan mode")
	}

	// exit_plan_mode should pass.
	err = hook(context.Background(), &agentcore.HookContext{ToolName: "exit_plan_mode"})
	if err != nil {
		t.Errorf("exit_plan_mode should be allowed in Plan mode, got error: %v", err)
	}
}

func TestPlanModeGateBeforeHook_AllowsAllInActMode(t *testing.T) {
	ca := &CovoAgent{}
	// Default phase is Act (not Plan).

	hook := ca.planModeGateBeforeHook()

	// All tools should pass in Act mode.
	tools := []string{"read", "write_file", "edit", "bash", "exit_plan_mode"}
	for _, name := range tools {
		err := hook(context.Background(), &agentcore.HookContext{ToolName: name})
		if err != nil {
			t.Errorf("tool %q should be allowed in Act mode, got error: %v", name, err)
		}
	}
}

func TestPlanModeGateBeforeHook_BlocksUnknownTools(t *testing.T) {
	ca := &CovoAgent{}
	ca.SetExecutionPhase(PhasePlan)

	hook := ca.planModeGateBeforeHook()

	// Unknown tools (not in PlanModeAllowedTools) should be BLOCKED.
	// This is deny-by-default — critical for security.
	unknownTools := []string{"some_custom_tool", "delete_file", "write", "apply_patch", "sessions_spawn"}
	for _, name := range unknownTools {
		err := hook(context.Background(), &agentcore.HookContext{ToolName: name})
		if err == nil {
			t.Errorf("unknown tool %q should be blocked in Plan mode (deny-by-default)", name)
		}
	}
}

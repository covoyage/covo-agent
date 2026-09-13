package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covonaut/agentcore"
)

// TestStripDelegationToolsByToolName verifies the tool-name form (the list
// actually passed as the child's toolset override) strips the whole family.
func TestStripDelegationToolsByToolName(t *testing.T) {
	in := []string{
		"read_file", "bash", "sessions_spawn", "sessions_spawn_batch",
		"sessions_wait", "close_agent", "interrupt_agent", "send_input",
		"sessions_fork", "subagents", "swarm", "swarm_orchestrate",
	}
	out := StripDelegationTools(in)
	want := map[string]bool{"read_file": true, "bash": true}
	if len(out) != len(want) {
		t.Fatalf("expected %d tools, got %v", len(want), out)
	}
	for _, name := range out {
		if !want[name] {
			t.Fatalf("unexpected survivor %q in %v", name, out)
		}
	}
}

// TestStripDelegationToolsLegacyToolsetName verifies the legacy form (a list
// of toolset names) still strips the "delegation" group name.
func TestStripDelegationToolsLegacyToolsetName(t *testing.T) {
	out := StripDelegationTools([]string{"filesystem", "delegation", "search"})
	if len(out) != 2 {
		t.Fatalf("expected delegation toolset stripped, got %v", out)
	}
	for _, n := range out {
		if n == "delegation" {
			t.Fatal("delegation toolset name survived")
		}
	}
}

// TestDelegateToolsetsForRoleOrchestratorKeepsDelegation verifies that an
// orchestrator child keeps delegation tools while a leaf loses them.
func TestDelegateToolsetsForRoleOrchestratorKeepsDelegation(t *testing.T) {
	parent := []string{"bash", "read_file", "sessions_spawn", "sessions_spawn_batch", "clarify"}
	// The spawn flow fills childRequested with the parent's tool names when
	// the model does not request specific toolsets.
	inherited := append([]string(nil), parent...)

	leaf := DelegateToolsetsForRole(parent, inherited, true, false)
	for _, n := range leaf {
		if n == "sessions_spawn" || n == "sessions_spawn_batch" || n == "clarify" {
			t.Fatalf("leaf child kept %q: %v", n, leaf)
		}
	}

	orch := DelegateToolsetsForRole(parent, inherited, true, true)
	hasSpawn := false
	for _, n := range orch {
		if n == "sessions_spawn" {
			hasSpawn = true
		}
		if n == "clarify" {
			t.Fatalf("orchestrator child kept clarify: %v", orch)
		}
	}
	if !hasSpawn {
		t.Fatalf("orchestrator child lost sessions_spawn: %v", orch)
	}
}

// TestSpawnToolDescriptionContainsRoster verifies the tool description embeds
// the live roster (personas/roles/depth) at construction time.
func TestSpawnToolDescriptionContainsRoster(t *testing.T) {
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "done", nil
	}
	tool := BuildSessionsSpawnTool(runner, nil,
		func() []string { return nil },
		func() []agentcore.Message { return nil },
		nil, nil)

	if !strings.Contains(tool.Description, "Roster for this session") {
		t.Fatal("expected roster section in tool description")
	}
	if !strings.Contains(tool.Description, "Max spawn depth") {
		t.Fatal("expected spawn depth line in roster")
	}
	// Personas are registered in init(); at least the roster header plus a
	// persona or role line should exist.
	if !strings.Contains(tool.Description, "Personas") && !strings.Contains(tool.Description, "Preset roles") {
		t.Fatalf("expected personas/roles line, description:\n%s", tool.Description)
	}
	_ = json.RawMessage{} // keep encoding/json import if assertions change
}

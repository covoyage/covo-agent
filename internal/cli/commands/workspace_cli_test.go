package commands

import (
	"testing"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
)

func resetCLIWorkspaceState() {
	shared.RuntimeState.SetSessionYolo(false)
	shared.RuntimeState.SetPermissionMode("")
	shared.RuntimeState.SetExtraWorkspaceRoots(nil)
}

func TestApplyCLIPermissionAndDirsInteractiveDefault(t *testing.T) {
	resetCLIWorkspaceState()
	t.Cleanup(resetCLIWorkspaceState)
	mode, roots, err := applyCLIPermissionAndDirs("", nil, t.TempDir(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if mode != agent.PermissionAsk {
		t.Fatalf("mode = %s", mode)
	}
	if len(roots) != 0 {
		t.Fatalf("roots = %v", roots)
	}
	if shared.RuntimeState.PermissionMode() != string(agent.PermissionAsk) {
		t.Fatalf("state mode = %s", shared.RuntimeState.PermissionMode())
	}
}

func TestApplyCLIPermissionAndDirsExecDefault(t *testing.T) {
	resetCLIWorkspaceState()
	t.Cleanup(resetCLIWorkspaceState)
	mode, _, err := applyCLIPermissionAndDirs("", nil, t.TempDir(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	if mode != agent.PermissionAuto {
		t.Fatalf("mode = %s", mode)
	}
}

func TestApplyCLIPermissionAndDirsYoloWins(t *testing.T) {
	resetCLIWorkspaceState()
	t.Cleanup(resetCLIWorkspaceState)
	mode, _, err := applyCLIPermissionAndDirs("ask", nil, t.TempDir(), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if mode != agent.PermissionFull {
		t.Fatalf("mode = %s", mode)
	}
	if !shared.RuntimeState.SessionYolo() {
		t.Fatal("expected session yolo")
	}
}

func TestApplyCLIPermissionAndDirsAddDir(t *testing.T) {
	resetCLIWorkspaceState()
	t.Cleanup(resetCLIWorkspaceState)
	extra := t.TempDir()
	work := t.TempDir()
	_, roots, err := applyCLIPermissionAndDirs("", []string{extra}, work, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 {
		t.Fatalf("roots = %v", roots)
	}
	if len(shared.RuntimeState.ExtraWorkspaceRoots()) != 1 {
		t.Fatalf("state roots = %v", shared.RuntimeState.ExtraWorkspaceRoots())
	}
}

package commands

import (
	"fmt"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/cli/commands/shared"
	"github.com/covoyage/covo-agent/internal/workspace"
)

func resolveExtraWorkspaceRoots(workingDir string, addDir []string) ([]string, error) {
	var roots []string
	for _, dir := range addDir {
		next, _, err := workspace.AddExtraRoot(workingDir, roots, dir)
		if err != nil {
			return nil, fmt.Errorf("add-dir %s: %w", dir, err)
		}
		roots = next
	}
	return roots, nil
}

func applyCLIPermissionAndDirs(permission string, addDir []string, workingDir string, interactive, yolo bool) (agent.PermissionMode, []string, error) {
	roots, err := resolveExtraWorkspaceRoots(workingDir, addDir)
	if err != nil {
		return "", nil, err
	}
	if yolo || shared.RuntimeState.SessionYolo() {
		permission = string(agent.PermissionFull)
	}
	mode, err := agent.NormalizePermissionMode(permission, interactive)
	if err != nil {
		return "", nil, err
	}
	shared.RuntimeState.SetExtraWorkspaceRoots(roots)
	shared.RuntimeState.SetPermissionMode(string(mode))
	shared.RuntimeState.SetSessionYolo(mode == agent.PermissionFull)
	return mode, roots, nil
}

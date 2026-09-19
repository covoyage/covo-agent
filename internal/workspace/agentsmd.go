package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const AgentsFileName = "AGENTS.md"

const defaultAgentsMarkdown = `# Project guidance

Describe how this repository should be worked on.

## Goal

- What this project does
- What a successful change looks like

## Layout

- Important packages, apps, or entry points
- Generated or vendored paths to avoid editing

## Commands

- Build:
- Test:
- Lint:

## Constraints

- Files or directories that must not be changed
- Required review or verification steps
`

// AgentsPath returns the AGENTS.md path for a working directory.
func AgentsPath(workingDir string) string {
	if workingDir == "" {
		workingDir, _ = os.Getwd()
	}
	return filepath.Join(workingDir, AgentsFileName)
}

// WriteAgentsFile creates AGENTS.md when missing.
func WriteAgentsFile(workingDir string, force bool) (path string, created bool, err error) {
	path = AgentsPath(workingDir)
	if !force {
		if _, statErr := os.Stat(path); statErr == nil {
			return path, false, nil
		} else if !os.IsNotExist(statErr) {
			return path, false, statErr
		}
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(defaultAgentsMarkdown)+"\n"), 0o644); err != nil {
		return path, false, fmt.Errorf("write %s: %w", path, err)
	}
	return path, true, nil
}

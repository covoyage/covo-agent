package agent

import (
	"os"
	"path/filepath"
)

// externalSkillEcosystems lists compatible home/project subdirectories whose
// skills can be reused. Each is scanned at <root>/skills for SKILL.md files.
var externalSkillEcosystems = []struct {
	dir        string // home/project subdirectory
	disableEnv string // per-source opt-out env var
}{
	{".agents", "COVO_DISABLE_AGENTS_SKILLS"},
	{".claude", "COVO_DISABLE_CLAUDE_SKILLS"},
	{".codex", "COVO_DISABLE_CODEX_SKILLS"},
	{".opencode", "COVO_DISABLE_OPENCODE_SKILLS"},
}

// ecosystemSkillRoots returns <base>/<ecosystem>/skills for each enabled
// external ecosystem. Missing directories are left in the list; skill.Load
// treats them as empty.
func ecosystemSkillRoots(base string) []string {
	if base == "" || envBool("COVO_DISABLE_EXTERNAL_SKILLS", false) {
		return nil
	}
	var roots []string
	for _, src := range externalSkillEcosystems {
		if envBool(src.disableEnv, false) {
			continue
		}
		roots = append(roots, filepath.Join(base, src.dir, "skills"))
	}
	return roots
}

// skillLoadPaths builds the ordered list of skill roots for skill.Load.
// First match wins on name collision:
//  1. workspace skills/ (the current repo)
//  2. project-level ecosystem skills (.agents/.claude/...)
//  3. personal ecosystem skills under $HOME
//  4. the managed covo skills directory (bundled + agent-created)
//  5. extra directories from config
func skillLoadPaths(covoSkillsDir, workDir string, extraDirs []string) []string {
	osHome, err := os.UserHomeDir()
	if err != nil {
		osHome = ""
	}

	var paths []string
	if workDir != "" {
		paths = append(paths, filepath.Join(workDir, "skills"))
		paths = append(paths, ecosystemSkillRoots(workDir)...)
	}
	if osHome != "" && osHome != workDir {
		paths = append(paths, ecosystemSkillRoots(osHome)...)
	}
	if covoSkillsDir != "" {
		paths = append(paths, covoSkillsDir)
	}
	paths = append(paths, extraDirs...)
	return paths
}

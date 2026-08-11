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
	{".claude", "COVO_DISABLE_CLAUDE_SKILLS"},
	{".codex", "COVO_DISABLE_CODEX_SKILLS"},
	{".agents", "COVO_DISABLE_AGENTS_SKILLS"},
	{".opencode", "COVO_DISABLE_OPENCODE_SKILLS"},
}

// externalSkillRoots returns the skill roots to scan from other agent
// ecosystems, both globally (under the OS home dir) and per-project (under the
// working dir). Each `<base>/<ecosystem>/skills` directory is included unless
// disabled.
//
// Discovery is enabled by default and is a no-op for users who don't have those
// directories (a missing path loads nothing). It can be turned off entirely
// with COVO_DISABLE_EXTERNAL_SKILLS, or per ecosystem with its source-specific
// opt-out environment variable.
func externalSkillRoots(osHome, workDir string) []string {
	if envBool("COVO_DISABLE_EXTERNAL_SKILLS", false) {
		return nil
	}
	var roots []string
	for _, src := range externalSkillEcosystems {
		if envBool(src.disableEnv, false) {
			continue
		}
		if osHome != "" {
			roots = append(roots, filepath.Join(osHome, src.dir, "skills"))
		}
		if workDir != "" && workDir != osHome {
			roots = append(roots, filepath.Join(workDir, src.dir, "skills"))
		}
	}
	return roots
}

// skillLoadPaths builds the ordered list of skill roots for skill.Load. The
// agent's own skills dir comes first so it wins any name collision with an
// external ecosystem skill.
func skillLoadPaths(covoSkillsDir, workDir string) []string {
	paths := []string{covoSkillsDir}
	osHome, err := os.UserHomeDir()
	if err != nil {
		osHome = ""
	}
	paths = append(paths, externalSkillRoots(osHome, workDir)...)
	return paths
}

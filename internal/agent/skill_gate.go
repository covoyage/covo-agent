package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/covoyage/covo-agent/internal/evolution"

	"github.com/covoyage/covonaut/skill"
	"gopkg.in/yaml.v3"
)

// SkillCatalogEntry is one loaded or gated skill for /skills.
type SkillCatalogEntry struct {
	Name   string
	Source string
	Path   string
	Kept   bool
	Reason string
}

// skillRequirements is the subset of SKILL.md frontmatter used to decide
// whether a skill should appear in the model catalog.
type skillRequirements struct {
	Platforms     []string `yaml:"platforms"`
	Prerequisites struct {
		Commands []string `yaml:"commands"`
		Env      []string `yaml:"env"`
		EnvVars  []string `yaml:"env_vars"`
	} `yaml:"prerequisites"`
}

// filterLoadedSkills drops disabled skills and skills whose declared
// binaries/env/platform requirements are not met. First-wins collision
// handling is already done by skill.Load.
func filterLoadedSkills(skills []skill.Skill, disabled []string, goos string) []skill.Skill {
	if len(skills) == 0 {
		return skills
	}
	deny := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		name = strings.TrimSpace(strings.ToLower(name))
		if name != "" {
			deny[name] = true
		}
	}
	out := make([]skill.Skill, 0, len(skills))
	for _, item := range skills {
		if deny[strings.ToLower(item.Name)] {
			continue
		}
		if !skillAvailable(item, goos) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func skillAvailable(item skill.Skill, goos string) bool {
	_, blocked := skillUnavailableReason(item, goos)
	return !blocked
}

func skillUnavailableReason(item skill.Skill, goos string) (string, bool) {
	if item.FilePath != "" && !evolution.MatchesBundledSkill(filepath.Dir(item.FilePath)) {
		result, err := evolution.ScanSkill(filepath.Dir(item.FilePath), "external")
		if err != nil {
			return "security audit failed: " + err.Error(), true
		}
		if result.Incomplete {
			return "security audit incomplete", true
		}
		for _, finding := range result.Findings {
			if finding.Severity == "critical" || finding.Severity == "high" {
				return "security audit: " + finding.PatternID + " in " + finding.File, true
			}
		}
	}
	req := parseSkillRequirements(item.FilePath)
	if len(req.Platforms) > 0 && !platformMatches(req.Platforms, goos) {
		return "platform", true
	}
	for _, cmd := range req.Prerequisites.Commands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		if _, err := exec.LookPath(cmd); err != nil {
			return "missing " + cmd, true
		}
	}
	envs := append(append([]string{}, req.Prerequisites.Env...), req.Prerequisites.EnvVars...)
	for _, env := range envs {
		env = strings.TrimSpace(env)
		if env == "" {
			continue
		}
		if os.Getenv(env) == "" {
			return "missing env " + env, true
		}
	}
	return "", false
}

func inspectSkillCatalog(skills []skill.Skill, disabled []string, goos, workDir string) []SkillCatalogEntry {
	deny := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		name = strings.TrimSpace(strings.ToLower(name))
		if name != "" {
			deny[name] = true
		}
	}
	out := make([]SkillCatalogEntry, 0, len(skills))
	for _, item := range skills {
		entry := SkillCatalogEntry{
			Name:   item.Name,
			Source: skillSourceLabel(item.FilePath, workDir),
			Path:   item.FilePath,
			Kept:   true,
		}
		if deny[strings.ToLower(item.Name)] {
			entry.Kept = false
			entry.Reason = "disabled"
		} else if reason, ok := skillUnavailableReason(item, goos); ok {
			entry.Kept = false
			entry.Reason = reason
		}
		out = append(out, entry)
	}
	return out
}

func skillSourceLabel(path, workDir string) string {
	path = filepath.ToSlash(path)
	workDir = filepath.ToSlash(workDir)
	switch {
	case workDir != "" && strings.Contains(path, strings.TrimSuffix(workDir, "/")+"/skills/"):
		return "workspace"
	case strings.Contains(path, "/.agents/skills/"):
		return ".agents"
	case strings.Contains(path, "/.claude/skills/"):
		return ".claude"
	case strings.Contains(path, "/.codex/skills/"):
		return ".codex"
	case strings.Contains(path, "/.opencode/skills/"):
		return ".opencode"
	case strings.Contains(path, "/.covo-agent/skills/"):
		return "managed"
	default:
		return "extra"
	}
}

func parseSkillRequirements(path string) skillRequirements {
	var req skillRequirements
	if strings.TrimSpace(path) == "" {
		return req
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return req
	}
	raw := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return req
	}
	rest := strings.TrimPrefix(raw, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return req
	}
	_ = yaml.Unmarshal([]byte(rest[:end]), &req)
	return req
}

func platformMatches(platforms []string, goos string) bool {
	if len(platforms) == 0 {
		return true
	}
	if goos == "" {
		return true
	}
	aliases := map[string]string{
		"macos": "darwin", "mac": "darwin", "darwin": "darwin",
		"linux":   "linux",
		"windows": "windows", "win32": "windows", "win": "windows",
	}
	for _, p := range platforms {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if mapped, ok := aliases[p]; ok {
			p = mapped
		}
		if p == goos {
			return true
		}
	}
	return false
}

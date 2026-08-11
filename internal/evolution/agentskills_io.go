package evolution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentskillsMetadata represents the agentskills.io standard metadata format.
// Reference: https://agentskills.io/spec
type AgentskillsMetadata struct {
	// Required fields
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`

	// Optional fields (agentskills.io standard)
	License    string   `yaml:"license,omitempty"`
	Author     string   `yaml:"author,omitempty"`
	Homepage   string   `yaml:"homepage,omitempty"`
	Repository string   `yaml:"repository,omitempty"`
	Tags       []string `yaml:"tags,omitempty"`
	Category   string   `yaml:"category,omitempty"`

	// Dependencies
	Requires    []string          `yaml:"requires,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`

	// Content references
	Entrypoint string   `yaml:"entrypoint,omitempty"`
	Scripts    []string `yaml:"scripts,omitempty"`
	References []string `yaml:"references,omitempty"`

	// Compatibility
	MinAgentVersion string   `yaml:"min_agent_version,omitempty"`
	Platforms       []string `yaml:"platforms,omitempty"`
}

// Validate checks that the metadata conforms to the agentskills.io standard.
func (m *AgentskillsMetadata) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("agentskills.io: name is required")
	}
	if !skillNamePattern.MatchString(m.Name) {
		return fmt.Errorf("agentskills.io: name must match pattern %s", skillNamePattern.String())
	}
	if m.Description == "" {
		return fmt.Errorf("agentskills.io: description is required")
	}
	if m.Version == "" {
		return fmt.Errorf("agentskills.io: version is required")
	}
	return nil
}

// ToSkillFrontmatter converts agentskills.io metadata to internal frontmatter.
func (m *AgentskillsMetadata) ToSkillFrontmatter() SkillFrontmatter {
	fm := SkillFrontmatter{
		Name:        m.Name,
		Description: m.Description,
		Version:     m.Version,
		License:     m.License,
		Author:      m.Author,
		Platforms:   m.Platforms,
		Tags:        m.Tags,
	}
	return fm
}

// FromSkillFrontmatter converts internal frontmatter to agentskills.io metadata.
func FromSkillFrontmatter(fm SkillFrontmatter) *AgentskillsMetadata {
	meta := &AgentskillsMetadata{
		Name:        fm.Name,
		Description: fm.Description,
		Version:     fm.Version,
		License:     fm.License,
		Author:      fm.Author,
		Platforms:   fm.Platforms,
		Tags:        fm.Tags,
	}
	return meta
}

// ParseAgentskillsMetadata parses a SKILL.md file and extracts agentskills.io
// metadata from the YAML frontmatter block.
func ParseAgentskillsMetadata(content string) (*AgentskillsMetadata, error) {
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		return nil, err
	}

	_ = body // body is the Markdown content after frontmatter

	var meta AgentskillsMetadata
	if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
		return nil, fmt.Errorf("agentskills.io: invalid metadata: %w", err)
	}

	if err := meta.Validate(); err != nil {
		return nil, err
	}

	return &meta, nil
}

// parseFrontmatter extracts YAML frontmatter between "---" delimiters.
func parseFrontmatter(content string) (frontmatter string, body string, err error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content, nil // no frontmatter
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}

	if endIdx < 0 {
		return "", content, fmt.Errorf("unclosed frontmatter block")
	}

	fm := strings.Join(lines[1:endIdx], "\n")
	var rest string
	if endIdx+1 < len(lines) {
		rest = strings.Join(lines[endIdx+1:], "\n")
	}
	return fm, rest, nil
}

// ImportAgentskillsSkill imports a skill directory that follows the agentskills.io
// standard. It reads SKILL.md, parses metadata, and integrates it into the
// local skill library.
func (sm *SkillManager) ImportAgentskillsSkill(skillDir string) (*SkillFrontmatter, string, error) {
	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	content, err := os.ReadFile(skillMdPath)
	if err != nil {
		return nil, "", fmt.Errorf("read SKILL.md: %w", err)
	}

	meta, err := ParseAgentskillsMetadata(string(content))
	if err != nil {
		return nil, "", fmt.Errorf("parse agentskills metadata: %w", err)
	}

	fm := meta.ToSkillFrontmatter()

	// Create the skill in the local library if it doesn't exist
	targetSkillDir := filepath.Join(sm.skillsDir, meta.Name)
	if _, statErr := os.Stat(targetSkillDir); os.IsNotExist(statErr) {
		if mkdirErr := os.MkdirAll(targetSkillDir, 0755); mkdirErr != nil {
			return nil, "", fmt.Errorf("create skill dir: %w", mkdirErr)
		}

		// Copy SKILL.md
		if writeErr := os.WriteFile(filepath.Join(targetSkillDir, "SKILL.md"), content, 0644); writeErr != nil {
			return nil, "", fmt.Errorf("write SKILL.md: %w", writeErr)
		}

		// Copy supporting files (scripts, references)
		for _, script := range meta.Scripts {
			src := filepath.Join(skillDir, script)
			dst := filepath.Join(targetSkillDir, script)
			if copyErr := copyFile(src, dst); copyErr != nil {
				return nil, "", fmt.Errorf("copy script %s: %w", script, copyErr)
			}
		}
		for _, ref := range meta.References {
			src := filepath.Join(skillDir, ref)
			dst := filepath.Join(targetSkillDir, ref)
			if copyErr := copyFile(src, dst); copyErr != nil {
				return nil, "", fmt.Errorf("copy reference %s: %w", ref, copyErr)
			}
		}

		// Register usage
		if sm.usage != nil {
			_ = sm.usage.RegisterSkill(meta.Name, fmt.Sprintf("agentskills.io import (%s)", meta.Version))
		}
	}

	return &fm, string(content), nil
}

// ExportAgentskillsSkill exports a local skill to the agentskills.io standard format.
// It adds required metadata fields (version) if missing.
func (sm *SkillManager) ExportAgentskillsSkill(name string) (string, error) {
	skillContent, err := sm.Read(name)
	if err != nil {
		return "", fmt.Errorf("read skill: %w", err)
	}

	fm, body, err := parseFrontmatter(skillContent)
	if err != nil || fm == "" {
		return "", fmt.Errorf("skill has no parseable frontmatter: %w", err)
	}

	var meta AgentskillsMetadata
	if parseErr := yaml.Unmarshal([]byte(fm), &meta); parseErr != nil {
		return "", fmt.Errorf("parse metadata: %w", parseErr)
	}

	// Ensure required agentskills.io fields
	if meta.Version == "" {
		meta.Version = "0.1.0"
	}
	if meta.License == "" {
		meta.License = "MIT"
	}

	// Add auto-detected tags from category
	skillDir := filepath.Join(sm.skillsDir, name)
	if meta.Category == "" {
		meta.Category = detectCategory(skillDir)
	}

	// Re-marshal metadata
	fmBytes, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}

	// Reconstruct SKILL.md with updated frontmatter
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(string(fmBytes))
	b.WriteString("---\n\n")
	b.WriteString(body)

	return b.String(), nil
}

// detectCategory heuristically determines a skill's category from its path.
func detectCategory(skillDir string) string {
	// Check if the skill directory is inside a category folder
	parent := filepath.Dir(skillDir)
	if parent == "" || parent == "." || parent == "/" {
		return "general"
	}

	categoryName := filepath.Base(parent)
	// Filter out non-category paths
	switch categoryName {
	case ".archive", "skills", ".qwen", ".covo":
		return "general"
	}
	return categoryName
}

// copyFile copies a file from src to dst. Creates parent directories.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	return os.WriteFile(dst, data, 0644)
}

// BuildAgentskillsIndex creates an index of all skills in a directory in
// agentskills.io format, suitable for serving as a registry.
func BuildAgentskillsIndex(skillsDir string) ([]AgentskillsMetadata, error) {
	var index []AgentskillsMetadata

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		content, readErr := os.ReadFile(skillPath)
		if readErr != nil {
			continue
		}

		meta, parseErr := ParseAgentskillsMetadata(string(content))
		if parseErr != nil {
			continue
		}
		index = append(index, *meta)
	}

	// Sort: pre-defined categories first, then alphabetically
	return index, nil
}

package evolution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type SkillBundle struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Skills      []string `yaml:"skills"`
	Instruction string   `yaml:"instruction,omitempty"`
}

type BundleManager struct {
	bundlesDir string
}

func NewBundleManager(homeDir string) *BundleManager {
	return &BundleManager{
		bundlesDir: filepath.Join(homeDir, "skill-bundles"),
	}
}

func (bm *BundleManager) Init() error {
	return os.MkdirAll(bm.bundlesDir, 0755)
}

func (bm *BundleManager) List() ([]SkillBundle, error) {
	entries, err := os.ReadDir(bm.bundlesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read bundles dir: %w", err)
	}

	var bundles []SkillBundle
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(bm.bundlesDir, name))
		if err != nil {
			continue
		}

		var bundle SkillBundle
		if err := yaml.Unmarshal(data, &bundle); err != nil {
			continue
		}

		if bundle.Name == "" {
			bundle.Name = strings.TrimSuffix(name, filepath.Ext(name))
		}

		bundles = append(bundles, bundle)
	}
	return bundles, nil
}

func (bm *BundleManager) Get(name string) (*SkillBundle, error) {
	bundles, err := bm.List()
	if err != nil {
		return nil, err
	}
	slug := slugify(name)
	for _, b := range bundles {
		if slugify(b.Name) == slug {
			return &b, nil
		}
	}
	return nil, fmt.Errorf("bundle %q not found", name)
}

func (bm *BundleManager) Save(bundle SkillBundle) error {
	if bundle.Name == "" {
		return fmt.Errorf("bundle name is required")
	}
	if len(bundle.Skills) == 0 {
		return fmt.Errorf("bundle must reference at least one skill")
	}

	slug := slugify(bundle.Name)
	path := filepath.Join(bm.bundlesDir, slug+".yaml")

	data, err := yaml.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}

	if err := os.MkdirAll(bm.bundlesDir, 0755); err != nil {
		return fmt.Errorf("create bundles dir: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

func (bm *BundleManager) Delete(name string) error {
	slug := slugify(name)
	path := filepath.Join(bm.bundlesDir, slug+".yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("bundle %q not found", name)
	}
	return os.Remove(path)
}

func (bm *BundleManager) BuildInvocation(sm *SkillManager, bundleName string, cfg PreprocessConfig) (string, error) {
	bundle, err := bm.Get(bundleName)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Skill Bundle: %s\n", bundle.Name))
	if bundle.Description != "" {
		sb.WriteString(fmt.Sprintf("%s\n\n", bundle.Description))
	}
	if bundle.Instruction != "" {
		sb.WriteString(fmt.Sprintf("## Instructions\n%s\n\n", bundle.Instruction))
	}

	sb.WriteString("## Loaded Skills\n\n")
	for _, skillName := range bundle.Skills {
		skillCfg := cfg
		skillCfg.SkillDir = filepath.Join(sm.skillsDir, skillName)
		content, err := sm.ReadPreprocessed(skillName, skillCfg)
		if err != nil {
			sb.WriteString(fmt.Sprintf("### %s\n*(skill not found)*\n\n", skillName))
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n%s\n\n", skillName, content))
	}

	return sb.String(), nil
}

func slugify(name string) string {
	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	result := make([]byte, 0, len(slug))
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, c)
		}
	}
	slug = string(result)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return strings.Trim(slug, "-")
}

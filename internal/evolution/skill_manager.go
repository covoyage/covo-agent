package evolution

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	maxSkillNameLength        = 64
	maxSkillDescriptionLength = 1024
	maxSkillTags              = 20
)

var skillNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type SkillConfigVar struct {
	Key         string `yaml:"key"`
	Description string `yaml:"description"`
	Default     string `yaml:"default,omitempty"`
	Prompt      string `yaml:"prompt,omitempty"`
}

// ImprovementEntry records a single skill improvement made by the agent during
// conversation. Each entry captures when and why the skill was improved.
type ImprovementEntry struct {
	Timestamp time.Time `yaml:"timestamp"`
	Summary   string    `yaml:"summary"`
}

type SkillFrontmatter struct {
	Name               string             `yaml:"name"`
	Description        string             `yaml:"description"`
	Version            string             `yaml:"version,omitempty"`
	License            string             `yaml:"license,omitempty"`
	Author             string             `yaml:"author,omitempty"`
	Platforms          []string           `yaml:"platforms,omitempty"`
	Tags               []string           `yaml:"tags,omitempty"`
	RelatedSkills      []string           `yaml:"related_skills,omitempty"`
	Tier               string             `yaml:"tier,omitempty"`
	Config             []SkillConfigVar   `yaml:"config,omitempty"`
	ImprovementHistory []ImprovementEntry `yaml:"improvement_history,omitempty"`
}

type SkillManager struct {
	skillsDir string
	usage     *SkillUsageTracker
}

func NewSkillManager(skillsDir string, usage *SkillUsageTracker) *SkillManager {
	return &SkillManager{
		skillsDir: skillsDir,
		usage:     usage,
	}
}

func (sm *SkillManager) Init() error {
	if err := os.MkdirAll(sm.skillsDir, 0755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}
	archiveDir := filepath.Join(sm.skillsDir, ".archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}

	// Seed bundled skills. Prefer an on-disk skills/ directory (dev tree or a
	// packaged share/ dir); fall back to unpacking the embedded skills so a
	// standalone binary with no skills/ folder alongside it still ships them.
	bundledDir := FindBundledSkillsDir()
	var cleanup func()
	if bundledDir == "" {
		if root, cl, err := unpackBundledSkills(); err == nil {
			bundledDir = root
			cleanup = cl
		}
	}
	if bundledDir != "" {
		synced, skipped, failed, _ := SyncBundledSkills(sm.skillsDir, bundledDir)
		_ = synced // can log if needed
		_ = skipped
		_ = failed
	}
	if cleanup != nil {
		cleanup()
	}
	if sm.usage != nil {
		if err := sm.reconcileSkillUsage(); err != nil {
			return fmt.Errorf("reconcile skill usage: %w", err)
		}
	}

	return nil
}

func (sm *SkillManager) reconcileSkillUsage() error {
	skills, err := sm.List()
	if err != nil {
		return err
	}
	manifest := readManifest(sm.skillsDir)
	records := make(map[string]SkillRegistration, len(skills))
	for _, skill := range skills {
		provenance := "installed"
		manifestKey := filepath.Base(skill.Dir)
		if skill.Category != "" {
			manifestKey = skill.Category + "/" + manifestKey
		}
		if _, bundled := manifest[manifestKey]; bundled {
			provenance = "bundled"
		}
		records[skill.ID] = SkillRegistration{Name: skill.Name, Provenance: provenance}
	}
	return sm.usage.ReconcileSkills(records)
}

func (sm *SkillManager) Create(name, description, body string) (string, error) {
	return sm.CreateWithMeta(name, description, "", nil, body)
}

func (sm *SkillManager) CreateWithMeta(name, description, tier string, extra map[string]any, body string) (string, error) {
	var err error
	name, err = validateSkillName(name)
	if err != nil {
		return "", err
	}
	if len(description) > maxSkillDescriptionLength {
		return "", fmt.Errorf("skill description exceeds %d characters", maxSkillDescriptionLength)
	}

	skillDir := filepath.Join(sm.skillsDir, name)
	if _, err := os.Stat(skillDir); err == nil {
		return "", fmt.Errorf("skill %q already exists", name)
	}

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return "", fmt.Errorf("create skill directory: %w", err)
	}

	fm := SkillFrontmatter{
		Name:        name,
		Description: description,
		Version:     "1.0.0",
	}
	if tier != "" {
		fm.Tier = tier
	}
	if extra != nil {
		if v, ok := extra["author"].(string); ok {
			fm.Author = v
		}
		if v, ok := extra["license"].(string); ok {
			fm.License = v
		}
		if v, ok := extra["platforms"].([]string); ok {
			fm.Platforms = v
		}
		if v, ok := extra["tags"].([]string); ok {
			fm.Tags = v
		}
	}

	fmYaml, err := yaml.Marshal(fm)
	if err != nil {
		return "", fmt.Errorf("marshal frontmatter: %w", err)
	}

	content := fmt.Sprintf("---\n%s---\n\n%s\n", string(fmYaml), body)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write SKILL.md: %w", err)
	}

	if sm.usage != nil {
		if err := sm.usage.RegisterSkillID(name, name, "agent-created"); err != nil {
			return "", fmt.Errorf("register skill usage: %w", err)
		}
	}

	return skillFile, nil
}

func (sm *SkillManager) Read(name string) (string, error) {
	skillDir, err := sm.resolveSkillDirForRead(name)
	if err != nil {
		return "", err
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		return "", fmt.Errorf("skill %q not found: %w", name, err)
	}
	return string(data), nil
}

// resolveSkillDir returns the on-disk directory for a skill identified by its
// bare name. Skills may live either directly under skillsDir (agent-created,
// flat layout) or one level deep inside a category subdirectory
// (skillsDir/<category>/<name>, used by bundled skills like
// github/codebase-inspection). It prefers the flat location, then scans
// category subdirectories. If the skill is not found anywhere, it returns the
// flat path so callers emit a sensible "not found" error.
func (sm *SkillManager) resolveSkillDir(name string) string {
	flat := filepath.Join(sm.skillsDir, name)
	if _, err := os.Stat(filepath.Join(flat, "SKILL.md")); err == nil {
		return flat
	}
	entries, err := os.ReadDir(sm.skillsDir)
	if err != nil {
		return flat
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		cand := filepath.Join(sm.skillsDir, e.Name(), name)
		if _, err := os.Stat(filepath.Join(cand, "SKILL.md")); err == nil {
			return cand
		}
	}
	return flat
}

func (sm *SkillManager) resolveSkillDirForRead(idOrName string) (string, error) {
	idOrName = filepath.ToSlash(strings.TrimSpace(idOrName))
	if category, name, qualified := strings.Cut(idOrName, "/"); qualified {
		if _, err := validateSkillName(category); err != nil {
			return "", err
		}
		if _, err := validateSkillName(name); err != nil {
			return "", err
		}
		dir := filepath.Join(sm.skillsDir, category, name)
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			return "", fmt.Errorf("skill %q not found: %w", idOrName, err)
		}
		return dir, nil
	}
	name, err := validateSkillName(idOrName)
	if err != nil {
		return "", err
	}
	return sm.resolveSkillDir(name), nil
}

func (sm *SkillManager) ReadPreprocessed(name string, cfg PreprocessConfig) (string, error) {
	content, err := sm.Read(name)
	if err != nil {
		return "", err
	}

	if cfg.SkillDir == "" {
		cfg.SkillDir, err = sm.resolveSkillDirForRead(name)
		if err != nil {
			return "", err
		}
	}

	return PreprocessSkillContent(content, cfg), nil
}

// RecordImprovement adds an improvement_history entry to a skill's frontmatter.
// The summary describes what was improved and why. If newBody is non-empty, the
// skill body is also updated atomically with the improvement record.
func (sm *SkillManager) RecordImprovement(name, summary string, newBody ...string) error {
	skillDir, err := sm.resolveSkillDirForRead(name)
	if err != nil {
		return err
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	existing, err := os.ReadFile(skillFile)
	if err != nil {
		return fmt.Errorf("skill %q not found: %w", name, err)
	}

	fm, _, err := parseSkillFile(string(existing))
	if err != nil {
		return fmt.Errorf("parse existing skill: %w", err)
	}

	// Add improvement entry
	fm.ImprovementHistory = append(fm.ImprovementHistory, ImprovementEntry{
		Timestamp: time.Now(),
		Summary:   summary,
	})

	// Bump minor version
	if fm.Version != "" {
		if v, pErr := parseVersion(fm.Version); pErr == nil {
			fm.Version = fmt.Sprintf("%d.%d.%d", v[0], v[1]+1, v[2])
		}
	}

	body := ""
	if len(newBody) > 0 && newBody[0] != "" {
		body = newBody[0]
	} else {
		// Preserve existing body
		_, existingBody, pErr := ParseSkillFrontmatter(string(existing))
		if pErr == nil {
			body = existingBody
		}
	}

	fmYaml, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshal frontmatter: %w", err)
	}

	content := fmt.Sprintf("---\n%s---\n\n%s\n", string(fmYaml), body)
	return os.WriteFile(skillFile, []byte(content), 0644)
}

// parseVersion parses a semver string into [major, minor, patch] integers.
func parseVersion(v string) ([]int, error) {
	var major, minor, patch int
	if _, err := fmt.Sscanf(v, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return nil, err
	}
	return []int{major, minor, patch}, nil
}

func (sm *SkillManager) Edit(name, newBody string) error {
	skillDir, err := sm.resolveSkillDirForRead(name)
	if err != nil {
		return err
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		return fmt.Errorf("skill %q not found", name)
	}

	existing, err := os.ReadFile(skillFile)
	if err != nil {
		return fmt.Errorf("read SKILL.md: %w", err)
	}

	fm, _, err := parseSkillFile(string(existing))
	if err != nil {
		return fmt.Errorf("parse existing skill: %w", err)
	}

	fmYaml, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("marshal frontmatter: %w", err)
	}

	content := fmt.Sprintf("---\n%s---\n\n%s\n", string(fmYaml), newBody)
	return os.WriteFile(skillFile, []byte(content), 0644)
}

func (sm *SkillManager) Patch(name, findStr, replaceStr string) error {
	skillDir, err := sm.resolveSkillDirForRead(name)
	if err != nil {
		return err
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		return fmt.Errorf("skill %q not found: %w", name, err)
	}

	content := string(data)
	if !strings.Contains(content, findStr) {
		// Provide helpful context: show what was searched and a snippet of actual content
		findPreview := findStr
		if len(findPreview) > 60 {
			findPreview = findPreview[:57] + "..."
		}
		// Find closest matching line to help debug
		lines := strings.Split(content, "\n")
		var closestLine string
		var maxMatch int
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			// Count common characters (simple similarity)
			match := 0
			for i := 0; i < len(findStr) && i < len(trimmed); i++ {
				if findStr[i] == trimmed[i] {
					match++
				}
			}
			if match > maxMatch {
				maxMatch = match
				closestLine = trimmed
			}
		}
		if len(closestLine) > 80 {
			closestLine = closestLine[:77] + "..."
		}
		return fmt.Errorf("patch skill: find string not found in %s\n  searched: %q\n  closest match: %q", skillFile, findPreview, closestLine)
	}

	newContent := strings.Replace(content, findStr, replaceStr, 1)
	return os.WriteFile(skillFile, []byte(newContent), 0644)
}

func (sm *SkillManager) Delete(name string) error {
	skillDir, err := sm.resolveSkillDirForRead(name)
	if err != nil {
		return err
	}
	id := filepath.ToSlash(strings.TrimSpace(name))
	if skill, ok := sm.Find(name); ok {
		id = skill.ID
	}

	if sm.usage != nil {
		rec, ok := sm.usage.GetRecord(id)
		if ok && rec.Pinned {
			return fmt.Errorf("skill %q is pinned and cannot be deleted", name)
		}
	}

	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return fmt.Errorf("skill %q not found", name)
	}

	archiveDir := filepath.Join(sm.skillsDir, ".archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	archiveName := strings.ReplaceAll(id, "/", "__")
	target := filepath.Join(archiveDir, archiveName)

	if _, err := os.Stat(target); err == nil {
		target = filepath.Join(archiveDir, fmt.Sprintf("%s-%d", archiveName, os.Getpid()))
	}

	if err := os.Rename(skillDir, target); err != nil {
		return fmt.Errorf("archive skill: %w", err)
	}

	if sm.usage != nil {
		_ = sm.usage.RemoveSkill(id)
	}
	return nil
}

func (sm *SkillManager) WriteFile(name, relPath, content string) error {
	skillDir, err := sm.resolveSkillDirForRead(name)
	if err != nil {
		return err
	}
	relPath, err = validateSkillRelPath(relPath)
	if err != nil {
		return err
	}

	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return fmt.Errorf("skill %q not found", name)
	}

	targetPath := filepath.Join(skillDir, relPath)
	targetDir := filepath.Dir(targetPath)

	realSkillDir, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		return fmt.Errorf("resolve skill dir: %w", err)
	}

	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}

	realTarget, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		return fmt.Errorf("resolve target dir: %w", err)
	}

	if !strings.HasPrefix(realTarget, realSkillDir+string(filepath.Separator)) && realTarget != realSkillDir {
		return fmt.Errorf("path traversal detected")
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	return os.WriteFile(targetPath, []byte(content), 0644)
}

func (sm *SkillManager) RemoveFile(name, relPath string) error {
	skillDir, err := sm.resolveSkillDirForRead(name)
	if err != nil {
		return err
	}
	relPath, err = validateSkillRelPath(relPath)
	if err != nil {
		return err
	}

	targetPath := filepath.Join(skillDir, relPath)

	realSkillDir, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		return fmt.Errorf("resolve skill dir: %w", err)
	}
	realTarget, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file %q not found", relPath)
		}
		return fmt.Errorf("resolve target: %w", err)
	}

	if !strings.HasPrefix(realTarget, realSkillDir+string(filepath.Separator)) {
		return fmt.Errorf("path traversal detected")
	}

	return os.Remove(targetPath)
}

// SkillInfo holds parsed skill metadata for listing.
type SkillInfo struct {
	SkillFrontmatter
	ID       string `json:"id"`
	Dir      string `json:"dir"`
	Category string `json:"category,omitempty"`
	State    string `json:"state,omitempty"`
}

func (sm *SkillManager) List() ([]SkillInfo, error) {
	var result []SkillInfo
	entries, err := os.ReadDir(sm.skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		catDir := filepath.Join(sm.skillsDir, e.Name())

		// Check if this entry itself has a SKILL.md (flat skill)
		if _, err := os.Stat(filepath.Join(catDir, "SKILL.md")); err == nil {
			info, loadErr := sm.loadSkillInfoFromDir(catDir, "", e.Name())
			if loadErr == nil {
				result = append(result, info)
			}
			continue
		}

		// Otherwise, treat as category dir with nested skills
		catEntries, readErr := os.ReadDir(catDir)
		if readErr != nil {
			continue
		}
		for _, ce := range catEntries {
			if !ce.IsDir() || strings.HasPrefix(ce.Name(), ".") {
				continue
			}
			skillDir := filepath.Join(catDir, ce.Name())
			if _, statErr := os.Stat(filepath.Join(skillDir, "SKILL.md")); statErr != nil {
				continue
			}
			info, loadErr := sm.loadSkillInfoFromDir(skillDir, e.Name(), ce.Name())
			if loadErr != nil {
				continue
			}
			result = append(result, info)
		}
	}
	return result, nil
}

func (sm *SkillManager) ListByCategory() (map[string][]SkillInfo, error) {
	skills, err := sm.List()
	if err != nil {
		return nil, err
	}
	grouped := make(map[string][]SkillInfo)
	for _, s := range skills {
		cat := s.Category
		if cat == "" {
			cat = "other"
		}
		grouped[cat] = append(grouped[cat], s)
	}
	for _, list := range grouped {
		sort.Slice(list, func(i, j int) bool {
			return list[i].Name < list[j].Name
		})
	}
	return grouped, nil
}

// Find resolves an exact ID or a bare name. Bare names prefer a flat skill,
// then the lexicographically first category-qualified ID for compatibility.
func (sm *SkillManager) Find(idOrName string) (SkillInfo, bool) {
	skills, err := sm.List()
	if err != nil {
		return SkillInfo{}, false
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	for _, skill := range skills {
		if skill.ID == idOrName {
			return skill, true
		}
	}
	// The exact flat ID check above intentionally precedes category-qualified
	// bare-name fallback, matching Read's flat-first compatibility behavior.
	for _, skill := range skills {
		if skill.Name == idOrName {
			return skill, true
		}
	}
	return SkillInfo{}, false
}

func (sm *SkillManager) loadSkillInfoFromDir(skillDir, category, name string) (SkillInfo, error) {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return SkillInfo{}, err
	}
	fm, _, err := ParseSkillFrontmatter(string(data))
	if err != nil {
		fm = nil
	}
	var info SkillInfo
	info.ID = name
	if category != "" {
		info.ID = category + "/" + name
	}
	info.Dir = skillDir
	info.Category = category
	if fm != nil {
		info.SkillFrontmatter = *fm
		if info.Name == "" {
			info.Name = name
		}
	} else {
		info.Name = name
	}
	if sm.usage != nil {
		if rec, ok := sm.usage.GetRecord(info.ID); ok {
			info.State = rec.State
		}
	}
	return info, nil
}

// ParseSkillFrontmatter parses the YAML frontmatter from a SKILL.md string
// into a SkillFrontmatter struct. Returns nil on missing/invalid frontmatter.
func ParseSkillFrontmatter(content string) (*SkillFrontmatter, string, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return nil, "", nil
	}

	rest := strings.TrimPrefix(content, "---")
	idx := strings.Index(rest, "---")
	if idx < 0 {
		return nil, "", nil
	}

	fmYaml := rest[:idx]
	body := strings.TrimSpace(rest[idx+3:])

	var fm SkillFrontmatter
	if err := yaml.Unmarshal([]byte(fmYaml), &fm); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter: %w", err)
	}

	return &fm, body, nil
}

// ListEnabledForPlatform returns skills that are:
// 1. Compatible with the given GOOS (or all platforms if empty)
// 2. Not in the disabled set
// 3. Matching the given tier (empty = all tiers)
func (sm *SkillManager) ListEnabledForPlatform(goos string, disabled map[string]bool, tier string) ([]SkillInfo, error) {
	all, err := sm.List()
	if err != nil {
		return nil, err
	}
	result := FilterByPlatform(all, goos)
	result = FilterDisabled(result, disabled)
	if tier != "" {
		result = FilterByTier(result, tier)
	}
	return result, nil
}

// FilterByPlatform filters skills compatible with the current OS.
// If Platforms is empty, the skill is compatible with all platforms.
func FilterByPlatform(skills []SkillInfo, goos string) []SkillInfo {
	if goos == "" {
		goos = runtime.GOOS
	}
	var result []SkillInfo
	for _, s := range skills {
		if SkillCompatibleWithPlatform(s.Platforms, goos) {
			result = append(result, s)
		}
	}
	return result
}

// platformNameMap maps user-facing platform names to runtime.GOOS values.
var platformNameMap = map[string]string{
	"macos":   "darwin",
	"mac":     "darwin",
	"darwin":  "darwin",
	"linux":   "linux",
	"windows": "windows",
	"win32":   "windows",
	"win":     "windows",
}

// SkillCompatibleWithPlatform returns true if the skill's platforms list
// includes the given GOOS. An empty platforms list means all platforms.
// User-facing names (macos, linux, windows) are mapped to runtime.GOOS values.
func SkillCompatibleWithPlatform(platforms []string, goos string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, p := range platforms {
		mapped, ok := platformNameMap[strings.ToLower(p)]
		if ok && mapped == goos {
			return true
		}
		if strings.EqualFold(p, goos) {
			return true
		}
	}
	return false
}

// FilterByTier filters skills matching the given tier(s).
// If tiers is empty, all tiers are returned.
func FilterByTier(skills []SkillInfo, tiers ...string) []SkillInfo {
	if len(tiers) == 0 {
		return skills
	}
	tierSet := make(map[string]bool)
	for _, t := range tiers {
		tierSet[strings.ToLower(t)] = true
	}
	var result []SkillInfo
	for _, s := range skills {
		tier := strings.ToLower(s.Tier)
		if tier == "" {
			tier = "core"
		}
		if tierSet[tier] {
			result = append(result, s)
		}
	}
	return result
}

// FilterDisabled filters out skills in the disabled set.
func FilterDisabled(skills []SkillInfo, disabled map[string]bool) []SkillInfo {
	if len(disabled) == 0 {
		return skills
	}
	var result []SkillInfo
	for _, s := range skills {
		if !disabled[s.Name] {
			result = append(result, s)
		}
	}
	return result
}

// ParseQualifiedName splits "namespace:skill-name" into (namespace, name).
// Returns ("", name) when there is no ":".
func ParseQualifiedName(name string) (string, string) {
	ns, rest, ok := strings.Cut(name, ":")
	if !ok {
		return "", name
	}
	return ns, rest
}

// ResolveSkillConfigValues returns a map of config key → resolved value
// for all declared config vars. Values are resolved from the provided
// config map (typically from config.yaml skills.config section), falling
// back to the declared default if not set.
func (sm *SkillManager) ResolveSkillConfigValues(cfgValues map[string]string) map[string]string {
	vars := sm.DiscoverSkillConfigVars()
	result := make(map[string]string, len(vars))
	for _, v := range vars {
		if val, ok := cfgValues[v.Key]; ok && val != "" {
			result[v.Key] = val
		} else if v.Default != "" {
			result[v.Key] = v.Default
		}
	}
	return result
}

// DiscoverSkillConfigVars scans all skills and collects declared config vars.
func (sm *SkillManager) DiscoverSkillConfigVars() []SkillConfigVar {
	skills, err := sm.List()
	if err != nil {
		return nil
	}
	var all []SkillConfigVar
	seen := make(map[string]bool)
	for _, s := range skills {
		for _, cv := range s.Config {
			if !seen[cv.Key] {
				seen[cv.Key] = true
				all = append(all, cv)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].Key < all[j].Key
	})
	return all
}

// DiscoverCategoryDescriptions scans category directories with DESCRIPTION.md.
func (sm *SkillManager) DiscoverCategoryDescriptions() map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(sm.skillsDir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		descFile := filepath.Join(sm.skillsDir, e.Name(), "DESCRIPTION.md")
		data, err := os.ReadFile(descFile)
		if err != nil {
			continue
		}
		desc := strings.TrimSpace(string(data))
		if desc != "" {
			result[e.Name()] = desc
		}
	}
	return result
}

// DetectCategory returns the category name for a skill by looking at parent
// directory names in the path.
func DetectCategory(skillDir, skillsRoot string) string {
	rel, err := filepath.Rel(skillsRoot, skillDir)
	if err != nil {
		return ""
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 3)
	if len(parts) >= 2 {
		return parts[0]
	}
	return ""
}

func (sm *SkillManager) SkillDirPath(name string) string {
	return filepath.Join(sm.skillsDir, name)
}

func (sm *SkillManager) RootDir() string {
	return sm.skillsDir
}

func validateSkillName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("skill name is required")
	}
	if len(name) > maxSkillNameLength {
		return "", fmt.Errorf("skill name exceeds %d characters", maxSkillNameLength)
	}

	// Accept "namespace:name" format — validate each part separately.
	ns, bareName := ParseQualifiedName(name)
	check := bareName
	if ns != "" {
		check = ns + ":" + bareName
	}

	if filepath.IsAbs(check) || filepath.Base(check) != check {
		return "", fmt.Errorf("invalid skill name %q", name)
	}

	// Validate each segment against the pattern.
	segments := []string{bareName}
	if ns != "" {
		segments = []string{ns, bareName}
	}
	for _, seg := range segments {
		if !skillNamePattern.MatchString(seg) {
			return "", fmt.Errorf("invalid skill name %q: use letters, numbers, underscores, or hyphens only", name)
		}
	}
	return name, nil
}

func validateSkillRelPath(relPath string) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return "", fmt.Errorf("relative path is required")
	}
	clean := filepath.Clean(relPath)
	// On Windows a volume-relative path (e.g. "\tmp\escape.txt") is not
	// reported absolute by filepath.IsAbs, so also reject any leading separator.
	if filepath.IsAbs(relPath) || strings.HasPrefix(clean, string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected")
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected")
	}
	return clean, nil
}

func parseSkillFile(content string) (*SkillFrontmatter, string, error) {
	fm, body, err := ParseSkillFrontmatter(content)
	if err != nil {
		return nil, "", err
	}
	if fm == nil {
		return nil, "", fmt.Errorf("missing YAML frontmatter")
	}
	return fm, body, nil
}

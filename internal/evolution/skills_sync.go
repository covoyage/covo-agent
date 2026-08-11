package evolution

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// bundledManifest tracks which bundled skills have been synced to the user dir.
// Format: one line per skill "name:md5hash".
const bundledManifestName = ".bundled_manifest"

// SyncBundledSkills copies bundled skills from the repo's skills/ directory
// into the user's ~/.covo-agent/skills/ directory, respecting user modifications.
//
// Behavior:
//   - If a skill doesn't exist in user dir → copy it.
//   - If a skill exists and its hash matches the bundled version → skip (already synced).
//   - If a skill exists but its hash differs → user modified it, skip (preserve user edits).
//   - After sync, write/update the manifest with current bundled hashes.
func SyncBundledSkills(userSkillsDir string, bundledSkillsDir string) (synced, skipped, failed int, err error) {
	if bundledSkillsDir == "" {
		return 0, 0, 0, nil
	}

	// Sync category DESCRIPTION.md files first.
	syncCategoryDescriptions(userSkillsDir, bundledSkillsDir)

	manifest := readManifest(userSkillsDir)
	bundledSkills, err := discoverBundledSkills(bundledSkillsDir)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("discover bundled skills: %w", err)
	}

	for name, srcPath := range bundledSkills {
		destPath := filepath.Join(userSkillsDir, name)
		bundledHash := dirHash(srcPath)

		if existingHash, exists := manifest[name]; exists {
			if existingHash == bundledHash {
				// Already synced and unchanged.
				skipped++
				continue
			}
			// User modified — preserve their version.
			skipped++
			continue
		}

		// Check if user has a skill at this path (possibly from hub install or manual copy).
		if _, err := os.Stat(destPath); err == nil {
			// User has it but not in manifest (pre-manual-install scenario).
			// Compute hash of user's copy.
			userHash := dirHash(destPath)
			if userHash == bundledHash {
				// Same content, just record it.
				manifest[name] = bundledHash
				skipped++
				continue
			}
			// Different content — user has their own version.
			skipped++
			continue
		}

		// Copy the bundled skill.
		if err := copyDir(srcPath, destPath); err != nil {
			failed++
			continue
		}
		manifest[name] = bundledHash
		synced++
	}

	if err := writeManifest(userSkillsDir, manifest); err != nil {
		return synced, skipped, failed, fmt.Errorf("write manifest: %w", err)
	}

	return synced, skipped, failed, nil
}

// FindBundledSkillsDir locates the built-in skills/ directory.
// Searches:
//  1. <binary_dir>/skills/
//  2. <binary_dir>/../share/covo-agent/skills/
//  3. Source-relative (for development)
func FindBundledSkillsDir() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, "skills")); err == nil {
			return filepath.Join(dir, "skills")
		}
		sharePath := filepath.Join(dir, "..", "share", "covo-agent", "skills")
		if _, err := os.Stat(sharePath); err == nil {
			return sharePath
		}
	}

	// Source-relative fallback (development mode).
	// This file is at internal/evolution/skills_sync.go
	// Project root is ../../ from here.
	_, file, _, ok := runtime.Caller(0)
	if ok {
		projectRoot := filepath.Join(filepath.Dir(file), "..", "..")
		devPath := filepath.Join(projectRoot, "skills")
		if _, err := os.Stat(devPath); err == nil {
			return devPath
		}
	}
	return ""
}

func readManifest(userSkillsDir string) map[string]string {
	manifestPath := filepath.Join(userSkillsDir, bundledManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return make(map[string]string)
	}
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, hash, ok := strings.Cut(line, ":")
		if ok {
			result[name] = hash
		} else {
			result[name] = ""
		}
	}
	return result
}

func writeManifest(userSkillsDir string, manifest map[string]string) error {
	manifestPath := filepath.Join(userSkillsDir, bundledManifestName)
	var sb strings.Builder
	for name, hash := range manifest {
		sb.WriteString(name)
		sb.WriteString(":")
		sb.WriteString(hash)
		sb.WriteString("\n")
	}
	return os.WriteFile(manifestPath, []byte(sb.String()), 0644)
}

func discoverBundledSkills(dir string) (map[string]string, error) {
	result := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		catPath := filepath.Join(dir, entry.Name())
		catEntries, err := os.ReadDir(catPath)
		if err != nil {
			continue
		}
		for _, catEntry := range catEntries {
			if !catEntry.IsDir() || strings.HasPrefix(catEntry.Name(), ".") {
				continue
			}
			skillPath := filepath.Join(catPath, catEntry.Name())
			skillMd := filepath.Join(skillPath, "SKILL.md")
			if _, err := os.Stat(skillMd); err == nil {
				key := entry.Name() + "/" + catEntry.Name()
				result[key] = skillPath
			}
		}
	}
	return result, nil
}

// syncCategoryDescriptions copies DESCRIPTION.md from each category in the
// bundled skills dir to the corresponding category in the user skills dir.
func syncCategoryDescriptions(userSkillsDir, bundledSkillsDir string) {
	entries, err := os.ReadDir(bundledSkillsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		src := filepath.Join(bundledSkillsDir, entry.Name(), "DESCRIPTION.md")
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dst := filepath.Join(userSkillsDir, entry.Name(), "DESCRIPTION.md")
		_ = os.MkdirAll(filepath.Dir(dst), 0755)
		_ = os.WriteFile(dst, data, 0644)
	}
}

func dirHash(dir string) string {
	h := md5.New()
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		h.Write([]byte(rel))
		f, _ := os.Open(path)
		if f != nil {
			io.Copy(h, f)
			f.Close()
		}
		return nil
	})
	return fmt.Sprintf("%x", h.Sum(nil))
}

func copyDir(src, dest string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		destPath := filepath.Join(dest, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, destPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(destPath, data, 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

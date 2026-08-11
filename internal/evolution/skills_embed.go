package evolution

import (
	"io/fs"
	"os"
	"path/filepath"

	covoagent "github.com/covoyage/covo-agent"
)

// unpackBundledSkills writes the embedded skills tree to a fresh temp directory
// and returns the path to the unpacked skills root plus a cleanup func. It is
// used as a fallback when no on-disk bundled skills directory is found (single
// binary distribution), so the existing SyncBundledSkills pipeline can run
// unchanged with the unpacked directory as its source.
func unpackBundledSkills() (skillsRoot string, cleanup func(), err error) {
	base, err := os.MkdirTemp("", "covo-bundled-skills-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(base) }

	walkErr := fs.WalkDir(covoagent.BundledSkillsFS, "skills", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		target := filepath.Join(base, p)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := covoagent.BundledSkillsFS.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if walkErr != nil {
		cleanup()
		return "", func() {}, walkErr
	}
	return filepath.Join(base, "skills"), cleanup, nil
}

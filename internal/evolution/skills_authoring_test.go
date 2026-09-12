package evolution

import (
	"io/fs"
	"strings"
	"testing"

	covoagent "github.com/covoyage/covo-agent"
)

// TestBundledSkillsAuthoringStandards enforces the house-style floor for
// every bundled SKILL.md: parseable frontmatter, a well-formed name, and a
// non-empty description within the hard 1024-char limit that SkillManager.Create
// enforces for agent-created skills. Bundled skills ship in the binary, so a
// violation here is a release blocker, not a per-user issue.
func TestBundledSkillsAuthoringStandards(t *testing.T) {
	checked := 0
	walkErr := fs.WalkDir(covoagent.BundledSkillsFS, "skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		content, err := covoagent.BundledSkillsFS.ReadFile(path)
		if err != nil {
			return err
		}
		checked++

		fm, _, err := ParseSkillFrontmatter(string(content))
		if err != nil {
			t.Errorf("%s: frontmatter parse failed: %v", path, err)
			return nil
		}
		if fm == nil {
			t.Errorf("%s: missing frontmatter", path)
			return nil
		}
		if fm.Name == "" {
			t.Errorf("%s: empty name", path)
		} else if !skillNamePattern.MatchString(fm.Name) {
			t.Errorf("%s: name %q violates naming pattern %s", path, fm.Name, skillNamePattern)
		}
		if strings.TrimSpace(fm.Description) == "" {
			t.Errorf("%s: empty description (skills are discovered by description)", path)
		}
		if len(fm.Description) > 1024 {
			t.Errorf("%s: description is %d chars; hard limit is 1024", path, len(fm.Description))
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk bundled skills: %v", walkErr)
	}
	if checked < 50 {
		t.Fatalf("expected to check the full bundled library, only parsed %d skills", checked)
	}
}

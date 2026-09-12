package agent

import (
	"path/filepath"
	"testing"
)

func TestEcosystemSkillRoots(t *testing.T) {
	base := filepath.FromSlash("/proj")

	t.Run("default includes all ecosystems", func(t *testing.T) {
		roots := ecosystemSkillRoots(base)
		want := []string{
			filepath.Join(base, ".agents", "skills"),
			filepath.Join(base, ".claude", "skills"),
			filepath.Join(base, ".codex", "skills"),
			filepath.Join(base, ".opencode", "skills"),
		}
		if len(roots) != len(want) {
			t.Fatalf("got %d roots, want %d: %v", len(roots), len(want), roots)
		}
		for i := range want {
			if roots[i] != want[i] {
				t.Errorf("root[%d] = %q, want %q", i, roots[i], want[i])
			}
		}
	})

	t.Run("master disable returns nil", func(t *testing.T) {
		t.Setenv("COVO_DISABLE_EXTERNAL_SKILLS", "true")
		if roots := ecosystemSkillRoots(base); roots != nil {
			t.Errorf("expected nil when disabled, got %v", roots)
		}
	})

	t.Run("per-source disable skips that ecosystem", func(t *testing.T) {
		t.Setenv("COVO_DISABLE_CLAUDE_SKILLS", "true")
		t.Setenv("COVO_DISABLE_OPENCODE_SKILLS", "1")
		roots := ecosystemSkillRoots(base)
		for _, r := range roots {
			eco := filepath.Base(filepath.Dir(r))
			if eco == ".claude" || eco == ".opencode" {
				t.Errorf("disabled ecosystem leaked into roots: %q", r)
			}
		}
		if len(roots) != 2 {
			t.Errorf("expected 2 roots after disabling claude+opencode, got %d: %v", len(roots), roots)
		}
	})

	t.Run("empty base returns nil", func(t *testing.T) {
		if roots := ecosystemSkillRoots(""); roots != nil {
			t.Errorf("expected nil for empty base, got %v", roots)
		}
	})
}

func TestSkillLoadPathsOrder(t *testing.T) {
	t.Setenv("COVO_DISABLE_EXTERNAL_SKILLS", "true")
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := filepath.FromSlash("/proj")
	covo := filepath.FromSlash("/home/u/.covo-agent/skills")
	extra := filepath.FromSlash("/extra/skills")
	paths := skillLoadPaths(covo, work, []string{extra})
	want := []string{
		filepath.Join(work, "skills"),
		covo,
		extra,
	}
	if len(paths) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(paths), len(want), paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestSkillLoadPathsWorkspaceBeatsManaged(t *testing.T) {
	t.Setenv("COVO_DISABLE_EXTERNAL_SKILLS", "true")
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := filepath.FromSlash("/repo")
	covo := filepath.FromSlash("/managed/skills")
	paths := skillLoadPaths(covo, work, nil)
	if len(paths) < 2 {
		t.Fatalf("expected workspace then managed, got %v", paths)
	}
	if paths[0] != filepath.Join(work, "skills") {
		t.Errorf("workspace skills should win collisions, got first=%q", paths[0])
	}
	if paths[len(paths)-1] != covo {
		t.Errorf("managed dir should be last among built-in roots, got %v", paths)
	}
}

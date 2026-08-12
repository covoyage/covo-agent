package agent

import (
	"path/filepath"
	"testing"
)

func TestExternalSkillRoots(t *testing.T) {
	home := filepath.FromSlash("/home/u")
	work := filepath.FromSlash("/proj")

	t.Run("default includes all ecosystems global+project", func(t *testing.T) {
		roots := externalSkillRoots(home, work)
		want := []string{
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(work, ".claude", "skills"),
			filepath.Join(home, ".codex", "skills"),
			filepath.Join(work, ".codex", "skills"),
			filepath.Join(home, ".agents", "skills"),
			filepath.Join(work, ".agents", "skills"),
			filepath.Join(home, ".opencode", "skills"),
			filepath.Join(work, ".opencode", "skills"),
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
		if roots := externalSkillRoots(home, work); roots != nil {
			t.Errorf("expected nil when disabled, got %v", roots)
		}
	})

	t.Run("per-source disable skips that ecosystem", func(t *testing.T) {
		t.Setenv("COVO_DISABLE_CLAUDE_SKILLS", "true")
		t.Setenv("COVO_DISABLE_OPENCODE_SKILLS", "1")
		roots := externalSkillRoots(home, work)
		for _, r := range roots {
			if filepath.Base(filepath.Dir(r)) == ".claude" || filepath.Base(filepath.Dir(r)) == ".opencode" {
				t.Errorf("disabled ecosystem leaked into roots: %q", r)
			}
		}
		// The two enabled ecosystems should remain (two roots each).
		if len(roots) != 4 {
			t.Errorf("expected 4 roots after disabling claude+opencode, got %d: %v", len(roots), roots)
		}
	})

	t.Run("empty workdir omits project roots", func(t *testing.T) {
		roots := externalSkillRoots(home, "")
		for _, r := range roots {
			if filepath.Dir(filepath.Dir(r)) != home {
				t.Errorf("expected only global roots, got %q", r)
			}
		}
		if len(roots) != 4 {
			t.Errorf("expected 4 global roots, got %d", len(roots))
		}
	})
}

func TestSkillLoadPathsOrder(t *testing.T) {
	t.Setenv("COVO_DISABLE_EXTERNAL_SKILLS", "true")
	paths := skillLoadPaths("/home/u/.covo-agent/skills", "/proj")
	if len(paths) != 1 || paths[0] != "/home/u/.covo-agent/skills" {
		t.Fatalf("expected only the covo skills dir first, got %v", paths)
	}
}

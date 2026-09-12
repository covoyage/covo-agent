package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunScriptInvokesPythonWithoutExecutableBit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hello.py")
	if err := os.WriteFile(script, []byte("print('ok-from-py')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := runScript(script, nil, dir)
	if err != nil {
		t.Fatalf("runScript: %v", err)
	}
	if !strings.Contains(out, "ok-from-py") {
		t.Fatalf("got %q", out)
	}
}

func TestExecuteScriptFindsCategorySkill(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "research", "arxiv")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: arxiv\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "echo.py"), []byte("print('arxiv-script')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := NewSkillManager(root, nil)
	out, err := mgr.ExecuteScript("arxiv", "echo.py", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "arxiv-script") {
		t.Fatalf("got %q", out)
	}
}

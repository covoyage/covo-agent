package evolution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillManageCreateRequiresWorkshop(t *testing.T) {
	tmp := t.TempDir()
	ext := NewEvolutionExtension(nil, NewSkillManager(tmp, NewSkillUsageTracker(tmp)), nil, NewSkillUsageTracker(tmp), nil)
	manage := ext.buildSkillManageTool()
	raw, _ := json.Marshal(map[string]any{
		"action":      "create",
		"name":        "needs-review",
		"description": "d",
		"body":        "body",
	})
	_, err := manage.Func(context.Background(), raw)
	if err == nil || err.Error() != "skill workshop is not enabled" {
		t.Fatalf("got %v", err)
	}
}

func TestSkillManageCreateDoesNotWriteLiveSkill(t *testing.T) {
	tmp := t.TempDir()
	store := NewWorkshopStore(tmp)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	ext := NewEvolutionExtension(nil, NewSkillManager(tmp, NewSkillUsageTracker(tmp)), nil, NewSkillUsageTracker(tmp), nil)
	ext.SetWorkshopStore(store)
	manage := ext.buildSkillManageTool()
	raw, _ := json.Marshal(map[string]any{
		"action":      "create",
		"name":        "needs-review",
		"description": "d",
		"body":        "body",
	})
	out, err := manage.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["status"] != "proposed" {
		t.Fatalf("got %#v", out)
	}
	if _, err := os.Stat(filepath.Join(tmp, "needs-review", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("create must not write live SKILL.md")
	}
	if n := len(store.ListPending()); n != 1 {
		t.Fatalf("pending = %d", n)
	}
}

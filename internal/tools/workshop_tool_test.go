package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/evolution"
)

func TestSkillWorkshopRejectsUserOnlyActions(t *testing.T) {
	tmp := t.TempDir()
	store := evolution.NewWorkshopStore(tmp)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	tool := BuildSkillWorkshopTool(store, nil)
	for _, action := range []string{"apply", "reject", "quarantine"} {
		raw, _ := json.Marshal(map[string]any{"action": action, "proposal_id": "prop-1"})
		_, err := tool.Func(context.Background(), raw)
		if err == nil || !strings.Contains(err.Error(), "user-only") {
			t.Fatalf("action %s: got %v", action, err)
		}
	}
}

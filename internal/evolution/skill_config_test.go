package evolution

import (
	"context"
	"encoding/json"
	"testing"
)

type memSkillConfig struct {
	vals map[string]string
}

func (m *memSkillConfig) Get() map[string]string { return m.vals }
func (m *memSkillConfig) Set(key, value string) error {
	if m.vals == nil {
		m.vals = map[string]string{}
	}
	m.vals[key] = value
	return nil
}

func TestSkillConfigSetPersists(t *testing.T) {
	tmp := t.TempDir()
	store := &memSkillConfig{vals: map[string]string{"EXISTING": "1"}}
	ext := NewEvolutionExtension(nil, NewSkillManager(tmp, NewSkillUsageTracker(tmp)), nil, NewSkillUsageTracker(tmp), nil)
	ext.SetSkillConfigStore(store)
	tool := ext.buildSkillConfigTool()

	raw, _ := json.Marshal(map[string]any{"action": "set", "key": "LINEAR_API_KEY", "value": "lin_xxx"})
	out, err := tool.Func(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["status"] != "saved" {
		t.Fatalf("got %#v", out)
	}
	if store.vals["LINEAR_API_KEY"] != "lin_xxx" {
		t.Fatalf("not persisted: %#v", store.vals)
	}
}

func TestSkillConfigSetRequiresStore(t *testing.T) {
	tmp := t.TempDir()
	ext := NewEvolutionExtension(nil, NewSkillManager(tmp, NewSkillUsageTracker(tmp)), nil, NewSkillUsageTracker(tmp), nil)
	tool := ext.buildSkillConfigTool()
	raw, _ := json.Marshal(map[string]any{"action": "set", "key": "K", "value": "V"})
	if _, err := tool.Func(context.Background(), raw); err == nil {
		t.Fatal("expected error without store")
	}
}

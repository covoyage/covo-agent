package marketplace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newTestMarketplace(t *testing.T, entries []PluginEntry) *Marketplace {
	t.Helper()
	data, _ := json.Marshal(entries)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/marketplace.json" {
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	m := New(t.TempDir(), ts.URL)
	return m
}

func TestMarketplace_List(t *testing.T) {
	entries := []PluginEntry{
		{Name: "test-skill", DisplayName: "Test Skill", Description: "A test", Type: PluginTypeSkill},
		{Name: "test-mcp", DisplayName: "Test MCP", Description: "An MCP", Type: PluginTypeMCP},
	}
	m := newTestMarketplace(t, entries)

	result, err := m.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
}

func TestMarketplace_ListFiltered(t *testing.T) {
	entries := []PluginEntry{
		{Name: "skill1", Type: PluginTypeSkill},
		{Name: "mcp1", Type: PluginTypeMCP},
		{Name: "skill2", Type: PluginTypeSkill},
	}
	m := newTestMarketplace(t, entries)

	skills, err := m.List(PluginTypeSkill)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
}

func TestMarketplace_Search(t *testing.T) {
	entries := []PluginEntry{
		{Name: "code-review", DisplayName: "Code Review", Description: "Review code", Type: PluginTypeSkill, Tags: []string{"review", "quality"}},
		{Name: "git-merge", DisplayName: "Git Merge", Description: "Merge branches", Type: PluginTypeSkill, Tags: []string{"git"}},
	}
	m := newTestMarketplace(t, entries)

	results, err := m.Search("review")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "code-review" {
		t.Errorf("expected 'code-review', got %q", results[0].Name)
	}
}

func TestMarketplace_Categories(t *testing.T) {
	entries := []PluginEntry{
		{Name: "s1", Type: PluginTypeSkill},
		{Name: "m1", Type: PluginTypeMCP},
		{Name: "s2", Type: PluginTypeSkill},
		{Name: "c1", Type: PluginTypeCommand},
	}
	m := newTestMarketplace(t, entries)

	cats := m.Categories()
	if len(cats) != 3 {
		t.Fatalf("expected 3 categories, got %d", len(cats))
	}
}

func TestMarketplace_IsInstalled_NotInstalled(t *testing.T) {
	m := newTestMarketplace(t, nil)
	if m.IsInstalled("nonexistent", PluginTypeSkill) {
		t.Error("expected not installed")
	}
}

func TestMarketplace_IsInstalled_Skill(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "http://example.com")

	// Create a fake skill
	skillDir := filepath.Join(dir, "skills", "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test"), 0644)

	if !m.IsInstalled("test-skill", PluginTypeSkill) {
		t.Error("expected test-skill to be installed")
	}
}

func TestMarketplace_Uninstall_NotInstalled(t *testing.T) {
	m := newTestMarketplace(t, nil)
	err := m.Uninstall("nonexistent")
	if err == nil {
		t.Error("expected error for uninstalling non-existent plugin")
	}
}

func TestMarketplace_Uninstall_Skill(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "http://example.com")

	skillDir := filepath.Join(dir, "skills", "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test"), 0644)

	if err := m.Uninstall("test-skill"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Error("expected skill dir to be removed")
	}
}

func TestPluginType_String(t *testing.T) {
	tests := []struct {
		pt   PluginType
		want string
	}{
		{PluginTypeSkill, "skill"},
		{PluginTypeMCP, "mcp"},
		{PluginTypeCommand, "command"},
	}
	for _, tt := range tests {
		if string(tt.pt) != tt.want {
			t.Errorf("got %q, want %q", tt.pt, tt.want)
		}
	}
}

func TestMarketplace_Install_NotFound(t *testing.T) {
	m := newTestMarketplace(t, []PluginEntry{})
	_, err := m.Install("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent plugin")
	}
	expected := fmt.Sprintf("marketplace: plugin %q not found", "nonexistent")
	if err.Error() != expected {
		t.Errorf("got %q, want %q", err.Error(), expected)
	}
}

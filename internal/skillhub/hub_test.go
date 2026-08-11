package skillhub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newTestHubServer(t *testing.T) (*httptest.Server, *Hub) {
	t.Helper()

	skills := []SkillInfo{
		{Name: "test-driven-development", Description: "Write tests before code, red-green-refactor cycle", Version: "1.0.0"},
		{Name: "code-review", Description: "Review pull requests with automated checks", Version: "2.1.0"},
		{Name: "debugging", Description: "Systematic debugging techniques and strategies", Version: "1.2.0"},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(skills)
	})
	mux.HandleFunc("/skills/test-driven-development/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("---\nname: test-driven-development\ndescription: Write tests before code\n---\n\nSkill body content"))
	})

	ts := httptest.NewServer(mux)

	homeDir := t.TempDir()
	hub := &Hub{
		registryURL: ts.URL,
		client:      ts.Client(),
		skillsDir:   filepath.Join(homeDir, "skills"),
	}

	return ts, hub
}

func TestHub_List(t *testing.T) {
	ts, hub := newTestHubServer(t)
	defer ts.Close()

	skills, err := hub.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(skills))
	}
	if skills[0].Name != "test-driven-development" {
		t.Fatalf("expected first skill name 'test-driven-development', got %q", skills[0].Name)
	}
	if skills[0].Version != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %q", skills[0].Version)
	}
}

func TestHub_List_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	hub := &Hub{registryURL: ts.URL, client: ts.Client()}
	skills, err := hub.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(skills))
	}
}

func TestHub_List_Error(t *testing.T) {
	hub := &Hub{registryURL: "http://127.0.0.1:1", client: http.DefaultClient}
	_, err := hub.List()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHub_Search(t *testing.T) {
	ts, hub := newTestHubServer(t)
	defer ts.Close()

	results, err := hub.Search("debug")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "debugging" {
		t.Fatalf("expected 'debugging', got %q", results[0].Name)
	}
}

func TestHub_Search_NoResults(t *testing.T) {
	ts, hub := newTestHubServer(t)
	defer ts.Close()

	results, err := hub.Search("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestHub_Search_CaseInsensitive(t *testing.T) {
	ts, hub := newTestHubServer(t)
	defer ts.Close()

	results, err := hub.Search("CODE-REVIEW")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "code-review" {
		t.Fatalf("expected 'code-review', got %q", results[0].Name)
	}
}

func TestHub_Search_DescriptionMatch(t *testing.T) {
	ts, hub := newTestHubServer(t)
	defer ts.Close()

	results, err := hub.Search("red-green")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "test-driven-development" {
		t.Fatalf("expected 'test-driven-development', got %q", results[0].Name)
	}
}

func TestHub_Install(t *testing.T) {
	ts, hub := newTestHubServer(t)
	defer ts.Close()

	path, err := hub.Install("test-driven-development")
	if err != nil {
		t.Fatal(err)
	}

	if filepath.Dir(path) != filepath.Join(hub.skillsDir, "test-driven-development") {
		t.Fatalf("installed path %q is outside expected skill directory", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !contains(content, "Skill body content") {
		t.Fatalf("expected skill body content in installed file, got %q", content)
	}
}

func TestHub_Install_NotFound(t *testing.T) {
	ts, hub := newTestHubServer(t)
	defer ts.Close()

	_, err := hub.Install("nonexistent-skill")
	if err == nil {
		t.Fatal("expected error for nonexistent skill, got nil")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

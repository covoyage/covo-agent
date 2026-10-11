package skillhub

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestHubInstallAuditsExistingPackageBeforeReplacingManifest(t *testing.T) {
	server, hub := newTestHubServer(t)
	defer server.Close()
	dir := filepath.Join(hub.skillsDir, "test-driven-development")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.pyc"), []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Install("test-driven-development"); err == nil {
		t.Fatal("package with hidden binary installed")
	}
	content, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil || string(content) != "original" {
		t.Fatal("failed audit replaced existing manifest")
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

// TestHubInstallDigestPin verifies that a registry content pin blocks
// installs whose SKILL.md bytes do not match, and that a matching install
// records a provenance sidecar.
func TestHubInstallDigestPin(t *testing.T) {
	body := "---\nname: pinned-skill\ndescription: A pinned skill\n---\n\nPinned body"
	goodPin := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))

	for _, tc := range []struct {
		pin    string
		wantOK bool
	}{
		{pin: goodPin, wantOK: true},
		{pin: strings.Repeat("0", 64), wantOK: false},
	} {
		skills := []SkillInfo{{Name: "pinned-skill", Description: "A pinned skill", SHA256: tc.pin}}
		mux := http.NewServeMux()
		mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(skills)
		})
		mux.HandleFunc("/skills/pinned-skill/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		})
		ts := httptest.NewServer(mux)

		homeDir := t.TempDir()
		hub := &Hub{
			registryURL: ts.URL,
			client:      ts.Client(),
			skillsDir:   filepath.Join(homeDir, "skills"),
		}

		path, err := hub.Install("pinned-skill")
		if tc.wantOK {
			if err != nil {
				t.Fatalf("pin %s: unexpected error: %v", tc.pin[:12], err)
			}
			prov, ok := ReadProvenance(filepath.Dir(path))
			if !ok {
				t.Fatal("expected provenance sidecar after install")
			}
			if prov.SHA256 != goodPin {
				t.Fatalf("provenance digest = %s, want %s", prov.SHA256, goodPin)
			}
			if prov.Source == "" || prov.InstalledAt == "" {
				t.Fatalf("provenance missing source/installed_at: %+v", prov)
			}
		} else if err == nil {
			t.Fatalf("pin %s: expected digest-mismatch block, got success", tc.pin[:12])
		}
		ts.Close()
	}
}

func TestHubInstallFailsClosedWithoutIndex(t *testing.T) {
	for _, index := range []string{"unavailable", "[]", "invalid"} {
		t.Run(index, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/index.json" {
					if index == "unavailable" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					fmt.Fprint(w, index)
					return
				}
				fmt.Fprint(w, "innocuous skill")
			}))
			defer server.Close()
			hub := &Hub{registryURL: server.URL, client: server.Client(), cacheDir: t.TempDir(), skillsDir: t.TempDir()}
			if _, err := hub.Install("test-skill"); err == nil {
				t.Fatal("installation skipped unavailable or missing index verification")
			}
			entries, err := os.ReadDir(hub.skillsDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed install wrote files: %v %v", entries, err)
			}
		})
	}
}

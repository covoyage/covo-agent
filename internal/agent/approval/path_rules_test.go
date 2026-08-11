package approval

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckPathPolicy_DenyEditPem(t *testing.T) {
	enableExecPolicy(t)
	resetPathRules()
	defer resetPathRules()

	loadPathRules(&pathPolicyFile{
		Deny: []string{"Edit(**/*.pem)"},
	})

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// edit_block on a .pem file should be denied.
	args, _ := json.Marshal(map[string]string{"path": "certs/server.pem"})
	d := sys.CheckPathPolicy("edit_block", args)
	if d == nil {
		t.Fatal("expected deny decision for .pem edit")
	}
	if d.Approved {
		t.Error("expected .pem edit to be denied")
	}
}

func TestCheckPathPolicy_AllowReadSrc(t *testing.T) {
	enableExecPolicy(t)
	resetPathRules()
	defer resetPathRules()

	loadPathRules(&pathPolicyFile{
		Allow: []string{"Read(src/**)"},
	})

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// read_file on a src file should be allowed.
	args, _ := json.Marshal(map[string]string{"path": "src/main.go"})
	d := sys.CheckPathPolicy("read_file", args)
	if d == nil {
		t.Fatal("expected allow decision for src read")
	}
	if !d.Approved {
		t.Error("expected src read to be allowed")
	}

	// read_file on a non-src file should not match (returns nil → falls through to TUI).
	args2, _ := json.Marshal(map[string]string{"path": "config/secrets.yaml"})
	d2 := sys.CheckPathPolicy("read_file", args2)
	if d2 != nil {
		t.Errorf("expected nil for non-matching path, got: %+v", d2)
	}
}

func TestCheckPathPolicy_DenyOverridesAllow(t *testing.T) {
	enableExecPolicy(t)
	resetPathRules()
	defer resetPathRules()

	loadPathRules(&pathPolicyFile{
		Allow: []string{"Edit(src/**)"},
		Deny:  []string{"Edit(src/secrets/**)"},
	})

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// Edit src/main.go should be allowed (matches allow, not deny).
	args, _ := json.Marshal(map[string]string{"path": "src/main.go"})
	d := sys.CheckPathPolicy("edit_block", args)
	if d == nil || !d.Approved {
		t.Error("expected src/main.go edit to be allowed")
	}

	// Edit src/secrets/key.pem should be denied (matches both, deny wins).
	args2, _ := json.Marshal(map[string]string{"path": "src/secrets/key.pem"})
	d2 := sys.CheckPathPolicy("edit_block", args2)
	if d2 == nil || d2.Approved {
		t.Error("expected src/secrets/ edit to be denied (deny overrides allow)")
	}
}

func TestCheckPathPolicy_AskFallsThrough(t *testing.T) {
	enableExecPolicy(t)
	resetPathRules()
	defer resetPathRules()

	loadPathRules(&pathPolicyFile{
		Ask: []string{"Edit(**/*.lock)"},
	})

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// Edit a .lock file should return nil (ask → falls through to TUI gate).
	args, _ := json.Marshal(map[string]string{"path": "package-lock.json"})
	d := sys.CheckPathPolicy("edit_block", args)
	if d != nil {
		t.Errorf("expected nil for ask rule (falls through to TUI), got: %+v", d)
	}
}

func TestCheckPathPolicy_MCPTool(t *testing.T) {
	enableExecPolicy(t)
	resetPathRules()
	defer resetPathRules()

	loadPathRules(&pathPolicyFile{
		Deny:  []string{"MCPTool(dangerous__*)"},
		Allow: []string{"MCPTool(safe__*)"},
	})

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// Dangerous MCP tool should be denied.
	args, _ := json.Marshal(map[string]string{"tool_name": "dangerous__delete_all"})
	d := sys.CheckPathPolicy("mcp_call", args)
	if d == nil || d.Approved {
		t.Error("expected dangerous MCP tool to be denied")
	}

	// Safe MCP tool should be allowed.
	args2, _ := json.Marshal(map[string]string{"tool_name": "safe__get_status"})
	d2 := sys.CheckPathPolicy("mcp_call", args2)
	if d2 == nil || !d2.Approved {
		t.Error("expected safe MCP tool to be allowed")
	}
}

func TestCheckPathPolicy_NoRulesReturnsNil(t *testing.T) {
	enableExecPolicy(t)
	resetPathRules()
	defer resetPathRules()

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}
	args, _ := json.Marshal(map[string]string{"path": "test.go"})

	d := sys.CheckPathPolicy("read_file", args)
	if d != nil {
		t.Errorf("expected nil when no path rules configured, got: %+v", d)
	}
}

func TestCheckPathPolicy_LoadFromPolicyFile(t *testing.T) {
	enableExecPolicy(t)
	resetPathRules()
	resetToolPolicy()
	defer resetPathRules()
	defer resetToolPolicy()

	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	policyContent := `
paths:
  deny:
    - "Edit(**/*.env)"
    - "Read(.env)"
  allow:
    - "Read(src/**)"
    - "Edit(src/**)"
`
	os.WriteFile(path, []byte(policyContent), 0644)

	if err := LoadPolicy(path); err != nil {
		t.Fatalf("LoadPolicy: %v", err)
	}

	sys := &System{config: Config{Logger: slog.Default()}, logger: slog.Default()}

	// Read .env should be denied.
	args, _ := json.Marshal(map[string]string{"path": ".env"})
	d := sys.CheckPathPolicy("read_file", args)
	if d == nil || d.Approved {
		t.Error("expected .env read to be denied by policy file")
	}

	// Read src/main.go should be allowed.
	args2, _ := json.Marshal(map[string]string{"path": "src/main.go"})
	d2 := sys.CheckPathPolicy("read_file", args2)
	if d2 == nil || !d2.Approved {
		t.Error("expected src/main.go read to be allowed by policy file")
	}

	// Edit src/config.go should be allowed.
	args3, _ := json.Marshal(map[string]string{"path": "src/config.go"})
	d3 := sys.CheckPathPolicy("edit_block", args3)
	if d3 == nil || !d3.Approved {
		t.Error("expected src/config.go edit to be allowed by policy file")
	}

	// Edit .env should be denied.
	args4, _ := json.Marshal(map[string]string{"path": ".env"})
	d4 := sys.CheckPathPolicy("edit_block", args4)
	if d4 == nil || d4.Approved {
		t.Error("expected .env edit to be denied by policy file")
	}
}

func TestMatchDoubleStar(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"src/main.go", "src/**", true},
		{"src/a/b/c.go", "src/**", true},
		{"src/main.go", "src/*", true},
		{"src/a/b.go", "src/*", false}, // * doesn't cross directories
		{"certs/server.pem", "**/*.pem", true},
		{"a/b/c/file.pem", "**/*.pem", true},
		{"file.pem", "**/*.pem", true},
		{"file.txt", "**/*.pem", false},
		{".env", ".env", true},
		{"config/.env", "**/.env", true},
	}

	for _, tt := range tests {
		t.Run(tt.path+"_"+tt.pattern, func(t *testing.T) {
			got := matchDoubleStar(tt.path, tt.pattern)
			if got != tt.want {
				t.Errorf("matchDoubleStar(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
			}
		})
	}
}

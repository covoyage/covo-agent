package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{
		"name": "demo",
		"skills": ["skills"],
		"commands": ["commands"],
		"mcpServers": {"fs": {"command": "npx", "args": ["-y", "x"]}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "commands", "hello.md"), []byte("# Hello\nSay hi."), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewBundleRegistry()
	reg.Add(b)
	if got := reg.MCPServers()["fs"].Command; got != "npx" {
		t.Fatalf("mcp command=%q", got)
	}
	cmds := reg.Commands()
	if len(cmds) != 1 || cmds[0].Name != "hello" {
		t.Fatalf("commands=%+v", cmds)
	}
}

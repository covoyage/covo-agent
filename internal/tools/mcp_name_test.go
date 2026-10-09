package tools

import "testing"

func TestSplitMCPToolName(t *testing.T) {
	server, tool, ok := SplitMCPToolName("mcp__github__search_repositories")
	if !ok || server != "github" || tool != "search_repositories" {
		t.Fatalf("got %q %q %v", server, tool, ok)
	}
	if _, _, ok := SplitMCPToolName("search_repositories"); ok {
		t.Fatal("plain tool name should not split")
	}
	if MCPToolName("fs", "read_file") != "mcp__fs__read_file" {
		t.Fatal("stable name")
	}
}

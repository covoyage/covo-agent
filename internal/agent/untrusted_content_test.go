package agent

import (
	"strings"
	"testing"
)

func TestIsUntrustedTool(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"web_search", true},
		{"web_fetch", true},
		{"mcp", true},
		{"browser_navigate", true},
		{"browser_extract", true},
		{"read", false},
		{"edit", false},
		{"bash", false},
		{"session_search", false},
		{"mcp__server__tool", false}, // old prefix no longer matches
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUntrustedTool(tt.name); got != tt.expected {
				t.Errorf("isUntrustedTool(%q) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

func TestWrapUntrustedContent(t *testing.T) {
	short := "hi"
	got := wrapUntrustedContent("test", short)
	if got != short {
		t.Error("short content should not be wrapped")
	}

	long := strings.Repeat("x", 50)
	got = wrapUntrustedContent("mcp", long)
	if !strings.HasPrefix(got, "<untrusted_tool_result source=\"mcp\">") {
		t.Errorf("expected untrusted wrapper prefix, got: %.60s...", got)
	}
	if !strings.HasSuffix(got, "-->") {
		t.Error("expected comment suffix")
	}

	// Already wrapped should not double-wrap
	got2 := wrapUntrustedContent("web_fetch", got)
	if got2 != got {
		t.Error("already-wrapped content should not be double-wrapped")
	}
}

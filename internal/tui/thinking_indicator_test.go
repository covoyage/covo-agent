package tui

import (
	"strings"
	"testing"
)

func TestToolStatusKnownTool(t *testing.T) {
	if got := toolStatus("read_file"); got != "reading file..." {
		t.Fatalf("toolStatus(read_file) = %q", got)
	}
}

func TestToolStatusUnknownToolIncludesName(t *testing.T) {
	const toolName = "custom_tool"
	if got := toolStatus(toolName); !strings.Contains(got, toolName) {
		t.Fatalf("toolStatus(%q) = %q, want tool name", toolName, got)
	}
}

package toolset

import (
	"testing"
)

func TestResolveToolsets_Leaf(t *testing.T) {
	tools, err := ResolveToolsets([]string{"filesystem"})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"read": true, "edit": true, "write_file": true, "append_file": true, "ls": true, "view": true, "delete": true, "move": true, "glob": true, "edit_block": true}
	if len(tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(tools), tools)
	}
	for _, name := range tools {
		if !expected[name] {
			t.Errorf("unexpected tool: %s", name)
		}
	}
}

func TestResolveToolsets_Composite(t *testing.T) {
	tools, err := ResolveToolsets([]string{"coding"})
	if err != nil {
		t.Fatal(err)
	}
	// coding includes: filesystem(10), search(4), shell(3), git(3), patch(2), documents(1), review(1), code_execution(1) = 25
	expectedCount := 10 + 4 + 3 + 3 + 2 + 1 + 1 + 1 // 25
	if len(tools) != expectedCount {
		t.Fatalf("expected %d tools, got %d: %v", expectedCount, len(tools), tools)
	}

	// Verify some specific tools
	toolSet := make(map[string]bool)
	for _, name := range tools {
		toolSet[name] = true
	}
	for _, want := range []string{"read", "grep", "bash", "git_status", "patch", "pdf"} {
		if !toolSet[want] {
			t.Errorf("missing expected tool: %s", want)
		}
	}
}

func TestResolveToolsets_Full(t *testing.T) {
	tools, err := ResolveToolsets([]string{"full"})
	if err != nil {
		t.Fatal(err)
	}
	// full → coding + creative + memory + skills + productivity
	// Should include everything
	if len(tools) < 30 {
		t.Fatalf("expected at least 30 tools from 'full', got %d", len(tools))
	}

	toolSet := make(map[string]bool)
	for _, name := range tools {
		toolSet[name] = true
	}
	for _, want := range []string{"read", "web_fetch", "tts", "memory", "todo", "pdf", "video_generate", "execute_code", "computer_use"} {
		if !toolSet[want] {
			t.Errorf("missing expected tool in full: %s", want)
		}
	}
}

func TestResolveToolsets_Deduplication(t *testing.T) {
	// coding includes filesystem, search, shell, git, patch, documents
	// If we also pass filesystem directly, tools should be deduped
	tools, err := ResolveToolsets([]string{"coding", "filesystem"})
	if err != nil {
		t.Fatal(err)
	}
	codingTools, _ := ResolveToolsets([]string{"coding"})
	if len(tools) != len(codingTools) {
		t.Errorf("dedup failed: coding=%d, coding+filesystem=%d", len(codingTools), len(tools))
	}
}

func TestResolveToolsets_CycleDetection(t *testing.T) {
	// Temporarily add a cycle
	orig := Toolsets["cycle_a"]
	Toolsets["cycle_a"] = ToolsetDef{
		Name:     "cycle_a",
		Tools:    []string{"tool_a"},
		Includes: []string{"cycle_b"},
	}
	Toolsets["cycle_b"] = ToolsetDef{
		Name:     "cycle_b",
		Tools:    []string{"tool_b"},
		Includes: []string{"cycle_a"},
	}
	defer func() {
		delete(Toolsets, "cycle_a")
		delete(Toolsets, "cycle_b")
		_ = orig
	}()

	_, err := ResolveToolsets([]string{"cycle_a"})
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestResolveToolsets_UnknownToolset(t *testing.T) {
	_, err := ResolveToolsets([]string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown toolset")
	}
}

func TestToolAvailability_CacheTTL(t *testing.T) {
	avail := NewToolAvailability()
	calls := 0
	avail.RegisterCheck("test_tool", func() (string, bool) {
		calls++
		return "", true
	})

	// First call should invoke check
	avail.IsAvailable("test_tool")
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Subsequent calls within TTL should use cache
	for i := 0; i < 10; i++ {
		avail.IsAvailable("test_tool")
	}
	if calls != 1 {
		t.Fatalf("expected still 1 call (cached), got %d", calls)
	}
}

func TestToolAvailability_NoCheckAlwaysAvailable(t *testing.T) {
	avail := NewToolAvailability()
	reason, ok := avail.IsAvailable("unregistered_tool")
	if !ok {
		t.Error("unregistered tool should be available")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

func TestFilterTools_Basic(t *testing.T) {
	avail := NewToolAvailability()
	allTools := []string{"read", "grep", "bash", "web_search", "tts"}
	result, err := FilterTools([]string{"filesystem", "search"}, avail, allTools)
	if err != nil {
		t.Fatal(err)
	}

	toolSet := make(map[string]bool)
	for _, name := range result.Tools {
		toolSet[name] = true
	}

	if !toolSet["read"] {
		t.Error("expected 'read' to be included")
	}
	if !toolSet["grep"] {
		t.Error("expected 'grep' to be included")
	}
	if toolSet["bash"] {
		t.Error("expected 'bash' to be excluded (not in filesystem/search)")
	}
	if toolSet["web_search"] {
		t.Error("expected 'web_search' to be excluded")
	}
}

func TestFilterTools_AvailabilityGating(t *testing.T) {
	avail := NewToolAvailability()
	avail.RegisterCheck("web_fetch", func() (string, bool) {
		return "no API key", false
	})

	allTools := []string{"read", "web_fetch"}
	result, err := FilterTools([]string{"filesystem", "web"}, avail, allTools)
	if err != nil {
		t.Fatal(err)
	}

	toolSet := make(map[string]bool)
	for _, name := range result.Tools {
		toolSet[name] = true
	}

	if !toolSet["read"] {
		t.Error("expected 'read' to be included")
	}
	if toolSet["web_fetch"] {
		t.Error("expected 'web_fetch' to be excluded by availability check")
	}
	if result.Excluded["web_fetch"] != "no API key" {
		t.Errorf("expected exclusion reason 'no API key', got %q", result.Excluded["web_fetch"])
	}
}

func TestCachedFilter_CacheHit(t *testing.T) {
	cf := NewCachedFilter()
	avail := NewToolAvailability()
	allTools := []string{"read", "grep"}

	r1, _ := cf.Filter([]string{"filesystem"}, avail, allTools)
	r2, _ := cf.Filter([]string{"filesystem"}, avail, allTools)

	// Should return same pointer (cache hit)
	if len(r1.Tools) != len(r2.Tools) {
		t.Error("cache hit should return same result")
	}
}

func TestCachedFilter_Invalidation(t *testing.T) {
	cf := NewCachedFilter()
	avail := NewToolAvailability()
	allTools := []string{"read", "grep"}

	cf.Filter([]string{"filesystem"}, avail, allTools)
	cf.InvalidateTools()

	// After invalidation, should recompute
	r2, _ := cf.Filter([]string{"filesystem"}, avail, allTools)
	if r2 == nil {
		t.Error("expected result after invalidation")
	}
}

func TestPlatformDefaults(t *testing.T) {
	tests := []struct {
		platform string
		wantMin  int
	}{
		{"cli", 1},
		{"code", 1},
		{"minimal", 3},
		{"unknown", 1}, // defaults to "full"
	}

	for _, tt := range tests {
		names := PlatformDefaults(tt.platform)
		if len(names) < tt.wantMin {
			t.Errorf("PlatformDefaults(%q): expected at least %d, got %d", tt.platform, tt.wantMin, len(names))
		}
	}
}

func TestDelegateToolsets_StripDangerous(t *testing.T) {
	result := DelegateToolsets(
		[]string{"full"},
		[]string{"coding", "delegation"},
		false, // don't restrict to parent
	)

	for _, name := range result {
		if name == "delegation" {
			t.Error("delegation should be stripped from child toolsets")
		}
	}
}

func TestDelegateToolNames_Intersect(t *testing.T) {
	parent := []string{"read", "grep", "bash", "todo"}
	child := []string{"read", "bash", "web_search", "clarify"}

	result := DelegateToolNames(parent, child)

	toolSet := make(map[string]bool)
	for _, name := range result {
		toolSet[name] = true
	}

	if !toolSet["read"] {
		t.Error("expected 'read' in child")
	}
	if !toolSet["bash"] {
		t.Error("expected 'bash' in child")
	}
	if toolSet["web_search"] {
		t.Error("'web_search' not in parent, should be excluded")
	}
	if toolSet["clarify"] {
		t.Error("'clarify' should always be stripped from delegation")
	}
}

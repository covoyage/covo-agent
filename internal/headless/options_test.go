package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolFilter_AllowAll(t *testing.T) {
	f := NewToolFilter(&Options{})
	if !f.IsAllowed("any_tool") {
		t.Error("expected all tools allowed with empty filter")
	}
}

func TestToolFilter_Whitelist(t *testing.T) {
	f := NewToolFilter(&Options{Tools: []string{"read_file", "write_file"}})
	if !f.IsAllowed("read_file") {
		t.Error("expected read_file allowed")
	}
	if f.IsAllowed("bash") {
		t.Error("expected bash not allowed")
	}
}

func TestToolFilter_Blacklist(t *testing.T) {
	f := NewToolFilter(&Options{DisallowedTools: []string{"bash"}})
	if !f.IsAllowed("read_file") {
		t.Error("expected read_file allowed")
	}
	if f.IsAllowed("bash") {
		t.Error("expected bash disallowed")
	}
}

func TestToolFilter_FilterTools(t *testing.T) {
	f := NewToolFilter(&Options{Tools: []string{"a", "b"}})
	filtered := f.FilterTools([]string{"a", "b", "c", "d"})
	if len(filtered) != 2 || filtered[0] != "a" || filtered[1] != "b" {
		t.Errorf("expected [a b], got %v", filtered)
	}
}

func TestPermissionGate_Allow(t *testing.T) {
	g := NewPermissionGate(&Options{
		Allow: []string{"edit:*", "bash:ls *"},
	})

	if g.Check("edit", "file.go") != "allow" {
		t.Error("expected edit:* to allow")
	}
	if g.Check("bash", "ls -la") != "allow" {
		t.Error("expected bash:ls * to allow")
	}
	if g.Check("bash", "rm -rf /") != "prompt" {
		t.Error("expected prompt for non-matching bash command")
	}
}

func TestPermissionGate_Deny(t *testing.T) {
	g := NewPermissionGate(&Options{
		Deny: []string{"bash:rm *"},
	})

	if g.Check("bash", "rm -rf /") != "deny" {
		t.Error("expected deny for rm command")
	}
	if g.Check("bash", "ls") != "prompt" {
		t.Error("expected prompt for non-denied bash command")
	}
}

func TestPermissionGate_DenyOverAllow(t *testing.T) {
	g := NewPermissionGate(&Options{
		Allow: []string{"bash:*"},
		Deny:  []string{"bash:rm *"},
	})

	if g.Check("bash", "rm -rf /") != "deny" {
		t.Error("deny should take precedence")
	}
	if g.Check("bash", "ls") != "allow" {
		t.Error("non-denied should be allowed")
	}
}

func TestPermissionGate_WildcardCategory(t *testing.T) {
	g := NewPermissionGate(&Options{
		Allow: []string{"*:safe_op"},
	})

	if g.Check("edit", "safe_op") != "allow" {
		t.Error("expected wildcard category to match")
	}
	if g.Check("bash", "safe_op") != "allow" {
		t.Error("expected wildcard category to match")
	}
}

func TestStreamingWriter_Events(t *testing.T) {
	var buf bytes.Buffer
	sw := NewStreamingWriter(&buf)

	sw.WriteText("hello", 1)
	sw.WriteToolCall("read_file", `{"path":"test.go"}`, 1)
	sw.WriteToolResult("read_file", "file contents", 1)
	sw.WriteError("something went wrong")
	sw.WriteDone()

	// Parse each line as JSON
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 events, got %d", len(lines))
	}

	var event OutputEvent
	json.Unmarshal([]byte(lines[0]), &event)
	if event.Type != "text" || event.Content != "hello" {
		t.Errorf("unexpected first event: %+v", event)
	}

	json.Unmarshal([]byte(lines[4]), &event)
	if event.Type != "done" {
		t.Errorf("expected done event, got %s", event.Type)
	}
}

func TestValidateOptions_Valid(t *testing.T) {
	opts := &Options{
		Prompt:       "hello",
		OutputFormat: "text",
	}
	if err := ValidateOptions(opts); err != nil {
		t.Errorf("expected valid: %v", err)
	}
}

func TestValidateOptions_EmptyPrompt(t *testing.T) {
	opts := &Options{}
	if err := ValidateOptions(opts); err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestValidateOptions_InvalidOutputFormat(t *testing.T) {
	opts := &Options{
		Prompt:       "hello",
		OutputFormat: "xml",
	}
	if err := ValidateOptions(opts); err == nil {
		t.Error("expected error for invalid output format")
	}
}

func TestValidateOptions_InvalidReasoningEffort(t *testing.T) {
	opts := &Options{
		Prompt:           "hello",
		ReasoningEffort:  "ultra",
	}
	if err := ValidateOptions(opts); err == nil {
		t.Error("expected error for invalid reasoning effort")
	}
}

func TestValidateOptions_ConflictingTools(t *testing.T) {
	opts := &Options{
		Prompt:          "hello",
		Tools:           []string{"bash"},
		DisallowedTools: []string{"bash"},
	}
	if err := ValidateOptions(opts); err == nil {
		t.Error("expected error for conflicting tools")
	}
}

func TestValidateOptions_NegativeMaxTurns(t *testing.T) {
	opts := &Options{
		Prompt:   "hello",
		MaxTurns: -1,
	}
	if err := ValidateOptions(opts); err == nil {
		t.Error("expected error for negative max turns")
	}
}

func TestContextWithTimeout(t *testing.T) {
	opts := &Options{Timeout: 0} // no timeout
	ctx, cancel := ContextWithTimeout(context.Background(), opts)
	defer cancel()
	if ctx == nil {
		t.Error("expected non-nil context")
	}
}

func TestParsePattern(t *testing.T) {
	p, ok := parsePattern("edit:*")
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if p.Category != "edit" || p.Glob != "*" {
		t.Errorf("unexpected pattern: %+v", p)
	}

	p, ok = parsePattern("bash:ls -la")
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if p.Category != "bash" || p.Glob != "ls -la" {
		t.Errorf("unexpected pattern: %+v", p)
	}

	_, ok = parsePattern("invalid")
	if ok {
		t.Error("expected parse to fail for pattern without colon")
	}
}

func TestMatchGlob(t *testing.T) {
	if !matchGlob("*", "anything") {
		t.Error("expected * to match anything")
	}
	if !matchGlob("ls *", "ls -la /tmp") {
		t.Error("expected ls * to match")
	}
	if !matchGlob("edit:*", "edit:file.go") {
		t.Error("expected edit:* to match")
	}
	if matchGlob("rm", "ls") {
		t.Error("expected exact match to fail")
	}
	if !matchGlob("exact", "exact") {
		t.Error("expected exact match to succeed")
	}
}

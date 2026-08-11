// Package headless provides enhanced headless (non-interactive) mode
// with tool filtering, max-turns, allow/deny lists, and streaming JSON output.
package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Options controls headless mode behavior.
type Options struct {
	// Prompt is the input prompt to execute.
	Prompt string

	// Tools is a whitelist of tool names to allow. Empty = all tools.
	Tools []string
	// DisallowedTools is a blacklist of tool names to block.
	DisallowedTools []string

	// MaxTurns limits the number of agent turns. 0 = unlimited.
	MaxTurns int

	// Allow is a list of permission patterns to auto-approve (e.g. "edit:*", "bash:ls *").
	Allow []string
	// Deny is a list of permission patterns to auto-deny.
	Deny []string

	// OutputFormat controls the output format: "text" or "streaming-json".
	OutputFormat string

	// SystemPrompt overrides the default system prompt.
	SystemPrompt string
	// AppendSystemPrompt appends to the default system prompt.
	AppendSystemPrompt string

	// ForkSession creates a fork of an existing session.
	ForkSession string

	// ReasoningEffort controls thinking depth: "low", "medium", "high".
	ReasoningEffort string

	// Timeout limits total execution time. 0 = no timeout.
	Timeout time.Duration
}

// OutputEvent represents a streaming JSON output event.
type OutputEvent struct {
	Type      string `json:"type"`       // "text", "tool_call", "tool_result", "error", "done"
	Content   string `json:"content"`    // text content or tool name
	ToolInput string `json:"tool_input,omitempty"`
	ToolResult string `json:"tool_result,omitempty"`
	Error     string `json:"error,omitempty"`
	Turn      int    `json:"turn,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// ToolFilter filters tools based on the allow/disallow lists.
type ToolFilter struct {
	allowed    map[string]bool
	disallowed map[string]bool
}

// NewToolFilter creates a tool filter from the options.
func NewToolFilter(opts *Options) *ToolFilter {
	f := &ToolFilter{
		allowed:    make(map[string]bool),
		disallowed: make(map[string]bool),
	}
	for _, t := range opts.Tools {
		f.allowed[strings.TrimSpace(t)] = true
	}
	for _, t := range opts.DisallowedTools {
		f.disallowed[strings.TrimSpace(t)] = true
	}
	return f
}

// IsAllowed checks if a tool should be available.
func (f *ToolFilter) IsAllowed(toolName string) bool {
	if f.disallowed[toolName] {
		return false
	}
	if len(f.allowed) == 0 {
		return true // no whitelist = allow all
	}
	return f.allowed[toolName]
}

// FilterTools returns the subset of tool names that pass the filter.
func (f *ToolFilter) FilterTools(tools []string) []string {
	var result []string
	for _, t := range tools {
		if f.IsAllowed(t) {
			result = append(result, t)
		}
	}
	return result
}

// PermissionGate evaluates allow/deny patterns for headless auto-approval.
type PermissionGate struct {
	allowPatterns []Pattern
	denyPatterns  []Pattern
}

// Pattern represents a permission pattern like "edit:*" or "bash:ls *".
type Pattern struct {
	Category string // "edit", "bash", "git", etc.
	Glob     string // glob pattern for the command/args
}

// NewPermissionGate creates a gate from the options.
func NewPermissionGate(opts *Options) *PermissionGate {
	gate := &PermissionGate{}
	for _, p := range opts.Allow {
		if pat, ok := parsePattern(p); ok {
			gate.allowPatterns = append(gate.allowPatterns, pat)
		}
	}
	for _, p := range opts.Deny {
		if pat, ok := parsePattern(p); ok {
			gate.denyPatterns = append(gate.denyPatterns, pat)
		}
	}
	return gate
}

// parsePattern parses a permission pattern "category:glob".
func parsePattern(s string) (Pattern, bool) {
	idx := strings.Index(s, ":")
	if idx <= 0 {
		return Pattern{}, false
	}
	return Pattern{
		Category: strings.TrimSpace(s[:idx]),
		Glob:     strings.TrimSpace(s[idx+1:]),
	}, true
}

// Check evaluates whether a tool call should be auto-approved, auto-denied,
// or requires manual approval.
// Returns: "allow", "deny", or "prompt".
func (g *PermissionGate) Check(category, command string) string {
	// Check deny first
	for _, p := range g.denyPatterns {
		if p.Category == category || p.Category == "*" {
			if matchGlob(p.Glob, command) || p.Glob == "*" {
				return "deny"
			}
		}
	}

	// Check allow
	for _, p := range g.allowPatterns {
		if p.Category == category || p.Category == "*" {
			if matchGlob(p.Glob, command) || p.Glob == "*" {
				return "allow"
			}
		}
	}

	return "prompt"
}

// matchGlob performs a simple glob match.
func matchGlob(pattern, text string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}

	// Simple wildcard matching
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		if len(parts) == 2 {
			prefix := parts[0]
			suffix := parts[1]
			return strings.HasPrefix(text, prefix) && strings.HasSuffix(text, suffix)
		}
	}

	return pattern == text
}

// StreamingWriter handles streaming JSON output to a writer.
type StreamingWriter struct {
	w  io.Writer
	enc *json.Encoder
}

// NewStreamingWriter creates a writer for streaming JSON events.
func NewStreamingWriter(w io.Writer) *StreamingWriter {
	return &StreamingWriter{
		w:   w,
		enc: json.NewEncoder(w),
	}
}

// WriteEvent writes a single output event.
func (sw *StreamingWriter) WriteEvent(event OutputEvent) error {
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	return sw.enc.Encode(event)
}

// WriteText writes a text content event.
func (sw *StreamingWriter) WriteText(content string, turn int) error {
	return sw.WriteEvent(OutputEvent{
		Type:    "text",
		Content: content,
		Turn:    turn,
	})
}

// WriteToolCall writes a tool call event.
func (sw *StreamingWriter) WriteToolCall(toolName, toolInput string, turn int) error {
	return sw.WriteEvent(OutputEvent{
		Type:      "tool_call",
		Content:   toolName,
		ToolInput: toolInput,
		Turn:      turn,
	})
}

// WriteToolResult writes a tool result event.
func (sw *StreamingWriter) WriteToolResult(toolName, result string, turn int) error {
	return sw.WriteEvent(OutputEvent{
		Type:       "tool_result",
		Content:    toolName,
		ToolResult: result,
		Turn:       turn,
	})
}

// WriteError writes an error event.
func (sw *StreamingWriter) WriteError(err string) error {
	return sw.WriteEvent(OutputEvent{
		Type:  "error",
		Error: err,
	})
}

// WriteDone writes the terminal done event.
func (sw *StreamingWriter) WriteDone() error {
	return sw.WriteEvent(OutputEvent{
		Type: "done",
	})
}

// ValidateOptions checks that the headless options are valid.
func ValidateOptions(opts *Options) error {
	if opts.Prompt == "" {
		return fmt.Errorf("prompt is required for headless mode")
	}

	switch opts.OutputFormat {
	case "", "text", "streaming-json":
		// valid
	default:
		return fmt.Errorf("invalid output format: %s (expected 'text' or 'streaming-json')", opts.OutputFormat)
	}

	switch opts.ReasoningEffort {
	case "", "low", "medium", "high":
		// valid
	default:
		return fmt.Errorf("invalid reasoning effort: %s (expected 'low', 'medium', or 'high')", opts.ReasoningEffort)
	}

	if opts.MaxTurns < 0 {
		return fmt.Errorf("max-turns must be non-negative")
	}

	// Check for conflicting tools (same tool in both lists)
	allowSet := make(map[string]bool)
	for _, t := range opts.Tools {
		allowSet[strings.TrimSpace(t)] = true
	}
	for _, t := range opts.DisallowedTools {
		if allowSet[strings.TrimSpace(t)] {
			return fmt.Errorf("tool %q appears in both --tools and --disallowed-tools", t)
		}
	}

	return nil
}

// ContextWithTimeout returns a context with the headless timeout applied.
func ContextWithTimeout(parent context.Context, opts *Options) (context.Context, context.CancelFunc) {
	if opts.Timeout > 0 {
		return context.WithTimeout(parent, opts.Timeout)
	}
	return context.WithCancel(parent)
}

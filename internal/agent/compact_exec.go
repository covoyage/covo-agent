package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

const (
	compactExecMinChars = 4000
	compactExecMinLines = 40
	compactExecHead     = 80
	compactExecTail     = 80
	compactExecMaxLine  = 160
)

var compactExecTools = map[string]bool{
	"bash":    true,
	"process": true,
	"sandbox": true,
}

// compactExecAfterToolCall shortens noisy command inventory output
// (ls/find/tree dumps) before it enters the conversation. File-like
// content and short results stay untouched.
func (ca *CovoAgent) compactExecAfterToolCall() func(
	ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult,
) *agentcore.ToolResult {
	return func(ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult) *agentcore.ToolResult {
		if result == nil || result.Err != nil || result.Result == "" {
			return nil
		}
		if !compactExecTools[tc.Name] {
			return nil
		}
		if strings.HasPrefix(result.Result, "<persisted-output") {
			return nil
		}
		out, ok := CompactInventoryOutput(result.Result)
		if !ok {
			return nil
		}
		modified := *result
		modified.Result = out
		return &modified
	}
}

// CompactInventoryOutput keeps the head and tail of a high-line-count,
// short-line dump and drops the middle. Returns ok=false when the text
// should stay raw.
func CompactInventoryOutput(s string) (string, bool) {
	if len(s) < compactExecMinChars {
		return s, false
	}
	lines := strings.Split(s, "\n")
	if len(lines) < compactExecMinLines {
		return s, false
	}
	if !looksLikeInventory(lines) {
		return s, false
	}
	if compactExecHead+compactExecTail >= len(lines) {
		return s, false
	}
	omitted := len(lines) - compactExecHead - compactExecTail
	var b strings.Builder
	b.Grow(compactExecMinChars)
	for _, line := range lines[:compactExecHead] {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\n...[%d lines omitted]...\n\n", omitted)
	for _, line := range lines[len(lines)-compactExecTail:] {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n"), true
}

func looksLikeInventory(lines []string) bool {
	long := 0
	empty := 0
	totalLen := 0
	for _, line := range lines {
		n := len(line)
		totalLen += n
		if n == 0 {
			empty++
			continue
		}
		if n > compactExecMaxLine {
			long++
		}
	}
	n := len(lines)
	if n == 0 {
		return false
	}
	// File dumps tend to have long wrapped/content lines.
	if long*100/n > 30 {
		return false
	}
	avg := totalLen / n
	if avg > 100 {
		return false
	}
	// A wall of blank lines is not an inventory listing.
	if empty*100/n > 50 {
		return false
	}
	return true
}

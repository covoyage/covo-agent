package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// ResultPersistenceConfig controls output size management.
type ResultPersistenceConfig struct {
	// PerResultThreshold is the max chars before an individual result is persisted (default 100K).
	PerResultThreshold int
	// TurnBudget is the max total chars across all results in one turn (default 200K).
	TurnBudget int
	// PreviewSize is the inline preview chars after persistence (default 1500).
	PreviewSize int
	// StorageDir is where persisted results are written.
	StorageDir string
}

func DefaultResultPersistenceConfig(homeDir string) ResultPersistenceConfig {
	return ResultPersistenceConfig{
		PerResultThreshold: 500_000,
		TurnBudget:         2_000_000,
		PreviewSize:        3000,
		StorageDir:         filepath.Join(homeDir, ".covo-agent", "results"),
	}
}

// toolsNeverPersist are tools where results should always stay in context.
var toolsNeverPersist = map[string]bool{
	"read":           true, // need full content for code understanding
	"grep":           true, // grep results are already bounded
	"find":           true,
	"glob":           true,
	"search_files":   true,
	"ls":             true,
	"git_status":     true,
	"git_diff":       true,
	"git_log":        true,
	"todo":           true,
	"clarify":        true,
	"cronjob":        true,
	"session_search": true,
	"diffs":          true,
	"update_plan":    true,
	"tool_search":     true,
	"tool_describe":   true,
	"standing_orders": true,
	"skill_workshop":  true,
}

// persistLargeResultAfterToolCall persists individual tool results that exceed
// the per-result threshold. Returns modified ToolResult with preview + file ref.
func (ca *CovoAgent) persistLargeResultAfterToolCall() func(
	ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult,
) *agentcore.ToolResult {
	cfg := DefaultResultPersistenceConfig(ca.homeDir)

	return func(ctx context.Context, tc agentcore.ToolCall, result *agentcore.ToolResult) *agentcore.ToolResult {
		if result.Err != nil || result.Result == "" {
			return nil
		}
		if toolsNeverPersist[tc.Name] {
			return nil
		}
		if len(result.Result) <= cfg.PerResultThreshold {
			return nil
		}

		// Persist full result to disk
		if err := os.MkdirAll(cfg.StorageDir, 0o700); err != nil {
			return nil // silently skip on error
		}
		filename := filepath.Join(cfg.StorageDir, safeToolFileID(tc.ID, tc.Name))
		if err := os.WriteFile(filename, []byte(result.Result), 0o600); err != nil {
			return nil
		}

		// Build preview with newline-aware truncation
		preview := truncateToLines(result.Result, cfg.PreviewSize)

		modified := *result
		modified.Result = fmt.Sprintf(
			"<persisted-output path=%q>\n%s\n...[truncated — full %d byte output at %s]\n</persisted-output>",
			filename, preview, len(result.Result), filename,
		)
		return &modified
	}
}

// enforceTurnBudget checks if total tool result chars exceed the per-turn budget
// and persists the largest results to stay under budget. Called after all tools
// in a turn have executed.
func enforceTurnBudget(
	results []agentcore.ToolResult,
	toolCalls []agentcore.ToolCall,
	cfg ResultPersistenceConfig,
) []agentcore.ToolResult {
	if len(results) == 0 {
		return results
	}

	total := 0
	for i := range results {
		if !toolsNeverPersist[toolCalls[i].Name] {
			total += len(results[i].Result)
		}
	}
	if total <= cfg.TurnBudget {
		return results
	}

	// Find the largest non-persisted results and persist them
	type indexed struct {
		idx  int
		size int
	}
	var candidates []indexed
	for i, r := range results {
		if r.Err != nil || toolsNeverPersist[toolCalls[i].Name] || strings.HasPrefix(r.Result, "<persisted-output") {
			continue
		}
		candidates = append(candidates, indexed{i, len(r.Result)})
	}

	// Sort candidates by size descending (simple bubble sort for small N)
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].size > candidates[i].size {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	modified := make([]agentcore.ToolResult, len(results))
	copy(modified, results)

	if err := os.MkdirAll(cfg.StorageDir, 0o700); err != nil {
		return modified
	}

	for _, c := range candidates {
		if total <= cfg.TurnBudget {
			break
		}
		tc := toolCalls[c.idx]
		filename := filepath.Join(cfg.StorageDir, safeToolFileID(tc.ID, tc.Name))
		if err := os.WriteFile(filename, []byte(modified[c.idx].Result), 0o600); err != nil {
			continue
		}

		preview := truncateToLines(modified[c.idx].Result, cfg.PreviewSize)
		modified[c.idx].Result = fmt.Sprintf(
			"<persisted-output path=%q>\n%s\n...[truncated — full output at %s]\n</persisted-output>",
			filename, preview, filename,
		)
		total -= c.size - len(modified[c.idx].Result)
	}

	return modified
}

// truncateToLines returns the first N characters of s, breaking at a newline boundary.
func truncateToLines(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := s[:maxLen]
	// Try to cut at the last newline within the limit
	if idx := strings.LastIndex(cut, "\n"); idx > maxLen/2 {
		return s[:idx]
	}
	return cut
}

// safeToolFileID creates a filesystem-safe filename from a tool call ID and name.
func safeToolFileID(id, name string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, fmt.Sprintf("%s_%s", name, id))
	if len(safe) > 100 {
		safe = safe[:100]
	}
	return safe + ".txt"
}

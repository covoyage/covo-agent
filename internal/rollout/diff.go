package rollout

import (
	"fmt"
	"strings"
)

// DiffKind classifies the type of difference found.
type DiffKind int

const (
	DiffIdentical DiffKind = iota
	DiffContent
	DiffToolCall
	DiffTurnCount
	DiffTokenUsage
	DiffMetadata
	DiffError
)

func (k DiffKind) String() string {
	switch k {
	case DiffContent:
		return "content"
	case DiffToolCall:
		return "tool_call"
	case DiffTurnCount:
		return "turn_count"
	case DiffTokenUsage:
		return "token_usage"
	case DiffMetadata:
		return "metadata"
	case DiffError:
		return "error"
	default:
		return "identical"
	}
}

// DiffItem is a single structured difference between two rollouts.
type DiffItem struct {
	Kind     DiffKind `json:"kind"`
	Turn     int      `json:"turn"`
	Path     string   `json:"path"`
	Original string   `json:"original"`
	Replayed string   `json:"replayed"`
}

// DiffResult contains the full structured diff between two rollouts.
type DiffResult struct {
	Identical bool       `json:"identical"`
	Items     []DiffItem `json:"items"`
}

// DiffRollouts performs a shape-aware comparison between two rollouts.
// It compares turn counts, per-turn content, tool call names/args,
// token usage, and metadata. Returns a structured DiffResult.
func DiffRollouts(original, replayed *Rollout) *DiffResult {
	r := &DiffResult{Identical: true}

	// Turn count.
	if len(original.Turns) != len(replayed.Turns) {
		r.Identical = false
		r.Items = append(r.Items, DiffItem{
			Kind:     DiffTurnCount,
			Original: fmt.Sprintf("%d", len(original.Turns)),
			Replayed: fmt.Sprintf("%d", len(replayed.Turns)),
		})
	}

	n := len(original.Turns)
	if len(replayed.Turns) < n {
		n = len(replayed.Turns)
	}

	for i := 0; i < n; i++ {
		ot := original.Turns[i]
		rt := replayed.Turns[i]
		turnNum := ot.Number
		omi := ot.MainInteraction()
		rmi := rt.MainInteraction()

		if omi == nil || rmi == nil {
			r.Identical = false
			r.Items = append(r.Items, DiffItem{
				Kind:     DiffContent,
				Turn:     turnNum,
				Path:     fmt.Sprintf("turns[%d].main_interaction", i),
				Original: present(omi != nil),
				Replayed: present(rmi != nil),
			})
			continue
		}

		// Content comparison.
		if omi.Response.Content != rmi.Response.Content {
			r.Identical = false
			r.Items = append(r.Items, DiffItem{
				Kind:     DiffContent,
				Turn:     turnNum,
				Path:     fmt.Sprintf("turns[%d].interactions.main.content", i),
				Original: truncate(omi.Response.Content, 200),
				Replayed: truncate(rmi.Response.Content, 200),
			})
		}

		// Tool call count.
		if len(omi.ToolCalls) != len(rmi.ToolCalls) {
			r.Identical = false
			r.Items = append(r.Items, DiffItem{
				Kind:     DiffToolCall,
				Turn:     turnNum,
				Path:     fmt.Sprintf("turns[%d].tool_calls", i),
				Original: fmt.Sprintf("%d calls", len(omi.ToolCalls)),
				Replayed: fmt.Sprintf("%d calls", len(rmi.ToolCalls)),
			})
		}

		// Per-tool-call comparison.
		tn := len(omi.ToolCalls)
		if len(rmi.ToolCalls) < tn {
			tn = len(rmi.ToolCalls)
		}
		for j := 0; j < tn; j++ {
			otc := omi.ToolCalls[j]
			rtc := rmi.ToolCalls[j]
			path := fmt.Sprintf("turns[%d].tool_calls[%d]", i, j)

			if otc.Name != rtc.Name {
				r.Identical = false
				r.Items = append(r.Items, DiffItem{
					Kind:     DiffToolCall,
					Turn:     turnNum,
					Path:     path + ".name",
					Original: otc.Name,
					Replayed: rtc.Name,
				})
			}

			if otc.Arguments != rtc.Arguments {
				r.Identical = false
				r.Items = append(r.Items, DiffItem{
					Kind:     DiffToolCall,
					Turn:     turnNum,
					Path:     path + ".arguments",
					Original: truncate(otc.Arguments, 200),
					Replayed: truncate(rtc.Arguments, 200),
				})
			}

			// Tool result: only compare for deterministic replays.
			if otc.Result != rtc.Result {
				r.Identical = false
				r.Items = append(r.Items, DiffItem{
					Kind:     DiffToolCall,
					Turn:     turnNum,
					Path:     path + ".result",
					Original: truncate(otc.Result, 200),
					Replayed: truncate(rtc.Result, 200),
				})
			}

			if otc.Error != rtc.Error {
				r.Identical = false
				r.Items = append(r.Items, DiffItem{
					Kind:     DiffError,
					Turn:     turnNum,
					Path:     path + ".error",
					Original: otc.Error,
					Replayed: rtc.Error,
				})
			}
		}

		// Finish reason.
		if omi.Response.FinishReason != rmi.Response.FinishReason {
			r.Identical = false
			r.Items = append(r.Items, DiffItem{
				Kind:     DiffContent,
				Turn:     turnNum,
				Path:     fmt.Sprintf("turns[%d].interactions.main.finish_reason", i),
				Original: string(omi.Response.FinishReason),
				Replayed: string(rmi.Response.FinishReason),
			})
		}

		// Token usage (informational, not a hard fail).
		if omi.Response.PromptTokens != rmi.Response.PromptTokens || omi.Response.CompletionTokens != rmi.Response.CompletionTokens {
			r.Identical = false
			r.Items = append(r.Items, DiffItem{
				Kind:     DiffTokenUsage,
				Turn:     turnNum,
				Path:     fmt.Sprintf("turns[%d].interactions.main.tokens", i),
				Original: fmt.Sprintf("%d+%d", omi.Response.PromptTokens, omi.Response.CompletionTokens),
				Replayed: fmt.Sprintf("%d+%d", rmi.Response.PromptTokens, rmi.Response.CompletionTokens),
			})
		}
	}

	// Metadata comparison.
	if len(original.Metadata) != len(replayed.Metadata) {
		r.Identical = false
		r.Items = append(r.Items, DiffItem{
			Kind:     DiffMetadata,
			Path:     "metadata",
			Original: fmt.Sprintf("%d keys", len(original.Metadata)),
			Replayed: fmt.Sprintf("%d keys", len(replayed.Metadata)),
		})
	}

	return r
}

// FormatDiff returns a human-readable diff report.
func FormatDiff(result *DiffResult) string {
	if result.Identical {
		return "rollouts are identical"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "rollouts differ (%d differences)\n\n", len(result.Items))

	for _, item := range result.Items {
		turnLabel := ""
		if item.Turn > 0 {
			turnLabel = fmt.Sprintf(" [turn %d]", item.Turn)
		}
		fmt.Fprintf(&b, "  %-12s%s %s\n", item.Kind, turnLabel, item.Path)
		if item.Original != "" || item.Replayed != "" {
			fmt.Fprintf(&b, "    - %s\n", item.Original)
			fmt.Fprintf(&b, "    + %s\n", item.Replayed)
		}
	}
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func present(b bool) string {
	if b {
		return "present"
	}
	return "missing"
}

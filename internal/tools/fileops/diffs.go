package fileops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildDiffsTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "diffs",
		Description: strings.Join([]string{
			"Generate a unified diff between two texts, or apply a unified patch.",
			"",
			"Modes:",
			"- 'diff': Generate a unified diff from before/after texts (default)",
			"- 'apply': Apply a unified diff patch to the original text",
			"- 'split': Show a side-by-side comparison of before/after texts",
			"",
			"Useful for previewing code changes, reviewing edits, or applying patches.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode": map[string]any{
					"type":        "string",
					"description": "Operation mode: 'diff' (default), 'apply', or 'split'.",
					"enum":        []string{"diff", "apply", "split"},
				},
				"before": map[string]any{
					"type":        "string",
					"description": "Original text (for 'diff' and 'split' modes).",
				},
				"after": map[string]any{
					"type":        "string",
					"description": "Modified text (for 'diff' and 'split' modes).",
				},
				"patch": map[string]any{
					"type":        "string",
					"description": "Unified diff patch to apply (for 'apply' mode).",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "Original text to patch (for 'apply' mode).",
				},
				"context_lines": map[string]any{
					"type":        "integer",
					"description": "Number of context lines around changes (default: 3).",
				},
				"filename": map[string]any{
					"type":        "string",
					"description": "Optional filename for diff headers.",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Mode         string `json:"mode"`
				Before       string `json:"before"`
				After        string `json:"after"`
				Patch        string `json:"patch"`
				Input        string `json:"input"`
				ContextLines int    `json:"context_lines"`
				Filename     string `json:"filename"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if params.Mode == "" {
				params.Mode = "diff"
			}
			if params.ContextLines <= 0 {
				params.ContextLines = 3
			}
			if params.Filename == "" {
				params.Filename = "file"
			}

			switch params.Mode {
			case "diff":
				if params.Before == "" && params.After == "" {
					return nil, fmt.Errorf("diff mode requires 'before' and 'after'")
				}
				return generateDiff(params.Before, params.After, params.Filename, params.ContextLines)

			case "apply":
				if params.Patch == "" || params.Input == "" {
					return nil, fmt.Errorf("apply mode requires 'patch' and 'input'")
				}
				return applyPatch(params.Input, params.Patch)

			case "split":
				if params.Before == "" && params.After == "" {
					return nil, fmt.Errorf("split mode requires 'before' and 'after'")
				}
				return generateSplit(params.Before, params.After, params.Filename)

			default:
				return nil, fmt.Errorf("unknown mode: %s", params.Mode)
			}
		},
	}
}

func generateDiff(before, after, filename string, contextLines int) (any, error) {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	// Use Myers-like simple LCS diff
	edits := computeEdits(beforeLines, afterLines)

	if len(edits) == 0 {
		return map[string]any{
			"status": "no_changes",
			"diff":   "",
			"stats":  map[string]int{"additions": 0, "deletions": 0, "unchanged": len(beforeLines)},
		}, nil
	}

	// Build unified diff with context
	diff := buildUnifiedDiff(beforeLines, afterLines, edits, filename, contextLines)

	additions, deletions := countChanges(edits)

	return map[string]any{
		"status": "ok",
		"diff":   diff,
		"stats": map[string]int{
			"additions": additions,
			"deletions": deletions,
			"unchanged": len(beforeLines) - deletions,
			"hunks":     countHunks(edits, contextLines),
		},
	}, nil
}

func applyPatch(input, patch string) (any, error) {
	inputLines := strings.Split(input, "\n")
	patchLines := strings.Split(patch, "\n")

	result, err := applyUnifiedPatch(inputLines, patchLines)
	if err != nil {
		return map[string]any{
			"status": "error",
			"error":  err.Error(),
		}, nil
	}

	return map[string]any{
		"status":     "applied",
		"result":     strings.Join(result, "\n"),
		"line_count": len(result),
	}, nil
}

func generateSplit(before, after, filename string) (any, error) {
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	edits := computeEdits(beforeLines, afterLines)

	var left, right, markers []string

	// Build side-by-side
	editMap := make(map[int]string) // line index in before -> action
	for _, e := range edits {
		editMap[e.Line] = e.Type
	}

	bi, ai := 0, 0
	width := 40

	for bi < len(beforeLines) || ai < len(afterLines) {
		var l, r, m string

		if bi < len(beforeLines) {
			l = padRight(beforeLines[bi], width)
			if editMap[bi] == "delete" {
				m += "-"
				bi++
			} else if editMap[bi] == "replace" {
				m += "~"
				bi++
			} else {
				m += " "
				bi++
			}
		} else {
			l = padRight("", width)
			m += " "
		}

		if ai < len(afterLines) {
			r = afterLines[ai]
			ai++
		}

		left = append(left, l)
		right = append(right, r)
		markers = append(markers, m)
	}

	return map[string]any{
		"status":   "ok",
		"left":     strings.Join(left, "\n"),
		"right":    strings.Join(right, "\n"),
		"filename": filename,
	}, nil
}

// --- Diff engine ---

type editOp struct {
	Line int
	Type string // "insert", "delete", "replace"
}

func computeEdits(before, after []string) []editOp {
	// LCS-based diff using DP table
	m, n := len(before), len(after)
	dp := lcsTable(before, after)

	var edits []editOp
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && before[i-1] == after[j-1] {
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			edits = append(edits, editOp{Line: i - 1, Type: "insert"})
			j--
		} else if i > 0 {
			edits = append(edits, editOp{Line: i - 1, Type: "delete"})
			i--
		} else {
			break
		}
	}

	// Reverse
	for i, j := 0, len(edits)-1; i < j; i, j = i+1, j-1 {
		edits[i], edits[j] = edits[j], edits[i]
	}

	return edits
}

func lcsTable(a, b []string) [][]int {
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return dp
}

func buildUnifiedDiff(before, after []string, edits []editOp, filename string, contextLines int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n", filename)
	fmt.Fprintf(&b, "+++ b/%s\n", filename)

	// Group edits into hunks
	editSet := make(map[int]string)
	for _, e := range edits {
		editSet[e.Line] = e.Type
	}

	// Find changed line ranges
	var changedRanges []int
	for _, e := range edits {
		changedRanges = append(changedRanges, e.Line)
	}

	if len(changedRanges) == 0 {
		return ""
	}

	// Build hunks
	hunks := buildHunks(before, after, editSet, contextLines)

	for _, hunk := range hunks {
		b.WriteString(hunk)
	}

	return b.String()
}

func buildHunks(before, after []string, editSet map[int]string, contextLines int) []string {

	// Find ranges of changed lines
	var ranges [][2]int // [start, end) in before-index space
	for i := 0; i < len(before); i++ {
		if _, ok := editSet[i]; ok {
			if len(ranges) > 0 && ranges[len(ranges)-1][1] == i {
				ranges[len(ranges)-1][1] = i + 1
			} else {
				ranges = append(ranges, [2]int{i, i + 1})
			}
		}
	}

	// Merge close ranges
	merged := []int{0}
	for _, r := range ranges {
		start := r[0] - contextLines
		if start < 0 {
			start = 0
		}
		if start <= merged[len(merged)-1] {
			merged[len(merged)-1] = r[1] + contextLines
		} else {
			merged = append(merged, start, r[1]+contextLines)
		}
	}
	if len(merged)%2 != 0 {
		merged = append(merged, len(before))
	}

	var hunks []string
	afterIdx := 0
	for i := 0; i < len(merged); i += 2 {
		start := merged[i]
		end := merged[i+1]
		if end > len(before) {
			end = len(before)
		}

		var hunk strings.Builder
		oldCount, newCount := 0, 0

		// Count lines in hunk
		for li := start; li < end; li++ {
			if _, ok := editSet[li]; ok {
				if editSet[li] == "delete" || editSet[li] == "replace" {
					oldCount++
				}
				if editSet[li] == "insert" || editSet[li] == "replace" {
					newCount++
				}
			} else {
				oldCount++
				newCount++
			}
		}

		fmt.Fprintf(&hunk, "@@ -%d,%d +%d,%d @@\n", start+1, oldCount, afterIdx+1, newCount)

		for li := start; li < end; li++ {
			if op, ok := editSet[li]; ok {
				switch op {
				case "delete":
					fmt.Fprintf(&hunk, "-%s\n", before[li])
				case "replace":
					fmt.Fprintf(&hunk, "-%s\n", before[li])
					if afterIdx < len(after) {
						fmt.Fprintf(&hunk, "+%s\n", after[afterIdx])
						afterIdx++
					}
				case "insert":
					if afterIdx < len(after) {
						fmt.Fprintf(&hunk, "+%s\n", after[afterIdx])
						afterIdx++
					}
				}
			} else {
				fmt.Fprintf(&hunk, " %s\n", before[li])
				afterIdx++
			}
		}

		hunks = append(hunks, hunk.String())
	}

	return hunks
}

func applyUnifiedPatch(inputLines, patchLines []string) ([]string, error) {
	var result []string
	inputIdx := 0

	for _, line := range patchLines {
		if strings.HasPrefix(line, "@@") {
			// Parse hunk header: @@ -old_start,old_count +new_start,new_count @@
			var oldStart int
			_, err := fmt.Sscanf(line, "@@ -%d,", &oldStart)
			if err != nil {
				continue
			}
			// Copy unchanged lines up to this hunk
			for inputIdx < oldStart-1 && inputIdx < len(inputLines) {
				result = append(result, inputLines[inputIdx])
				inputIdx++
			}
		} else if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			// Skip unified diff header lines
		} else if strings.HasPrefix(line, "-") {
			// Verify the line matches
			expected := line[1:]
			if inputIdx < len(inputLines) && inputLines[inputIdx] == expected {
				inputIdx++ // skip the deleted line
			}
		} else if strings.HasPrefix(line, "+") {
			result = append(result, line[1:])
		} else if strings.HasPrefix(line, " ") {
			// Context line — copy from input
			if inputIdx < len(inputLines) {
				result = append(result, inputLines[inputIdx])
				inputIdx++
			}
		}
	}

	// Copy remaining lines
	for inputIdx < len(inputLines) {
		result = append(result, inputLines[inputIdx])
		inputIdx++
	}

	return result, nil
}

func countChanges(edits []editOp) (additions, deletions int) {
	for _, e := range edits {
		switch e.Type {
		case "insert":
			additions++
		case "delete":
			deletions++
		case "replace":
			additions++
			deletions++
		}
	}
	return
}

func countHunks(edits []editOp, contextLines int) int {
	if len(edits) == 0 {
		return 0
	}
	hunks := 1
	lastLine := edits[0].Line
	for _, e := range edits[1:] {
		if e.Line-lastLine > contextLines*2+1 {
			hunks++
		}
		lastLine = e.Line
	}
	return hunks
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

package fileops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

type editEntry struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type editParams struct {
	Path       string      `json:"path"`
	Edits      []editEntry `json:"edits"`
	Old        string      `json:"old"`
	New        string      `json:"new"`
	ReplaceAll bool        `json:"replace_all"`
}

type editResult struct {
	Status           string       `json:"status"`
	Path             string       `json:"path"`
	Replaced         int          `json:"replaced"`
	OldLines         int          `json:"old_lines"`
	NewLines         int          `json:"new_lines"`
	FuzzyMatch       bool         `json:"fuzzy_match"`
	Diff             string       `json:"diff"`
	Patch            string       `json:"patch"`
	FirstChangedLine *int         `json:"first_changed_line"`
	LineEnding       string       `json:"line_ending,omitempty"`
	EditDetails      []editDetail `json:"edit_details,omitempty"`
}

type editDetail struct {
	Index      int    `json:"index"`
	OldText    string `json:"oldText"`
	NewText    string `json:"newText"`
	FuzzyMatch bool   `json:"fuzzy_match"`
}

func BuildEditBlockTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "edit_block",
		Description: strings.Join([]string{
			"Apply targeted text replacements to a file using block matching.",
			"",
			"Supports two calling patterns:",
			"1. SINGLE EDIT (backward compatible): use 'old' + 'new' + optional 'replace_all'",
			"2. MULTI EDIT (recommended): use 'edits' array for multiple replacements in one call",
			"",
			"Each edit is matched against the ORIGINAL file content (not incrementally).",
			"Do not include overlapping or nested edits. If two changes touch the same",
			"block, merge them into a single edit.",
			"",
			"Matching: exact match is attempted first. If it fails, fuzzy matching is used",
			"(normalizing Unicode smart quotes, dashes, and whitespace).",
			"",
			"For new files, use the 'write_file' tool instead.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit (relative or absolute).",
				},
				"edits": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": map[string]any{
								"type":        "string",
								"description": "Exact text to find in the original file. Must be unique and not overlap with other edits.",
							},
							"newText": map[string]any{
								"type":        "string",
								"description": "Replacement text for this edit.",
							},
						},
						"required": []string{"oldText", "newText"},
					},
					"description": "Array of targeted replacements. Each matched against the original file, not incrementally. Merged if provided alongside 'old'/'new'.",
				},
				"old": map[string]any{
					"type":        "string",
					"description": "Text to search for. Use for single edits. If 'edits' is also provided, this is appended as an additional edit.",
				},
				"new": map[string]any{
					"type":        "string",
					"description": "Replacement text. Required when 'old' is provided.",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "When using single-edit mode: replace all occurrences (default: false). Ignored in multi-edit mode.",
				},
			},
			"required": []string{"path"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			params, err := parseEditParams(args)
			if err != nil {
				return nil, err
			}

			edits := resolveEdits(params)

			if len(edits) == 0 {
				return nil, fmt.Errorf("at least one edit is required (provide 'edits' array or 'old'+'new')")
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}

			path := params.Path
			if strings.HasPrefix(path, "~") {
				home, _ := os.UserHomeDir()
				path = strings.Replace(path, "~", home, 1)
			}
			path = resolveReadPath(path, "")

			var result *editResult

			lockErr := withFileMutationLock(path, func() error {
				content, err := os.ReadFile(path)
				if err != nil {
					if os.IsNotExist(err) {
						return fmt.Errorf("file not found: %s (use write_file to create new files)", path)
					}
					return fmt.Errorf("read file: %w", err)
				}

				originalText := string(content)
				lineEnding := detectLineEnding(originalText)

				matches, matchErr := matchAllEdits(originalText, edits)
				if matchErr != nil {
					return matchErr
				}

				if hasOverlap(matches) {
					return fmt.Errorf("overlapping edits detected in %s. Edits must target non-overlapping regions. Merge overlapping changes into a single edit.", path)
				}

				newText := applyEdits(originalText, matches)

				if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
					return fmt.Errorf("write file: %w", err)
				}
				notifyAfterWrite(path, "edit_block", "")

				diff := generateEditDiff(originalText, newText)
				patch := generateUnifiedPatch(path, originalText, newText)
				firstLine := firstChangedLineNumber(originalText, newText)

				totalOldLines := 0
				totalNewLines := 0
				anyFuzzy := false
				details := make([]editDetail, 0, len(matches))

				for i, m := range matches {
					totalOldLines += strings.Count(m.OldText, "\n") + 1
					totalNewLines += strings.Count(m.NewText, "\n") + 1
					if m.FuzzyMatch {
						anyFuzzy = true
					}
					details = append(details, editDetail{
						Index:      i,
						OldText:    m.OldText,
						NewText:    m.NewText,
						FuzzyMatch: m.FuzzyMatch,
					})
				}

				result = &editResult{
					Status:           "ok",
					Path:             path,
					Replaced:         len(matches),
					OldLines:         totalOldLines,
					NewLines:         totalNewLines,
					FuzzyMatch:       anyFuzzy,
					Diff:             diff,
					Patch:            patch,
					FirstChangedLine: firstLine,
					LineEnding:       lineEnding,
					EditDetails:      details,
				}
				return nil
			})

			if lockErr != nil {
				return nil, lockErr
			}

			return result, nil
		},
	}
}

func parseEditParams(args json.RawMessage) (*editParams, error) {
	var raw map[string]any
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	var params editParams

	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	if editsRaw, ok := raw["edits"]; ok {
		if strVal, ok := editsRaw.(string); ok {
			var parsed []editEntry
			if err := json.Unmarshal([]byte(strVal), &parsed); err == nil {
				params.Edits = append(params.Edits, parsed...)
			}
		}
	}

	return &params, nil
}

func resolveEdits(params *editParams) []editEntry {
	var edits []editEntry

	edits = append(edits, params.Edits...)

	if params.Old != "" || params.New != "" {
		if params.ReplaceAll {
			edits = append(edits, editEntry{OldText: params.Old, NewText: params.New})
		} else {
			edits = append(edits, editEntry{OldText: params.Old, NewText: params.New})
		}
	}

	var deduped []editEntry
	seen := make(map[string]bool)
	for _, e := range edits {
		key := e.OldText + "\x00" + e.NewText
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, e)
		}
	}

	return deduped
}

type editMatch struct {
	EditIndex  int
	OldText    string
	NewText    string
	StartIdx   int
	EndIdx     int
	FuzzyMatch bool
}

func matchAllEdits(content string, edits []editEntry) ([]editMatch, error) {
	var matches []editMatch

	for i, edit := range edits {
		if edit.OldText == "" {
			return nil, fmt.Errorf("edit[%d]: oldText is required (use write_file for new files)", i)
		}
		if edit.OldText == edit.NewText {
			return nil, fmt.Errorf("edit[%d]: oldText and newText are identical", i)
		}

		startIdx, endIdx, found, usedFuzzy, ambiguous := fuzzyFindIndex(content, edit.OldText)
		if ambiguous {
			return nil, fmt.Errorf(
				"edit[%d]: oldText matches multiple locations in the file. "+
					"Add more surrounding context lines to make the match unique.",
				i,
			)
		}
		if !found {
			return nil, fmt.Errorf(
				"edit[%d]: oldText not found in file. Make sure it matches exactly (including whitespace). "+
					"If the text contains Unicode quotes or special characters, check that they match the file content.",
				i,
			)
		}

		matches = append(matches, editMatch{
			EditIndex:  i,
			OldText:    edit.OldText,
			NewText:    edit.NewText,
			StartIdx:   startIdx,
			EndIdx:     endIdx,
			FuzzyMatch: usedFuzzy,
		})
	}

	return matches, nil
}

func hasOverlap(matches []editMatch) bool {
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].StartIdx < matches[j].StartIdx
	})
	for i := 1; i < len(matches); i++ {
		if matches[i-1].EndIdx > matches[i].StartIdx {
			return true
		}
	}
	return false
}

func applyEdits(content string, matches []editMatch) string {
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].StartIdx > matches[j].StartIdx
	})

	result := content
	for _, m := range matches {
		// Use pre-computed byte offsets. Edits are non-overlapping (checked by
		// hasOverlap) and applied from end to start, so earlier offsets remain
		// valid after later edits are applied. This avoids re-running fuzzy
		// matching on the mutated string, which could pick up false positives
		// or hit ambiguity from text introduced by a previous edit.
		if m.StartIdx < 0 || m.EndIdx > len(result) || m.StartIdx > m.EndIdx {
			continue
		}
		result = result[:m.StartIdx] + m.NewText + result[m.EndIdx:]
	}
	return result
}

func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 {
		return "lf"
	}
	if crlfIdx != -1 && crlfIdx <= lfIdx {
		return "crlf"
	}
	return "lf"
}

func generateUnifiedPatch(filename, oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	hunks := computeUnifiedHunks(oldLines, newLines, 3)
	if len(hunks) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	b.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	for _, h := range hunks {
		b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			h.oldStart+1, h.oldCount, h.newStart+1, h.newCount))

		for _, line := range h.lines {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return b.String()
}

type unifiedHunk struct {
	oldStart, oldCount int
	newStart, newCount int
	lines              []string
}

func computeUnifiedHunks(oldLines, newLines []string, contextLines int) []unifiedHunk {
	type diffOp struct {
		op               byte
		oldLine, newLine int
		text             string
	}

	maxLen := len(oldLines) + len(newLines)

	edits := computeEditOps(oldLines, newLines)

	var ops []diffOp
	oi, ni := 0, 0
	for _, edit := range edits {
		if edit.op == '=' {
			for k := 0; k < edit.count; k++ {
				ops = append(ops, diffOp{op: ' ', oldLine: oi, newLine: ni, text: oldLines[oi]})
				oi++
				ni++
			}
		} else if edit.op == '-' {
			for k := 0; k < edit.count; k++ {
				ops = append(ops, diffOp{op: '-', oldLine: oi, newLine: -1, text: oldLines[oi]})
				oi++
			}
		} else if edit.op == '+' {
			for k := 0; k < edit.count; k++ {
				ops = append(ops, diffOp{op: '+', oldLine: -1, newLine: ni, text: newLines[ni]})
				ni++
			}
		}
	}
	_ = maxLen

	changed := make([]bool, len(ops))
	for i, op := range ops {
		if op.op != ' ' {
			for j := maxInt(0, i-contextLines); j <= minInt(len(ops)-1, i+contextLines); j++ {
				changed[j] = true
			}
		}
	}

	var hunks []unifiedHunk
	var current *unifiedHunk
	gap := 0

	for i, op := range ops {
		if changed[i] {
			if current == nil {
				current = &unifiedHunk{
					oldStart: op.oldLine,
					newStart: op.newLine,
				}
				if op.oldLine == -1 {
					current.oldStart = ops[maxInt(0, i-1)].oldLine + 1
				}
				if op.newLine == -1 {
					current.newStart = ops[maxInt(0, i-1)].newLine + 1
				}
			}
			gap = 0

			switch op.op {
			case ' ':
				current.lines = append(current.lines, " "+op.text)
				current.oldCount++
				current.newCount++
			case '-':
				current.lines = append(current.lines, "-"+op.text)
				current.oldCount++
			case '+':
				current.lines = append(current.lines, "+"+op.text)
				current.newCount++
			}
		} else if current != nil {
			gap++
			if gap > contextLines*2 {
				hunks = append(hunks, *current)
				current = nil
			} else {
				current.lines = append(current.lines, " "+op.text)
				current.oldCount++
				current.newCount++
			}
		}
	}

	if current != nil {
		hunks = append(hunks, *current)
	}

	return hunks
}

type rawEdit struct {
	op    byte
	count int
}

func computeEditOps(oldLines, newLines []string) []rawEdit {
	equalRuns := computeLCSRuns(oldLines, newLines)
	var edits []rawEdit

	oi := 0
	for _, run := range equalRuns {
		if run.oldIdx > oi {
			edits = append(edits, rawEdit{op: '-', count: run.oldIdx - oi})
		}
		if run.newIdx > oi {
			edits = append(edits, rawEdit{op: '+', count: run.newIdx - (run.oldIdx - (run.oldIdx - oi))})
		}
		count := run.count
		edits = append(edits, rawEdit{op: '=', count: count})
		oi = run.oldIdx + count
	}

	if oi < len(oldLines) {
		edits = append(edits, rawEdit{op: '-', count: len(oldLines) - oi})
	}
	if oi < len(newLines) {
		edits = append(edits, rawEdit{op: '+', count: len(newLines) - oi})
	}

	var merged []rawEdit
	for _, e := range edits {
		if e.count == 0 {
			continue
		}
		if len(merged) > 0 && merged[len(merged)-1].op == e.op {
			merged[len(merged)-1].count += e.count
		} else {
			merged = append(merged, e)
		}
	}

	return merged
}

type equalRun struct {
	oldIdx int
	newIdx int
	count  int
}

func computeLCSRuns(oldLines, newLines []string) []equalRun {
	m := len(oldLines)
	n := len(newLines)
	if m == 0 || n == 0 {
		return nil
	}

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				if dp[i+1][j] > dp[i][j+1] {
					dp[i][j] = dp[i+1][j]
				} else {
					dp[i][j] = dp[i][j+1]
				}
			}
		}
	}

	var runs []equalRun
	i, j := 0, 0
	for i < m && j < n {
		if oldLines[i] == newLines[j] {
			start := 0
			for i+start < m && j+start < n && oldLines[i+start] == newLines[j+start] {
				start++
			}
			runs = append(runs, equalRun{oldIdx: i, newIdx: j, count: start})
			i += start
			j += start
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}

	return runs
}

func generateEditDiff(oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLine := len(oldLines)
	if len(newLines) > maxLine {
		maxLine = len(newLines)
	}
	width := len(fmt.Sprintf("%d", maxLine))

	var output []string
	oldIdx, newIdx := 0, 0

	for oldIdx < len(oldLines) || newIdx < len(newLines) {
		if oldIdx < len(oldLines) && newIdx < len(newLines) && oldLines[oldIdx] == newLines[newIdx] {
			lineNum := fmt.Sprintf("%*d", width, newIdx+1)
			output = append(output, fmt.Sprintf(" %s %s", lineNum, oldLines[oldIdx]))
			oldIdx++
			newIdx++
		} else if newIdx < len(newLines) && (oldIdx >= len(oldLines) || !lineInSlice(oldLines[oldIdx:], newLines[newIdx])) {
			lineNum := fmt.Sprintf("%*d", width, newIdx+1)
			output = append(output, fmt.Sprintf("+%s %s", lineNum, newLines[newIdx]))
			newIdx++
		} else if oldIdx < len(oldLines) {
			lineNum := fmt.Sprintf("%*d", width, oldIdx+1)
			output = append(output, fmt.Sprintf("-%s %s", lineNum, oldLines[oldIdx]))
			oldIdx++
		}
	}

	return strings.Join(output, "\n")
}

func firstChangedLineNumber(oldContent, newContent string) *int {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	for i := 0; i < len(newLines) && i < len(oldLines); i++ {
		if oldLines[i] != newLines[i] {
			v := i + 1
			return &v
		}
	}
	if len(newLines) != len(oldLines) {
		v := minInt(len(oldLines), len(newLines)) + 1
		return &v
	}
	return nil
}

func lineInSlice(lines []string, target string) bool {
	for _, l := range lines {
		if l == target {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

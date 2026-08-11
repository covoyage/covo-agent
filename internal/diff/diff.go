// Package diff provides a minimal unified-diff implementation based on the
// Longest Common Subsequence (LCS) algorithm. Unlike naive forward-scan
// approaches, LCS correctly handles pure insertions and pure deletions
// without marking surrounding lines as changed.
package diff

import (
	"fmt"
	"strings"
)

// LineKind classifies a diff line.
type LineKind int

const (
	KindContext LineKind = iota // unchanged line (prefix " ")
	KindAdd                     // added line (prefix "+")
	KindDel                     // removed line (prefix "-")
)

// Line represents a single line in a diff.
type Line struct {
	Kind    LineKind
	Content string
}

// FileDiff is a structured unified diff for one file.
type FileDiff struct {
	Path    string
	Unified string
}

// Hunk is a contiguous block of changes with surrounding context.
type Hunk struct {
	OldStart int // 1-based start line in old file (0 if hunk is pure addition at start)
	OldCount int // number of old lines in hunk
	NewStart int // 1-based start line in new file (0 if hunk is pure deletion at start)
	NewCount int // number of new lines in hunk
	Lines    []Line
}

// MaxLines is the safety cap on total diff output lines.
const defaultMaxLines = 200

// Unified produces a list of hunks representing the differences between
// oldText and newText. Each hunk includes up to contextLines lines of
// surrounding context. The output is capped at maxLines total lines.
func Unified(oldText, newText, filename string, contextLines, maxLines int) string {
	if oldText == newText {
		return ""
	}

	if contextLines < 0 {
		contextLines = 3
	}
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}

	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	// For very large inputs, fall back to a simple "show everything" diff
	// to avoid O(n*m) memory blowup.
	if len(oldLines)*len(newLines) > 4_000_000 {
		return fallbackDiff(oldLines, newLines, filename, maxLines)
	}

	diffs := lcsDiff(oldLines, newLines)
	hunks := groupHunks(diffs, contextLines)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	b.WriteString(fmt.Sprintf("+++ b/%s\n", filename))

	total := 0
	for _, h := range hunks {
		hunkLines := formatHunk(h)
		if total+len(hunkLines) > maxLines {
			remaining := maxLines - total
			if remaining > 0 {
				b.WriteString(strings.Join(hunkLines[:remaining], "\n"))
				b.WriteString("\n")
			}
			b.WriteString("... (diff truncated)\n")
			break
		}
		b.WriteString(strings.Join(hunkLines, "\n"))
		b.WriteString("\n")
		total += len(hunkLines)
	}

	return strings.TrimRight(b.String(), "\n")
}

// splitLines splits text into lines, preserving the content without the
// trailing newline. An empty string produces an empty slice (not a slice
// with one empty element).
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	// strings.Split("a\n", "\n") → ["a", ""], trim trailing empty
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lcsDiff computes the LCS between old and new, then backtracks to produce
// a sequence of diff lines (context, add, del) in order.
func lcsDiff(oldLines, newLines []string) []Line {
	m, n := len(oldLines), len(newLines)

	// dp[i][j] = length of LCS of oldLines[0..i-1] and newLines[0..j-1]
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max2(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	// Backtrack from (m, n) to (0, 0).
	var result []Line
	i, j := m, n
	for i > 0 && j > 0 {
		if oldLines[i-1] == newLines[j-1] {
			result = append(result, Line{Kind: KindContext, Content: oldLines[i-1]})
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			result = append(result, Line{Kind: KindDel, Content: oldLines[i-1]})
			i--
		} else {
			result = append(result, Line{Kind: KindAdd, Content: newLines[j-1]})
			j--
		}
	}
	for i > 0 {
		result = append(result, Line{Kind: KindDel, Content: oldLines[i-1]})
		i--
	}
	for j > 0 {
		result = append(result, Line{Kind: KindAdd, Content: newLines[j-1]})
		j--
	}

	// Reverse the result (we built it backwards).
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}

	return result
}

// groupHunks groups consecutive non-context lines into hunks, including
// up to contextLines of surrounding context. Adjacent hunks whose context
// ranges overlap are merged.
func groupHunks(diffs []Line, contextLines int) []Hunk {
	if len(diffs) == 0 {
		return nil
	}

	// First, identify the indices of all non-context lines.
	type changeRange struct{ start, end int } // [start, end) in diffs
	var changes []changeRange

	inChange := false
	changeStart := 0
	for idx, d := range diffs {
		if d.Kind != KindContext {
			if !inChange {
				changeStart = idx
				inChange = true
			}
		} else {
			if inChange {
				changes = append(changes, changeRange{changeStart, idx})
				inChange = false
			}
		}
	}
	if inChange {
		changes = append(changes, changeRange{changeStart, len(diffs)})
	}

	if len(changes) == 0 {
		return nil
	}

	// Expand each change by contextLines and merge overlapping ranges.
	mergedStart := max2(0, changes[0].start-contextLines)
	mergedEnd := min2(len(diffs), changes[0].end+contextLines)

	var hunks []Hunk
	// Track old/new line numbers as we walk through diffs.
	oldLine, newLine := 0, 0

	for idx, d := range diffs {
		if idx == mergedStart {
			// Start of a hunk — record the line numbers.
			hunks = append(hunks, Hunk{
				OldStart: oldLine + 1,
				NewStart: newLine + 1,
			})
		}

		if idx >= mergedStart && idx < mergedEnd {
			hunk := &hunks[len(hunks)-1]
			hunk.Lines = append(hunk.Lines, d)
			hunk.OldCount += lineCounts(d).old
			hunk.NewCount += lineCounts(d).new
		}

		// Advance line counters.
		switch d.Kind {
		case KindContext:
			oldLine++
			newLine++
		case KindDel:
			oldLine++
		case KindAdd:
			newLine++
		}

		// Check if we need to start a new hunk (gap after current mergedEnd).
		if idx == mergedEnd-1 {
			// Look ahead for the next change range.
			for ci := 0; ci < len(changes); ci++ {
				if changes[ci].start >= mergedEnd {
					nextStart := max2(0, changes[ci].start-contextLines)
					nextEnd := min2(len(diffs), changes[ci].end+contextLines)
					// Check if this merges with the previous hunk.
					if nextStart <= mergedEnd {
						// Merge — extend the current hunk.
						mergedEnd = nextEnd
						// Continue the loop to find a non-overlapping next change.
						continue
					}
					mergedStart = nextStart
					mergedEnd = nextEnd
					break
				}
			}
		}
	}

	return hunks
}

type lineCount struct{ old, new int }

func lineCounts(d Line) lineCount {
	switch d.Kind {
	case KindContext:
		return lineCount{1, 1}
	case KindDel:
		return lineCount{1, 0}
	case KindAdd:
		return lineCount{0, 1}
	}
	return lineCount{0, 0}
}

func formatHunk(h Hunk) []string {
	var lines []string
	lines = append(lines, fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount))
	for _, l := range h.Lines {
		var prefix string
		switch l.Kind {
		case KindContext:
			prefix = " "
		case KindAdd:
			prefix = "+"
		case KindDel:
			prefix = "-"
		}
		lines = append(lines, prefix+l.Content)
	}
	return lines
}

// fallbackDiff produces a simple "delete all old, add all new" diff for
// very large inputs where LCS would be too expensive.
func fallbackDiff(oldLines, newLines []string, filename string, maxLines int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- a/%s\n", filename))
	b.WriteString(fmt.Sprintf("+++ b/%s\n", filename))
	b.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@", len(oldLines), len(newLines)))

	count := 0
	for _, l := range oldLines {
		if count >= maxLines {
			b.WriteString("\n... (diff truncated)")
			return b.String()
		}
		b.WriteString("\n-" + l)
		count++
	}
	for _, l := range newLines {
		if count >= maxLines {
			b.WriteString("\n... (diff truncated)")
			return b.String()
		}
		b.WriteString("\n+" + l)
		count++
	}
	return b.String()
}

func max2(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

package fileops

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func normalizeForFuzzyMatch(text string) string {
	text = sanitizeBinaryOutput(stripAnsi(text))

	// Normalize line endings: CRLF → LF, then ensure trailing newline consistency
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimRight(text, "\n")

	text = norm.NFKC.String(text)

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		// Trim trailing whitespace
		line = strings.TrimRight(line, " \t")
		// Normalize leading indentation: tabs → 4 spaces, then collapse consecutive spaces
		line = normalizeIndent(line)
		lines[i] = line
	}
	text = strings.Join(lines, "\n")

	text = strings.Map(func(r rune) rune {
		switch r {
		case '\u2018', '\u2019', '\u201A', '\u201B':
			return '\''
		case '\u201C', '\u201D', '\u201E', '\u201F':
			return '"'
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			return '-'
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005',
			'\u2006', '\u2007', '\u2008', '\u2009', '\u200A',
			'\u202F', '\u205F', '\u3000':
			return ' '
		}
		return r
	}, text)

	return text
}

// normalizeIndent converts leading tabs to 4 spaces, then collapses any
// multi-space indentation to single-space-per-level. This allows matching
// across tab/2-space/4-space indentation styles.
func normalizeIndent(line string) string {
	if line == "" {
		return line
	}

	leading := ""
	tail := line
	for len(tail) > 0 {
		if tail[0] == '\t' {
			leading += "    " // tab → 4 spaces
			tail = tail[1:]
		} else if tail[0] == ' ' {
			leading += " "
			tail = tail[1:]
		} else {
			break
		}
	}
	// Collapse each group of 2 or 4 spaces to a single "▸" marker for
	// indentation-level comparison, so 2-space and 4-space indents match.
	if leading != "" {
		// Normalize: convert all indentation to 4-space units then to 2-space units
		// This handles both 2-space and 4-space files by collapsing to 2-space cmp
		collapsed := strings.ReplaceAll(leading, "    ", "  ")
		collapsed = strings.ReplaceAll(collapsed, "   ", "  ")
		collapsed = strings.ReplaceAll(collapsed, "  ", "  ")
		leading = collapsed
	}

	return leading + tail
}

// fuzzyFindIndex locates oldText in content using a multi-level fallback:
//  1. Exact match (strings.Index) with uniqueness check
//  2. Normalized match (NFKC + indent + smart quote normalization) with uniqueness check
//  3. BlockAnchor match (first/last line anchors + Levenshtein similarity) with ambiguity check
//
// Returns:
//   - startIndex, endIndex: byte offsets in content for the match region
//   - found: true if a unique match was found at some level
//   - usedFuzzy: true if level 2 (normalized) or level 3 (BlockAnchor) was used
//   - ambiguous: true if oldText appears multiple times and no level could uniquely resolve it
//
// If multiple levels each find matches but none is unique, ambiguous=true is returned
// so the caller can refuse the edit rather than silently picking the first match.
func fuzzyFindIndex(content, oldText string) (startIndex, endIndex int, found, usedFuzzy, ambiguous bool) {
	anyAmbiguous := false

	// Level 1: exact match with uniqueness check.
	if idx, ok, amb := findUniqueIndex(content, oldText); ok && !amb {
		return idx, idx + len(oldText), true, false, false
	} else if amb {
		anyAmbiguous = true
	}

	// Level 2: normalized match with uniqueness check.
	// The match is found in normalized space, then verified against the original
	// content: we re-normalize content[nIdx:end] and check it equals fuzzyOldText.
	// This ensures the byte offsets are correct. When normalization changes byte
	// lengths (e.g. smart quotes 3 bytes → 1 byte), the offsets in normalized
	// space don't map directly to original space — verification fails and we
	// fall through to BlockAnchor (level 3) which computes exact byte offsets.
	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	if nIdx, nOk, nAmb := findUniqueIndex(fuzzyContent, fuzzyOldText); nOk && !nAmb {
		end := nIdx + len(fuzzyOldText)
		if end <= len(content) && normalizeForFuzzyMatch(content[nIdx:end]) == fuzzyOldText {
			return nIdx, end, true, true, false
		}
		// Offsets don't map correctly; fall through to BlockAnchor.
	} else if nAmb {
		anyAmbiguous = true
	}

	// Level 3: BlockAnchor + Levenshtein. Uses first/last non-empty normalized
	// lines as anchors, then verifies the full block via Levenshtein similarity.
	// Returns exact byte offsets in the original content.
	if bStart, bEnd, bOk, bAmb := fuzzyFindBlockAnchor(content, oldText); bOk && !bAmb {
		return bStart, bEnd, true, true, false
	} else if bAmb {
		anyAmbiguous = true
	}

	return -1, -1, false, false, anyAmbiguous
}

// findUniqueIndex returns the byte index of the first occurrence of substr in s.
//   - If substr appears exactly once: returns (index, true, false)
//   - If substr appears multiple times: returns (firstIndex, true, true) — ambiguous
//   - If substr does not appear: returns (-1, false, false)
func findUniqueIndex(s, substr string) (index int, found bool, ambiguous bool) {
	idx := strings.Index(s, substr)
	if idx == -1 {
		return -1, false, false
	}
	lastIdx := strings.LastIndex(s, substr)
	if idx != lastIdx {
		return idx, true, true
	}
	return idx, true, false
}

// levenshtein computes the rune-level edit distance between two strings using
// the standard two-row DP. Memory is O(min(len(a), len(b))).
func levenshtein(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}

	// Make rb the shorter one to minimize memory.
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}

	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := 0; j <= len(rb); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

// levenshteinSimilarity returns a similarity ratio in [0, 1] where 1 means
// identical and 0 means completely different.
func levenshteinSimilarity(a, b string) float64 {
	if a == "" && b == "" {
		return 1.0
	}
	ra := []rune(a)
	rb := []rune(b)
	maxLen := maxInt(len(ra), len(rb))
	if maxLen == 0 {
		return 1.0
	}
	return 1.0 - float64(levenshtein(a, b))/float64(maxLen)
}

// lineStartByteOffset returns the byte offset of the start of lines[lineIdx]
// in the original content (where lines = strings.Split(content, "\n")).
// If lineIdx >= len(lines), returns len(strings.Join(lines, "\n")).
func lineStartByteOffset(lines []string, lineIdx int) int {
	if lineIdx <= 0 {
		return 0
	}
	if lineIdx >= len(lines) {
		return len(strings.Join(lines, "\n"))
	}
	return len(strings.Join(lines[:lineIdx], "\n")) + 1
}

// fuzzyFindBlockAnchor implements BlockAnchor matching: it locates oldText in
// content by using the first and last non-empty lines (after normalization) as
// anchors, then verifies each candidate block via Levenshtein similarity.
//
// Thresholds:
//   - single candidate: threshold 0.0 (accept any similarity)
//   - multiple candidates: threshold 0.3 (require 30% similarity to disambiguate)
//
// If multiple candidates pass the threshold, the result is ambiguous.
// Returns byte offsets in the ORIGINAL content.
func fuzzyFindBlockAnchor(content, oldText string) (startIndex, endIndex int, found, ambiguous bool) {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")

	if len(oldLines) == 0 {
		return -1, -1, false, false
	}

	// Find first and last non-empty normalized lines of oldText as anchors.
	firstAnchor := ""
	firstAnchorIdx := -1
	lastAnchor := ""
	lastAnchorIdx := -1
	for i, line := range oldLines {
		norm := normalizeForFuzzyMatch(strings.TrimSpace(line))
		if norm == "" {
			continue
		}
		if firstAnchorIdx == -1 {
			firstAnchor = norm
			firstAnchorIdx = i
		}
		lastAnchor = norm
		lastAnchorIdx = i
	}

	if firstAnchorIdx == -1 {
		return -1, -1, false, false // oldText is all empty/whitespace
	}

	// Allow some slack in the anchor distance to tolerate middle-line
	// insertions/deletions in the content vs. oldText.
	expectedSpan := lastAnchorIdx - firstAnchorIdx
	tolerance := expectedSpan / 4
	if tolerance < 2 {
		tolerance = 2
	}

	type anchorCandidate struct {
		startLine int
		endLine   int
	}
	var candidates []anchorCandidate

	for i := 0; i < len(contentLines); i++ {
		if normalizeForFuzzyMatch(strings.TrimSpace(contentLines[i])) != firstAnchor {
			continue
		}
		// Look for lastAnchor within [i+expectedSpan-tolerance, i+expectedSpan+tolerance].
		lo := i + expectedSpan - tolerance
		if lo < i {
			lo = i
		}
		hi := i + expectedSpan + tolerance
		if hi >= len(contentLines) {
			hi = len(contentLines) - 1
		}
		for j := lo; j <= hi; j++ {
			if normalizeForFuzzyMatch(strings.TrimSpace(contentLines[j])) == lastAnchor {
				candidates = append(candidates, anchorCandidate{startLine: i, endLine: j})
				break // pair with first matching lastAnchor in range
			}
		}
	}

	if len(candidates) == 0 {
		return -1, -1, false, false
	}

	// Score each candidate by Levenshtein similarity of the full normalized block.
	threshold := 0.0
	if len(candidates) > 1 {
		threshold = 0.3
	}
	normOld := normalizeForFuzzyMatch(oldText)

	type scored struct {
		anchorCandidate
		score float64
	}
	var passed []scored
	for _, c := range candidates {
		blockText := strings.Join(contentLines[c.startLine:c.endLine+1], "\n")
		score := levenshteinSimilarity(normalizeForFuzzyMatch(blockText), normOld)
		if score >= threshold {
			passed = append(passed, scored{c, score})
		}
	}

	if len(passed) == 0 {
		return -1, -1, false, false
	}
	if len(passed) > 1 {
		return -1, -1, true, true // ambiguous
	}

	c := passed[0].anchorCandidate
	startIdx := lineStartByteOffset(contentLines, c.startLine)
	endIdx := lineStartByteOffset(contentLines, c.endLine+1)
	return startIdx, endIdx, true, false
}

func isControlOrInvalid(r rune) bool {
	return unicode.Is(unicode.C, r) && r != '\n' && r != '\r' && r != '\t'
}

func sanitizeBinaryOutput(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range text {
		if isControlOrInvalid(r) {
			builder.WriteRune('\uFFFD')
		} else {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func stripAnsi(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	i := 0
	runes := []rune(text)
	for i < len(runes) {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) && ((runes[i] >= '0' && runes[i] <= '9') || runes[i] == ';' || runes[i] == '?') {
				i++
			}
			if i < len(runes) && ((runes[i] >= 'A' && runes[i] <= 'Z') || (runes[i] >= 'a' && runes[i] <= 'z')) {
				i++
			}
			continue
		}
		builder.WriteRune(runes[i])
		i++
	}
	return builder.String()
}

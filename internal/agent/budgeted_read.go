package agent

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// budgetedReadTruncMarker is appended when content is cut short so the reader
// knows there is more (and can Read the file with an offset to see the rest).
const budgetedReadTruncMarker = "\n…[truncated — Read the file with an offset to see the rest]"

// italicLineRe matches a line that is entirely an italic instruction — wrapped
// in single * or _ (not bold **/__). These lines commonly carry guidance like
// "_update this section when X_" and are cheap, high-value, so they are always
// preserved by ReadBudgetedSectionAware.
var italicLineRe = regexp.MustCompile(`^\s*[*_][^*_\n]+[*_]\s*$`)

// sectionHeaderRe matches markdown ATX headers (# through ######).
var sectionHeaderRe = regexp.MustCompile(`^#{1,6}\s`)

// isIndexLine reports whether a line is a bullet/index entry (starts with
// "- " or "* " outside of an italic wrap). Index lines summarize a section's
// contents and are always preserved.
func isIndexLine(line string) bool {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
		// Distinguish bullet "*" from italic "*" — italic is matched above and
		// a bullet is followed by a space and then content.
		return true
	}
	return false
}

// section is a parsed markdown section: an optional header line (## Title) and
// the body lines that follow it until the next header.
type section struct {
	header string // the header line including its "#" prefix, "" for preamble
	lines  []string
}

// parseSections splits markdown content into sections delimited by ATX headers
// (#..######). Content before the first header becomes a preamble section with
// an empty header.
func parseSections(content string) []section {
	lines := strings.Split(content, "\n")
	var sections []section
	cur := section{}
	for _, line := range lines {
		if sectionHeaderRe.MatchString(line) {
			if len(cur.lines) > 0 || cur.header != "" {
				sections = append(sections, cur)
			}
			cur = section{header: line}
		} else {
			cur.lines = append(cur.lines, line)
		}
	}
	if len(cur.lines) > 0 || cur.header != "" {
		sections = append(sections, cur)
	}
	if len(sections) == 0 {
		return []section{{lines: lines}}
	}
	return sections
}

// classify splits a section's body lines into "always-keep" (italic
// instructions and index/bullet lines) and "body" (everything else). Blank
// lines are kept with the body to preserve paragraph structure.
func (s *section) classify() (keep []string, body []string) {
	for _, line := range s.lines {
		if italicLineRe.MatchString(line) || isIndexLine(line) {
			keep = append(keep, line)
		} else {
			body = append(body, line)
		}
	}
	return keep, body
}

// ReadBudgetedSectionAware reads markdown content under a token budget while
// preserving navigability. It parses content into ## sections and, within each
// section, always keeps:
//   - headers (## lines)
//   - italic instruction lines (*text* / _text_)
//   - index/bullet lines (- item)
//
// Only body paragraphs participate in trimming. When the skeleton (headers +
// italic + index) alone exceeds the budget, the skeleton is returned so the
// reader at least knows what sections exist and can Read the full file on
// demand. Otherwise bodies are trimmed proportionally, each cut ending at a
// line boundary with a truncation marker.
//
// budgetTokens uses the chars/4 token heuristic (consistent with
// estimateTokens). Pass <= 0 to disable budgeting and return content as-is.
func ReadBudgetedSectionAware(content string, budgetTokens int) string {
	if budgetTokens <= 0 || estimateTokens(content) <= budgetTokens {
		return content
	}

	sections := parseSections(content)

	// First pass: compute the skeleton cost (headers + always-keep lines) and
	// the body cost per section.
	type secPlan struct {
		header   string
		keep     []string
		body     []string
		bodyToks int
	}
	plans := make([]secPlan, 0, len(sections))
	skeletonToks := 0
	totalBodyToks := 0
	for _, s := range sections {
		keep, body := s.classify()
		if s.header != "" {
			skeletonToks += estimateTokens(s.header + "\n")
		}
		for _, k := range keep {
			skeletonToks += estimateTokens(k + "\n")
		}
		bt := estimateTokens(strings.Join(body, "\n"))
		plans = append(plans, secPlan{header: s.header, keep: keep, body: body, bodyToks: bt})
		totalBodyToks += bt
	}

	// Skeleton-only mode: even without any body, the structure exceeds budget.
	// Return just headers + keep lines so the reader can navigate on demand.
	if skeletonToks >= budgetTokens {
		var b strings.Builder
		for _, p := range plans {
			if p.header != "" {
				b.WriteString(p.header)
				b.WriteByte('\n')
			}
			for _, k := range p.keep {
				b.WriteString(k)
				b.WriteByte('\n')
			}
		}
		return strings.TrimRight(b.String(), "\n")
	}

	// Distribute the remaining budget across section bodies proportionally.
	remaining := budgetTokens - skeletonToks
	var b strings.Builder
	for _, p := range plans {
		if p.header != "" {
			b.WriteString(p.header)
			b.WriteByte('\n')
		}
		for _, k := range p.keep {
			b.WriteString(k)
			b.WriteByte('\n')
		}
		if len(p.body) == 0 {
			continue
		}
		// Proportional share of the remaining budget.
		share := remaining
		if totalBodyToks > 0 {
			share = remaining * p.bodyToks / totalBodyToks
		}
		body := strings.Join(p.body, "\n")
		if estimateTokens(body) <= share {
			b.WriteString(body)
			b.WriteByte('\n')
		} else {
			// Trim to the share, ending at a line boundary.
			trimmed := trimToTokens(body, share)
			b.WriteString(trimmed)
			b.WriteString(budgetedReadTruncMarker)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ReadBudgeted truncates content to fit within budgetTokens, ending at the
// last complete line and appending a truncation marker when cut. Use this for
// non-markdown content or when section-awareness is not needed.
func ReadBudgeted(content string, budgetTokens int) string {
	if budgetTokens <= 0 || estimateTokens(content) <= budgetTokens {
		return content
	}
	return trimToTokens(content, budgetTokens) + budgetedReadTruncMarker
}

// trimToTokens returns the longest prefix of content (ending at a line
// boundary) whose estimated token count does not exceed budget.
func trimToTokens(content string, budget int) string {
	// chars-per-token ≈ 4; find the byte cutoff then walk back to a newline.
	maxChars := budget * 4
	if maxChars <= 0 {
		return ""
	}
	if len(content) <= maxChars {
		return content
	}
	cut := maxChars
	// Walk back to the previous newline so we don't cut mid-line. Include the
	// newline itself so the result ends at a clean line boundary.
	if idx := strings.LastIndex(content[:cut], "\n"); idx >= 0 {
		cut = idx + 1
	} else {
		// Single very long line — cut at maxChars, but walk back to a valid
		// UTF-8 rune boundary so we don't split a multi-byte sequence (e.g.
		// CJK characters, which are 3 bytes each in UTF-8).
		cut = maxChars
		for cut > 0 && !utf8.RuneStart(content[cut]) {
			cut--
		}
	}
	return content[:cut]
}

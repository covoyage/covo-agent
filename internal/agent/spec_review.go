package agent

// Spec-anchored two-phase code review.
//
// Design principles:
//
//  1. Anti-anchoring: Phase 1 sees ONLY the diff (no spec, no report).
//     This produces an independent quality assessment that isn't biased by
//     knowing what the spec requires.
//
//  2. Spec-anchored downgrade: Phase 2 sees the Phase 1 assessment + the
//     spec. It verifies spec coverage and can ONLY downgrade the rating
//     (pass → concerns → fail), never upgrade. This prevents the reviewer
//     from being lenient after seeing an optimistic Phase 1.
//
//  3. Coverage annotation: Each spec section is annotated as covered,
//     partial, missing, or n/a relative to the diff, providing traceability
//     from spec requirements to code changes.
//
// Usage:
//   reviewer := NewSpecReviewer(llmCallFn)
//   result, err := reviewer.Review(ctx, diff, specMarkdown)
//   // result.Phase1 = independent assessment
//   // result.Phase2 = spec-anchored assessment (downgrade-only)
//   // result.Covers = per-section coverage annotations

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Rating is the quality assessment level. The ordering matters:
// RatingPass > RatingConcerns > RatingFail.
type Rating string

const (
	RatingPass     Rating = "pass"
	RatingConcerns Rating = "concerns"
	RatingFail     Rating = "fail"
)

// ratingOrder maps ratings to numeric levels for comparison.
var ratingOrder = map[Rating]int{
	RatingPass:     3,
	RatingConcerns: 2,
	RatingFail:     1,
}

// canDowngrade returns true if `from` is a higher rating than `to`
// (i.e., downgrading from → to is allowed).
func canDowngrade(from, to Rating) bool {
	fromLevel, ok1 := ratingOrder[from]
	toLevel, ok2 := ratingOrder[to]
	if !ok1 || !ok2 {
		return false
	}
	return toLevel < fromLevel
}

// CoverStatus describes how well a spec section is covered by the diff.
type CoverStatus string

const (
	CoverCovered   CoverStatus = "covered" // diff fully implements this section
	CoverPartial   CoverStatus = "partial" // diff partially implements this section
	CoverMissing   CoverStatus = "missing" // diff doesn't address this section
	CoverNotApplic CoverStatus = "n/a"     // section is non-functional (e.g. intro)
)

// SpecSection is a parsed section of a specification document.
type SpecSection struct {
	ID      string `json:"id"`      // anchor ID derived from title (e.g. "auth-login")
	Title   string `json:"title"`   // section heading text
	Content string `json:"content"` // section body (without the heading)
}

// CoverAnnotation annotates a single spec section's coverage.
type CoverAnnotation struct {
	SpecID string      `json:"spec_id"`
	Status CoverStatus `json:"status"`
	Note   string      `json:"note,omitempty"`
}

// Phase1Assessment is the independent review of the diff (no spec visible).
type Phase1Assessment struct {
	Rating  Rating   `json:"rating"`
	Summary string   `json:"summary"`
	Issues  []string `json:"issues,omitempty"`
}

// Phase2Assessment is the spec-anchored review that can only downgrade.
type Phase2Assessment struct {
	Rating          Rating            `json:"rating"` // final rating (≤ Phase1 rating)
	Summary         string            `json:"summary"`
	Issues          []string          `json:"issues,omitempty"`
	Covers          []CoverAnnotation `json:"covers"`
	Downgraded      bool              `json:"downgraded"` // true if rating < Phase1 rating
	DowngradeReason string            `json:"downgrade_reason,omitempty"`
}

// SpecReviewResult is the complete two-phase review output.
type SpecReviewResult struct {
	Phase1 Phase1Assessment `json:"phase1"`
	Phase2 Phase2Assessment `json:"phase2"`
}

// SpecReviewer performs two-phase spec-anchored code review.
type SpecReviewer struct {
	llmCall func(ctx context.Context, systemPrompt, userPrompt string) (string, error)
	timeout time.Duration
}

// NewSpecReviewer creates a reviewer backed by the given LLM call function.
func NewSpecReviewer(llmCall func(ctx context.Context, systemPrompt, userPrompt string) (string, error)) *SpecReviewer {
	return &SpecReviewer{
		llmCall: llmCall,
		timeout: 120 * time.Second,
	}
}

// SetTimeout overrides the default review timeout.
func (r *SpecReviewer) SetTimeout(d time.Duration) {
	r.timeout = d
}

// Review performs the full two-phase review.
func (r *SpecReviewer) Review(ctx context.Context, diff, specMarkdown string) (*SpecReviewResult, error) {
	sections := ParseSpecSections(specMarkdown)

	// Phase 1: independent diff review (no spec visible).
	phase1, err := r.runPhase1(ctx, diff)
	if err != nil {
		return nil, fmt.Errorf("phase 1: %w", err)
	}

	// Phase 2: spec-anchored review (can only downgrade).
	phase2, err := r.runPhase2(ctx, phase1, sections, diff)
	if err != nil {
		// Phase 2 failure is non-fatal — return Phase 1 with a note.
		return &SpecReviewResult{
			Phase1: phase1,
			Phase2: Phase2Assessment{
				Rating:  phase1.Rating,
				Summary: "Phase 2 review failed; using Phase 1 assessment.",
				Covers:  nil,
			},
		}, nil
	}

	return &SpecReviewResult{
		Phase1: phase1,
		Phase2: phase2,
	}, nil
}

// runPhase1 sends only the diff to the LLM for an independent quality assessment.
func (r *SpecReviewer) runPhase1(ctx context.Context, diff string) (Phase1Assessment, error) {
	systemPrompt := `You are a code reviewer. Review the following code diff and provide an independent quality assessment.

You do NOT have access to any specification or design document. Judge the code purely on its own merits.

Respond in JSON format:
{
  "rating": "pass" | "concerns" | "fail",
  "summary": "brief overall assessment",
  "issues": ["issue 1", "issue 2"]
}

Rating criteria:
- "pass": Code is clean, correct, and follows good practices.
- "concerns": Code has minor issues, potential edge cases, or style problems.
- "fail": Code has bugs, security issues, or fundamental problems.`

	userPrompt := fmt.Sprintf("Review this code diff:\n\n```\n%s\n```", diff)

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	response, err := r.llmCall(ctx, systemPrompt, userPrompt)
	if err != nil {
		return Phase1Assessment{}, err
	}

	return parsePhase1Response(response), nil
}

// runPhase2 sends the Phase 1 assessment + spec sections to the LLM.
// The reviewer can only DOWNGRADE the rating (pass → concerns → fail), never upgrade.
func (r *SpecReviewer) runPhase2(ctx context.Context, phase1 Phase1Assessment, sections []SpecSection, diff string) (Phase2Assessment, error) {
	var specText strings.Builder
	for _, s := range sections {
		specText.WriteString(fmt.Sprintf("### [%s] %s\n%s\n\n", s.ID, s.Title, s.Content))
	}

	systemPrompt := fmt.Sprintf(`You are a spec-anchored code reviewer. You are given:
1. A Phase 1 quality assessment (produced without seeing the spec).
2. The specification with labeled sections.
3. The code diff.

Your job: verify that the diff covers the spec requirements. You can ONLY DOWNGRADE the rating (pass → concerns → fail), NEVER upgrade it.

Current Phase 1 rating: %s

Rules:
- If the diff fully covers all spec sections and Phase 1 was "pass", keep "pass".
- If any spec section is missing or partially covered, downgrade by at least one level.
- If a critical spec requirement is entirely missing, downgrade to "fail".
- You may NOT upgrade the rating above %s under any circumstances.

Annotate each spec section's coverage:
- "covered": diff fully implements this section
- "partial": diff partially implements this section
- "missing": diff doesn't address this section
- "n/a": section is non-functional (intro, overview, etc.)

Respond in JSON format:
{
  "rating": "pass" | "concerns" | "fail",
  "summary": "brief assessment focusing on spec coverage",
  "issues": ["spec coverage issue 1", ...],
  "covers": [{"spec_id": "...", "status": "covered|partial|missing|n/a", "note": "..."}],
  "downgraded": true/false,
  "downgrade_reason": "reason if downgraded, empty otherwise"
}`, phase1.Rating, phase1.Rating)

	userPrompt := fmt.Sprintf("## Phase 1 Assessment\nRating: %s\nSummary: %s\nIssues: %s\n\n## Specification\n%s\n## Code Diff\n```\n%s\n```",
		phase1.Rating,
		phase1.Summary,
		strings.Join(phase1.Issues, "; "),
		specText.String(),
		diff,
	)

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	response, err := r.llmCall(ctx, systemPrompt, userPrompt)
	if err != nil {
		return Phase2Assessment{}, err
	}

	phase2 := parsePhase2Response(response)

	// Enforce downgrade-only policy:
	// - Same rating: allowed (no change).
	// - Lower rating: allowed (downgrade).
	// - Higher rating: BLOCKED — clamp back to Phase 1 rating.
	if phase2.Rating == phase1.Rating {
		// No change — allowed as-is. The rating did not change, so any
		// "downgraded": true reported by the LLM is incorrect; reset it
		// (and clear any stale downgrade reason) to avoid false positives.
		phase2.Downgraded = false
		phase2.DowngradeReason = ""
	} else if canDowngrade(phase1.Rating, phase2.Rating) {
		// Downgrade — allowed.
		phase2.Downgraded = true
	} else {
		// Upgrade attempt — clamp back to Phase 1 rating.
		phase2.Rating = phase1.Rating
		phase2.Downgraded = false
		phase2.DowngradeReason = "upgrade attempt blocked by downgrade-only policy"
	}

	return phase2, nil
}

// parsePhase1Response parses the LLM's JSON response for Phase 1.
func parsePhase1Response(response string) Phase1Assessment {
	var raw struct {
		Rating  string   `json:"rating"`
		Summary string   `json:"summary"`
		Issues  []string `json:"issues"`
	}
	if err := json.Unmarshal([]byte(extractJSON(response)), &raw); err != nil {
		return Phase1Assessment{
			Rating:  RatingConcerns,
			Summary: "Failed to parse review response: " + response,
		}
	}
	return Phase1Assessment{
		Rating:  normalizeRating(raw.Rating),
		Summary: raw.Summary,
		Issues:  raw.Issues,
	}
}

// parsePhase2Response parses the LLM's JSON response for Phase 2.
func parsePhase2Response(response string) Phase2Assessment {
	var raw struct {
		Rating          string            `json:"rating"`
		Summary         string            `json:"summary"`
		Issues          []string          `json:"issues"`
		Covers          []CoverAnnotation `json:"covers"`
		Downgraded      bool              `json:"downgraded"`
		DowngradeReason string            `json:"downgrade_reason"`
	}
	if err := json.Unmarshal([]byte(extractJSON(response)), &raw); err != nil {
		return Phase2Assessment{
			Rating:  RatingConcerns,
			Summary: "Failed to parse review response: " + response,
		}
	}
	return Phase2Assessment{
		Rating:          normalizeRating(raw.Rating),
		Summary:         raw.Summary,
		Issues:          raw.Issues,
		Covers:          raw.Covers,
		Downgraded:      raw.Downgraded,
		DowngradeReason: raw.DowngradeReason,
	}
}

// normalizeRating converts a string to a Rating, defaulting to "concerns" for unknown values.
func normalizeRating(s string) Rating {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pass":
		return RatingPass
	case "concerns":
		return RatingConcerns
	case "fail":
		return RatingFail
	default:
		return RatingConcerns
	}
}

// extractJSON finds the first JSON object in the response, handling fenced code blocks.
func extractJSON(text string) string {
	// Try fenced ```json block first.
	if idx := strings.Index(text, "```json\n"); idx >= 0 {
		rest := text[idx+len("```json\n"):]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	// Try plain fenced block.
	if idx := strings.Index(text, "```\n"); idx >= 0 {
		rest := text[idx+len("```\n"):]
		if end := strings.Index(rest, "```"); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	// Try to find raw JSON object.
	start := strings.Index(text, "{")
	if start < 0 {
		return text
	}
	// Find matching closing brace (naive but works for well-formed JSON).
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return text[start:]
}

// ---------------------------------------------------------------------------
// Spec section parsing
// ---------------------------------------------------------------------------

var specHeaderRe = regexp.MustCompile(`(?m)^#{2,3}\s+(.+)$`)

// ParseSpecSections parses a markdown specification into sections.
// Each `##` or `###` heading starts a new section. The section ID is derived
// from the heading text (slugified). Content before the first heading is
// treated as a preamble (no section ID).
func ParseSpecSections(markdown string) []SpecSection {
	if markdown == "" {
		return nil
	}

	matches := specHeaderRe.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return nil
	}

	var sections []SpecSection
	for i, match := range matches {
		titleStart := match[2]
		titleEnd := match[3]
		title := strings.TrimSpace(markdown[titleStart:titleEnd])

		// Content runs from end of this heading line to the start of the next.
		contentStart := titleEnd
		// Skip the rest of the heading line (up to newline).
		if nl := strings.Index(markdown[contentStart:], "\n"); nl >= 0 {
			contentStart = contentStart + nl + 1
		}
		contentEnd := len(markdown)
		if i+1 < len(matches) {
			contentEnd = matches[i+1][0]
		}
		content := strings.TrimSpace(markdown[contentStart:contentEnd])

		sections = append(sections, SpecSection{
			ID:      slugify(title),
			Title:   title,
			Content: content,
		})
	}

	return sections
}

// slugify converts a heading title to a URL-safe anchor ID.
// E.g. "User Authentication" → "user-authentication"
//
// Non-ASCII characters (e.g. CJK) are encoded as their Unicode code point
// (e.g. "中" → "u4e2d") so that distinct titles yield distinct IDs. Dropping
// such characters would collapse every CJK heading to the empty string and
// cause section ID collisions (see bug P0-1 unicode61).
func slugify(title string) string {
	s := strings.ToLower(title)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		case r < 0x80:
			// Skip other ASCII punctuation/symbols.
		default:
			// Non-ASCII (CJK etc.): encode as hex to remain distinguishable.
			fmt.Fprintf(&b, "u%04x", r)
		}
	}
	result := b.String()
	// Collapse consecutive hyphens and trim leading/trailing hyphens.
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return strings.Trim(result, "-")
}

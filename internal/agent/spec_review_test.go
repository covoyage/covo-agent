package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- slugify tests ---

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"User Authentication", "user-authentication"},
		{"Login Flow", "login-flow"},
		{"API v2 — Endpoints", "api-v2-u2014-endpoints"},
		{"  Trim  Me  ", "trim-me"},
		{"Simple", "simple"},
		// Non-ASCII chars are encoded as their code point so distinct titles
		// yield distinct IDs (previously CJK was dropped, causing collisions).
		{"CJK 中文", "cjk-u4e2du6587"},
		{"用户认证", "u7528u6237u8ba4u8bc1"},
		{"授权管理", "u6388u6743u7ba1u7406"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSlugify_CJKDistinct verifies that two different CJK titles produce
// different slugs (regression test for section ID collisions).
func TestSlugify_CJKDistinct(t *testing.T) {
	a := slugify("用户认证")
	b := slugify("授权管理")
	if a == "" || b == "" {
		t.Errorf("slugify returned empty string for CJK title: %q vs %q", a, b)
	}
	if a == b {
		t.Errorf("expected distinct slugs for distinct CJK titles, both = %q", a)
	}
}

// TestSlugify_NonEmpty verifies that slugify never returns an empty string
// for non-empty input (previously pure-CJK titles collapsed to "").
func TestSlugify_NonEmpty(t *testing.T) {
	inputs := []string{"用户认证", "授权管理", "中文", "测试", "a", "1"}
	for _, in := range inputs {
		if got := slugify(in); got == "" {
			t.Errorf("slugify(%q) returned empty string for non-empty input", in)
		}
	}
}

// --- ParseSpecSections tests ---

func TestParseSpecSections_Empty(t *testing.T) {
	if sections := ParseSpecSections(""); sections != nil {
		t.Errorf("expected nil for empty spec, got %v", sections)
	}
}

func TestParseSpecSections_NoHeaders(t *testing.T) {
	if sections := ParseSpecSections("Just plain text\nno headers"); sections != nil {
		t.Errorf("expected nil for no headers, got %v", sections)
	}
}

func TestParseSpecSections_ParsesH2Headers(t *testing.T) {
	spec := `# Project Spec

## Authentication
The auth module handles login.

## API Endpoints
- GET /users
- POST /users

## Testing
Use unit tests.`

	sections := ParseSpecSections(spec)
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}

	if sections[0].ID != "authentication" || sections[0].Title != "Authentication" {
		t.Errorf("section 0: expected ID=authentication, Title=Authentication, got ID=%s Title=%s", sections[0].ID, sections[0].Title)
	}
	if !strings.Contains(sections[0].Content, "auth module") {
		t.Errorf("section 0: expected auth content, got %q", sections[0].Content)
	}

	if sections[1].ID != "api-endpoints" || sections[1].Title != "API Endpoints" {
		t.Errorf("section 1: expected ID=api-endpoints, got ID=%s", sections[1].ID)
	}

	if sections[2].ID != "testing" {
		t.Errorf("section 2: expected ID=testing, got ID=%s", sections[2].ID)
	}
}

func TestParseSpecSections_ParsesH3Headers(t *testing.T) {
	spec := `## Overview
### Sub-section
Content here.`

	sections := ParseSpecSections(spec)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections (h2 + h3), got %d", len(sections))
	}
	if sections[0].Title != "Overview" {
		t.Errorf("section 0: expected Overview, got %s", sections[0].Title)
	}
	if sections[1].Title != "Sub-section" {
		t.Errorf("section 1: expected Sub-section, got %s", sections[1].Title)
	}
}

// TestParseSpecSections_CJKDistinctIDs verifies that CJK headings produce
// distinct, non-empty section IDs (regression for slugify dropping CJK).
func TestParseSpecSections_CJKDistinctIDs(t *testing.T) {
	spec := `## 用户认证
登录与令牌。

## 授权管理
角色与权限。`

	sections := ParseSpecSections(spec)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].ID == "" || sections[1].ID == "" {
		t.Fatalf("expected non-empty IDs, got %q and %q", sections[0].ID, sections[1].ID)
	}
	if sections[0].ID == sections[1].ID {
		t.Errorf("expected distinct IDs for distinct CJK titles, both = %q", sections[0].ID)
	}
}

// --- canDowngrade tests ---

func TestCanDowngrade(t *testing.T) {
	tests := []struct {
		from, to Rating
		want     bool
	}{
		{RatingPass, RatingConcerns, true},
		{RatingPass, RatingFail, true},
		{RatingConcerns, RatingFail, true},
		{RatingConcerns, RatingPass, false},
		{RatingFail, RatingConcerns, false},
		{RatingFail, RatingPass, false},
		{RatingPass, RatingPass, false},
		{RatingFail, RatingFail, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			if got := canDowngrade(tt.from, tt.to); got != tt.want {
				t.Errorf("canDowngrade(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

// --- normalizeRating tests ---

func TestNormalizeRating(t *testing.T) {
	tests := []struct {
		input string
		want  Rating
	}{
		{"pass", RatingPass},
		{"PASS", RatingPass},
		{" Pass ", RatingPass},
		{"concerns", RatingConcerns},
		{"fail", RatingFail},
		{"unknown", RatingConcerns},
		{"", RatingConcerns},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeRating(tt.input); got != tt.want {
				t.Errorf("normalizeRating(%q) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

// --- extractJSON tests ---

func TestExtractJSON_RawJSON(t *testing.T) {
	input := `{"rating": "pass", "summary": "good"}`
	result := extractJSON(input)
	if result != input {
		t.Errorf("expected same JSON, got %q", result)
	}
}

func TestExtractJSON_FencedJSONBlock(t *testing.T) {
	input := "Here's the review:\n```json\n{\"rating\": \"pass\"}\n```\nDone."
	result := extractJSON(input)
	if result != `{"rating": "pass"}` {
		t.Errorf("expected extracted JSON, got %q", result)
	}
}

func TestExtractJSON_FencedPlainBlock(t *testing.T) {
	input := "Result:\n```\n{\"rating\": \"fail\"}\n```"
	result := extractJSON(input)
	if result != `{"rating": "fail"}` {
		t.Errorf("expected extracted JSON, got %q", result)
	}
}

func TestExtractJSON_JSONInText(t *testing.T) {
	input := `The review result is {"rating": "concerns"} and that's final.`
	result := extractJSON(input)
	if !strings.Contains(result, `"rating": "concerns"`) {
		t.Errorf("expected JSON extracted from text, got %q", result)
	}
}

// --- parsePhase1Response tests ---

func TestParsePhase1Response(t *testing.T) {
	response := `{"rating": "pass", "summary": "Clean code", "issues": []}`
	p1 := parsePhase1Response(response)

	if p1.Rating != RatingPass {
		t.Errorf("expected pass, got %s", p1.Rating)
	}
	if p1.Summary != "Clean code" {
		t.Errorf("expected summary, got %q", p1.Summary)
	}
}

func TestParsePhase1Response_WithIssues(t *testing.T) {
	response := `{"rating": "concerns", "summary": "Some issues", "issues": ["bug", "style"]}`
	p1 := parsePhase1Response(response)

	if p1.Rating != RatingConcerns {
		t.Errorf("expected concerns, got %s", p1.Rating)
	}
	if len(p1.Issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(p1.Issues))
	}
	if p1.Issues[0] != "bug" {
		t.Errorf("expected first issue 'bug', got %q", p1.Issues[0])
	}
}

func TestParsePhase1Response_InvalidJSON(t *testing.T) {
	p1 := parsePhase1Response("not json at all")
	if p1.Rating != RatingConcerns {
		t.Errorf("expected concerns for invalid response, got %s", p1.Rating)
	}
}

// --- parsePhase2Response tests ---

func TestParsePhase2Response_WithCovers(t *testing.T) {
	response := `{
		"rating": "concerns",
		"summary": "Missing tests",
		"issues": ["no test coverage"],
		"covers": [
			{"spec_id": "auth", "status": "covered", "note": "fully implemented"},
			{"spec_id": "testing", "status": "missing", "note": "no tests"}
		],
		"downgraded": true,
		"downgrade_reason": "missing test coverage"
	}`
	p2 := parsePhase2Response(response)

	if p2.Rating != RatingConcerns {
		t.Errorf("expected concerns, got %s", p2.Rating)
	}
	if len(p2.Covers) != 2 {
		t.Fatalf("expected 2 covers, got %d", len(p2.Covers))
	}
	if p2.Covers[0].SpecID != "auth" || p2.Covers[0].Status != CoverCovered {
		t.Errorf("cover 0: expected auth/covered, got %s/%s", p2.Covers[0].SpecID, p2.Covers[0].Status)
	}
	if p2.Covers[1].Status != CoverMissing {
		t.Errorf("cover 1: expected missing, got %s", p2.Covers[1].Status)
	}
	if !p2.Downgraded {
		t.Error("expected downgraded=true")
	}
}

// --- End-to-end Review tests with mock LLM ---

func TestSpecReviewer_Review_TwoPhase(t *testing.T) {
	phase1Response := `{"rating": "pass", "summary": "Clean implementation", "issues": []}`
	phase2Response := `{
		"rating": "concerns",
		"summary": "Missing test coverage for spec section",
		"issues": ["no unit tests"],
		"covers": [
			{"spec_id": "auth", "status": "covered", "note": ""},
			{"spec_id": "testing", "status": "missing", "note": "no tests written"}
		],
		"downgraded": true,
		"downgrade_reason": "missing test coverage"
	}`

	callCount := 0
	llmCall := func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		callCount++
		if callCount == 1 {
			// Phase 1: should NOT contain spec
			if strings.Contains(userPrompt, "Specification") {
				t.Error("Phase 1 should not see the spec")
			}
			return phase1Response, nil
		}
		// Phase 2: should contain spec and Phase 1 assessment
		if !strings.Contains(userPrompt, "Specification") {
			t.Error("Phase 2 should see the spec")
		}
		if !strings.Contains(userPrompt, "Phase 1 Assessment") {
			t.Error("Phase 2 should see Phase 1 assessment")
		}
		return phase2Response, nil
	}

	reviewer := NewSpecReviewer(llmCall)
	diff := "+func Auth() { ... }"
	spec := `## Auth
Implement authentication.

## Testing
Write unit tests.`

	result, err := reviewer.Review(context.Background(), diff, spec)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 LLM calls (one per phase), got %d", callCount)
	}

	// Phase 1
	if result.Phase1.Rating != RatingPass {
		t.Errorf("Phase 1: expected pass, got %s", result.Phase1.Rating)
	}

	// Phase 2: downgraded from pass to concerns
	if result.Phase2.Rating != RatingConcerns {
		t.Errorf("Phase 2: expected concerns, got %s", result.Phase2.Rating)
	}
	if !result.Phase2.Downgraded {
		t.Error("expected Phase 2 to be downgraded")
	}
	if len(result.Phase2.Covers) != 2 {
		t.Errorf("expected 2 covers, got %d", len(result.Phase2.Covers))
	}
}

func TestSpecReviewer_Review_NoDowngrade(t *testing.T) {
	phase1Response := `{"rating": "pass", "summary": "Great code", "issues": []}`
	phase2Response := `{
		"rating": "pass",
		"summary": "All spec sections covered",
		"issues": [],
		"covers": [
			{"spec_id": "auth", "status": "covered", "note": ""}
		],
		"downgraded": false,
		"downgrade_reason": ""
	}`

	callCount := 0
	llmCall := func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return phase1Response, nil
		}
		return phase2Response, nil
	}

	reviewer := NewSpecReviewer(llmCall)
	result, err := reviewer.Review(context.Background(), "diff", "## Auth\nDo auth.")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if result.Phase2.Rating != RatingPass {
		t.Errorf("expected pass (no downgrade), got %s", result.Phase2.Rating)
	}
	if result.Phase2.Downgraded {
		t.Error("should not be downgraded")
	}
}

func TestSpecReviewer_Review_BlocksUpgradeAttempt(t *testing.T) {
	// Phase 1 says "fail", Phase 2 tries to upgrade to "pass" — should be blocked.
	phase1Response := `{"rating": "fail", "summary": "Bugs found", "issues": ["crash"]}`
	phase2Response := `{
		"rating": "pass",
		"summary": "Actually looks good",
		"issues": [],
		"covers": [{"spec_id": "auth", "status": "covered", "note": ""}],
		"downgraded": false,
		"downgrade_reason": ""
	}`

	callCount := 0
	llmCall := func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return phase1Response, nil
		}
		return phase2Response, nil
	}

	reviewer := NewSpecReviewer(llmCall)
	result, err := reviewer.Review(context.Background(), "diff", "## Auth\nDo auth.")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	// Phase 2 rating should be clamped back to Phase 1's "fail"
	if result.Phase2.Rating != RatingFail {
		t.Errorf("expected fail (upgrade blocked), got %s", result.Phase2.Rating)
	}
	if result.Phase2.Downgraded {
		t.Error("should not be marked as downgraded (it was clamped, not downgraded)")
	}
	if !strings.Contains(result.Phase2.DowngradeReason, "upgrade attempt blocked") {
		t.Errorf("expected upgrade-blocked reason, got %q", result.Phase2.DowngradeReason)
	}
}

func TestSpecReviewer_Review_Phase2FailureIsNonFatal(t *testing.T) {
	phase1Response := `{"rating": "pass", "summary": "Good", "issues": []}`

	callCount := 0
	llmCall := func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return phase1Response, nil
		}
		// Phase 2 fails
		return "", context.DeadlineExceeded
	}

	reviewer := NewSpecReviewer(llmCall)
	result, err := reviewer.Review(context.Background(), "diff", "## Auth\nDo auth.")

	// Should not return an error — Phase 2 failure is non-fatal
	if err != nil {
		t.Fatalf("expected no error for Phase 2 failure, got: %v", err)
	}
	if result.Phase1.Rating != RatingPass {
		t.Errorf("Phase 1 should still be pass, got %s", result.Phase1.Rating)
	}
	if result.Phase2.Rating != RatingPass {
		t.Errorf("Phase 2 should fall back to Phase 1 rating, got %s", result.Phase2.Rating)
	}
	if !strings.Contains(result.Phase2.Summary, "Phase 2 review failed") {
		t.Errorf("expected failure note in summary, got %q", result.Phase2.Summary)
	}
}

func TestSpecReviewer_Review_Phase1Failure(t *testing.T) {
	llmCall := func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		return "", context.Canceled
	}

	reviewer := NewSpecReviewer(llmCall)
	_, err := reviewer.Review(context.Background(), "diff", "## Auth\nDo auth.")
	if err == nil {
		t.Fatal("expected error for Phase 1 failure")
	}
	if !strings.Contains(err.Error(), "phase 1") {
		t.Errorf("expected phase 1 error, got: %v", err)
	}
}

func TestSpecReviewer_Review_AllowsDowngrade(t *testing.T) {
	// pass → fail is a valid downgrade
	phase1Response := `{"rating": "pass", "summary": "Clean", "issues": []}`
	phase2Response := `{
		"rating": "fail",
		"summary": "Critical spec requirement missing",
		"issues": ["auth not implemented"],
		"covers": [{"spec_id": "auth", "status": "missing", "note": ""}],
		"downgraded": true,
		"downgrade_reason": "auth entirely missing"
	}`

	callCount := 0
	llmCall := func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return phase1Response, nil
		}
		return phase2Response, nil
	}

	reviewer := NewSpecReviewer(llmCall)
	result, err := reviewer.Review(context.Background(), "diff", "## Auth\nDo auth.")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if result.Phase2.Rating != RatingFail {
		t.Errorf("expected fail (downgraded from pass), got %s", result.Phase2.Rating)
	}
	if !result.Phase2.Downgraded {
		t.Error("expected downgraded=true")
	}
}

// TestSpecReviewer_Review_DowngradedTrueSameRatingReset verifies that when the
// LLM reports "downgraded": true but the rating is actually unchanged, the
// Downgraded field is reset to false (and the reason cleared).
func TestSpecReviewer_Review_DowngradedTrueSameRatingReset(t *testing.T) {
	phase1Response := `{"rating": "pass", "summary": "Clean", "issues": []}`
	// LLM claims downgraded=true with a reason, but the rating is still "pass".
	phase2Response := `{
		"rating": "pass",
		"summary": "All covered",
		"issues": [],
		"covers": [{"spec_id": "auth", "status": "covered", "note": ""}],
		"downgraded": true,
		"downgrade_reason": "bogus reason"
	}`

	callCount := 0
	llmCall := func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return phase1Response, nil
		}
		return phase2Response, nil
	}

	reviewer := NewSpecReviewer(llmCall)
	result, err := reviewer.Review(context.Background(), "diff", "## Auth\nDo auth.")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if result.Phase2.Rating != RatingPass {
		t.Errorf("expected pass, got %s", result.Phase2.Rating)
	}
	if result.Phase2.Downgraded {
		t.Error("expected Downgraded to be reset to false when rating unchanged")
	}
	if result.Phase2.DowngradeReason != "" {
		t.Errorf("expected empty downgrade reason, got %q", result.Phase2.DowngradeReason)
	}
}

// TestSpecReviewer_Review_DowngradedFalseSameRatingStaysFalse verifies that
// when downgraded=false and the rating is unchanged, Downgraded stays false.
func TestSpecReviewer_Review_DowngradedFalseSameRatingStaysFalse(t *testing.T) {
	phase1Response := `{"rating": "concerns", "summary": "Some issues", "issues": ["x"]}`
	phase2Response := `{
		"rating": "concerns",
		"summary": "Still some issues",
		"issues": ["x"],
		"covers": [{"spec_id": "auth", "status": "partial", "note": ""}],
		"downgraded": false,
		"downgrade_reason": ""
	}`

	callCount := 0
	llmCall := func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		callCount++
		if callCount == 1 {
			return phase1Response, nil
		}
		return phase2Response, nil
	}

	reviewer := NewSpecReviewer(llmCall)
	result, err := reviewer.Review(context.Background(), "diff", "## Auth\nDo auth.")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if result.Phase2.Rating != RatingConcerns {
		t.Errorf("expected concerns, got %s", result.Phase2.Rating)
	}
	if result.Phase2.Downgraded {
		t.Error("expected Downgraded to stay false when rating unchanged")
	}
	if result.Phase2.DowngradeReason != "" {
		t.Errorf("expected empty downgrade reason, got %q", result.Phase2.DowngradeReason)
	}
}

// --- JSON round-trip test ---

func TestSpecReviewResult_JSONRoundTrip(t *testing.T) {
	result := &SpecReviewResult{
		Phase1: Phase1Assessment{
			Rating:  RatingPass,
			Summary: "Clean code",
			Issues:  []string{"minor style"},
		},
		Phase2: Phase2Assessment{
			Rating:  RatingConcerns,
			Summary: "Missing tests",
			Issues:  []string{"no tests"},
			Covers: []CoverAnnotation{
				{SpecID: "auth", Status: CoverCovered},
				{SpecID: "testing", Status: CoverMissing, Note: "no tests"},
			},
			Downgraded:      true,
			DowngradeReason: "missing test coverage",
		},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded SpecReviewResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Phase1.Rating != RatingPass {
		t.Errorf("Phase1 rating: expected pass, got %s", decoded.Phase1.Rating)
	}
	if decoded.Phase2.Rating != RatingConcerns {
		t.Errorf("Phase2 rating: expected concerns, got %s", decoded.Phase2.Rating)
	}
	if !decoded.Phase2.Downgraded {
		t.Error("expected downgraded=true")
	}
	if len(decoded.Phase2.Covers) != 2 {
		t.Errorf("expected 2 covers, got %d", len(decoded.Phase2.Covers))
	}
}

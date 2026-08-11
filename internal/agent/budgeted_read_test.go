package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadBudgetedSectionAware_NoBudget(t *testing.T) {
	in := "## Title\nbody line\n"
	// budget <= 0 disables budgeting.
	if got := ReadBudgetedSectionAware(in, 0); got != in {
		t.Errorf("budget 0 should return content as-is, got %q", got)
	}
	// content already under budget returns as-is.
	if got := ReadBudgetedSectionAware(in, 1000); got != in {
		t.Errorf("under-budget content should return as-is, got %q", got)
	}
}

func TestReadBudgetedSectionAware_KeepsHeadersAndItalic(t *testing.T) {
	// A large body that exceeds budget, but headers + italic + index must survive.
	body := strings.Repeat("this is a long body paragraph that will be trimmed. ", 200)
	in := "## Section A\n" +
		"_update this when X changes_\n" +
		"- item one\n" +
		"- item two\n" +
		body + "\n" +
		"## Section B\n" +
		"_another instruction_\n" +
		body + "\n"

	// Tiny budget forces skeleton mode.
	got := ReadBudgetedSectionAware(in, 30)
	if !strings.Contains(got, "## Section A") {
		t.Errorf("header A missing from skeleton: %q", got)
	}
	if !strings.Contains(got, "## Section B") {
		t.Errorf("header B missing from skeleton: %q", got)
	}
	if !strings.Contains(got, "_update this when X changes_") {
		t.Errorf("italic instruction missing from skeleton: %q", got)
	}
	if !strings.Contains(got, "- item one") || !strings.Contains(got, "- item two") {
		t.Errorf("index lines missing from skeleton: %q", got)
	}
	// Body should have been dropped.
	if strings.Contains(got, "this is a long body paragraph") {
		t.Errorf("body should be dropped in skeleton mode: %q", got)
	}
}

func TestReadBudgetedSectionAware_PreservesBodyWhenFits(t *testing.T) {
	in := "## Title\nshort body\n"
	// budget large enough to keep everything.
	got := ReadBudgetedSectionAware(in, 1000)
	if got != in {
		t.Errorf("expected content preserved, got %q", got)
	}
}

func TestReadBudgetedSectionAware_TruncatesBodyProportionally(t *testing.T) {
	bodyA := strings.Repeat("alpha body line.\n", 50)
	bodyB := strings.Repeat("beta body line.\n", 50)
	in := "## A\n" + bodyA + "## B\n" + bodyB

	// Budget enough for skeleton + some body, but not all.
	got := ReadBudgetedSectionAware(in, 200)
	if !strings.Contains(got, "## A") || !strings.Contains(got, "## B") {
		t.Errorf("headers must survive: %q", got)
	}
	if !strings.Contains(got, "…[truncated") {
		t.Errorf("expected a truncation marker, got: …%q", got[len(got)-80:])
	}
	// Should retain at least some body from both sections.
	if !strings.Contains(got, "alpha body line") {
		t.Errorf("expected some alpha body retained")
	}
}

func TestReadBudgeted_Simple(t *testing.T) {
	in := strings.Repeat("line of text\n", 100)
	got := ReadBudgeted(in, 50)
	if !strings.Contains(got, "…[truncated") {
		t.Errorf("expected truncation marker")
	}
	// Should end at a line boundary (before the marker).
	if !strings.HasSuffix(strings.TrimSuffix(got, budgetedReadTruncMarker), "\n") {
		t.Errorf("expected to end at line boundary")
	}
}

func TestParseSections_Preamble(t *testing.T) {
	in := "preamble line\n## H1\nbody1\n## H2\nbody2\n"
	secs := parseSections(in)
	if len(secs) != 3 {
		t.Fatalf("expected 3 sections (preamble + 2), got %d", len(secs))
	}
	if secs[0].header != "" {
		t.Errorf("first section should be preamble (empty header), got %q", secs[0].header)
	}
	if secs[1].header != "## H1" {
		t.Errorf("second section header = %q", secs[1].header)
	}
	if secs[2].header != "## H2" {
		t.Errorf("third section header = %q", secs[2].header)
	}
}

// TestTrimToTokens_CJKSingleLineValidUTF8 ensures that trimming a single very
// long line of CJK characters (which are 3 bytes per rune in UTF-8) does not
// split a multi-byte sequence and produce invalid UTF-8. This covers the
// fallback branch of trimToTokens where no newline is present.
func TestTrimToTokens_CJKSingleLineValidUTF8(t *testing.T) {
	// "中" is 3 bytes in UTF-8. 1000 runes => 3000 bytes, single line (no \n).
	// A budget of 100 tokens => maxChars = 400 bytes; 400 % 3 == 1, so the raw
	// byte cut lands in the middle of a rune and must be walked back.
	const runeStr = "中"
	in := strings.Repeat(runeStr, 1000)
	if !utf8.ValidString(in) {
		t.Fatalf("input must be valid UTF-8")
	}

	got := trimToTokens(in, 100)

	if !utf8.ValidString(got) {
		t.Errorf("trimToTokens produced invalid UTF-8: %q (len=%d)", got, len(got))
	}
	// Must be a proper prefix of the input (no corruption / dropped runes).
	if !strings.HasPrefix(in, got) {
		t.Errorf("trimToTokens result is not a prefix of the input: %q", got)
	}
	// Must have been truncated.
	if len(got) >= len(in) {
		t.Errorf("expected truncation, got full content (len=%d)", len(got))
	}
	// The result length should be a multiple of the rune width (no partial rune).
	if len(got)%len(runeStr) != 0 {
		t.Errorf("result length %d is not a multiple of rune width %d", len(got), len(runeStr))
	}
}

// TestTrimToTokens_CJKViaReadBudgeted checks the public ReadBudgeted path also
// yields valid UTF-8 (prefix + truncation marker) for CJK single-line content.
func TestTrimToTokens_CJKViaReadBudgeted(t *testing.T) {
	in := strings.Repeat("你好世界", 500) // 4 runes * 3 bytes * 500 = 6000 bytes, no newline
	got := ReadBudgeted(in, 100)

	if !utf8.ValidString(got) {
		t.Errorf("ReadBudgeted produced invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, budgetedReadTruncMarker) {
		t.Errorf("expected truncation marker in result")
	}
	// The portion before the marker must be a valid prefix of the input.
	body := strings.TrimSuffix(got, budgetedReadTruncMarker)
	if !strings.HasPrefix(in, body) {
		t.Errorf("ReadBudgeted body is not a prefix of the input: %q", body)
	}
}

// TestTrimToTokens_ASCIISingleLine verifies that the single-line fallback still
// works correctly for pure ASCII content (where every byte is a rune start).
func TestTrimToTokens_ASCIISingleLine(t *testing.T) {
	in := strings.Repeat("a", 1000) // single long line, no newline
	got := trimToTokens(in, 100)    // budget 100 => maxChars 400 bytes

	want := strings.Repeat("a", 400)
	if got != want {
		t.Errorf("ASCII trim mismatch: got len=%d, want len=%d", len(got), len(want))
	}
	if !utf8.ValidString(got) {
		t.Errorf("result should be valid UTF-8")
	}
}

// TestTrimToTokens_MultilineStillCutsAtNewline confirms the newline-aware
// branch still cuts at a line boundary (regression guard for the UTF-8 fix,
// which only touches the no-newline fallback).
func TestTrimToTokens_MultilineStillCutsAtNewline(t *testing.T) {
	in := strings.Repeat("line of text\n", 100)
	got := trimToTokens(in, 50) // budget 50 => maxChars 200 bytes
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("expected result to end at a line boundary (newline), got suffix %q", got[len(got)-20:])
	}
	if !strings.HasPrefix(in, got) {
		t.Errorf("result should be a prefix of the input")
	}
}

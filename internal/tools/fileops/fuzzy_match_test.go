package fileops

import (
	"strings"
	"testing"
)

// --- levenshtein ---------------------------------------------------------

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"gumbo", "gambol", 2},
		// rune-level: 中文 each counted as 1 rune
		{"你好", "你好吗", 1},
		{"你好", "世界", 2},
	}
	for _, c := range cases {
		got := levenshtein(c.a, c.b)
		if got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestLevenshteinSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"abc", "abc", 1.0},
		{"", "", 1.0},
		{"abc", "xyz", 0.0},
		{"kitten", "sitting", 4.0 / 7.0}, // distance 3, maxLen 7
	}
	for _, c := range cases {
		got := levenshteinSimilarity(c.a, c.b)
		if absFloat(got-c.want) > 1e-9 {
			t.Errorf("levenshteinSimilarity(%q, %q) = %f, want %f", c.a, c.b, got, c.want)
		}
	}
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// --- findUniqueIndex -----------------------------------------------------

func TestFindUniqueIndex(t *testing.T) {
	// Unique occurrence
	idx, found, amb := findUniqueIndex("hello world", "world")
	if !found || amb || idx != 6 {
		t.Errorf("unique: got idx=%d found=%v amb=%v, want 6/true/false", idx, found, amb)
	}

	// Multiple occurrences → ambiguous
	idx, found, amb = findUniqueIndex("foo bar foo", "foo")
	if !found || !amb || idx != 0 {
		t.Errorf("ambiguous: got idx=%d found=%v amb=%v, want 0/true/true", idx, found, amb)
	}

	// Not found
	idx, found, amb = findUniqueIndex("hello", "world")
	if found || amb || idx != -1 {
		t.Errorf("not found: got idx=%d found=%v amb=%v, want -1/false/false", idx, found, amb)
	}
}

// --- fuzzyFindIndex: exact match -----------------------------------------

func TestFuzzyFindIndex_ExactMatch(t *testing.T) {
	content := "package main\n\nfunc foo() {}\n"
	start, end, found, usedFuzzy, amb := fuzzyFindIndex(content, "func foo() {}")
	if !found || usedFuzzy || amb {
		t.Errorf("got found=%v fuzzy=%v amb=%v, want true/false/false", found, usedFuzzy, amb)
	}
	if !strings.Contains(content[start:end], "func foo() {}") {
		t.Errorf("slice [%d:%d] = %q does not contain match", start, end, content[start:end])
	}
}

func TestFuzzyFindIndex_ExactMatchAmbiguous(t *testing.T) {
	content := "foo\nbar\nfoo\n"
	_, _, found, _, amb := fuzzyFindIndex(content, "foo")
	if found {
		t.Error("expected found=false when ambiguous at exact level")
	}
	if !amb {
		t.Error("expected ambiguous=true for duplicate exact match")
	}
}

// --- fuzzyFindIndex: normalized match ------------------------------------

func TestFuzzyFindIndex_NormalizedSmartQuote(t *testing.T) {
	// content uses ASCII quotes, oldText uses smart quotes — after normalization they match
	content := "func main() {\n\tmsg := \"hello\"\n}\n"
	oldText := "func main() {\n\tmsg := \u201Chello\u201D\n}\n"
	start, end, found, usedFuzzy, amb := fuzzyFindIndex(content, oldText)
	if !found {
		t.Fatal("expected normalized match to succeed")
	}
	if !usedFuzzy {
		t.Error("expected usedFuzzy=true for normalized match")
	}
	if amb {
		t.Error("expected ambiguous=false")
	}
	got := content[start:end]
	if !strings.Contains(got, "hello") {
		t.Errorf("matched slice %q should contain 'hello'", got)
	}
}

func TestFuzzyFindIndex_StripsANSI(t *testing.T) {
	content := "status: ready\n"
	oldText := "\x1b[32mstatus:\x1b[0m ready\n"
	start, end, found, usedFuzzy, ambiguous := fuzzyFindIndex(content, oldText)
	if !found || !usedFuzzy || ambiguous {
		t.Fatalf("got found=%v fuzzy=%v ambiguous=%v", found, usedFuzzy, ambiguous)
	}
	if content[start:end] != "status: ready" {
		t.Fatalf("matched %q", content[start:end])
	}
}

func TestNormalizeForFuzzyMatchSanitizesControlCharacters(t *testing.T) {
	got := normalizeForFuzzyMatch("ready\x00")
	if got != "ready\uFFFD" {
		t.Fatalf("normalized = %q", got)
	}
}

func TestFuzzyFindIndex_NormalizedAmbiguous(t *testing.T) {
	// Two identical lines with smart quotes — should be ambiguous at normalized level
	content := "msg := \u201Chello\u201D\nmsg := \u201Chello\u201D\n"
	oldText := "msg := \"hello\"\n"
	_, _, found, _, amb := fuzzyFindIndex(content, oldText)
	if found {
		t.Error("expected found=false when ambiguous at normalized level")
	}
	if !amb {
		t.Error("expected ambiguous=true for duplicate normalized match")
	}
}

// --- fuzzyFindIndex: BlockAnchor + Levenshtein ---------------------------

func TestFuzzyFindIndex_BlockAnchorLevenshtein(t *testing.T) {
	// oldText has a typo in the middle line ("quck" vs "quick") which prevents
	// exact and normalized matching. BlockAnchor uses the first/last lines as
	// anchors and Levenshtein tolerates the middle-line difference.
	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	oldText := "func main() {\n\tfmt.Println(\"hello\")\n}\n"
	// Introduce a character-level difference that normalization can't fix:
	content = strings.Replace(content, "Println", "Prntln", 1)
	// oldText keeps the correct spelling; anchors (first/last line) still match
	start, end, found, usedFuzzy, amb := fuzzyFindIndex(content, oldText)
	if !found {
		t.Fatal("expected BlockAnchor+Levenshtein match to succeed")
	}
	if !usedFuzzy {
		t.Error("expected usedFuzzy=true for BlockAnchor match")
	}
	if amb {
		t.Error("expected ambiguous=false for unique anchor match")
	}
	matched := content[start:end]
	if !strings.Contains(matched, "Prntln") {
		t.Errorf("matched slice %q should contain 'Prntln'", matched)
	}
}

func TestFuzzyFindIndex_BlockAnchorAmbiguous(t *testing.T) {
	// Two functions with the same signature — anchors match both, both pass
	// the 0.3 threshold → ambiguous.
	content := "func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 1\n}\n"
	oldText := "func a() {\n\treturn 1\n}\n"
	// Note: after normalization both blocks are identical to oldText, so this
	// is caught at the normalized level first. To isolate BlockAnchor we need
	// a case where normalized matching fails but anchors match.
	// Use a middle-line difference that normalization can't fix:
	content = "func x() {\n\treturn 1\n}\n\nfunc y() {\n\treturn 2\n}\n"
	oldText = "func x() {\n\treturn 9\n}\n"
	// firstAnchor = "func x() {", lastAnchor = "}", expectedSpan = 2
	// Only one candidate (the func x block), so threshold=0.0, should match.
	_, _, found, _, amb := fuzzyFindIndex(content, oldText)
	if !found {
		t.Error("expected single-candidate BlockAnchor to match")
	}
	if amb {
		t.Error("expected ambiguous=false for single candidate")
	}
}

func TestFuzzyFindIndex_BlockAnchorMultipleCandidatesAmbiguous(t *testing.T) {
	// Two blocks with identical first/last anchors but different middle content
	// that differs enough to pass the 0.3 threshold → ambiguous.
	content := "func handler() {\n\tlog.Println(\"a\")\n\treturn nil\n}\n\n" +
		"func handler() {\n\tlog.Println(\"b\")\n\treturn nil\n}\n"
	// oldText differs from both in the middle line
	oldText := "func handler() {\n\tlog.Println(\"x\")\n\treturn nil\n}\n"
	_, _, found, _, amb := fuzzyFindIndex(content, oldText)
	if found {
		t.Error("expected found=false when multiple candidates pass threshold")
	}
	if !amb {
		t.Error("expected ambiguous=true for multiple passing candidates")
	}
}

// --- fuzzyFindBlockAnchor: line offset correctness -----------------------

func TestFuzzyFindBlockAnchor_ByteOffsets(t *testing.T) {
	content := "header\nfunc main() {\n\tbody()\n}\nfooter\n"
	oldText := "func main() {\n\tbody()\n}\n"
	start, end, found, amb := fuzzyFindBlockAnchor(content, oldText)
	if !found || amb {
		t.Fatalf("expected found=true amb=false, got found=%v amb=%v", found, amb)
	}
	// start should be at byte offset of "func main()"
	wantStart := strings.Index(content, "func main()")
	if start != wantStart {
		t.Errorf("start = %d, want %d", start, wantStart)
	}
	// end should be just after the "}" line, i.e., at "footer"
	wantEnd := strings.Index(content, "footer")
	if end != wantEnd {
		t.Errorf("end = %d, want %d (footer at %d)", end, wantEnd, wantEnd)
	}
}

// --- lineStartByteOffset -------------------------------------------------

func TestLineStartByteOffset(t *testing.T) {
	lines := []string{"a", "b", "c"}
	// content = "a\nb\nc"
	if got := lineStartByteOffset(lines, 0); got != 0 {
		t.Errorf("offset(0) = %d, want 0", got)
	}
	if got := lineStartByteOffset(lines, 1); got != 2 {
		t.Errorf("offset(1) = %d, want 2", got)
	}
	if got := lineStartByteOffset(lines, 2); got != 4 {
		t.Errorf("offset(2) = %d, want 4", got)
	}
	if got := lineStartByteOffset(lines, 3); got != 5 {
		t.Errorf("offset(3) = %d, want 5 (len of content)", got)
	}
}

// --- matchAllEdits: ambiguous error --------------------------------------

func TestMatchAllEdits_AmbiguousError(t *testing.T) {
	content := "foo\nbar\nfoo\n"
	edits := []editEntry{{OldText: "foo", NewText: "baz"}}
	matches, err := matchAllEdits(content, edits)
	if err == nil {
		t.Fatalf("expected error for ambiguous match, got matches=%v", matches)
	}
	if !strings.Contains(err.Error(), "multiple locations") {
		t.Errorf("error %q should mention 'multiple locations'", err.Error())
	}
}

func TestMatchAllEdits_NotFound(t *testing.T) {
	content := "foo\nbar\n"
	edits := []editEntry{{OldText: "baz", NewText: "qux"}}
	_, err := matchAllEdits(content, edits)
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q should mention 'not found'", err.Error())
	}
}

func TestMatchAllEdits_ExactMatch(t *testing.T) {
	content := "package main\n\nfunc foo() {}\n"
	edits := []editEntry{{OldText: "func foo() {}", NewText: "func bar() {}"}}
	matches, err := matchAllEdits(content, edits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0]
	if m.FuzzyMatch {
		t.Error("expected FuzzyMatch=false for exact match")
	}
	if content[m.StartIdx:m.EndIdx] != "func foo() {}" {
		t.Errorf("slice [%d:%d] = %q, want 'func foo() {}'", m.StartIdx, m.EndIdx, content[m.StartIdx:m.EndIdx])
	}
}

// --- applyEdits: uses pre-computed offsets -------------------------------

func TestApplyEdits_MultipleNonOverlapping(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	matches := []editMatch{
		{StartIdx: 0, EndIdx: 5, OldText: "alpha", NewText: "ALPHA"},
		{StartIdx: 6, EndIdx: 10, OldText: "beta", NewText: "BETA"},
		{StartIdx: 11, EndIdx: 16, OldText: "gamma", NewText: "GAMMA"},
	}
	got := applyEdits(content, matches)
	want := "ALPHA\nBETA\nGAMMA\n"
	if got != want {
		t.Errorf("applyEdits = %q, want %q", got, want)
	}
}

package compression

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/covoyage/covonaut/agentcore"
)

// fakeInnerEngine is a minimal ContextEngine that emits a single compaction
// summary message so we can test EnhancedContextEngine's enhancement.
type fakeInnerEngine struct{}

func (fakeInnerEngine) Name() string                                  { return "fake" }
func (fakeInnerEngine) OnSessionStart(context.Context, string, int64) {}
func (fakeInnerEngine) OnSessionReset()                               {}
func (fakeInnerEngine) OnSessionEnd()                                 {}
func (fakeInnerEngine) UpdateFromResponse(agentcore.TokenUsage)       {}
func (fakeInnerEngine) ShouldCompact([]agentcore.Message, []agentcore.ToolDefinition, int64) bool {
	return true
}
func (fakeInnerEngine) GetToolSchemas() []agentcore.ToolDefinition { return nil }
func (fakeInnerEngine) ContextLength() int64                       { return 100000 }
func (fakeInnerEngine) ThresholdTokens() int64                     { return 80000 }
func (fakeInnerEngine) CompressionCount() int64                    { return 0 }
func (fakeInnerEngine) LastSavingsPct() float64                    { return 0 }
func (fakeInnerEngine) CheckFeasibility(int64) string              { return "" }
func (fakeInnerEngine) Compress(_ context.Context, _ []agentcore.Message, _ string) ([]agentcore.Message, int64, error) {
	return []agentcore.Message{
		{Role: agentcore.RoleSystem, Type: agentcore.MessageTypeCompactionSummary, Content: "summary body"},
	}, 1, nil
}

// mockContextEngine is a minimal ContextEngine implementation for testing
// EnhancedContextEngine without depending on the real compaction engine.
type mockContextEngine struct {
	contextLength    int64
	thresholdTokens  int64
	compressionCount int64
	lastSavingsPct   float64
	shouldCompact    bool
	toolDefs         []agentcore.ToolDefinition
	compressFn       func(ctx context.Context, msgs []agentcore.Message, focusTopic string) ([]agentcore.Message, int64, error)
}

func (m *mockContextEngine) Name() string { return "mock" }
func (m *mockContextEngine) OnSessionStart(_ context.Context, _ string, _ int64) {}
func (m *mockContextEngine) OnSessionReset()                                     {}
func (m *mockContextEngine) OnSessionEnd()                                       {}
func (m *mockContextEngine) UpdateFromResponse(_ agentcore.TokenUsage)           {}
func (m *mockContextEngine) ShouldCompact(msgs []agentcore.Message, _ []agentcore.ToolDefinition, _ int64) bool {
	return m.shouldCompact
}
func (m *mockContextEngine) Compress(ctx context.Context, msgs []agentcore.Message, focusTopic string) ([]agentcore.Message, int64, error) {
	if m.compressFn != nil {
		return m.compressFn(ctx, msgs, focusTopic)
	}
	// Default: replace all messages with a single compaction summary.
	summary := agentcore.Message{
		Type:    agentcore.MessageTypeCompactionSummary,
		Role:    agentcore.RoleUser,
		Content: "Summary of earlier conversation.",
	}
	recent := msgs[len(msgs)-2:]
	result := append([]agentcore.Message{summary}, recent...)
	return result, int64(len(msgs) - 2), nil
}
func (m *mockContextEngine) GetToolSchemas() []agentcore.ToolDefinition { return m.toolDefs }
func (m *mockContextEngine) ContextLength() int64                       { return m.contextLength }
func (m *mockContextEngine) ThresholdTokens() int64                     { return m.thresholdTokens }
func (m *mockContextEngine) CompressionCount() int64                    { return m.compressionCount }
func (m *mockContextEngine) LastSavingsPct() float64                    { return m.lastSavingsPct }
func (m *mockContextEngine) CheckFeasibility(_ int64) string            { return "" }

func makeToolResult(id, content string) agentcore.Message {
	return agentcore.Message{
		Role:       agentcore.RoleTool,
		ToolCallID: id,
		Content:    content,
	}
}

func makeAssistantWithToolCall(id, name, args string) agentcore.Message {
	return agentcore.Message{
		Role: agentcore.RoleAssistant,
		ToolCalls: []agentcore.ToolCall{
			{ID: id, Name: name, Arguments: args},
		},
		Content: "",
	}
}

func makeUserMsg(content string) agentcore.Message {
	return agentcore.Message{Role: agentcore.RoleUser, Content: content}
}

// --- Carried state tests (existing behavior) ---

func TestEnhancedContextEngineCarriesState(t *testing.T) {
	eng := NewEnhancedContextEngine(fakeInnerEngine{})
	eng.SetStateProvider(func() string {
		return "Active goal: ship the feature\nUnfinished todos:\n  - [pending] write docs"
	})

	out, cut, err := eng.Compress(context.Background(), []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "do the thing"},
	}, "")
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if cut != 1 {
		t.Fatalf("expected cut=1, got %d", cut)
	}
	var summary string
	for _, m := range out {
		if m.Type == agentcore.MessageTypeCompactionSummary {
			summary = m.Content
		}
	}
	if summary == "" {
		t.Fatal("no compaction summary in output")
	}
	if !strings.Contains(summary, "## Carried State") {
		t.Errorf("summary missing Carried State section: %q", summary)
	}
	if !strings.Contains(summary, "ship the feature") || !strings.Contains(summary, "write docs") {
		t.Errorf("summary missing carried goal/todos: %q", summary)
	}
}

func TestEnhancedContextEngineNoStateProvider(t *testing.T) {
	eng := NewEnhancedContextEngine(fakeInnerEngine{})
	// No state provider set — must not panic and must not add the section.
	out, _, err := eng.Compress(context.Background(), nil, "topic")
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	for _, m := range out {
		if m.Type == agentcore.MessageTypeCompactionSummary && strings.Contains(m.Content, "## Carried State") {
			t.Errorf("unexpected Carried State section without provider: %q", m.Content)
		}
	}
}

// --- MicroCompact tests ---

func TestMicroCompact_ReplacesLargeToolResults(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{contextLength: 128000})

	largeResult := strings.Repeat("x", 5000)
	msgs := []agentcore.Message{
		makeUserMsg("read the file"),
		makeAssistantWithToolCall("call1", "read", `{"path":"test.go"}`),
		makeToolResult("call1", largeResult),
	}

	result, saved := engine.MicroCompact(msgs)

	// The tool result should be truncated.
	toolResult := result[2]
	if toolResult.Content == largeResult {
		t.Error("expected tool result to be truncated, got original")
	}
	if !strings.Contains(toolResult.Content, "[tool result truncated") {
		t.Errorf("expected truncation placeholder, got: %s", toolResult.Content[:min(100, len(toolResult.Content))])
	}
	if saved <= 0 {
		t.Errorf("expected positive chars saved, got %d", saved)
	}

	// The tool_use message should be unchanged.
	if len(result[1].ToolCalls) != 1 {
		t.Errorf("expected tool_use to be preserved, got %d tool calls", len(result[1].ToolCalls))
	}
	if result[1].ToolCalls[0].Name != "read" {
		t.Errorf("expected tool name 'read', got %q", result[1].ToolCalls[0].Name)
	}
}

func TestMicroCompact_KeepsSmallToolResults(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{contextLength: 128000})

	smallResult := "file content is short"
	msgs := []agentcore.Message{
		makeAssistantWithToolCall("call1", "read", `{}`),
		makeToolResult("call1", smallResult),
	}

	result, saved := engine.MicroCompact(msgs)

	// Small result should be unchanged.
	if result[1].Content != smallResult {
		t.Errorf("expected small tool result to be unchanged, got: %s", result[1].Content)
	}
	if saved != 0 {
		t.Errorf("expected 0 chars saved for small results, got %d", saved)
	}
}

func TestMicroCompact_PreservesHeadContent(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{contextLength: 128000})

	// Create a result with a distinctive prefix.
	largeResult := "UNIQUE_PREFIX_DATA\n" + strings.Repeat("body", 1000)
	msgs := []agentcore.Message{
		makeToolResult("call1", largeResult),
	}

	result, _ := engine.MicroCompact(msgs)

	// The head should contain the prefix.
	if !strings.Contains(result[0].Content, "UNIQUE_PREFIX_DATA") {
		t.Errorf("expected head content to be preserved, got: %s", result[0].Content[:min(100, len(result[0].Content))])
	}
}

func TestMicroCompact_DoesNotModifyOriginal(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{contextLength: 128000})

	largeResult := strings.Repeat("x", 5000)
	msgs := []agentcore.Message{
		makeToolResult("call1", largeResult),
	}

	_, _ = engine.MicroCompact(msgs)

	// Original should be unchanged.
	if msgs[0].Content != largeResult {
		t.Error("expected original message list to be unmodified")
	}
}

func TestMicroCompact_MultipleToolResults(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{contextLength: 128000})

	msgs := []agentcore.Message{
		makeToolResult("call1", strings.Repeat("a", 3000)),
		makeToolResult("call2", "small"),
		makeToolResult("call3", strings.Repeat("b", 4000)),
	}

	result, saved := engine.MicroCompact(msgs)

	// call1 and call3 should be truncated, call2 should be unchanged.
	if result[0].Content == strings.Repeat("a", 3000) {
		t.Error("expected call1 to be truncated")
	}
	if result[1].Content != "small" {
		t.Errorf("expected call2 to be unchanged, got: %s", result[1].Content)
	}
	if result[2].Content == strings.Repeat("b", 4000) {
		t.Error("expected call3 to be truncated")
	}
	if saved <= 0 {
		t.Errorf("expected positive chars saved, got %d", saved)
	}
}

// TestMicroCompact_CJKValidUTF8 verifies that rune-aware truncation does not
// produce invalid UTF-8 when tool result content contains multi-byte CJK
// characters. Previously, byte-level slicing (content[:microCompactKeepHead])
// could split a multi-byte rune mid-sequence.
func TestMicroCompact_CJKValidUTF8(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{contextLength: 128000})

	// Each Chinese char is 3 bytes in UTF-8. 2000 chars = 6000 bytes, well
	// above microCompactThreshold(500). microCompactKeepHead=200 runes.
	largeResult := strings.Repeat("你好世界", 500) // 2000 runes, 6000 bytes
	msgs := []agentcore.Message{
		makeToolResult("call1", largeResult),
	}

	result, saved := engine.MicroCompact(msgs)
	if saved <= 0 {
		t.Fatalf("expected positive chars saved, got %d", saved)
	}
	// The truncated content must be valid UTF-8.
	if !utf8.ValidString(result[0].Content) {
		t.Errorf("truncated tool result is not valid UTF-8: %q", result[0].Content)
	}
	// Should contain the placeholder.
	if !strings.Contains(result[0].Content, "[tool result truncated") {
		t.Errorf("expected truncation placeholder, got: %s", result[0].Content[:min(100, len(result[0].Content))])
	}
}

// TestMicroCompact_CJKShortRunesSkipped verifies that CJK content with fewer
// runes than microCompactKeepHead (despite exceeding the byte threshold) is
// NOT truncated — truncating would make the result longer (head + placeholder
// > original), so it should be skipped.
func TestMicroCompact_CJKShortRunesSkipped(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{contextLength: 128000})

	// 180 CJK chars = 540 bytes (> microCompactThreshold=500) but only 180
	// runes (< microCompactKeepHead=200). Rune-aware truncation to 200 runes
	// would not shorten it, so it must be left unchanged.
	content := strings.Repeat("你", 180)
	msgs := []agentcore.Message{
		makeToolResult("call1", content),
	}

	result, saved := engine.MicroCompact(msgs)
	if saved != 0 {
		t.Errorf("expected 0 chars saved for short-rune CJK content, got %d", saved)
	}
	if result[0].Content != content {
		t.Errorf("expected content unchanged, got truncated: %q", result[0].Content)
	}
	if !utf8.ValidString(result[0].Content) {
		t.Errorf("content is not valid UTF-8: %q", result[0].Content)
	}
}

// --- Boundary insertion tests ---

func TestInsertBoundaryAfter_InsertsAtCorrectPosition(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{})

	msgs := []agentcore.Message{
		makeUserMsg("msg1"),
		makeUserMsg("msg2"),
		makeUserMsg("msg3"),
	}

	result := engine.insertBoundaryAfter(msgs, 1, "test")

	if len(result) != 4 {
		t.Fatalf("expected 4 messages (3 + 1 boundary), got %d", len(result))
	}
	if result[1].Content != "msg2" {
		t.Errorf("expected msg2 at index 1, got: %s", result[1].Content)
	}
	if result[2].Content != boundaryMarker {
		t.Errorf("expected boundary marker at index 2, got: %s", result[2].Content)
	}
	if result[3].Content != "msg3" {
		t.Errorf("expected msg3 at index 3, got: %s", result[3].Content)
	}
	if result[2].Metadata["boundary"] != true {
		t.Error("expected boundary metadata to be set")
	}
}

func TestInsertBoundaryAfter_InvalidIndex(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{})

	msgs := []agentcore.Message{makeUserMsg("msg1")}

	// Index out of range should return original.
	result := engine.insertBoundaryAfter(msgs, 5, "test")
	if len(result) != 1 {
		t.Errorf("expected unchanged list for invalid index, got %d messages", len(result))
	}
}

func TestInsertBoundaryAfterSummary(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{})

	msgs := []agentcore.Message{
		{Type: agentcore.MessageTypeCompactionSummary, Role: agentcore.RoleUser, Content: "Summary..."},
		makeUserMsg("recent msg 1"),
		makeUserMsg("recent msg 2"),
	}

	result := engine.insertBoundaryAfterSummary(msgs)

	if len(result) != 4 {
		t.Fatalf("expected 4 messages (3 + 1 boundary), got %d", len(result))
	}
	if result[0].Type != agentcore.MessageTypeCompactionSummary {
		t.Error("expected summary at index 0")
	}
	if result[1].Content != boundaryMarker {
		t.Errorf("expected boundary marker at index 1, got: %s", result[1].Content)
	}
}

func TestInsertBoundaryAfterSummary_NoSummary(t *testing.T) {
	engine := NewEnhancedContextEngine(&mockContextEngine{})

	msgs := []agentcore.Message{
		makeUserMsg("msg1"),
		makeUserMsg("msg2"),
	}

	result := engine.insertBoundaryAfterSummary(msgs)
	if len(result) != 2 {
		t.Errorf("expected unchanged list when no summary, got %d messages", len(result))
	}
}

// --- Compress integration tests ---

func TestCompress_MicroCompactSufficient_SkipsFullCompaction(t *testing.T) {
	mock := &mockContextEngine{
		contextLength: 128000,
		shouldCompact: true, // initially needs compaction
	}
	// After microcompact, ShouldCompact returns false (sufficient).
	mock.shouldCompact = true
	engine := NewEnhancedContextEngine(mock)

	largeResult := strings.Repeat("x", 10000)
	msgs := []agentcore.Message{
		makeUserMsg("read file"),
		makeAssistantWithToolCall("call1", "read", `{}`),
		makeToolResult("call1", largeResult),
		makeUserMsg("now edit it"),
	}

	// Make mock return false for ShouldCompact after microcompact.
	mock.shouldCompact = false

	result, cut, err := engine.Compress(context.Background(), msgs, "test task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Microcompact was sufficient. A non-zero cut is returned as a signal so
	// the caller (agentcore) calls ReplaceMessages with the trimmed list.
	// Returning 0 would cause the trimmed result to be discarded.
	if cut <= 0 {
		t.Errorf("expected cut>0 when microcompact is sufficient (signal to replace), got %d", cut)
	}
	// Tool result should be truncated.
	found := false
	for _, m := range result {
		if m.Role == agentcore.RoleTool && strings.Contains(m.Content, "[tool result truncated") {
			found = true
		}
	}
	if !found {
		t.Error("expected truncated tool result in microcompacted messages")
	}
	// Microcompact is in-place trimming (no summary seam), so NO boundary
	// marker should be inserted — the boundary text "Earlier conversation was
	// compacted above" would be semantically wrong.
	for _, m := range result {
		if m.Content == boundaryMarker {
			t.Errorf("did not expect boundary marker in microcompacted messages, got one: %s", m.Content)
		}
	}
}

// TestCompress_MicroCompactReturnsNonZeroCut verifies that the microcompact
// path returns a non-zero cut so the caller (agentcore) executes
// ReplaceMessages with the trimmed messages. The agentcore contract is:
//   if messagesCut > 0 { a.state.ReplaceMessages(newMsgs) }
// Returning cut=0 would cause the trimmed result to be silently discarded.
func TestCompress_MicroCompactReturnsNonZeroCut(t *testing.T) {
	mock := &mockContextEngine{
		contextLength: 128000,
		shouldCompact: false, // microcompact is sufficient
	}
	engine := NewEnhancedContextEngine(mock)

	largeResult := strings.Repeat("x", 10000)
	msgs := []agentcore.Message{
		makeUserMsg("read file"),
		makeAssistantWithToolCall("call1", "read", `{}`),
		makeToolResult("call1", largeResult),
	}

	_, cut, err := engine.Compress(context.Background(), msgs, "task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cut <= 0 {
		t.Errorf("expected cut>0 from microcompact path (signal for ReplaceMessages), got %d", cut)
	}
}

func TestCompress_FallsBackToFullCompaction(t *testing.T) {
	// When microcompact is not sufficient (ShouldCompact still true after
	// microcompact), fall back to full compaction.
	mock := &mockContextEngine{
		contextLength: 128000,
		shouldCompact: true, // always needs compaction
		compressFn: func(ctx context.Context, msgs []agentcore.Message, focusTopic string) ([]agentcore.Message, int64, error) {
			summary := agentcore.Message{
				Type:    agentcore.MessageTypeCompactionSummary,
				Role:    agentcore.RoleUser,
				Content: "Summary of conversation.",
			}
			recent := msgs[len(msgs)-1:]
			result := append([]agentcore.Message{summary}, recent...)
			return result, int64(len(msgs) - 1), nil
		},
	}
	engine := NewEnhancedContextEngine(mock)

	msgs := []agentcore.Message{
		makeUserMsg("msg1"),
		makeUserMsg("msg2"),
		makeUserMsg("msg3"),
	}

	result, cut, err := engine.Compress(context.Background(), msgs, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cut == 0 {
		t.Error("expected non-zero cut for full compaction")
	}
	// Should have a compaction summary.
	foundSummary := false
	foundBoundary := false
	for _, m := range result {
		if m.Type == agentcore.MessageTypeCompactionSummary {
			foundSummary = true
		}
		if m.Content == boundaryMarker {
			foundBoundary = true
		}
	}
	if !foundSummary {
		t.Error("expected compaction summary in result")
	}
	if !foundBoundary {
		t.Error("expected boundary marker after compaction summary")
	}
}

// TestCompress_FullCompactionUsesMicrocompactedMsgs verifies that when
// microcompact is NOT sufficient (falls back to full compaction), the
// microcompacted (trimmed) messages are passed to the inner engine rather
// than the original untrimmed messages. This ensures trimming work is not
// wasted.
func TestCompress_FullCompactionUsesMicrocompactedMsgs(t *testing.T) {
	var capturedMsgs []agentcore.Message
	mock := &mockContextEngine{
		contextLength: 128000,
		shouldCompact: true, // always needs compaction → forces fallback
		compressFn: func(ctx context.Context, msgs []agentcore.Message, focusTopic string) ([]agentcore.Message, int64, error) {
			// Capture the messages passed to inner.Compress.
			capturedMsgs = make([]agentcore.Message, len(msgs))
			copy(capturedMsgs, msgs)
			summary := agentcore.Message{
				Type:    agentcore.MessageTypeCompactionSummary,
				Role:    agentcore.RoleUser,
				Content: "Summary.",
			}
			return []agentcore.Message{summary}, 1, nil
		},
	}
	engine := NewEnhancedContextEngine(mock)

	largeResult := strings.Repeat("x", 10000)
	msgs := []agentcore.Message{
		makeUserMsg("read file"),
		makeAssistantWithToolCall("call1", "read", `{}`),
		makeToolResult("call1", largeResult),
		makeUserMsg("now edit"),
	}

	_, _, err := engine.Compress(context.Background(), msgs, "task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The messages passed to inner.Compress should have been microcompacted —
	// the large tool result should be truncated, not the original 10000 chars.
	foundTruncated := false
	for _, m := range capturedMsgs {
		if m.Role == agentcore.RoleTool {
			if strings.Contains(m.Content, "[tool result truncated") {
				foundTruncated = true
			}
			if m.Content == largeResult {
				t.Error("inner.Compress received the ORIGINAL untrimmed tool result; expected microcompacted version")
			}
		}
	}
	if !foundTruncated {
		t.Error("expected inner.Compress to receive microcompacted messages with truncated tool results")
	}
}

// --- Enhanced summary tests (existing behavior, verify still works) ---

func TestCompress_EnhancesSummaryWithTask(t *testing.T) {
	mock := &mockContextEngine{
		contextLength: 128000,
		shouldCompact: true,
		compressFn: func(ctx context.Context, msgs []agentcore.Message, focusTopic string) ([]agentcore.Message, int64, error) {
			summary := agentcore.Message{
				Type:    agentcore.MessageTypeCompactionSummary,
				Role:    agentcore.RoleUser,
				Content: "Summary of conversation.",
			}
			return []agentcore.Message{summary, makeUserMsg("recent")}, 2, nil
		},
	}
	engine := NewEnhancedContextEngine(mock)

	msgs := []agentcore.Message{
		makeUserMsg("old msg"),
		makeUserMsg("recent msg"),
	}

	result, _, err := engine.Compress(context.Background(), msgs, "Fix the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Summary should be enhanced with prefix, active task, and end marker.
	summary := result[0]
	if !strings.Contains(summary.Content, "[CONTEXT COMPACTION") {
		t.Error("expected summary prefix")
	}
	if !strings.Contains(summary.Content, "## Active Task") {
		t.Error("expected active task section")
	}
	if !strings.Contains(summary.Content, "Fix the bug") {
		t.Error("expected focus topic in active task")
	}
	if !strings.Contains(summary.Content, "END OF CONTEXT SUMMARY") {
		t.Error("expected end marker")
	}
}

func TestExtractLastUserInstruction(t *testing.T) {
	msgs := []agentcore.Message{
		makeUserMsg("first instruction"),
		{Role: agentcore.RoleAssistant, Content: "response"},
		makeUserMsg("latest instruction"),
	}

	result := extractLastUserInstruction(msgs)
	if result != "latest instruction" {
		t.Errorf("expected 'latest instruction', got %q", result)
	}
}

func TestExtractLastUserInstruction_TruncatesLongContent(t *testing.T) {
	long := strings.Repeat("x", 300)
	msgs := []agentcore.Message{makeUserMsg(long)}

	result := extractLastUserInstruction(msgs)
	// 200 chars + "…" (3 bytes in UTF-8)
	if len(result) > 203 {
		t.Errorf("expected truncation to ~200 chars, got %d", len(result))
	}
	if !strings.HasSuffix(result, "…") {
		t.Error("expected truncation suffix")
	}
}

// TestExtractLastUserInstruction_CJKValidUTF8 verifies that rune-aware
// truncation does not produce invalid UTF-8 for CJK content. Previously,
// byte-level slicing (content[:200]) could split a 3-byte CJK rune.
func TestExtractLastUserInstruction_CJKValidUTF8(t *testing.T) {
	// 250 Chinese chars = 750 bytes. Byte-level truncation at 200 would
	// split the 67th character (byte 200 is the 2nd byte of char 67).
	long := strings.Repeat("你", 250)
	msgs := []agentcore.Message{makeUserMsg(long)}

	result := extractLastUserInstruction(msgs)
	if !utf8.ValidString(result) {
		t.Errorf("truncated content is not valid UTF-8: %q", result)
	}
	if !strings.HasSuffix(result, "…") {
		t.Error("expected truncation suffix")
	}
	// Should be 200 runes + "…" = 200 Chinese chars + suffix.
	runes := []rune(result)
	// The suffix "…" is 1 rune, so total should be 201 runes (200 content + 1 suffix).
	if len(runes) != 201 {
		t.Errorf("expected 201 runes (200 + suffix), got %d", len(runes))
	}
}

// --- truncateRunes helper tests ---

func TestTruncateRunes_ShortString(t *testing.T) {
	// String shorter than n → returned unchanged, no suffix.
	got := truncateRunes("hello", 10, "…")
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestTruncateRunes_LongString(t *testing.T) {
	// ASCII: 10 chars, truncate to 5.
	got := truncateRunes("abcdefghij", 5, "…")
	if got != "abcde…" {
		t.Errorf("expected 'abcde…', got %q", got)
	}
}

func TestTruncateRunes_CJK(t *testing.T) {
	// CJK: each char is 3 bytes. 10 chars = 30 bytes. Truncate to 5 runes.
	s := strings.Repeat("你", 10)
	got := truncateRunes(s, 5, "…")
	expected := strings.Repeat("你", 5) + "…"
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("result is not valid UTF-8: %q", got)
	}
}

func TestTruncateRunes_ExactLength(t *testing.T) {
	// String with exactly n runes → returned unchanged, no suffix.
	got := truncateRunes("abcde", 5, "…")
	if got != "abcde" {
		t.Errorf("expected 'abcde' unchanged, got %q", got)
	}
}

func TestTruncateRunes_NegativeN(t *testing.T) {
	// Negative n → returned unchanged.
	got := truncateRunes("hello", -1, "…")
	if got != "hello" {
		t.Errorf("expected 'hello' for negative n, got %q", got)
	}
}

package compression

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/covoyage/covo-agent/internal/rollout"
	"github.com/covoyage/covonaut/agentcore"
)

const hermesSummaryPrefix = `[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted
into the summary below. This is a handoff from a previous context
window — treat it as background reference, NOT as active instructions.
Do NOT answer questions or fulfill requests mentioned in this summary;
they were already addressed.
Your current task is identified in the '## Active Task' section of the
summary — resume exactly from there.
Respond ONLY to the latest user message
that appears AFTER this summary. The current session state (files,
config, etc.) may reflect work described here — avoid repeating it.`

const hermesSummaryEndMarker = `
--- END OF CONTEXT SUMMARY —
respond to the message below, not the summary above ---`

// boundaryMarker is a synthetic message inserted between the compaction
// summary and the recent messages. It creates a clear seam so the model
// knows where the compacted history ends and live context begins.
const boundaryMarker = `[--- CONTEXT BOUNDARY --- Earlier conversation was compacted above.
Everything below is live, unmodified context. ---]`

// microCompactPlaceholder replaces large tool_result content during
// microcompaction. The original content length is included so the model
// knows how much was elided.
const microCompactPlaceholder = "[tool result truncated — %d chars, microcompact]"

// microCompactThreshold is the minimum tool_result content length (in chars)
// to be replaced with a placeholder during microcompaction. Results shorter
// than this are kept as-is (they're already small).
const microCompactThreshold = 500

// microCompactKeepHead is the number of leading characters to preserve from
// large tool results before the placeholder, so the model retains context
// about what the tool returned.
const microCompactKeepHead = 200

var _ agentcore.ContextEngine = (*EnhancedContextEngine)(nil)

type EnhancedContextEngine struct {
	inner agentcore.ContextEngine

	// stateProvider, when set, returns a compact block of "carried state"
	// (e.g. active goal + unfinished todos) that is injected into the
	// compaction summary so it survives context reconstruction. May be nil.
	stateProvider func() string

	// compressionSwitch, when set and HasAux() is true, is activated before
	// calling the inner engine's Compress so that the LLM summarization call
	// routes through the auxiliary compression provider instead of the main
	// provider. This respects the auxiliary.compression config.
	compressionSwitch *CompressionProviderSwitch

	// memoryFlusher, when set, is called before full compaction to persist
	// important context from the conversation into agent memory.
	memoryFlusher MemoryFlusher

	// memoryRecaller, when set, is called after full compaction to recall
	// relevant memory entries and inject them into the compaction summary.
	memoryRecaller MemoryRecaller
}

func NewEnhancedContextEngine(inner agentcore.ContextEngine) *EnhancedContextEngine {
	return &EnhancedContextEngine{
		inner: inner,
	}
}

// SetStateProvider installs a callback that supplies carried state (goal/todos)
// to inject into compaction summaries.
func (e *EnhancedContextEngine) SetStateProvider(f func() string) {
	e.stateProvider = f
}

// SetCompressionSwitch installs a provider switch that routes the inner
// engine's LLM summarization call to the auxiliary compression provider
// when one is configured (auxiliary.compression.{provider,model}).
func (e *EnhancedContextEngine) SetCompressionSwitch(s *CompressionProviderSwitch) {
	e.compressionSwitch = s
}

// SetMemoryFlusher installs a flusher that persists important context to
// agent memory before full compaction. When set, the engine calls
// FlushToMemory before the inner engine's Compress, ensuring critical
// information survives even if the compaction summary is imperfect.
func (e *EnhancedContextEngine) SetMemoryFlusher(f MemoryFlusher) {
	e.memoryFlusher = f
}

// SetMemoryRecaller installs a recaller that searches agent memory for
// entries relevant to the current task after compaction and injects them
// into the compaction summary. This creates a flush→recall closed loop:
// important context is flushed before compaction, then recalled after.
func (e *EnhancedContextEngine) SetMemoryRecaller(r MemoryRecaller) {
	e.memoryRecaller = r
}

func (e *EnhancedContextEngine) Name() string {
	return "enhanced-compressor"
}

func (e *EnhancedContextEngine) OnSessionStart(ctx context.Context, model string, contextLength int64) {
	e.inner.OnSessionStart(ctx, model, contextLength)
}

func (e *EnhancedContextEngine) OnSessionReset() {
	e.inner.OnSessionReset()
}

func (e *EnhancedContextEngine) OnSessionEnd() {
	e.inner.OnSessionEnd()
}

func (e *EnhancedContextEngine) UpdateFromResponse(usage agentcore.TokenUsage) {
	e.inner.UpdateFromResponse(usage)
}

func (e *EnhancedContextEngine) ShouldCompact(msgs []agentcore.Message, toolDefs []agentcore.ToolDefinition, contextWindow int64) bool {
	return e.inner.ShouldCompact(msgs, toolDefs, contextWindow)
}

func (e *EnhancedContextEngine) Compress(ctx context.Context, msgs []agentcore.Message, focusTopic string) ([]agentcore.Message, int64, error) {
	// Auto-detect current task from the latest user instruction when no topic specified.
	if focusTopic == "" {
		focusTopic = extractLastUserInstruction(msgs)
	}

	// --- Microcompact pre-pass ---
	// Before falling back to expensive LLM-based compaction, try replacing
	// large tool_result contents with placeholders. This often brings the
	// token count back under the threshold without losing conversation flow
	// (tool_use messages are preserved, so the model can still see what was
	// called and why). If microcompact produces a meaningful reduction, we
	// return the microcompacted list and skip full compaction entirely.
	microResult, microSaved := e.MicroCompact(msgs)
	if microSaved > 0 && e.isMicroCompactSufficient(microResult) {
		// Microcompact trims tool_result contents in place; it does not
		// summarize or remove messages, so no boundary marker is needed
		// (the boundary text "Earlier conversation was compacted above"
		// would be semantically wrong — nothing above was summarized).
		//
		// Return a non-zero cut as a signal so the caller (agentcore) calls
		// ReplaceMessages with the trimmed list. The agentcore contract only
		// replaces messages when cut > 0; returning 0 would cause the trimmed
		// result to be silently discarded. The value 1 is a sentinel meaning
		// "a change occurred, please apply newMsgs", not a literal count of
		// removed messages (microcompact removes none, it only trims content).
		return microResult, 1, nil
	}

	// --- Memory flush pre-pass ---
	// Before full compaction, flush important context to agent memory so
	// it persists even if the compaction summary is imperfect. This is
	// skipped for microcompact (which doesn't summarize, just trims).
	if e.memoryFlusher != nil {
		if flushErr := e.memoryFlusher.FlushToMemory(ctx, microResult, focusTopic); flushErr != nil {
			// Flush failure is non-fatal — log and continue.
			_ = flushErr
		}
	}

	// Pass microResult (not the original msgs) so that any trimming done by
	// the microcompact pre-pass is preserved when the inner engine summarizes.
	// When microSaved == 0, microResult is an unchanged copy of msgs, so this
	// is equivalent to passing the original list.
	//
	// Activate the compression provider switch so the inner engine's LLM
	// call routes through the auxiliary compression provider when one is
	// configured. The switch is a no-op when no auxiliary provider is set.
	if e.compressionSwitch != nil && e.compressionSwitch.HasAux() {
		e.compressionSwitch.SetActive(true)
		defer e.compressionSwitch.SetActive(false)
	}
	// Annotate the context so the rollout recorder can name the summarization
	// LLM call as "compression" rather than generic "aux".
	ctx = rollout.WithInteractionKind(ctx, "compression")
	result, cut, err := e.inner.Compress(ctx, microResult, focusTopic)
	if err != nil {
		return result, cut, err
	}
	if cut == 0 {
		return result, 0, nil
	}

	e.enhanceSummaryMessages(result, focusTopic)

	// --- Memory recall post-pass ---
	// After compaction, recall relevant memory entries and inject them
	// into the compaction summary. This creates the flush→recall closed
	// loop: context flushed before compaction is recalled after.
	if e.memoryRecaller != nil {
		if recalled, recallErr := e.memoryRecaller.RecallFromMemory(ctx, focusTopic); recallErr == nil && recalled != "" {
			e.injectRecalledMemory(result, recalled)
		}
	}

	// Insert a boundary marker between the compaction summary and the
	// recent messages. This creates a clear seam so the model knows where
	// the compacted history ends and live context begins.
	result = e.insertBoundaryAfterSummary(result)

	return result, cut, nil
}

func (e *EnhancedContextEngine) enhanceSummaryMessages(msgs []agentcore.Message, focusTopic string) {
	for i := range msgs {
		if msgs[i].Type == agentcore.MessageTypeCompactionSummary {
			content := msgs[i].Content

			if !strings.Contains(content, "[CONTEXT COMPACTION") {
				content = hermesSummaryPrefix + "\n\n" + content
			}

			if !strings.Contains(content, hermesSummaryEndMarker) {
				content = strings.TrimSuffix(content, "\n")
				// Inject current task reminder before end marker.
				// Check for "## Active Task\n" (with newline) to avoid matching
				// the mention in the prefix text ('## Active Task' in quotes).
				if focusTopic != "" && !strings.Contains(content, "## Active Task\n") {
					content += "\n\n## Active Task\n" + focusTopic
				}
				// Inject carried state (active goal + unfinished todos) so the
				// objective and task progress survive context reconstruction.
				if e.stateProvider != nil && !strings.Contains(content, "## Carried State") {
					if state := strings.TrimSpace(e.stateProvider()); state != "" {
						content += "\n\n## Carried State\n" + state
					}
				}
				content += hermesSummaryEndMarker
			}

			msgs[i].Content = content
		}
	}
}

// injectRecalledMemory appends recalled memory entries to the compaction
// summary message, inserting them before the end marker so they appear as
// part of the summary context.
func (e *EnhancedContextEngine) injectRecalledMemory(msgs []agentcore.Message, recalled string) {
	for i := range msgs {
		if msgs[i].Type != agentcore.MessageTypeCompactionSummary {
			continue
		}
		content := msgs[i].Content
		// If the end marker is present, insert before it.
		if idx := strings.Index(content, hermesSummaryEndMarker); idx >= 0 {
			content = content[:idx] + recalled + content[idx:]
		} else {
			// No end marker — just append.
			content += recalled
		}
		msgs[i].Content = content
		return // only inject into the first summary
	}
}

// extractLastUserInstruction returns the last user message as a focus topic
// for compaction, truncated to a reasonable length. Returns "" if not found.
func extractLastUserInstruction(msgs []agentcore.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == agentcore.RoleUser && msgs[i].Type != agentcore.MessageTypeCompactionSummary {
			content := strings.TrimSpace(msgs[i].Content)
			const maxLen = 200
			content = truncateRunes(content, maxLen, "…")
			return content
		}
	}
	return ""
}

// truncateRunes truncates s to at most n runes, appending suffix when
// truncation occurs. It is rune-aware so it never splits a multi-byte UTF-8
// sequence (which would produce invalid UTF-8 for CJK or emoji content). If n
// is negative or the rune count of s is already <= n, s is returned unchanged.
func truncateRunes(s string, n int, suffix string) string {
	if n < 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + suffix
}

// MicroCompact walks through the message list and replaces large tool_result
// contents with short placeholders while keeping tool_use messages intact.
// This preserves the conversation flow (the model can still see what tools
// were called and with what arguments) while dramatically reducing token
// count from verbose tool outputs (file reads, search results, etc.).
//
// Returns the microcompacted message list and the number of chars saved.
// The original message list is not modified — a new list is returned.
func (e *EnhancedContextEngine) MicroCompact(msgs []agentcore.Message) ([]agentcore.Message, int64) {
	var totalSaved int64
	result := make([]agentcore.Message, len(msgs))
	copy(result, msgs)

	for i := range result {
		// Only compact tool result messages (Role == tool, with a ToolCallID).
		if result[i].Role != agentcore.RoleTool || result[i].ToolCallID == "" {
			continue
		}
		content := result[i].Content
		if len(content) < microCompactThreshold {
			continue
		}
		// Preserve a head of the content so the model retains context about
		// what the tool returned, followed by a placeholder. Use rune-aware
		// truncation so multi-byte UTF-8 content (CJK, emoji) is never split
		// mid-rune, which would produce invalid UTF-8.
		placeholder := fmt.Sprintf(microCompactPlaceholder, len(content))
		head := truncateRunes(content, microCompactKeepHead, "…\n")
		saved := int64(len(content)) - int64(len(head)) - int64(len(placeholder))
		// saved <= 0 also guards against the case where rune-aware truncation
		// did not actually shorten the content (e.g. CJK text with fewer runes
		// than microCompactKeepHead despite exceeding the byte threshold).
		if saved <= 0 {
			continue
		}
		result[i].Content = head + placeholder
		totalSaved += saved
	}
	return result, totalSaved
}

// isMicroCompactSufficient checks whether the microcompacted message list
// is small enough to skip full compaction. It uses the inner engine's
// ShouldCompact with the microcompacted messages — if the inner engine
// says "no need to compact," microcompact was sufficient.
func (e *EnhancedContextEngine) isMicroCompactSufficient(msgs []agentcore.Message) bool {
	contextWindow := e.inner.ContextLength()
	if contextWindow <= 0 {
		return false
	}
	return !e.inner.ShouldCompact(msgs, e.inner.GetToolSchemas(), contextWindow)
}

// insertBoundaryAfter inserts a synthetic boundary marker message after the
// given index in the message list. The boundary creates a clear seam so the
// model knows where compacted/trimmed context ends and live context begins.
func (e *EnhancedContextEngine) insertBoundaryAfter(msgs []agentcore.Message, afterIndex int, note string) []agentcore.Message {
	if afterIndex < 0 || afterIndex >= len(msgs) {
		return msgs
	}
	boundary := agentcore.Message{
		Role:    agentcore.RoleUser,
		Content: boundaryMarker,
		Type:    agentcore.MessageTypeCustom,
		Metadata: map[string]any{
			"boundary": true,
			"note":     note,
		},
	}
	// Insert after the specified index.
	result := make([]agentcore.Message, 0, len(msgs)+1)
	result = append(result, msgs[:afterIndex+1]...)
	result = append(result, boundary)
	result = append(result, msgs[afterIndex+1:]...)
	return result
}

// insertBoundaryAfterSummary finds the compaction summary message and inserts
// a boundary marker immediately after it. If no summary is found, the
// messages are returned unchanged.
func (e *EnhancedContextEngine) insertBoundaryAfterSummary(msgs []agentcore.Message) []agentcore.Message {
	for i, m := range msgs {
		if m.Type == agentcore.MessageTypeCompactionSummary {
			return e.insertBoundaryAfter(msgs, i, "full compaction: earlier messages summarized")
		}
	}
	return msgs
}

func (e *EnhancedContextEngine) GetToolSchemas() []agentcore.ToolDefinition {
	return e.inner.GetToolSchemas()
}

func (e *EnhancedContextEngine) ContextLength() int64 {
	return e.inner.ContextLength()
}

func (e *EnhancedContextEngine) ThresholdTokens() int64 {
	return e.inner.ThresholdTokens()
}

func (e *EnhancedContextEngine) CompressionCount() int64 {
	return e.inner.CompressionCount()
}

func (e *EnhancedContextEngine) LastSavingsPct() float64 {
	return e.inner.LastSavingsPct()
}

func (e *EnhancedContextEngine) CheckFeasibility(mainModelContextLength int64) string {
	return e.inner.CheckFeasibility(mainModelContextLength)
}

func BuildEnhancedCompactionConfig(modelContextLength int64) agentcore.CompactionConfig {
	if modelContextLength <= 0 {
		modelContextLength = 128000 // fallback
	}
	autoCompactLimit := int64(0)
	if v := os.Getenv("COVO_AUTO_COMPACT_TOKEN_LIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			autoCompactLimit = n
		}
	}
	return agentcore.CompactionConfig{
		ContextWindow:         modelContextLength,
		ReserveTokens:         32000,
		KeepRecentTokens:      6000,
		StructuredCompaction:  true,
		ProtectFirstN:         5,
		CompressionThreshold:  0.90, // compress at 90% of the model's context window
		AutoCompactTokenLimit: autoCompactLimit,
		AntiThrashEnabled:     true,
	}
}

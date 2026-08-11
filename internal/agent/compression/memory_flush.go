package compression

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// MemoryStore is a minimal interface for flushing context to and recalling
// from persistent memory. This avoids a direct dependency on the evolution
// package, which would create a circular import.
type MemoryStore interface {
	Add(store, content string) error
	Snapshot(store string) string
}

// MemoryFlusher is called before full compaction to persist important
// context from the conversation being compacted into agent memory.
// This ensures that critical information (decisions, file paths, error
// resolutions) survives even if the compaction summary is imperfect.
type MemoryFlusher interface {
	// FlushToMemory extracts key context from the messages being compacted
	// and writes it to the agent memory store. The focusTopic helps guide
	// what's considered "important."
	FlushToMemory(ctx context.Context, msgs []agentcore.Message, focusTopic string) error
}

// MemoryRecaller is called after compaction to recall relevant memory
// entries and inject them into the compacted context.
type MemoryRecaller interface {
	// RecallFromMemory searches agent memory for entries relevant to the
	// focus topic and returns them as a string to append to the compaction
	// summary.
	RecallFromMemory(ctx context.Context, focusTopic string) (string, error)
}

// --- LLM-based flush implementation ---

// FlushLLM is an LLM interface for generating memory flush summaries.
type FlushLLM interface {
	// SummarizeForMemory generates a concise summary of important context
	// from the given messages, suitable for persisting to agent memory.
	SummarizeForMemory(ctx context.Context, msgs []agentcore.Message, focusTopic string) (string, error)
}

// LLMMemoryFlusher implements MemoryFlusher using an LLM to generate summaries.
type LLMMemoryFlusher struct {
	store MemoryStore
	llm   FlushLLM
}

// NewLLMMemoryFlusher creates a flusher that uses the given LLM to summarize
// important context before compaction and writes it to the memory store.
func NewLLMMemoryFlusher(store MemoryStore, llm FlushLLM) *LLMMemoryFlusher {
	return &LLMMemoryFlusher{store: store, llm: llm}
}

func (f *LLMMemoryFlusher) FlushToMemory(ctx context.Context, msgs []agentcore.Message, focusTopic string) error {
	if f.store == nil || f.llm == nil {
		return nil
	}

	summary, err := f.llm.SummarizeForMemory(ctx, msgs, focusTopic)
	if err != nil {
		return fmt.Errorf("memory flush LLM call: %w", err)
	}
	summary = strings.TrimSpace(summary)
	if summary == "" || summary == "Nothing to save." {
		return nil
	}

	// Prefix with timestamp so the entry is identifiable as a compaction flush.
	entry := fmt.Sprintf("[compaction flush %s] %s", time.Now().Format("2006-01-02 15:04"), summary)
	return f.store.Add("agent", entry)
}

// --- Keyword-based recall implementation ---

// KeywordMemoryRecaller implements MemoryRecaller using simple keyword matching
// against the memory store.
type KeywordMemoryRecaller struct {
	store MemoryStore
}

// NewKeywordMemoryRecaller creates a recaller that searches agent memory
// for entries relevant to the focus topic using keyword matching.
func NewKeywordMemoryRecaller(store MemoryStore) *KeywordMemoryRecaller {
	return &KeywordMemoryRecaller{store: store}
}

func (r *KeywordMemoryRecaller) RecallFromMemory(_ context.Context, focusTopic string) (string, error) {
	if r.store == nil {
		return "", nil
	}

	snapshot := r.store.Snapshot("agent")
	if snapshot == "" {
		return "", nil
	}

	// If no focus topic, return a trimmed snapshot of recent entries.
	if focusTopic == "" {
		return trimMemorySnapshot(snapshot, 500), nil
	}

	// Split into entries and score by keyword overlap with focus topic.
	keywords := extractKeywords(focusTopic)
	if len(keywords) == 0 {
		return trimMemorySnapshot(snapshot, 500), nil
	}

	entries := splitMemoryEntries(snapshot)
	var relevant []string
	for _, entry := range entries {
		entryLower := strings.ToLower(entry)
		for _, kw := range keywords {
			if strings.Contains(entryLower, strings.ToLower(kw)) {
				relevant = append(relevant, strings.TrimSpace(entry))
				break
			}
		}
	}

	if len(relevant) == 0 {
		return "", nil
	}

	// Limit to top 3 most relevant entries to avoid bloating the summary.
	if len(relevant) > 3 {
		relevant = relevant[len(relevant)-3:]
	}

	var b strings.Builder
	b.WriteString("\n## Recalled from Memory\n")
	for _, entry := range relevant {
		// Truncate individual entries to keep the recall concise.
		if len(entry) > 300 {
			entry = entry[:300] + "..."
		}
		b.WriteString("- ")
		b.WriteString(entry)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// extractKeywords splits a focus topic into keywords for memory matching.
func extractKeywords(topic string) []string {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "must": true, "shall": true, "can": true,
		"of": true, "in": true, "to": true, "for": true, "with": true,
		"on": true, "at": true, "from": true, "by": true, "about": true,
		"as": true, "into": true, "like": true, "through": true, "after": true,
		"over": true, "between": true, "out": true, "against": true, "during": true,
		"without": true, "before": true, "under": true, "around": true, "among": true,
		"and": true, "or": true, "but": true, "not": true, "no": true, "if": true,
		"then": true, "else": true, "when": true, "where": true, "why": true,
		"how": true, "all": true, "each": true, "every": true, "both": true,
		"few": true, "more": true, "most": true, "other": true, "some": true,
		"such": true, "only": true, "own": true, "same": true, "so": true,
		"than": true, "too": true, "very": true, "just": true, "now": true,
		"this": true, "that": true, "these": true, "those": true,
		"i": true, "you": true, "he": true, "she": true, "it": true,
		"we": true, "they": true, "me": true, "him": true, "her": true,
		"us": true, "them": true, "my": true, "your": true, "his": true,
		"its": true, "our": true, "their": true,
	}

	words := strings.Fields(strings.ToLower(topic))
	var keywords []string
	for _, w := range words {
		w = strings.Trim(w, ".,!?;:\"'()[]{}")
		if len(w) < 3 {
			continue
		}
		if stopWords[w] {
			continue
		}
		keywords = append(keywords, w)
	}
	return keywords
}

// splitMemoryEntries splits a memory snapshot into individual entries.
// Entries are typically separated by § (section sign) or newlines starting
// with - or [.
func splitMemoryEntries(snapshot string) []string {
	if strings.Contains(snapshot, "§") {
		parts := strings.Split(snapshot, "§")
		var entries []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				entries = append(entries, p)
			}
		}
		return entries
	}
	// Fall back to line-based splitting for newline-delimited entries.
	var entries []string
	for _, line := range strings.Split(snapshot, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			entries = append(entries, line)
		}
	}
	return entries
}

// trimMemorySnapshot limits a memory snapshot to approximately maxChars,
// keeping the most recent entries (at the end).
func trimMemorySnapshot(snapshot string, maxChars int) string {
	if len(snapshot) <= maxChars {
		return snapshot
	}
	// Keep the last maxChars characters.
	return "...\n" + snapshot[len(snapshot)-maxChars:]
}

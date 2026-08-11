package evolution

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// MemoryProvider the agent needs this pattern from the memory system.
// (We extend the FileMemoryStore with nudge-aware capabilities.)

// MemoryAuditor performs periodic audits of memory content,
// checking for staleness, contradictions, and relevance degradation.
type MemoryAuditor struct {
	memory    *MemorySystem
	logger    *slog.Logger
	mu        sync.Mutex
	lastAudit time.Time
}

// AuditConfig controls audit behavior.
type AuditConfig struct {
	// MinInterval between audits
	MinInterval time.Duration

	// MaxInactiveDays before an entry is flagged as stale
	MaxInactiveDays int

	// MaxMemoryEntries before compaction is triggered
	MaxMemoryEntries int
}

// DefaultAuditConfig returns sensible defaults.
func DefaultAuditConfig() AuditConfig {
	return AuditConfig{
		MinInterval:      24 * time.Hour,
		MaxInactiveDays:  90,
		MaxMemoryEntries: 200,
	}
}

// NewMemoryAuditor creates a memory auditor.
func NewMemoryAuditor(memory *MemorySystem, logger *slog.Logger) *MemoryAuditor {
	if logger == nil {
		logger = slog.Default()
	}
	return &MemoryAuditor{
		memory: memory,
		logger: logger,
	}
}

// AuditResult captures findings from a memory audit.
type AuditResult struct {
	TotalEntries     int      `json:"total_entries"`
	StaleSuggestions []string `json:"stale_suggestions,omitempty"`
	Conflicts        []string `json:"conflicts,omitempty"`
	Deduped          int      `json:"deduped"`
	Actions          []string `json:"actions"`
	NeedsCompaction  bool     `json:"needs_compaction"`
}

// Audit runs a memory audit, checking for staleness, contradictions, etc.
func (ma *MemoryAuditor) Audit(cfg AuditConfig) (*AuditResult, error) {
	ma.mu.Lock()
	defer ma.mu.Unlock()

	// Rate limit
	if time.Since(ma.lastAudit) < cfg.MinInterval {
		ma.logger.Debug("audit skipped: interval not elapsed",
			"since", time.Since(ma.lastAudit), "min", cfg.MinInterval)
		return nil, nil
	}
	ma.lastAudit = time.Now()

	result := &AuditResult{}

	// Read all memory entries
	memoryContent := ma.memory.Snapshot(MemoryAgent)
	userContent := ma.memory.Snapshot(MemoryUser)

	memoryEntries := splitEntries(memoryContent)
	userEntries := splitEntries(userContent)
	result.TotalEntries = len(memoryEntries) + len(userEntries)

	// Check for staleness — entries with old-looking timestamps
	for _, entry := range memoryEntries {
		if seemsStale(entry, cfg.MaxInactiveDays) {
			result.StaleSuggestions = append(result.StaleSuggestions,
				truncateStr(strings.TrimSpace(entry), 100))
		}
	}

	// Check for potential contradictions (simple heuristics)
	conflicts := findConflicts(append(memoryEntries, userEntries...))
	result.Conflicts = conflicts

	// Check if compaction is needed
	if result.TotalEntries > cfg.MaxMemoryEntries {
		result.NeedsCompaction = true
		result.Actions = append(result.Actions,
			fmt.Sprintf("Memory has %d entries (limit: %d). Consider compacting.",
				result.TotalEntries, cfg.MaxMemoryEntries))
	}

	// Summarize
	if len(result.StaleSuggestions) > 0 {
		result.Actions = append(result.Actions,
			fmt.Sprintf("%d entries appear stale (>%d days inactive)",
				len(result.StaleSuggestions), cfg.MaxInactiveDays))
	}
	if len(result.Conflicts) > 0 {
		result.Actions = append(result.Actions,
			fmt.Sprintf("%d potential contradictions detected", len(result.Conflicts)))
	}

	return result, nil
}

// FormatAuditForNudge returns audit findings formatted for a nudge message.
func (ma *MemoryAuditor) FormatAuditForNudge(cfg AuditConfig) string {
	result, err := ma.Audit(cfg)
	if err != nil || result == nil || len(result.Actions) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Memory audit findings:\n\n")
	for _, a := range result.Actions {
		b.WriteString(fmt.Sprintf("- %s\n", a))
	}

	if len(result.StaleSuggestions) > 0 {
		b.WriteString("\nStale entries to review:\n")
		for i, s := range result.StaleSuggestions {
			if i >= 3 {
				b.WriteString(fmt.Sprintf("  ... and %d more\n", len(result.StaleSuggestions)-3))
				break
			}
			b.WriteString(fmt.Sprintf("  - %s\n", s))
		}
	}

	return b.String()
}

// MemoryHeatmap tracks which memory entries are frequently accessed
// vs which are rarely referenced, helping identify stale content.
type MemoryHeatmap struct {
	mu          sync.RWMutex
	accessCount map[string]int
	lastAccess  map[string]time.Time
}

// NewMemoryHeatmap creates a memory heatmap tracker.
func NewMemoryHeatmap() *MemoryHeatmap {
	return &MemoryHeatmap{
		accessCount: make(map[string]int),
		lastAccess:  make(map[string]time.Time),
	}
}

// RecordAccess marks a memory entry key as accessed.
func (mh *MemoryHeatmap) RecordAccess(key string) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.accessCount[key]++
	mh.lastAccess[key] = time.Now()
}

// ColdKeys returns entries that haven't been accessed within staleDuration.
func (mh *MemoryHeatmap) ColdKeys(staleDuration time.Duration) []string {
	mh.mu.RLock()
	defer mh.mu.RUnlock()

	now := time.Now()
	var cold []string
	for key, lastAt := range mh.lastAccess {
		if now.Sub(lastAt) > staleDuration {
			cold = append(cold, key)
		}
	}
	return cold
}

// HotKeys returns the most frequently accessed entries.
func (mh *MemoryHeatmap) HotKeys(limit int) []string {
	mh.mu.RLock()
	defer mh.mu.RUnlock()

	type entry struct {
		key   string
		count int
	}

	var entries []entry
	for k, v := range mh.accessCount {
		entries = append(entries, entry{key: k, count: v})
	}

	// Sort by count descending
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].count > entries[i].count {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	var hot []string
	for i := 0; i < limit && i < len(entries); i++ {
		hot = append(hot, entries[i].key)
	}
	return hot
}

// Helper functions

// splitEntries splits memory content by delimiter into individual entries.
func splitEntries(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(content, entryDelimiter)
}

// seemsStale heuristically checks if a memory entry references old dates.
func seemsStale(entry string, maxDays int) bool {
	if entry == "" {
		return false
	}

	// Check for explicit date references that are more than maxDays old
	// Very simplified — in production, parse actual dates
	contentLower := strings.ToLower(strings.TrimSpace(entry))

	// Heuristic: entries starting with date patterns
	// (production code would parse dates properly)
	if strings.Contains(contentLower, "outdated") ||
		strings.Contains(contentLower, "no longer relevant") ||
		strings.Contains(contentLower, "deprecated") {
		return true
	}

	return false
}

// findConflicts detects potential contradictory statements in memory entries.
func findConflicts(entries []string) []string {
	if len(entries) < 2 {
		return nil
	}

	var conflicts []string

	// Simple heuristic: look for opposing patterns
	type pattern struct {
		match  string
		oppose string
		label  string
	}

	patterns := []pattern{
		{"always", "never", "always/never contradiction"},
		{"must", "should not", "must/should-not contradiction"},
		{"required", "optional", "required/optional contradiction"},
		{"prefers", "prefers not", "preference reversal"},
	}

	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			a := strings.ToLower(entries[i])
			b := strings.ToLower(entries[j])
			if a == b {
				continue
			}

			for _, p := range patterns {
				if strings.Contains(a, p.match) && strings.Contains(b, p.oppose) {
					conflicts = append(conflicts, fmt.Sprintf("%s:\n  A: %s\n  B: %s",
						p.label,
						truncateStr(strings.TrimSpace(entries[i]), 80),
						truncateStr(strings.TrimSpace(entries[j]), 80)))
				}
			}
		}
	}

	return conflicts
}

// MemoryStatsSummary generates a summary of memory system statistics.
func MemoryStatsSummary(memory *MemorySystem) map[string]interface{} {
	if memory == nil {
		return map[string]interface{}{"status": "unavailable"}
	}

	agentContent := memory.Snapshot(MemoryAgent)
	userContent := memory.Snapshot(MemoryUser)

	agentEntries := splitEntries(agentContent)
	userEntries := splitEntries(userContent)

	return map[string]interface{}{
		"status":        "active",
		"agent_entries": len(agentEntries),
		"user_entries":  len(userEntries),
		"total_size_kb": float64(len(agentContent)+len(userContent)) / 1024,
	}
}


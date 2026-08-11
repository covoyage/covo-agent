package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type DreamPhase string

const (
	DreamPhaseLight DreamPhase = "light"
	DreamPhaseREM   DreamPhase = "rem"
	DreamPhaseDeep  DreamPhase = "deep"
)

type DreamingConfig struct {
	Enabled         bool          `json:"enabled"`
	Interval        time.Duration `json:"interval"`
	Model           string        `json:"model,omitempty"`
	MinScore        float64       `json:"min_score"`
	MaxEntriesLight int           `json:"max_entries_light"`
	MaxPromoteDaily int           `json:"max_promote_daily"`
}

func DefaultDreamingConfig() DreamingConfig {
	return DreamingConfig{
		Enabled:         false,
		Interval:        24 * time.Hour,
		MinScore:        0.6,
		MaxEntriesLight: 50,
		MaxPromoteDaily: 10,
	}
}

type DreamEntry struct {
	Phase       DreamPhase `json:"phase"`
	Timestamp   time.Time  `json:"timestamp"`
	Summary     string     `json:"summary"`
	EntriesIn   int        `json:"entries_in"`
	EntriesOut  int        `json:"entries_out"`
	Conflicts   int        `json:"conflicts"`
	StaleFound  int        `json:"stale_found"`
}

type DreamingEngine struct {
	mu       sync.RWMutex
	runMu    sync.Mutex
	cfg      DreamingConfig
	memory   *MemorySystem
	logger   *slog.Logger
	dir      string
	diary    []DreamEntry
	lastRun  time.Time
	runCount int
}

func NewDreamingEngine(memory *MemorySystem, cfg DreamingConfig, dir string, logger *slog.Logger) *DreamingEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &DreamingEngine{
		memory: memory,
		cfg:    cfg,
		dir:    dir,
		logger: logger,
	}
}

func (e *DreamingEngine) Config() DreamingConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

func (e *DreamingEngine) SetConfig(cfg DreamingConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = cfg
}

func (e *DreamingEngine) LastRun() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastRun
}

func (e *DreamingEngine) RunCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.runCount
}

func (e *DreamingEngine) Diary() []DreamEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]DreamEntry, len(e.diary))
	copy(out, e.diary)
	return out
}

func (e *DreamingEngine) Run(ctx context.Context) (*DreamEntry, error) {
	e.runMu.Lock()
	defer e.runMu.Unlock()

	if e.memory == nil {
		return nil, fmt.Errorf("memory system not initialized")
	}

	cfg := e.Config()

	entry := DreamEntry{
		Phase:     DreamPhaseLight,
		Timestamp: time.Now(),
	}

	agentEntries, _ := e.memory.Read(MemoryAgent)
	userEntries, _ := e.memory.Read(MemoryUser)

	allEntries := make([]string, 0, len(agentEntries)+len(userEntries))
	for _, me := range agentEntries {
		allEntries = append(allEntries, me.Content)
	}
	for _, me := range userEntries {
		allEntries = append(allEntries, me.Content)
	}

	if len(allEntries) == 0 {
		entry.Summary = "No memory entries to consolidate"
		e.appendDiary(entry)
		return &entry, nil
	}

	entry.EntriesIn = len(allEntries)
	entry.Phase = DreamPhaseLight

	staleIndices := map[int]bool{}
	for i, entryText := range allEntries {
		if seemsStale(entryText, 90) {
			staleIndices[i] = true
		}
	}
	entry.StaleFound = len(staleIndices)

	if entry.EntriesIn > cfg.MaxEntriesLight {
		if entry.StaleFound == 0 {
			entry.StaleFound = entry.EntriesIn - e.cfg.MaxEntriesLight
		}
	}

	remCandidates := e.remPhase(ctx, allEntries, staleIndices, cfg)
	entry.Conflicts = len(remCandidates.conflicts)

	deepEntry := e.deepPhase(ctx, allEntries, staleIndices, remCandidates)
	entry.EntriesOut = deepEntry.entriesOut
	entry.Summary = deepEntry.summary
	entry.Phase = DreamPhaseDeep

	e.mu.Lock()
	e.lastRun = time.Now()
	e.runCount++
	e.mu.Unlock()

	e.appendDiary(entry)
	e.writeDiary()

	return &entry, nil
}

type remResult struct {
	conflicts  []string
	themes     []string
	promoteIdx []int
}

func (e *DreamingEngine) remPhase(_ context.Context, entries []string, stale map[int]bool, cfg DreamingConfig) remResult {
	result := remResult{}

	conflictPairs := findConflicts(entries)
	result.conflicts = conflictPairs

	themeClusters := e.clusterByTheme(entries, stale)
	result.themes = themeClusters

	promoteScore := make([]float64, len(entries))
	for i, entry := range entries {
		if stale[i] {
			continue
		}
		score := e.scoreEntry(entry, len(entries))
		promoteScore[i] = score
	}

	for i, score := range promoteScore {
		if score >= cfg.MinScore {
			result.promoteIdx = append(result.promoteIdx, i)
		}
	}

	return result
}

func (e *DreamingEngine) clusterByTheme(entries []string, stale map[int]bool) []string {
	seen := map[string]bool{}
	var clusters []string

	for i, entry := range entries {
		if stale[i] {
			continue
		}
		theme := extractTheme(entry)
		if theme == "" || seen[theme] {
			continue
		}
		seen[theme] = true

		count := 0
		for j, other := range entries {
			if i != j && !stale[j] && extractTheme(other) == theme {
				count++
			}
		}
		if count > 0 {
			clusters = append(clusters, fmt.Sprintf("%s (%d entries)", theme, count+1))
		}
	}

	return clusters
}

func extractTheme(entry string) string {
	lower := strings.ToLower(strings.TrimSpace(entry))
	if len(lower) > 80 {
		lower = lower[:80]
	}

	prefixes := []string{"prefers ", "uses ", "likes ", "dislikes ", "works with ", "experience with "}
	for _, p := range prefixes {
		if idx := strings.Index(lower, p); idx >= 0 {
			end := idx + len(p) + 40
			if end > len(lower) {
				end = len(lower)
			}
			return strings.TrimSpace(lower[idx:end])
		}
	}

	keywords := []string{"project", "tool", "language", "framework", "workflow", "preference"}
	for _, kw := range keywords {
		if idx := strings.Index(lower, kw); idx >= 0 {
			end := idx + 40
			if end > len(lower) {
				end = len(lower)
			}
			return strings.TrimSpace(lower[idx:end])
		}
	}

	return ""
}

func (e *DreamingEngine) scoreEntry(entry string, total int) float64 {
	score := 0.5
	length := len(strings.TrimSpace(entry))
	if length > 200 {
		score += 0.1
	}
	if length > 500 {
		score += 0.1
	}
	if strings.Contains(entry, "always") || strings.Contains(entry, "never") {
		score += 0.1
	}
	if strings.Contains(entry, "critical") || strings.Contains(entry, "important") || strings.Contains(entry, "required") {
		score += 0.1
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}

type deepResult struct {
	entriesOut int
	summary    string
}

func (e *DreamingEngine) deepPhase(_ context.Context, entries []string, stale map[int]bool, rem remResult) deepResult {
	if len(rem.promoteIdx) == 0 && len(rem.conflicts) == 0 {
		return deepResult{entriesOut: len(entries), summary: "No changes needed"}
	}

	removed := 0
	merged := 0

	agentEntries, _ := e.memory.Read(MemoryAgent)

	isAgentEntry := func(idx int) bool {
		return idx < len(agentEntries)
	}

	removedIndices := map[int]bool{}
	for i := range stale {
		if i < len(entries) {
			removedIndices[i] = true
			removed++
		}
	}

	mergeMap := map[int]int{}
	for _, theme := range rem.themes {
		parts := strings.SplitN(theme, " (", 2)
		if len(parts) < 1 {
			continue
		}
		themeKey := parts[0]
		matching := map[MemoryStore][]int{}
		for i, entry := range entries {
			if removedIndices[i] {
				continue
			}
			if themeKey != "" && strings.Contains(strings.ToLower(entry), strings.ToLower(themeKey)) {
				store := MemoryAgent
				if !isAgentEntry(i) {
					store = MemoryUser
				}
				matching[store] = append(matching[store], i)
			}
		}
		for _, indices := range matching {
			if len(indices) > 1 {
				for k := 1; k < len(indices); k++ {
					mergeMap[indices[k]] = indices[0]
					removedIndices[indices[k]] = true
					merged++
				}
			}
		}
	}

	for i, entry := range entries {
		if removedIndices[i] {
			continue
		}
		if target, ok := mergeMap[i]; ok {
			baseText := entries[target]
			combined := baseText + "\n- " + strings.TrimSpace(entry)
			store := MemoryAgent
			if !isAgentEntry(i) {
				store = MemoryUser
			}
			e.memory.Replace(store, baseText, combined)
		}
	}

	for i := range stale {
		if i < len(entries) {
			store := MemoryAgent
			if !isAgentEntry(i) {
				store = MemoryUser
			}
			e.memory.Remove(store, entries[i])
		}
	}

	parts := []string{}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("removed %d stale entries", removed))
	}
	if merged > 0 {
		parts = append(parts, fmt.Sprintf("merged %d duplicate entries", merged))
	}
	if len(rem.conflicts) > 0 {
		parts = append(parts, fmt.Sprintf("flagged %d conflicts for review", len(rem.conflicts)))
	}

	summary := "No changes needed"
	if len(parts) > 0 {
		summary = strings.Join(parts, "; ")
	}

	return deepResult{
		entriesOut: len(entries) - removed - merged,
		summary:    summary,
	}
}

func (e *DreamingEngine) appendDiary(entry DreamEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.diary = append(e.diary, entry)
	if len(e.diary) > 100 {
		e.diary = e.diary[len(e.diary)-100:]
	}
}

func (e *DreamingEngine) writeDiary() {
	e.mu.RLock()
	diary := make([]DreamEntry, len(e.diary))
	copy(diary, e.diary)
	e.mu.RUnlock()

	dir := filepath.Join(e.dir, "dreaming")
	os.MkdirAll(dir, 0755)

	var b strings.Builder
	b.WriteString("# Dream Diary\n\n")
	for _, d := range diary {
		b.WriteString(fmt.Sprintf("## %s Phase — %s\n", d.Phase, d.Timestamp.Format(time.RFC3339)))
		b.WriteString(fmt.Sprintf("- Entries in: %d → out: %d\n", d.EntriesIn, d.EntriesOut))
		if d.StaleFound > 0 {
			b.WriteString(fmt.Sprintf("- Stale found: %d\n", d.StaleFound))
		}
		if d.Conflicts > 0 {
			b.WriteString(fmt.Sprintf("- Conflicts: %d\n", d.Conflicts))
		}
		b.WriteString(fmt.Sprintf("- Summary: %s\n\n", d.Summary))
	}

	filePath := filepath.Join(dir, "DREAMS.md")
	os.WriteFile(filePath, []byte(b.String()), 0644)
}



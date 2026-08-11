package memory

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// DreamPhase represents the current state of the memory lifecycle.
type DreamPhase string

const (
	PhaseIdle    DreamPhase = "idle"
	PhaseExtract DreamPhase = "extract"
	PhaseDream   DreamPhase = "dream"
	PhaseForget  DreamPhase = "forget"
)

// DreamCycle tracks the memory lifecycle for consolidation.
type DreamCycle struct {
	mu           sync.RWMutex
	phase        DreamPhase
	extractCount int
	dreamCount   int
	forgetCount  int
	lastExtract  time.Time
	lastDream    time.Time

	// Recent conversation turns for extraction
	recentTurns []string

	// Consolidated memories (higher-level)
	insights []dreamInsight
}

type dreamInsight struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Sources   []string  `json:"sources"`
	CreatedAt time.Time `json:"created_at"`
}

func NewDreamCycle() *DreamCycle {
	return &DreamCycle{phase: PhaseIdle}
}

// QueueTurn adds a conversation turn for later extraction.
func (dc *DreamCycle) QueueTurn(turn string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.recentTurns = append(dc.recentTurns, turn)
	// Auto-trigger extract after 5 turns
	if len(dc.recentTurns) >= 5 {
		dc.phase = PhaseExtract
	}
}

// GetExtractionPrompt returns turns ready for extraction, then clears them.
func (dc *DreamCycle) GetExtractionPrompt() string {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if len(dc.recentTurns) == 0 {
		return ""
	}

	turns := strings.Join(dc.recentTurns, "\n---\n")
	dc.recentTurns = nil
	dc.extractCount++
	dc.lastExtract = time.Now()
	dc.phase = PhaseIdle

	return turns
}

// AddInsight adds a consolidated insight from dreaming.
func (dc *DreamCycle) AddInsight(id, content string, sources []string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.insights = append(dc.insights, dreamInsight{
		ID:        id,
		Content:   content,
		Sources:   sources,
		CreatedAt: time.Now(),
	})
	dc.dreamCount++
	dc.lastDream = time.Now()
}

// SearchInsights searches consolidated insights.
func (dc *DreamCycle) SearchInsights(query string) []dreamInsight {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	type scored struct {
		insight dreamInsight
		score   int
	}
	var results []scored
	for _, ins := range dc.insights {
		score := countMatches(strings.ToLower(ins.Content), strings.ToLower(query))
		if score > 0 {
			results = append(results, scored{ins, score})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	var out []dreamInsight
	for i := 0; i < len(results) && i < 10; i++ {
		out = append(out, results[i].insight)
	}
	return out
}

// Stats returns dream cycle statistics.
func (dc *DreamCycle) Stats() map[string]any {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return map[string]any{
		"phase":         string(dc.phase),
		"extract_count": dc.extractCount,
		"dream_count":   dc.dreamCount,
		"forget_count":  dc.forgetCount,
		"insight_count": len(dc.insights),
		"pending_turns": len(dc.recentTurns),
		"last_extract":  dc.lastExtract.Format(time.RFC3339),
		"last_dream":    dc.lastDream.Format(time.RFC3339),
	}
}

func countMatches(text, query string) int {
	terms := strings.Fields(query)
	count := 0
	for _, term := range terms {
		count += strings.Count(text, term)
	}
	return count
}

// --- Dream Memory Tools ---

func BuildMemoryExtractTool(dc *DreamCycle) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "memory_extract",
		Description: strings.Join([]string{
			"Extract structured facts from recent conversation turns.",
			"Use this after significant work to extract:",
			"- Key decisions and reasoning",
			"- User preferences and conventions",
			"- Technical details and file changes",
			"- Open tasks and blockers",
			"",
			"Provide the recent conversation turns as input.",
			"Store extracted facts with memory_semantic_store for later recall.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"turns": map[string]any{
					"type":        "string",
					"description": "Recent conversation turns to extract facts from.",
				},
			},
			"required": []string{"turns"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Turns string `json:"turns"`
			}
			json.Unmarshal(args, &params)
			if strings.TrimSpace(params.Turns) == "" {
				return map[string]any{
					"phase":   "idle",
					"message": "no turns provided",
				}, nil
			}

			dc.mu.Lock()
			dc.extractCount++
			dc.lastExtract = time.Now()
			dc.mu.Unlock()

			return map[string]any{
				"phase": "extract",
				"turns": params.Turns,
				"instructions": strings.Join([]string{
					"Extract structured facts from the conversation above:",
					"1. DECISIONS: What was decided and why",
					"2. PREFERENCES: User conventions, tools, styles",
					"3. CHANGES: Files created/modified, architectures chosen",
					"4. TASKS: What's done, in-progress, blocked",
					"",
					"Output each fact as: [category] fact",
					"Then call memory_semantic_store for each fact with source='extract'.",
				}, "\n"),
			}, nil
		},
	}
}

func BuildMemoryDreamTool(dc *DreamCycle) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "memory_dream",
		Description: strings.Join([]string{
			"Consolidate scattered memory entries into higher-level insights.",
			"Use this to synthesize related facts into coherent knowledge.",
			"Input: list of memory entries (from memory_semantic_search or memory_extract).",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"memories": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "object"},
					"description": "Array of memory entries to consolidate.",
				},
			},
			"required": []string{"memories"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Memories []map[string]any `json:"memories"`
			}
			json.Unmarshal(args, &params)

			if len(params.Memories) == 0 {
				return map[string]any{
					"phase":   "idle",
					"message": "no memories to consolidate",
				}, nil
			}

			// Build a context for consolidation
			var sources []string
			var contents []string
			for _, m := range params.Memories {
				if id, ok := m["id"].(string); ok {
					sources = append(sources, id)
				}
				if content, ok := m["content"].(string); ok {
					contents = append(contents, content)
				}
			}

			return map[string]any{
				"phase":    "dream",
				"sources":  sources,
				"snapshot": strings.Join(contents, "\n---\n"),
				"instructions": strings.Join([]string{
					"Consolidate the memory entries above. For each cluster of related facts:",
					"1. Identify the core insight or pattern",
					"2. Note conflicting or evolving information",
					"3. Write the consolidated insight as a concise summary",
					"",
					"Then call memory_semantic_store for each consolidated insight",
					"with source='dream'.",
				}, "\n"),
			}, nil
		},
	}
}

func BuildMemoryDreamStatsTool(dc *DreamCycle) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "memory_dream_stats",
		Description: "View dream memory lifecycle statistics.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			return dc.Stats(), nil
		},
	}
}

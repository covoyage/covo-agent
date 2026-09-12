package memory

import (
	"context"
	"encoding/json"
	"fmt"
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
			"Extract structured facts from recent conversation turns and store them as insights.",
			"Does not call another tool — facts are saved in the dream cycle immediately.",
			"Use this after significant work to capture decisions, preferences, changes, and tasks.",
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
					"count":   0,
				}, nil
			}

			facts := extractFacts(params.Turns)
			ids := make([]string, 0, len(facts))
			for i, f := range facts {
				id := fmt.Sprintf("extract-%d-%d", time.Now().UnixNano(), i)
				dc.AddInsight(id, f, []string{"extract"})
				ids = append(ids, id)
			}

			dc.mu.Lock()
			dc.extractCount++
			dc.lastExtract = time.Now()
			dc.phase = PhaseExtract
			dc.mu.Unlock()

			return map[string]any{
				"phase": "extract",
				"count": len(facts),
				"ids":   ids,
				"facts": facts,
			}, nil
		},
	}
}

func BuildMemoryDreamTool(dc *DreamCycle) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "memory_dream",
		Description: strings.Join([]string{
			"Consolidate scattered memory entries into higher-level insights and store them.",
			"Does not ask the model to call another tool — insights are saved immediately.",
			"Input: list of memory entries (from memory_extract or prior notes).",
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
					"count":   0,
				}, nil
			}

			var sources []string
			var contents []string
			for _, m := range params.Memories {
				if id, ok := m["id"].(string); ok && id != "" {
					sources = append(sources, id)
				}
				if content, ok := m["content"].(string); ok && strings.TrimSpace(content) != "" {
					contents = append(contents, strings.TrimSpace(content))
				}
			}
			if len(contents) == 0 {
				return map[string]any{
					"phase":   "idle",
					"message": "no memory content to consolidate",
					"count":   0,
				}, nil
			}

			insight := consolidateMemories(contents)
			id := fmt.Sprintf("dream-%d", time.Now().UnixNano())
			dc.AddInsight(id, insight, sources)

			return map[string]any{
				"phase":   "dream",
				"id":      id,
				"insight": insight,
				"sources": sources,
				"count":   1,
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

func extractFacts(turns string) []string {
	var facts []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(turns, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || line == "---" {
			continue
		}
		line = strings.TrimLeft(line, "-*•")
		line = strings.TrimSpace(line)
		if len(line) < 12 {
			continue
		}
		key := strings.ToLower(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		facts = append(facts, line)
		if len(facts) >= 20 {
			break
		}
	}
	if len(facts) == 0 && strings.TrimSpace(turns) != "" {
		snippet := strings.Join(strings.Fields(turns), " ")
		if len(snippet) > 400 {
			snippet = snippet[:400] + "..."
		}
		facts = append(facts, snippet)
	}
	return facts
}

func consolidateMemories(contents []string) string {
	var parts []string
	seen := map[string]bool{}
	for _, c := range contents {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		key := strings.ToLower(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return fmt.Sprintf("Consolidated %d notes:\n- %s", len(parts), strings.Join(parts, "\n- "))
}

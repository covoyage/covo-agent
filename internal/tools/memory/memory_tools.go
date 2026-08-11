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

// MemoryEntry represents a stored memory with importance scoring.
type MemoryEntry struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	Importance float64   `json:"importance"` // 0.0-1.0
	Tags       []string  `json:"tags,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// EnhancedMemoryStore provides semantic memory capabilities.
type EnhancedMemoryStore struct {
	mu      sync.RWMutex
	entries map[string]*MemoryEntry
	counter int
}

func NewEnhancedMemoryStore() *EnhancedMemoryStore {
	return &EnhancedMemoryStore{entries: make(map[string]*MemoryEntry)}
}

func (s *EnhancedMemoryStore) Store(content string, importance float64, tags []string) *MemoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	id := fmt.Sprintf("mem-%d", s.counter)
	e := &MemoryEntry{
		ID:         id,
		Content:    content,
		Importance: importance,
		Tags:       tags,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	s.entries[id] = e
	return e
}

func (s *EnhancedMemoryStore) Recall(query string, limit int) []*MemoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	type scored struct {
		entry *MemoryEntry
		score float64
	}
	var results []scored
	lower := strings.ToLower(query)
	for _, e := range s.entries {
		score := similarity(lower, strings.ToLower(e.Content))
		// Boost by importance
		score += e.Importance * 0.2
		// Boost by tag match
		for _, tag := range e.Tags {
			if strings.Contains(lower, tag) {
				score += 0.3
			}
		}
		if score > 0 {
			results = append(results, scored{e, score})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	out := make([]*MemoryEntry, len(results))
	for i, r := range results {
		out[i] = r.entry
	}
	return out
}

// similarity computes a simple token-overlap score.
func similarity(query, text string) float64 {
	qWords := strings.Fields(query)
	if len(qWords) == 0 {
		return 0
	}
	hits := 0
	for _, w := range qWords {
		if strings.Contains(text, w) {
			hits++
		}
	}
	return float64(hits) / float64(len(qWords))
}

func (s *EnhancedMemoryStore) Forget(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.entries[id]
	if ok {
		delete(s.entries, id)
	}
	return ok
}

func (s *EnhancedMemoryStore) List() []*MemoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*MemoryEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func BuildMemoryRecallTool(store *EnhancedMemoryStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "memory_recall",
		Description: strings.Join([]string{
			"Search through long-term memories using semantic keyword matching.",
			"Results are scored by relevance with importance and tag boosts.",
			"Use this to find past decisions, user preferences, or stored knowledge.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query — keywords or phrases to match against memories.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results to return (default: 10).",
				},
			},
			"required": []string{"query"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.Limit <= 0 {
				params.Limit = 10
			}
			results := store.Recall(params.Query, params.Limit)
			out := make([]map[string]any, len(results))
			for i, r := range results {
				out[i] = map[string]any{
					"id":         r.ID,
					"content":    r.Content,
					"importance": r.Importance,
					"tags":       r.Tags,
					"created_at": r.CreatedAt.Format(time.RFC3339),
				}
			}
			return map[string]any{
				"results": out,
				"count":   len(out),
				"query":   params.Query,
			}, nil
		},
	}
}

func BuildMemoryStoreTool(store *EnhancedMemoryStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "memory_store",
		Description: strings.Join([]string{
			"Save important information to long-term memory with an importance score.",
			"Use this for user preferences, important decisions, project conventions, or lessons learned.",
			"Importance should be 0.0 (routine) to 1.0 (critical). Tag with keywords for easier recall.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "The information to store.",
				},
				"importance": map[string]any{
					"type":        "number",
					"description": "Importance score 0.0-1.0 (default: 0.5).",
				},
				"tags": map[string]any{
					"type":        "array",
					"description": "Keywords for categorizing this memory.",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"content"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Content    string   `json:"content"`
				Importance float64  `json:"importance"`
				Tags       []string `json:"tags"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Content) == "" {
				return nil, fmt.Errorf("content is required")
			}
			if params.Importance == 0 {
				params.Importance = 0.5
			}
			if params.Importance < 0 {
				params.Importance = 0
			}
			if params.Importance > 1 {
				params.Importance = 1
			}
			e := store.Store(params.Content, params.Importance, params.Tags)
			return map[string]any{
				"id":         e.ID,
				"importance": e.Importance,
				"stored":     true,
			}, nil
		},
	}
}

func BuildMemoryForgetTool(store *EnhancedMemoryStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "memory_forget",
		Description: strings.Join([]string{
			"Delete a specific memory by ID.",
			"Use this for GDPR compliance, removing outdated info, or correcting mistakes.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Memory entry ID to delete.",
				},
			},
			"required": []string{"id"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.ID == "" {
				return nil, fmt.Errorf("id is required")
			}
			if store.Forget(params.ID) {
				return map[string]any{"deleted": true, "id": params.ID}, nil
			}
			return nil, fmt.Errorf("memory %q not found", params.ID)
		},
	}
}

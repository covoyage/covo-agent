package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
	TodoCancelled  TodoStatus = "cancelled"
)

type TodoItem struct {
	ID        string     `json:"id"`
	Content   string     `json:"content"`
	Status    TodoStatus `json:"status"`
	Priority  string     `json:"priority,omitempty"`
	DependsOn []string   `json:"depends_on,omitempty"`
}

type TodoStore struct {
	mu    sync.RWMutex
	items []TodoItem
}

func NewTodoStore() *TodoStore {
	return &TodoStore{}
}

func (s *TodoStore) Write(items []TodoItem, merge bool) []TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !merge {
		s.items = items
		return s.items
	}

	// Merge: update existing by id, append new
	byID := make(map[string]int)
	for i, item := range s.items {
		byID[item.ID] = i
	}

	for _, item := range items {
		if idx, ok := byID[item.ID]; ok {
			s.items[idx] = item
		} else {
			s.items = append(s.items, item)
		}
	}
	return s.items
}

func (s *TodoStore) Read() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]TodoItem, len(s.items))
	copy(out, s.items)
	return out
}

func (s *TodoStore) Summary() (pending, inProgress, completed, cancelled int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		switch item.Status {
		case TodoPending:
			pending++
		case TodoInProgress:
			inProgress++
		case TodoCompleted:
			completed++
		case TodoCancelled:
			cancelled++
		}
	}
	return
}

// FormatForInjection renders active todos for system prompt injection.
func (s *TodoStore) FormatForInjection() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var active []TodoItem
	for _, item := range s.items {
		if item.Status == TodoPending || item.Status == TodoInProgress {
			active = append(active, item)
		}
	}
	if len(active) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Active tasks:\n")
	for _, item := range active {
		status := "☐"
		if item.Status == TodoInProgress {
			status = "◐"
		}
		b.WriteString(fmt.Sprintf("  %s [%s] %s\n", status, item.ID, item.Content))
	}
	return b.String()
}

// buildTodoTool creates the in-memory task tracking tool.
// If store is nil, a new store is created (but the caller should keep a reference
// for prompt injection). We accept an external store so it can be shared.
func BuildTodoTool(store *TodoStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "todo",
		Description: strings.Join([]string{
			"Manage an in-memory task list for tracking work within the current session.",
			"Use this to break down complex tasks, track progress, and ensure nothing is missed.",
			"",
			"When no 'todos' parameter is given, reads and returns the current task list.",
			"When 'todos' is given, writes the task list (replaces or merges).",
			"",
			"Status values: pending, in_progress, completed, cancelled.",
			"Each todo must have an 'id' (short string like '1', '2a') and 'content'.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "Task items to write. Omit to read current list.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":       map[string]any{"type": "string", "description": "Short unique identifier"},
							"content":  map[string]any{"type": "string", "description": "Task description"},
							"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
							"priority": map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
						},
						"required": []string{"id", "content"},
					},
				},
				"merge": map[string]any{
					"type":        "boolean",
					"description": "If true, merge with existing list (update by id, append new). Default: false (replace).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Todos []TodoItem `json:"todos"`
				Merge bool       `json:"merge"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if len(params.Todos) == 0 {
				// Read mode
				items := store.Read()
				p, ip, c, ca := store.Summary()
				return map[string]any{
					"todos":   items,
					"summary": map[string]int{"pending": p, "in_progress": ip, "completed": c, "cancelled": ca},
				}, nil
			}

			// Write mode — validate
			for i, item := range params.Todos {
				if item.ID == "" {
					return nil, fmt.Errorf("todo[%d]: id is required", i)
				}
				if item.Content == "" {
					return nil, fmt.Errorf("todo[%d]: content is required", i)
				}
				if item.Status == "" {
					params.Todos[i].Status = TodoPending
				}
			}

			store.Write(params.Todos, params.Merge)
			items := store.Read()
			p, ip, c, ca := store.Summary()
			return map[string]any{
				"status":  "updated",
				"todos":   items,
				"summary": map[string]int{"pending": p, "in_progress": ip, "completed": c, "cancelled": ca},
			}, nil
		},
	}
}

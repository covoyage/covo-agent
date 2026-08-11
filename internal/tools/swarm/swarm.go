package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

type SwarmStatus string

const (
	SwarmQueued    SwarmStatus = "queued"
	SwarmRunning   SwarmStatus = "running"
	SwarmCompleted SwarmStatus = "completed"
	SwarmFailed    SwarmStatus = "failed"
)

type SwarmTask struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Status      SwarmStatus `json:"status"`
	Assignee    string      `json:"assignee,omitempty"`
	Priority    string      `json:"priority,omitempty"`
	DependsOn   []string    `json:"depends_on,omitempty"`
	Result      string      `json:"result,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type SwarmBoard struct {
	mu    sync.RWMutex
	tasks map[string]*SwarmTask
	order []string
}

func NewSwarmBoard() *SwarmBoard {
	return &SwarmBoard{
		tasks: make(map[string]*SwarmTask),
		order: make([]string, 0),
	}
}

func (b *SwarmBoard) Add(task *SwarmTask) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task.CreatedAt = time.Now()
	task.UpdatedAt = task.CreatedAt
	if task.Status == "" {
		task.Status = SwarmQueued
	}
	b.tasks[task.ID] = task
	b.order = append(b.order, task.ID)
}

func (b *SwarmBoard) Update(id string, status SwarmStatus, result string) (*SwarmTask, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[id]
	if !ok {
		return nil, false
	}
	if status != "" {
		task.Status = status
	}
	if result != "" {
		task.Result = result
	}
	task.UpdatedAt = time.Now()
	return task, true
}

func (b *SwarmBoard) Get(id string) *SwarmTask {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.tasks[id]
}

func (b *SwarmBoard) List() []*SwarmTask {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]*SwarmTask, 0, len(b.order))
	for _, id := range b.order {
		if t, ok := b.tasks[id]; ok {
			result = append(result, t)
		}
	}
	return result
}

func (b *SwarmBoard) Summary() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var sb strings.Builder
	columns := map[SwarmStatus][]*SwarmTask{}
	for _, id := range b.order {
		if t, ok := b.tasks[id]; ok {
			columns[t.Status] = append(columns[t.Status], t)
		}
	}

	order := []SwarmStatus{SwarmQueued, SwarmRunning, SwarmCompleted, SwarmFailed}
	for _, status := range order {
		tasks := columns[status]
		if len(tasks) == 0 {
			continue
		}
		icon := map[SwarmStatus]string{
			SwarmQueued: "⏳", SwarmRunning: "🔄", SwarmCompleted: "✅", SwarmFailed: "❌",
		}[status]
		sb.WriteString(fmt.Sprintf("\n## %s %s (%d)\n", icon, status, len(tasks)))
		for _, t := range tasks {
			sb.WriteString(fmt.Sprintf("- %s: %s", t.ID, t.Title))
			if t.Priority != "" {
				sb.WriteString(fmt.Sprintf(" [%s]", t.Priority))
			}
			if t.Assignee != "" {
				sb.WriteString(fmt.Sprintf(" @%s", t.Assignee))
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func BuildSwarmTool(board *SwarmBoard) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "swarm",
		Description: strings.Join([]string{
			"Manage a kanban-style task board for multi-agent swarm orchestration.",
			"",
			"Use this to coordinate parallel work across multiple agents:",
			"  1. add tasks to the board (queued)",
			"  2. dispatch agents via sessions_spawn for each task",
			"  3. update task status as agents complete",
			"",
			"Actions:",
			"- add: Add a task to the board",
			"- list: Show all tasks organized by status column",
			"- update: Update task status (queued/running/completed/failed)",
			"- get: Get details for a specific task",
			"",
			"Status flow: queued -> running -> completed/failed",
			"Set dependencies with depends_on to ensure correct ordering.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action: add, list, update, get",
					"enum":        []string{"add", "list", "update", "get"},
				},
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task ID (required for update, get).",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Task title (required for add).",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Task description with implementation details.",
				},
				"priority": map[string]any{
					"type":        "string",
					"description": "Priority: high, medium, low",
					"enum":        []string{"high", "medium", "low"},
				},
				"status": map[string]any{
					"type":        "string",
					"description": "New status (for update).",
					"enum":        []string{"queued", "running", "completed", "failed"},
				},
				"result": map[string]any{
					"type":        "string",
					"description": "Result summary (for update, when completed/failed).",
				},
				"depends_on": map[string]any{
					"type":        "array",
					"description": "Task IDs this task depends on.",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action      string   `json:"action"`
				TaskID      string   `json:"task_id"`
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Priority    string   `json:"priority"`
				Status      string   `json:"status"`
				Result      string   `json:"result"`
				DependsOn   []string `json:"depends_on"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch params.Action {
			case "add":
				if strings.TrimSpace(params.Title) == "" {
					return nil, fmt.Errorf("title is required for add")
				}
				task := &SwarmTask{
					ID:          fmt.Sprintf("task-%d", time.Now().UnixNano()),
					Title:       params.Title,
					Description: params.Description,
					Priority:    params.Priority,
					DependsOn:   params.DependsOn,
				}
				board.Add(task)
				return map[string]any{
					"action":  "added",
					"task_id": task.ID,
				}, nil

			case "list":
				return map[string]any{
					"action": "listed",
					"board":  board.Summary(),
					"tasks":  board.List(),
					"count":  len(board.List()),
				}, nil

			case "update":
				if params.TaskID == "" {
					return nil, fmt.Errorf("task_id is required for update")
				}
				task, ok := board.Update(params.TaskID, SwarmStatus(params.Status), params.Result)
				if !ok {
					return nil, fmt.Errorf("task %s not found", params.TaskID)
				}
				return map[string]any{
					"action": "updated",
					"task":   task,
				}, nil

			case "get":
				if params.TaskID == "" {
					return nil, fmt.Errorf("task_id is required for get")
				}
				task := board.Get(params.TaskID)
				if task == nil {
					return nil, fmt.Errorf("task %s not found", params.TaskID)
				}
				return map[string]any{
					"action": "get",
					"task":   task,
				}, nil

			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

package kanban

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// KanbanManager manages one or more kanban boards for the agent.
type KanbanManager struct {
	mu      sync.Mutex
	homeDir string
	boards  map[string]*Board
	active  string // ID of the currently active board
}

// NewKanbanManager creates a kanban manager.
func NewKanbanManager(homeDir string) *KanbanManager {
	return &KanbanManager{
		homeDir: homeDir,
		boards:  make(map[string]*Board),
	}
}

// GetOrCreateBoard loads a board by name, creating one if it doesn't exist.
func (km *KanbanManager) GetOrCreateBoard(name string) (*Board, error) {
	km.mu.Lock()
	defer km.mu.Unlock()

	if b, ok := km.boards[name]; ok {
		return b, nil
	}

	// Try loading from disk
	id := fmt.Sprintf("board-%s", sanitizeBoardName(name))
	b, err := LoadBoard(id, km.homeDir)
	if err != nil {
		return nil, err
	}

	if b.Name == "" {
		b.Name = name
		_ = b.Save()
	}

	km.boards[name] = b
	km.active = name
	return b, nil
}

// ActiveBoard returns the currently active board.
func (km *KanbanManager) ActiveBoard() *Board {
	km.mu.Lock()
	defer km.mu.Unlock()

	if b, ok := km.boards[km.active]; ok {
		return b
	}
	return nil
}

// BuildKanbanTool creates the kanban tool definition for agentcore.
func BuildKanbanTool(km *KanbanManager) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "kanban",
		Description: strings.Join([]string{
			"Manage a kanban-style task board for structured project execution.",
			"Use this to break down large tasks, track dependencies, and coordinate parallel work.",
			"",
			"The board supports hierarchical tasks (epics → stories → tasks → subtasks).",
			"Status flow: backlog → todo → in_progress → review → done",
			"Tasks can be BLOCKED by dependencies on other tasks.",
			"",
			"Actions:",
			"  list   — View the board summary",
			"  create — Add new tasks to the board",
			"  update — Change task status, assignee, etc.",
			"  swarm  — Show which tasks can run in parallel",
			"  done   — Mark task(s) as completed",
			"  block  — Mark a task as blocked with a reason",
			"  unblock — Move a blocked task back to todo",
			"  comment — Add a note/comment to a task",
			"  heartbeat — Record activity to signal the task is alive",
			"  link   — Create a relationship between two tasks",
			"",
			"When 'board' is not specified, the active board is used.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "create", "update", "swarm", "done", "block", "unblock", "comment", "heartbeat", "link"},
					"description": "Action to perform",
				},
				"board": map[string]any{
					"type":        "string",
					"description": "Board name (creates if not exists). Default: active board.",
				},
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task ID (for update/done actions)",
				},
				"tasks": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":          map[string]any{"type": "string", "description": "Optional task ID (auto-generated if omitted)"},
							"title":       map[string]any{"type": "string", "description": "Task title"},
							"description": map[string]any{"type": "string", "description": "Task description"},
							"priority":    map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}},
							"parent_id":   map[string]any{"type": "string", "description": "Parent task ID for subtasks"},
							"depends_on":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							"status":      map[string]any{"type": "string", "enum": []string{"backlog", "todo", "in_progress", "review", "done", "blocked", "cancelled"}},
						},
						"required": []string{"title"},
					},
					"description": "Tasks to create. Omit for read actions.",
				},
				"updates": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":       map[string]any{"type": "string"},
						"description": map[string]any{"type": "string"},
						"status":      map[string]any{"type": "string", "enum": []string{"backlog", "todo", "in_progress", "review", "done", "blocked", "cancelled"}},
						"priority":    map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}},
						"assignee":    map[string]any{"type": "string"},
						"depends_on":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"notes":       map[string]any{"type": "string", "description": "Note to append to task"},
					},
					"description": "Fields to update (for update action)",
				},
				"task_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Task IDs to mark as done (for done action)",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Reason for block or comment text (for block/comment actions)",
				},
				"target_id": map[string]any{
					"type":        "string",
					"description": "ID of the task to link to (for link action)",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action   string                   `json:"action"`
				Board    string                   `json:"board"`
				TaskID   string                   `json:"task_id"`
				Tasks    []map[string]interface{} `json:"tasks"`
				Updates  map[string]interface{}   `json:"updates"`
				TaskIDs  []string                 `json:"task_ids"`
				Reason   string                   `json:"reason"`
				TargetID string                   `json:"target_id"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			boardName := params.Board
			if boardName == "" && km.active != "" {
				boardName = km.active
			}
			if boardName == "" {
				boardName = "default"
			}

			board, err := km.GetOrCreateBoard(boardName)
			if err != nil {
				return nil, fmt.Errorf("load board: %w", err)
			}

			switch params.Action {
			case "list", "":
				return km.handleList(board)
			case "create":
				return km.handleCreate(board, params.Tasks)
			case "update":
				if params.TaskID == "" {
					return nil, fmt.Errorf("task_id required for update")
				}
				return km.handleUpdate(board, params.TaskID, params.Updates)
			case "swarm":
				return km.handleSwarm(board)
			case "done":
				return km.handleDone(board, params.TaskIDs)
			case "block":
				if params.TaskID == "" {
					return nil, fmt.Errorf("task_id required for block")
				}
				return km.handleBlock(board, params.TaskID, params.Reason)
			case "unblock":
				if params.TaskID == "" {
					return nil, fmt.Errorf("task_id required for unblock")
				}
				return km.handleUnblock(board, params.TaskID)
			case "comment":
				if params.TaskID == "" || params.Reason == "" {
					return nil, fmt.Errorf("task_id and reason required for comment")
				}
				return km.handleComment(board, params.TaskID, params.Reason)
			case "heartbeat":
				if params.TaskID == "" {
					return nil, fmt.Errorf("task_id required for heartbeat")
				}
				return km.handleHeartbeat(board, params.TaskID)
			case "link":
				if params.TaskID == "" || params.TargetID == "" {
					return nil, fmt.Errorf("task_id and target_id required for link")
				}
				return km.handleLink(board, params.TaskID, params.TargetID)
			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

func (km *KanbanManager) handleList(b *Board) (any, error) {
	progress := b.Progress()
	tasksByStatus := map[string][]*Task{
		"backlog":     b.TasksByStatus(StatusBacklog),
		"todo":        b.TasksByStatus(StatusTodo),
		"in_progress": b.TasksByStatus(StatusInProgress),
		"review":      b.TasksByStatus(StatusReview),
		"blocked":     b.TasksByStatus(StatusBlocked),
		"done":        b.TasksByStatus(StatusDone),
	}

	return map[string]any{
		"action":          "list",
		"board":           b.Name,
		"progress":        progress,
		"tasks_by_status": tasksByStatus,
		"summary":         b.Summary(),
	}, nil
}

func (km *KanbanManager) handleCreate(b *Board, taskDefs []map[string]interface{}) (any, error) {
	if len(taskDefs) == 0 {
		return nil, fmt.Errorf("at least one task required")
	}

	var created []*Task
	for _, def := range taskDefs {
		task := Task{
			ID:     NewTaskID(),
			Status: StatusTodo,
		}

		if id, ok := def["id"].(string); ok && id != "" {
			task.ID = id
		}
		if title, ok := def["title"].(string); ok {
			task.Title = title
		}
		if desc, ok := def["description"].(string); ok {
			task.Description = desc
		}
		if priority, ok := def["priority"].(string); ok {
			task.Priority = TaskPriority(priority)
		}
		if parentID, ok := def["parent_id"].(string); ok {
			task.ParentID = parentID
		}
		if deps, ok := def["depends_on"].([]interface{}); ok {
			for _, d := range deps {
				if dStr, ok := d.(string); ok {
					task.DependsOn = append(task.DependsOn, dStr)
				}
			}
		}
		if tags, ok := def["tags"].([]interface{}); ok {
			for _, t := range tags {
				if tStr, ok := t.(string); ok {
					task.Tags = append(task.Tags, tStr)
				}
			}
		}
		if status, ok := def["status"].(string); ok {
			task.Status = TaskStatus(status)
		}

		createdTask, err := b.AddTask(task)
		if err != nil {
			return nil, err
		}
		created = append(created, createdTask)
	}

	return map[string]any{
		"action":  "create",
		"board":   b.Name,
		"created": created,
		"count":   len(created),
	}, nil
}

func (km *KanbanManager) handleUpdate(b *Board, taskID string, updates map[string]interface{}) (any, error) {
	task, err := b.UpdateTask(taskID, updates)
	if err != nil {
		return nil, err
	}

	progress := b.Progress()
	return map[string]any{
		"action":   "update",
		"board":    b.Name,
		"task":     task,
		"progress": progress,
	}, nil
}

func (km *KanbanManager) handleSwarm(b *Board) (any, error) {
	plan := b.SwarmPlan()
	ready := b.ReadyTasks()

	type taskSummary struct {
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		Priority string   `json:"priority"`
		Depends  []string `json:"depends_on"`
	}

	var summaries []taskSummary
	for _, t := range ready {
		summaries = append(summaries, taskSummary{
			ID:       t.ID,
			Title:    t.Title,
			Priority: string(t.Priority),
			Depends:  t.DependsOn,
		})
	}

	return map[string]any{
		"action":      "swarm",
		"board":       b.Name,
		"ready_tasks": summaries,
		"ready_count": len(ready),
		"swarm_plan":  plan,
		"blocked":     b.BlockedTasks(),
	}, nil
}

func (km *KanbanManager) handleDone(b *Board, taskIDs []string) (any, error) {
	if len(taskIDs) == 0 {
		return nil, fmt.Errorf("at least one task_id required")
	}

	var completed []*Task
	for _, id := range taskIDs {
		task, err := b.UpdateTask(id, map[string]interface{}{"status": "done"})
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", id, err)
		}
		completed = append(completed, task)
	}

	progress := b.Progress()
	return map[string]any{
		"action":    "done",
		"board":     b.Name,
		"completed": completed,
		"progress":  progress,
	}, nil
}

func (km *KanbanManager) handleBlock(b *Board, taskID, reason string) (any, error) {
	task, err := b.BlockTask(taskID, reason)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"action": "block",
		"board":  b.Name,
		"task":   task,
		"reason": reason,
	}, nil
}

func (km *KanbanManager) handleUnblock(b *Board, taskID string) (any, error) {
	task, err := b.UnblockTask(taskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"action": "unblock",
		"board":  b.Name,
		"task":   task,
	}, nil
}

func (km *KanbanManager) handleComment(b *Board, taskID, comment string) (any, error) {
	task, err := b.AddComment(taskID, comment)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"action":  "comment",
		"board":   b.Name,
		"task":    task,
		"comment": comment,
	}, nil
}

func (km *KanbanManager) handleHeartbeat(b *Board, taskID string) (any, error) {
	task, err := b.HeartbeatTask(taskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"action": "heartbeat",
		"board":  b.Name,
		"task":   task,
		"alive":  true,
	}, nil
}

func (km *KanbanManager) handleLink(b *Board, fromID, toID string) (any, error) {
	task, err := b.LinkTask(fromID, toID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"action": "link",
		"board":  b.Name,
		"from":   fromID,
		"to":     toID,
		"task":   task,
	}, nil
}

func sanitizeBoardName(name string) string {
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	return strings.Trim(name, "-")
}

// KanbanTool implements agentcore.ToolInterface for manual tool dispatch.
type KanbanTool struct {
	manager *KanbanManager
}

func NewKanbanTool(km *KanbanManager) *KanbanTool {
	return &KanbanTool{manager: km}
}

func (kt *KanbanTool) Name() string {
	return "kanban"
}

func (kt *KanbanTool) Description() string {
	return "Kanban board for structured task management"
}

func (kt *KanbanTool) Parameters() map[string]any {
	return nil // handled by BuildKanbanTool
}

func (kt *KanbanTool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	tool := BuildKanbanTool(kt.manager)
	return tool.Func(ctx, args)
}

// Package kanban provides a task board system for structured work decomposition,
// state tracking, and parallel execution coordination.
//
// The kanban system supports:
//   - Hierarchical task decomposition (epic → story → subtask)
//   - State machine with lanes: backlog, todo, in_progress, review, done, blocked
//   - Dependency tracking between tasks
//   - Parallel task gating (swarm mode)
//   - Progress tracking with status snapshots
//   - Cross-session persistence via JSON snapshot files
package kanban

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TaskStatus represents the workflow lane a task is currently in.
type TaskStatus string

const (
	StatusBacklog    TaskStatus = "backlog"
	StatusTodo       TaskStatus = "todo"
	StatusInProgress TaskStatus = "in_progress"
	StatusReview     TaskStatus = "review"
	StatusDone       TaskStatus = "done"
	StatusBlocked    TaskStatus = "blocked"
	StatusCancelled  TaskStatus = "cancelled"
)

// ValidTransitions defines allowed state transitions for each status.
var ValidTransitions = map[TaskStatus][]TaskStatus{
	StatusBacklog:    {StatusTodo, StatusCancelled},
	StatusTodo:       {StatusInProgress, StatusCancelled, StatusBacklog},
	StatusInProgress: {StatusReview, StatusBlocked, StatusDone, StatusCancelled, StatusTodo},
	StatusReview:     {StatusDone, StatusInProgress, StatusTodo},
	StatusBlocked:    {StatusTodo, StatusInProgress, StatusCancelled},
	StatusDone:       {StatusInProgress, StatusTodo}, // re-open
	StatusCancelled:  {StatusTodo, StatusBacklog},
}

// CanTransition checks if a transition from current to target is valid.
func CanTransition(from, to TaskStatus) bool {
	allowed, ok := ValidTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// TaskPriority indicates importance.
type TaskPriority string

const (
	PriorityCritical TaskPriority = "critical"
	PriorityHigh     TaskPriority = "high"
	PriorityMedium   TaskPriority = "medium"
	PriorityLow      TaskPriority = "low"
)

// Task represents a single unit of work in the kanban board.
type Task struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Status      TaskStatus   `json:"status"`
	Priority    TaskPriority `json:"priority"`
	ParentID    string       `json:"parent_id,omitempty"` // For subtask hierarchy
	ChildrenIDs []string     `json:"children_ids,omitempty"`
	DependsOn   []string     `json:"depends_on,omitempty"` // Blocked until these are done
	Assignee    string       `json:"assignee,omitempty"`   // Sub-agent ID for swarm mode
	Tags        []string     `json:"tags,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	CompletedAt *time.Time   `json:"completed_at,omitempty"`
	Notes       []string     `json:"notes,omitempty"`
	HeartbeatAt *time.Time   `json:"heartbeat_at,omitempty"`
	LinkedTo    []string     `json:"linked_to,omitempty"`

	// Metrics
	EstimatedHours float64 `json:"estimated_hours,omitempty"`
	ActualHours    float64 `json:"actual_hours,omitempty"`
}

// Board represents a full kanban board with tasks, lanes, and operations.
type Board struct {
	mu        sync.RWMutex
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Tasks     map[string]*Task `json:"tasks"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	storePath string
}

// NewBoard creates a new empty kanban board.
func NewBoard(id, name, homeDir string) *Board {
	return &Board{
		ID:        id,
		Name:      name,
		Tasks:     make(map[string]*Task),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		storePath: filepath.Join(homeDir, "kanban", id+".json"),
	}
}

// LoadBoard loads a persisted board from disk.
func LoadBoard(id, homeDir string) (*Board, error) {
	storePath := filepath.Join(homeDir, "kanban", id+".json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty board
			return NewBoard(id, "", homeDir), nil
		}
		return nil, fmt.Errorf("read board: %w", err)
	}

	var b Board
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse board: %w", err)
	}
	b.storePath = storePath
	return &b, nil
}

// Save persists the board to disk. The caller must hold b.mu (write lock).
// Use SaveLocked for external callers that don't already hold the lock.
func (b *Board) Save() error {
	if b.storePath == "" {
		return fmt.Errorf("board has no store path")
	}

	if err := os.MkdirAll(filepath.Dir(b.storePath), 0755); err != nil {
		return err
	}

	b.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal board: %w", err)
	}
	return os.WriteFile(b.storePath, data, 0644)
}

// SaveLocked acquires the board lock and persists to disk.
// Safe for concurrent access from external callers.
func (b *Board) SaveLocked() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Save()
}

// AddTask creates a new task on the board.
func (b *Board) AddTask(task Task) (*Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if task.ID == "" {
		task.ID = NewTaskID()
	}
	if _, exists := b.Tasks[task.ID]; exists {
		return nil, fmt.Errorf("task %q already exists", task.ID)
	}

	if task.Status == "" {
		task.Status = StatusTodo
	}
	if task.Priority == "" {
		task.Priority = PriorityMedium
	}
	now := time.Now()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now

	// Add to parent's children list if applicable
	if task.ParentID != "" {
		if parent, ok := b.Tasks[task.ParentID]; ok {
			parent.ChildrenIDs = append(parent.ChildrenIDs, task.ID)
		}
	}

	taskCopy := task
	b.Tasks[task.ID] = &taskCopy
	_ = b.Save()
	return &taskCopy, nil
}

// UpdateTask modifies an existing task.
func (b *Board) UpdateTask(id string, updates map[string]interface{}) (*Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.Tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %q not found", id)
	}

	// Handle status transition validation
	if newStatus, hasStatus := updates["status"]; hasStatus {
		newSt, ok := newStatus.(string)
		if !ok {
			return nil, fmt.Errorf("status must be a string")
		}
		targetStatus := TaskStatus(newSt)
		if targetStatus == task.Status {
			// no-op; allow
		} else if !CanTransition(task.Status, targetStatus) {
			return nil, fmt.Errorf("invalid transition: %s → %s", task.Status, targetStatus)
		} else {
			task.Status = targetStatus
		}
		if targetStatus == StatusDone || targetStatus == StatusCancelled {
			now := time.Now()
			task.CompletedAt = &now
		} else {
			task.CompletedAt = nil
		}
	}

	if title, ok := updates["title"]; ok {
		if t, ok := title.(string); ok {
			task.Title = t
		}
	}
	if desc, ok := updates["description"]; ok {
		if d, ok := desc.(string); ok {
			task.Description = d
		}
	}
	if priority, ok := updates["priority"]; ok {
		if p, ok := priority.(string); ok {
			task.Priority = TaskPriority(p)
		}
	}
	if assignee, ok := updates["assignee"]; ok {
		if a, ok := assignee.(string); ok {
			task.Assignee = a
		}
	}
	if deps, ok := updates["depends_on"]; ok {
		if d, ok := deps.([]interface{}); ok {
			task.DependsOn = nil
			for _, dep := range d {
				if depStr, ok := dep.(string); ok {
					task.DependsOn = append(task.DependsOn, depStr)
				}
			}
		}
	}
	if tags, ok := updates["tags"]; ok {
		if t, ok := tags.([]interface{}); ok {
			task.Tags = nil
			for _, tag := range t {
				if tagStr, ok := tag.(string); ok {
					task.Tags = append(task.Tags, tagStr)
				}
			}
		}
	}
	if notes, ok := updates["notes"]; ok {
		if n, ok := notes.(string); ok {
			task.Notes = append(task.Notes, n)
		}
	}
	if parentID, ok := updates["parent_id"]; ok {
		if p, ok := parentID.(string); ok {
			task.ParentID = p
		}
	}

	task.UpdatedAt = time.Now()
	_ = b.Save()
	return task, nil
}

// GetTask returns a task by ID.
func (b *Board) GetTask(id string) (*Task, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	t, ok := b.Tasks[id]
	return t, ok
}

// BlockTask marks a task as blocked with a reason note.
func (b *Board) BlockTask(id, reason string) (*Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.Tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	if t.Status == StatusBlocked {
		return t, nil
	}
	if !CanTransition(t.Status, StatusBlocked) {
		return nil, fmt.Errorf("cannot block task in %s status", t.Status)
	}
	t.Status = StatusBlocked
	t.UpdatedAt = time.Now()
	if reason != "" {
		t.Notes = append(t.Notes, fmt.Sprintf("[blocked] %s — %s", time.Now().Format(time.RFC3339), reason))
	}
	_ = b.Save()
	return t, nil
}

// UnblockTask moves a blocked task back to its previous ready state.
func (b *Board) UnblockTask(id string) (*Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.Tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	if t.Status != StatusBlocked {
		return nil, fmt.Errorf("task %s is not blocked", id)
	}
	t.Status = StatusTodo
	t.UpdatedAt = time.Now()
	t.Notes = append(t.Notes, fmt.Sprintf("[unblocked] %s", time.Now().Format(time.RFC3339)))
	_ = b.Save()
	return t, nil
}

// AddComment appends a comment to a task's notes.
func (b *Board) AddComment(id, comment string) (*Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.Tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	t.Notes = append(t.Notes, fmt.Sprintf("[comment] %s — %s", time.Now().Format(time.RFC3339), comment))
	t.UpdatedAt = time.Now()
	_ = b.Save()
	return t, nil
}

// HeartbeatTask records an activity heartbeat on a task.
func (b *Board) HeartbeatTask(id string) (*Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.Tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	now := time.Now()
	t.HeartbeatAt = &now
	t.UpdatedAt = now
	_ = b.Save()
	return t, nil
}

// LinkTask creates a link relationship between two tasks.
func (b *Board) LinkTask(fromID, toID string) (*Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.Tasks[fromID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", fromID)
	}
	if _, ok := b.Tasks[toID]; !ok {
		return nil, fmt.Errorf("task %s not found", toID)
	}
	for _, l := range t.LinkedTo {
		if l == toID {
			return t, nil // already linked
		}
	}
	t.LinkedTo = append(t.LinkedTo, toID)
	t.UpdatedAt = time.Now()
	_ = b.Save()
	return t, nil
}

// RemoveTask deletes a task and removes it from its parent's children list.
func (b *Board) RemoveTask(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.Tasks[id]
	if !ok {
		return fmt.Errorf("task %q not found", id)
	}

	// Remove from parent's children
	if task.ParentID != "" {
		if parent, pok := b.Tasks[task.ParentID]; pok {
			parent.ChildrenIDs = removeStr(parent.ChildrenIDs, id)
		}
	}

	// Remove children references
	for _, childID := range task.ChildrenIDs {
		if child, cok := b.Tasks[childID]; cok {
			child.ParentID = ""
		}
	}
	// Also clear ParentID for any task pointing to the removed one
	// (ChildrenIDs may not reflect all children if set via UpdateTask)
	for _, t := range b.Tasks {
		if t.ParentID == id {
			t.ParentID = ""
		}
	}

	// Remove this task from other tasks' dependency lists
	for _, t := range b.Tasks {
		t.DependsOn = removeStr(t.DependsOn, id)
		t.LinkedTo = removeStr(t.LinkedTo, id)
	}

	delete(b.Tasks, id)
	_ = b.Save()
	return nil
}

// TasksByStatus returns all tasks in a given status lane.
func (b *Board) TasksByStatus(status TaskStatus) []*Task {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []*Task
	for _, t := range b.Tasks {
		if t.Status == status {
			task := *t
			result = append(result, &task)
		}
	}
	// Sort by priority then creation time
	sort.Slice(result, func(i, j int) bool {
		pi := priorityOrder(result[i].Priority)
		pj := priorityOrder(result[j].Priority)
		if pi != pj {
			return pi < pj
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

// BlockedTasks returns tasks that are ready to execute but blocked by dependencies.
func (b *Board) BlockedTasks() []*Task {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var blocked []*Task
	for _, t := range b.Tasks {
		if t.Status == StatusBlocked || b.isDependencyBlocked(t) {
			task := *t
			blocked = append(blocked, &task)
		}
	}
	return blocked
}

// ReadyTasks returns tasks that are in todo/backlog and have no blocking dependencies.
func (b *Board) ReadyTasks() []*Task {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var ready []*Task
	for _, t := range b.Tasks {
		if (t.Status == StatusTodo || t.Status == StatusBacklog) && !b.isDependencyBlocked(t) {
			task := *t
			ready = append(ready, &task)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		return priorityOrder(ready[i].Priority) < priorityOrder(ready[j].Priority)
	})
	return ready
}

// isDependencyBlocked checks if a task has unmet dependencies.
func (b *Board) isDependencyBlocked(t *Task) bool {
	for _, depID := range t.DependsOn {
		depTask, ok := b.Tasks[depID]
		if !ok || depTask.Status != StatusDone {
			return true
		}
	}

	// Also blocked if parent isn't in progress/done
	if t.ParentID != "" {
		parent, ok := b.Tasks[t.ParentID]
		if ok && parent.Status != StatusInProgress && parent.Status != StatusDone {
			return true
		}
	}

	return false
}

// RootTasks returns top-level tasks (no parent).
func (b *Board) RootTasks() []*Task {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var roots []*Task
	for _, t := range b.Tasks {
		if t.ParentID == "" || b.Tasks[t.ParentID] == nil {
			task := *t
			roots = append(roots, &task)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].CreatedAt.Before(roots[j].CreatedAt)
	})
	return roots
}

// TaskTree returns a nested structure representing the task hierarchy.
type TaskTreeNode struct {
	Task     *Task           `json:"task"`
	Children []*TaskTreeNode `json:"children"`
}

func (b *Board) TaskTree() []*TaskTreeNode {
	b.mu.RLock()
	defer b.mu.RUnlock()

	childrenMap := make(map[string][]*Task)
	for _, t := range b.Tasks {
		if t.ParentID != "" {
			task := t
			childrenMap[t.ParentID] = append(childrenMap[t.ParentID], task)
		}
	}

	var buildTree func(task *Task) *TaskTreeNode
	buildTree = func(task *Task) *TaskTreeNode {
		node := &TaskTreeNode{Task: task}
		for _, child := range childrenMap[task.ID] {
			node.Children = append(node.Children, buildTree(child))
		}
		return node
	}

	var roots []*TaskTreeNode
	for _, t := range b.Tasks {
		if t.ParentID == "" || b.Tasks[t.ParentID] == nil {
			roots = append(roots, buildTree(t))
		}
	}

	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Task.CreatedAt.Before(roots[j].Task.CreatedAt)
	})

	return roots
}

// Progress returns completion statistics.
type Progress struct {
	Total      int     `json:"total"`
	Backlog    int     `json:"backlog"`
	Todo       int     `json:"todo"`
	InProgress int     `json:"in_progress"`
	Review     int     `json:"review"`
	Done       int     `json:"done"`
	Blocked    int     `json:"blocked"`
	Cancelled  int     `json:"cancelled"`
	Percent    float64 `json:"percent"`
}

func (b *Board) Progress() Progress {
	b.mu.RLock()
	defer b.mu.RUnlock()

	p := Progress{Total: len(b.Tasks)}
	for _, t := range b.Tasks {
		switch t.Status {
		case StatusBacklog:
			p.Backlog++
		case StatusTodo:
			p.Todo++
		case StatusInProgress:
			p.InProgress++
		case StatusReview:
			p.Review++
		case StatusDone:
			p.Done++
		case StatusBlocked:
			p.Blocked++
		case StatusCancelled:
			p.Cancelled++
		}
	}

	completable := p.Total - p.Cancelled
	if completable > 0 {
		p.Percent = float64(p.Done) / float64(completable) * 100
	}
	return p
}

// SwarmPlan identifies tasks ready for parallel execution.
type SwarmPlan struct {
	Independent [][]string `json:"independent"` // Groups of tasks that can run in parallel
	Sequential  []string   `json:"sequential"`  // Tasks that depend on others
}

func (b *Board) SwarmPlan() *SwarmPlan {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Find tasks that are in todo/backlog and not blocked
	var ready []*Task
	for _, t := range b.Tasks {
		status := t.Status
		if (status == StatusTodo || status == StatusBacklog) && !b.isDependencyBlocked(t) {
			ready = append(ready, t)
		}
	}

	// Group into independent batches (tasks with no inter-dependencies can run in parallel)
	independent := findIndependentGroups(ready, b.Tasks)

	// Sequential tasks (those with unmet dependencies)
	var sequential []string
	for _, t := range b.Tasks {
		if (t.Status == StatusTodo || t.Status == StatusBacklog) && b.isDependencyBlocked(t) {
			sequential = append(sequential, t.ID)
		}
	}

	return &SwarmPlan{
		Independent: independent,
		Sequential:  sequential,
	}
}

// Summary renders a human-readable board summary.
func (b *Board) Summary() string {
	progress := b.Progress()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Kanban: %s\n\n", b.Name))
	sb.WriteString(fmt.Sprintf("Progress: %d/%d (%.0f%%) | ",
		progress.Done, progress.Total-progress.Cancelled, progress.Percent))
	sb.WriteString(fmt.Sprintf("Todo: %d | In Progress: %d | Review: %d | Done: %d | Blocked: %d\n\n",
		progress.Todo, progress.InProgress, progress.Review, progress.Done, progress.Blocked))

	// Show active tasks first
	if active := b.TasksByStatus(StatusInProgress); len(active) > 0 {
		sb.WriteString("### 🟡 In Progress\n")
		for _, t := range active {
			sb.WriteString(fmt.Sprintf("- %s [%s] **%s**\n",
				t.ID, t.Priority, t.Title))
		}
		sb.WriteString("\n")
	}

	if blocked := b.TasksByStatus(StatusBlocked); len(blocked) > 0 {
		sb.WriteString("### 🔴 Blocked\n")
		for _, t := range blocked {
			sb.WriteString(fmt.Sprintf("- %s **%s** (depends on: %s)\n",
				t.ID, t.Title, strings.Join(t.DependsOn, ", ")))
		}
		sb.WriteString("\n")
	}

	// Ready tasks (todo)
	if ready := b.TasksByStatus(StatusTodo); len(ready) > 0 {
		sb.WriteString("### 📋 Ready\n")
		for _, t := range ready {
			depStr := ""
			if len(t.DependsOn) > 0 {
				depStr = fmt.Sprintf(" ← %s", strings.Join(t.DependsOn, ", "))
			}
			sb.WriteString(fmt.Sprintf("- %s [%s] **%s**%s\n",
				t.ID, t.Priority, t.Title, depStr))
		}
	}

	return sb.String()
}

// FormatForSystemPrompt renders an abbreviated board for agent context injection.
func (b *Board) FormatForSystemPrompt() string {
	progress := b.Progress()

	var sb strings.Builder
	sb.WriteString("\n--- ACTIVE KANBAN BOARD ---\n")
	sb.WriteString(fmt.Sprintf("Board: %s (%d%% complete, %d remaining)\n\n",
		b.Name, int(progress.Percent), progress.Total-progress.Done-progress.Cancelled))

	// Show top-level tasks with their status
	sb.WriteString("Tasks:\n")
	roots := b.RootTasks()
	for _, t := range roots {
		icon := statusIcon(t.Status)
		children := t.ChildrenIDs
		childStr := ""
		if len(children) > 0 {
			childStr = fmt.Sprintf(" [%d subtasks]", len(children))
		}
		sb.WriteString(fmt.Sprintf("  %s %s [%s] **%s**%s\n",
			icon, t.ID, t.Priority, t.Title, childStr))
	}

	sb.WriteString("\nUse the kanban tool to update task statuses as you work.\n")
	return sb.String()
}

func statusIcon(s TaskStatus) string {
	switch s {
	case StatusDone:
		return "✅"
	case StatusInProgress:
		return "🔄"
	case StatusReview:
		return "👁"
	case StatusBlocked:
		return "🚫"
	case StatusCancelled:
		return "❌"
	case StatusTodo:
		return "📋"
	case StatusBacklog:
		return "📥"
	default:
		return "❓"
	}
}

// Helper functions

func priorityOrder(p TaskPriority) int {
	switch p {
	case PriorityCritical:
		return 0
	case PriorityHigh:
		return 1
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 3
	default:
		return 2
	}
}

func removeStr(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// findIndependentGroups partitions ready tasks into groups that can run in
// parallel (no dependencies between group members).
func findIndependentGroups(tasks []*Task, allTasks map[string]*Task) [][]string {
	if len(tasks) == 0 {
		return nil
	}

	// Build dependency adjacency
	type node struct {
		id    string
		deps  map[string]bool
		group int
	}

	nodes := make(map[string]*node)
	for _, t := range tasks {
		n := &node{id: t.ID, deps: make(map[string]bool), group: -1}
		for _, depID := range t.DependsOn {
			if _, exists := allTasks[depID]; exists {
				n.deps[depID] = true
			}
		}
		nodes[t.ID] = n
	}

	// Greedy grouping: tasks with no deps in the ready set go to group 0,
	// tasks whose deps are all in earlier groups go to the next group.
	assigned := 0
	currentGroup := 0
	for assigned < len(nodes) {
		for _, n := range nodes {
			if n.group >= 0 {
				continue
			}

			// Check if all deps are resolved (either done or in earlier groups)
			allResolved := true
			for depID := range n.deps {
				depNode, ok := nodes[depID]
				if !ok {
					// Dep is not in ready set — check if it's already done
					depTask, exists := allTasks[depID]
					if exists && depTask.Status == StatusDone {
						continue
					}
					// Dep not in ready, not done → blocked
					allResolved = false
					break
				}
				if depNode.group < 0 || depNode.group > currentGroup {
					allResolved = false
					break
				}
			}

			if allResolved {
				n.group = currentGroup
				assigned++
			}
		}
		currentGroup++
	}

	// Build groups
	groups := make([][]string, currentGroup)
	for _, n := range nodes {
		if n.group >= 0 {
			groups[n.group] = append(groups[n.group], n.id)
		}
	}

	return groups
}

// NewTaskID generates a unique task ID.
var taskIDCounter atomic.Int64

func NewTaskID() string {
	n := taskIDCounter.Add(1)
	return fmt.Sprintf("T-%03d", n)
}

// ResetTaskIDCounter resets the counter (useful for tests).
func ResetTaskIDCounter() {
	taskIDCounter.Store(0)
}

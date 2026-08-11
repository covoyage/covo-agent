package kanban

import (
	"strings"
	"testing"
	"time"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to TaskStatus
		want     bool
	}{
		{StatusBacklog, StatusTodo, true},
		{StatusTodo, StatusInProgress, true},
		{StatusInProgress, StatusReview, true},
		{StatusInProgress, StatusDone, true},
		{StatusDone, StatusInProgress, true},
		{StatusReview, StatusInProgress, true},
		{StatusDone, StatusCancelled, false},
		{StatusCancelled, StatusDone, false},
		{StatusInProgress, StatusBacklog, false},
		{StatusBlocked, StatusInProgress, true},
		{StatusInProgress, StatusBlocked, true},
		{StatusTodo, StatusTodo, false}, // self-transition handled by UpdateTask, not CanTransition
		{StatusDone, StatusTodo, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"→"+string(tt.to), func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestNewTaskID(t *testing.T) {
	ResetTaskIDCounter()

	id1 := NewTaskID()
	id2 := NewTaskID()
	id3 := NewTaskID()

	if id1 == id2 || id2 == id3 {
		t.Error("task IDs should be unique")
	}
	if len(id1) != 5 || id1[:2] != "T-" {
		t.Errorf("unexpected ID format: %q", id1)
	}
}

func TestBoard_NewBoard(t *testing.T) {
	b := NewBoard("board-1", "test board", t.TempDir())
	if b.Name != "test board" {
		t.Errorf("Name = %q, want %q", b.Name, "test board")
	}
	if b.Tasks == nil {
		t.Error("Tasks map should be initialized")
	}
}

func TestBoard_AddTask(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())

	task, err := b.AddTask(Task{Title: "my task", Description: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "my task" {
		t.Errorf("Title = %q", task.Title)
	}
	if task.Status != StatusTodo {
		t.Errorf("default Status = %q, want %q", task.Status, StatusTodo)
	}
	if task.Priority != PriorityMedium {
		t.Errorf("default Priority = %q, want %q", task.Priority, PriorityMedium)
	}
	if task.ID == "" {
		t.Error("ID should be auto-generated")
	}
}

func TestBoard_AddTaskDuplicateID(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	b.AddTask(Task{ID: "t1", Title: "first"})
	_, err := b.AddTask(Task{ID: "t1", Title: "second"})
	if err == nil {
		t.Error("expected error for duplicate ID")
	}
}

func TestBoard_GetTask(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "find me"})

	got, ok := b.GetTask(added.ID)
	if !ok {
		t.Fatal("GetTask returned false")
	}
	if got.Title != "find me" {
		t.Errorf("Title = %q", got.Title)
	}

	_, ok = b.GetTask("nonexistent")
	if ok {
		t.Error("GetTask should return false for nonexistent ID")
	}
}

func TestBoard_UpdateTask(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "original", Priority: PriorityLow})

	updated, err := b.UpdateTask(added.ID, map[string]interface{}{
		"title":    "updated",
		"priority": "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "updated" {
		t.Errorf("Title = %q", updated.Title)
	}
	if updated.Priority != PriorityHigh {
		t.Errorf("Priority = %q", updated.Priority)
	}
}

func TestBoard_UpdateTaskStatusTransition(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "task"})

	updated, err := b.UpdateTask(added.ID, map[string]interface{}{
		"status": "in_progress",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusInProgress {
		t.Errorf("Status = %q", updated.Status)
	}

	// Invalid transition
	_, err = b.UpdateTask(added.ID, map[string]interface{}{
		"status": "backlog",
	})
	if err == nil {
		t.Error("expected error for invalid transition in_progress→backlog")
	}
}

func TestBoard_UpdateTaskNoopTransition(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "task"})

	_, err := b.UpdateTask(added.ID, map[string]interface{}{
		"status": "todo", // same as default
	})
	if err != nil {
		t.Errorf("no-op transition should be allowed: %v", err)
	}
}

func TestBoard_UpdateTaskCompletedAt(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "task"})

	// Move to in_progress first, then done — CompletedAt should be set
	b.UpdateTask(added.ID, map[string]interface{}{"status": "in_progress"})
	done, _ := b.UpdateTask(added.ID, map[string]interface{}{"status": "done"})
	if done.CompletedAt == nil || done.CompletedAt.IsZero() {
		t.Error("CompletedAt should be set when moving to done")
	}
	completedTime := *done.CompletedAt

	// Re-open — CompletedAt should be cleared
	reopened, _ := b.UpdateTask(added.ID, map[string]interface{}{"status": "in_progress"})
	if reopened.CompletedAt != nil {
		t.Error("CompletedAt should be cleared when re-opening from done")
	}

	// Complete again — CompletedAt should be newer
	b.UpdateTask(added.ID, map[string]interface{}{"status": "done"})
	redone, _ := b.GetTask(added.ID)
	if redone.CompletedAt == nil || redone.CompletedAt.Equal(completedTime) {
		t.Error("CompletedAt should be updated on re-completion")
	}
}

func TestBoard_UpdateTaskNotFound(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	_, err := b.UpdateTask("nonexistent", map[string]interface{}{"title": "nope"})
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestBoard_BlockUnblockTask(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "task"})
	b.UpdateTask(added.ID, map[string]interface{}{"status": "in_progress"})

	blocked, err := b.BlockTask(added.ID, "blocked by external dependency")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != StatusBlocked {
		t.Errorf("Status = %q, want %q", blocked.Status, StatusBlocked)
	}
	if len(blocked.Notes) == 0 {
		t.Error("Notes should be recorded on block")
	}
	if !contains(blocked.Notes[0], "blocked") {
		t.Errorf("Note should mention blocked: %q", blocked.Notes[0])
	}

	unblocked, err := b.UnblockTask(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unblocked.Status == StatusBlocked {
		t.Errorf("Status should not be blocked after unblock")
	}
}

func TestBoard_RemoveTask(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	parent, _ := b.AddTask(Task{Title: "parent"})
	child, _ := b.AddTask(Task{Title: "child"})
	b.UpdateTask(child.ID, map[string]interface{}{"parent_id": parent.ID})

	// Link tasks
	b.LinkTask(parent.ID, child.ID)

	if err := b.RemoveTask(parent.ID); err != nil {
		t.Fatal(err)
	}

	// Verify parent is gone
	if _, ok := b.GetTask(parent.ID); ok {
		t.Error("parent should be removed")
	}

	// Verify child's ParentID was cleared
	c, ok := b.GetTask(child.ID)
	if !ok {
		t.Fatal("child should still exist")
	}
	if c.ParentID != "" {
		t.Errorf("child.ParentID should be cleared after parent removal, got %q", c.ParentID)
	}
}

func TestBoard_RemoveTaskCleansDepsAndLinks(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	a, _ := b.AddTask(Task{Title: "A"})
	b2, _ := b.AddTask(Task{Title: "B"})

	// B depends on A, and links to A
	b.UpdateTask(b2.ID, map[string]interface{}{"depends_on": []string{a.ID}})
	b.LinkTask(b2.ID, a.ID)

	// Remove A
	if err := b.RemoveTask(a.ID); err != nil {
		t.Fatal(err)
	}

	// B should no longer reference A
	gotB, _ := b.GetTask(b2.ID)
	for _, dep := range gotB.DependsOn {
		if dep == a.ID {
			t.Error("B.DependsOn should not contain removed task A")
		}
	}
	for _, link := range gotB.LinkedTo {
		if link == a.ID {
			t.Error("B.LinkedTo should not contain removed task A")
		}
	}
}

func TestBoard_AddComment(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "task"})

	updated, err := b.AddComment(added.ID, "this is a comment")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Notes) != 1 {
		t.Errorf("Notes len = %d, want 1", len(updated.Notes))
	}
	if !contains(updated.Notes[0], "comment") || !contains(updated.Notes[0], "this is a comment") {
		t.Errorf("Note[0] = %q", updated.Notes[0])
	}
}

func TestBoard_HeartbeatTask(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "task"})
	original := added.UpdatedAt

	time.Sleep(2 * time.Millisecond)
	updated, err := b.HeartbeatTask(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UpdatedAt.Equal(original) {
		t.Error("UpdatedAt should have advanced")
	}
}

func TestBoard_LinkTask(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	a, _ := b.AddTask(Task{Title: "A"})
	b2, _ := b.AddTask(Task{Title: "B"})

	_, err := b.LinkTask(a.ID, b2.ID)
	if err != nil {
		t.Fatal(err)
	}

	gotA, _ := b.GetTask(a.ID)
	if len(gotA.LinkedTo) != 1 || gotA.LinkedTo[0] != b2.ID {
		t.Errorf("A.LinkedTo = %v", gotA.LinkedTo)
	}
}

func TestBoard_TasksByStatus(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	t1, _ := b.AddTask(Task{Title: "todo1"})
	t2, _ := b.AddTask(Task{Title: "todo2"})
	ip, _ := b.AddTask(Task{Title: "in prog"})
	b.UpdateTask(ip.ID, map[string]interface{}{"status": "in_progress"})

	todos := b.TasksByStatus(StatusTodo)
	if len(todos) != 2 {
		t.Errorf("todo tasks = %d, want 2 (ids: %v)", len(todos), todos)
	}
	progs := b.TasksByStatus(StatusInProgress)
	if len(progs) != 1 {
		t.Errorf("in_progress tasks = %d, want 1", len(progs))
	}
	// Verify correct IDs
	todoIDs := make(map[string]bool)
	for _, t := range todos {
		todoIDs[t.ID] = true
	}
	if !todoIDs[t1.ID] || !todoIDs[t2.ID] {
		t.Errorf("todo tasks should include %s and %s", t1.ID, t2.ID)
	}
	if progs[0].ID != ip.ID {
		t.Errorf("in_progress task should be %s, got %s", ip.ID, progs[0].ID)
	}
}

func TestBoard_BlockedTasks(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	added, _ := b.AddTask(Task{Title: "b"})
	b.UpdateTask(added.ID, map[string]interface{}{"status": "in_progress"})
	b.BlockTask(added.ID, "reason")

	blocked := b.BlockedTasks()
	if len(blocked) != 1 {
		t.Errorf("blocked tasks = %d, want 1", len(blocked))
	}
}

func TestBoard_ReadyTasks(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	a, _ := b.AddTask(Task{Title: "A"})
	b2, _ := b.AddTask(Task{Title: "B", DependsOn: []string{a.ID}})
	c, _ := b.AddTask(Task{Title: "C", DependsOn: []string{"nonexistent"}})

	ready := b.ReadyTasks()
	readyIDs := make(map[string]bool)
	for _, r := range ready {
		readyIDs[r.ID] = true
	}

	if !readyIDs[a.ID] {
		t.Error("A should be ready (no deps)")
	}
	if readyIDs[b2.ID] {
		t.Error("B should not be ready (depends on A, but A is not done)")
	}
	if readyIDs[c.ID] {
		t.Error("C should NOT be ready (dangling dep IS blocking per isDependencyBlocked)")
	}
}

func TestBoard_RootTasks(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	a, _ := b.AddTask(Task{Title: "A"})
	b2, _ := b.AddTask(Task{Title: "B"})
	c, _ := b.AddTask(Task{Title: "C"})

	b.UpdateTask(b2.ID, map[string]interface{}{"parent_id": a.ID})
	b.UpdateTask(c.ID, map[string]interface{}{"parent_id": "nonexistent"})

	roots := b.RootTasks()
	rootIDs := make(map[string]bool)
	for _, r := range roots {
		rootIDs[r.ID] = true
	}

	if !rootIDs[a.ID] {
		t.Error("A should be a root task")
	}
	if rootIDs[b2.ID] {
		t.Errorf("B (%s) should not be a root task (child of A)", b2.ID)
	}
	if !rootIDs[c.ID] {
		t.Errorf("C (%s) should be a root task (dangling parent treated as orphan)", c.ID)
	}
}

func TestBoard_TaskTree(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	a, _ := b.AddTask(Task{Title: "A"})
	b2, _ := b.AddTask(Task{Title: "B"})
	c, _ := b.AddTask(Task{Title: "C"})

	b.UpdateTask(b2.ID, map[string]interface{}{"parent_id": a.ID})
	b.UpdateTask(c.ID, map[string]interface{}{"parent_id": "nonexistent"})

	tree := b.TaskTree()
	treeIDs := make(map[string]bool)
	var collect func(nodes []*TaskTreeNode)
	collect = func(nodes []*TaskTreeNode) {
		for _, n := range nodes {
			treeIDs[n.Task.ID] = true
			collect(n.Children)
		}
	}
	collect(tree)

	if !treeIDs[a.ID] {
		t.Error("A should be in tree")
	}
	if !treeIDs[b2.ID] {
		t.Error("B should be in tree (child of A)")
	}
	if !treeIDs[c.ID] {
		t.Error("C should be in tree (orphan with dangling parent)")
	}
}

func TestBoard_Progress(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	b.AddTask(Task{Title: "a"})
	b.AddTask(Task{Title: "b"})
	b.AddTask(Task{Title: "c", Status: StatusInProgress})
	b.AddTask(Task{Title: "d", Status: StatusDone})
	b.AddTask(Task{Title: "e", Status: StatusCancelled})

	p := b.Progress()
	if p.Total != 5 {
		t.Errorf("Total = %d, want 5", p.Total)
	}
	if p.Done != 1 {
		t.Errorf("Done = %d, want 1", p.Done)
	}
	if p.Cancelled != 1 {
		t.Errorf("Cancelled = %d, want 1", p.Cancelled)
	}
	if p.InProgress != 1 {
		t.Errorf("InProgress = %d, want 1", p.InProgress)
	}
}

func TestBoard_SwarmPlan(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	a, _ := b.AddTask(Task{Title: "A"})           // no deps → ready
	b2, _ := b.AddTask(Task{Title: "B", DependsOn: []string{a.ID}}) // depends on A → blocked
	c, _ := b.AddTask(Task{Title: "C"})           // no deps → ready

	plan := b.SwarmPlan()
	if plan == nil {
		t.Fatal("SwarmPlan returned nil")
	}

	// A and C should be in independent groups (no deps)
	independentIDs := make(map[string]bool)
	for _, group := range plan.Independent {
		for _, id := range group {
			independentIDs[id] = true
		}
	}
	if !independentIDs[a.ID] {
		t.Errorf("A (%s) should be in independent group", a.ID)
	}
	if !independentIDs[c.ID] {
		t.Errorf("C (%s) should be in independent group", c.ID)
	}

	// B should be sequential (depends on A, which is not done)
	seqSet := make(map[string]bool)
	for _, id := range plan.Sequential {
		seqSet[id] = true
	}
	if !seqSet[b2.ID] {
		t.Errorf("B (%s) should be sequential", b2.ID)
	}
	if len(plan.Sequential) != 1 {
		t.Errorf("Sequential = %v, want just [%s]", plan.Sequential, b2.ID)
	}
}

func TestBoard_Summary(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	b.AddTask(Task{Title: "task"})

	summary := b.Summary()
	if summary == "" {
		t.Error("Summary should not be empty")
	}
	if !contains(summary, "Kanban") {
		t.Errorf("Summary should contain 'Kanban': %q", summary)
	}
}

func TestBoard_FormatForSystemPrompt(t *testing.T) {
	b := NewBoard("b1", "test", t.TempDir())
	b.AddTask(Task{Title: "my task"})

	formatted := b.FormatForSystemPrompt()
	if !contains(formatted, "my task") {
		t.Errorf("FormatForSystemPrompt should contain task title: %q", formatted)
	}
}

func TestBoard_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	b := NewBoard("board-save-test", "save test", dir)
	b.AddTask(Task{Title: "persisted"})

	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadBoard("board-save-test", dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "save test" {
		t.Errorf("Name = %q", loaded.Name)
	}
	if len(loaded.Tasks) != 1 {
		t.Errorf("Tasks = %d, want 1", len(loaded.Tasks))
	}
}

func TestBoard_SaveLocked(t *testing.T) {
	dir := t.TempDir()
	b := NewBoard("board-locked", "locked", dir)
	b.AddTask(Task{Title: "test"})

	if err := b.SaveLocked(); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadBoard("board-locked", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tasks) != 1 {
		t.Errorf("Tasks = %d, want 1", len(loaded.Tasks))
	}
}

func TestKanbanManager_GetOrCreateBoard(t *testing.T) {
	km := NewKanbanManager(t.TempDir())

	b, err := km.GetOrCreateBoard("my-board")
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "my-board" {
		t.Errorf("Name = %q", b.Name)
	}

	// Second call returns same board
	b2, err := km.GetOrCreateBoard("my-board")
	if err != nil {
		t.Fatal(err)
	}
	if b2 != b {
		t.Error("second GetOrCreateBoard should return same board")
	}

	active := km.ActiveBoard()
	if active != b {
		t.Error("ActiveBoard should return the last created board")
	}
}

func TestKanbanManager_ActiveBoardEmpty(t *testing.T) {
	km := NewKanbanManager(t.TempDir())
	if b := km.ActiveBoard(); b != nil {
		t.Error("ActiveBoard should be nil when no board created")
	}
}

func TestBoard_Concurrency(t *testing.T) {
	b := NewBoard("b-con", "concurrent", t.TempDir())

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			b.AddTask(Task{Title: "task"})
			b.Progress()
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 50; i++ {
			b.AddTask(Task{Title: "task"})
			b.Progress()
		}
		done <- struct{}{}
	}()

	<-done
	<-done

	if len(b.Tasks) != 100 {
		t.Errorf("total tasks = %d, want 100", len(b.Tasks))
	}
}

func FuzzCanTransition(f *testing.F) {
	f.Add("todo", "in_progress")
	f.Add("done", "todo")
	f.Add("in_progress", "backlog")
	f.Add("", "todo")
	f.Add("invalid", "done")
	f.Fuzz(func(t *testing.T, from, to string) {
		CanTransition(TaskStatus(from), TaskStatus(to))
	})
}

func FuzzTaskStatus(f *testing.F) {
	f.Add("backlog")
	f.Add("todo")
	f.Add("in_progress")
	f.Add("review")
	f.Add("done")
	f.Add("blocked")
	f.Add("cancelled")
	f.Add("")
	f.Add("INVALID")
	f.Add("in progress")
	f.Fuzz(func(t *testing.T, s string) {
		ts := TaskStatus(s)
		ts.IsTerminal()  // must not panic
	})
}

func BenchmarkBoardConcurrency(b *testing.B) {
	board := NewBoard("bench", "bench", b.TempDir())
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			board.AddTask(Task{Title: "bench"})
			board.Progress()
		}
	})
}

func BenchmarkBoardSaveAndLoad(b *testing.B) {
	dir := b.TempDir()
	board := NewBoard("bench-save", "bench", dir)
	for i := 0; i < 100; i++ {
		board.AddTask(Task{Title: "task"})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := board.Save(); err != nil {
			b.Fatal(err)
		}
		if _, err := LoadBoard("bench-save", dir); err != nil {
			b.Fatal(err)
		}
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// IsTerminal returns true if the task status is a terminal (unchangeable) state.
func (s TaskStatus) IsTerminal() bool {
	return s == StatusDone || s == StatusCancelled
}

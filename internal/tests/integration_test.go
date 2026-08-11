package tests

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/covoyage/covo-agent/internal/goal"
	"github.com/covoyage/covo-agent/internal/kanban"
)

func setupGoalDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestGoalAndKanbanIntegration(t *testing.T) {
	ctx := context.Background()

	// --- Goal side ---
	db := setupGoalDB(t)
	store, err := goal.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	g := &goal.Goal{
		SessionID:   "session-1",
		GoalID:      "goal-1",
		Objective:   "implement login feature",
		Status:      goal.StatusActive,
		TokenBudget: int64Ptr(10000),
	}
	if err := store.Put(ctx, g); err != nil {
		t.Fatal(err)
	}

	// --- Kanban side ---
	boardDir := t.TempDir()
	b := kanban.NewBoard("board-1", "implement login feature", boardDir)

	task1, err := b.AddTask(kanban.Task{Title: "design login UI"})
	if err != nil {
		t.Fatal(err)
	}
	task2, err := b.AddTask(kanban.Task{Title: "implement auth API"})
	if err != nil {
		t.Fatal(err)
	}
	b.LinkTask(task1.ID, task2.ID)

	// --- Progress tasks: todo → in_progress → done ---
	b.UpdateTask(task1.ID, map[string]interface{}{"status": "in_progress"})
	b.UpdateTask(task1.ID, map[string]interface{}{"status": "done"})
	b.UpdateTask(task2.ID, map[string]interface{}{"status": "in_progress"})

	// --- Record usage against goal ---
	acct := goal.NewAccounting(store)
	outcome, err := acct.RecordUsage(ctx, "session-1", 500, 120, goal.AccountingActiveOnly, strPtr("goal-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Updated {
		t.Error("expected goal to be updated")
	}

	// --- Verify goal state ---
	gotGoal, err := store.Get(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotGoal.TokensUsed != 500 {
		t.Errorf("TokensUsed = %d, want 500", gotGoal.TokensUsed)
	}

	// --- Verify kanban progress ---
	progress := b.Progress()
	if progress.Done != 1 {
		t.Errorf("Done = %d, want 1", progress.Done)
	}
	if progress.InProgress != 1 {
		t.Errorf("InProgress = %d, want 1", progress.InProgress)
	}

	// --- Verify board persistence ---
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := kanban.LoadBoard("board-1", boardDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tasks) != 2 {
		t.Errorf("loaded tasks = %d, want 2", len(loaded.Tasks))
	}
}

func int64Ptr(v int64) *int64 { return &v }
func strPtr(v string) *string { return &v }

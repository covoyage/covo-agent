package goal

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	db := setupTestDB(t)
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		s    Status
		want bool
	}{
		{StatusActive, false},
		{StatusPaused, false},
		{StatusBlocked, false},
		{StatusUsageLimited, false},
		{StatusBudgetLimited, true},
		{StatusComplete, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.s), func(t *testing.T) {
			if got := tt.s.IsTerminal(); got != tt.want {
				t.Errorf("IsTerminal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatus_IsStopped(t *testing.T) {
	tests := []struct {
		s    Status
		want bool
	}{
		{StatusActive, false},
		{StatusPaused, true},
		{StatusBlocked, true},
		{StatusUsageLimited, true},
		{StatusBudgetLimited, true},
		{StatusComplete, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.s), func(t *testing.T) {
			if got := tt.s.IsStopped(); got != tt.want {
				t.Errorf("IsStopped() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccountingState_StartTurn(t *testing.T) {
	as := NewAccountingState()
	as.StartTurn("turn-1", TokenUsage{}, false)
	// StartTurn initializes the turn but does not set planMode — enabled by default
	if as.PlanMode() {
		t.Error("PlanMode should be false after StartTurn with planMode=false")
	}
}

func TestAccountingState_MarkGoalActive(t *testing.T) {
	as := NewAccountingState()
	as.MarkGoalActive("goal-1")
	if g := as.ActiveGoalID(); g != "goal-1" {
		t.Errorf("ActiveGoalID = %q, want %q", g, "goal-1")
	}
}

func TestAccountingState_ClearActiveGoal(t *testing.T) {
	as := NewAccountingState()
	as.MarkGoalActive("goal-1")
	as.ClearActiveGoal()
	if g := as.ActiveGoalID(); g != "" {
		t.Errorf("ActiveGoalID = %q, want empty", g)
	}
}

func TestAccountingState_SteeringInjected(t *testing.T) {
	as := NewAccountingState()

	// First call returns true
	if !as.SteeringInjected("continuation") {
		t.Error("first SteeringInjected should return true")
	}

	// Second call returns false (dedup)
	if as.SteeringInjected("continuation") {
		t.Error("second SteeringInjected should return false (dedup)")
	}

	// Different kind should still return true
	if !as.SteeringInjected("objective_change") {
		t.Error("SteeringInjected for different kind should return true")
	}
}

func TestAccountingState_ResetSteering(t *testing.T) {
	as := NewAccountingState()
	as.SteeringInjected("continuation")
	as.ResetSteering()

	if !as.SteeringInjected("continuation") {
		t.Error("after ResetSteering, SteeringInjected should return true")
	}
}

func TestAccountingState_PlanMode(t *testing.T) {
	as := NewAccountingState()
	as.StartTurn("turn-1", TokenUsage{}, true)
	if !as.PlanMode() {
		t.Error("PlanMode should be true when started with planMode=true")
	}

	as.StartTurn("turn-2", TokenUsage{}, false)
	if as.PlanMode() {
		t.Error("PlanMode should be false when started with planMode=false")
	}
}

func TestAccountingState_TurnIsCurrentActiveGoal(t *testing.T) {
	as := NewAccountingState()
	as.StartTurn("turn-1", TokenUsage{}, false)
	as.MarkGoalActive("goal-1")

	if !as.TurnIsCurrentActiveGoal("turn-1", "goal-1") {
		t.Error("TurnIsCurrentActiveGoal should match")
	}
	if as.TurnIsCurrentActiveGoal("turn-1", "goal-2") {
		t.Error("TurnIsCurrentActiveGoal should not match different goal")
	}
	if as.TurnIsCurrentActiveGoal("turn-2", "goal-1") {
		t.Error("TurnIsCurrentActiveGoal should not match different turn")
	}
}

func TestAccountingState_BudgetLimitReported(t *testing.T) {
	as := NewAccountingState()

	// First call with new goalID returns true (first time, go ahead)
	if !as.BudgetLimitReported("goal-1") {
		t.Error("first BudgetLimitReported should return true")
	}

	// Second call with same goalID returns false (already reported, skip)
	if as.BudgetLimitReported("goal-1") {
		t.Error("second BudgetLimitReported should return false")
	}

	// Different goalID returns true
	if !as.BudgetLimitReported("goal-2") {
		t.Error("BudgetLimitReported for different goal should return true")
	}

	// After clear, same goalID returns true again
	as.ClearBudgetLimitReported()
	if !as.BudgetLimitReported("goal-2") {
		t.Error("BudgetLimitReported after ClearBudgetLimitReported should return true")
	}
}

func TestGoalTokenDelta(t *testing.T) {
	tests := []struct {
		name  string
		usage *TokenUsage
		want  int64
	}{
		{"nil", nil, 0},
		{"zero", &TokenUsage{}, 0},
		{"output only", &TokenUsage{OutputTokens: 50}, 50},
		{"input+output", &TokenUsage{InputTokens: 100, OutputTokens: 50}, 150},
		{"cached excluded", &TokenUsage{InputTokens: 300, CachedInputTokens: 200, OutputTokens: 50}, 150},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GoalTokenDelta(tt.usage); got != tt.want {
				t.Errorf("GoalTokenDelta = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAccountingState_FinishTurn(t *testing.T) {
	as := NewAccountingState()
	as.StartTurn("turn-1", TokenUsage{}, false)

	// Record token usage
	as.RecordTokenUsage("turn-1", TokenUsage{InputTokens: 10, OutputTokens: 5})

	// Snapshot before FinishTurn
	snap := as.Snapshot("turn-1")
	if snap == nil {
		t.Fatal("Snapshot should not be nil before FinishTurn")
	}
	if snap.TokenDelta != 15 {
		t.Errorf("TokenDelta = %d, want 15", snap.TokenDelta)
	}

	as.FinishTurn("turn-1")

	// After FinishTurn, snapshot should be nil (turn deleted)
	if snap2 := as.Snapshot("turn-1"); snap2 != nil {
		t.Error("Snapshot after FinishTurn should be nil")
	}
}

func TestAccountingState_ConcurrentSafe(t *testing.T) {
	as := NewAccountingState()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			as.SteeringInjected("kind")
			as.MarkGoalActive("g")
			as.StartTurn("t", TokenUsage{}, false)
			as.FinishTurn("t")
		}()
	}
	wg.Wait()
}

func TestShouldClearActiveGoal(t *testing.T) {
	tests := []struct {
		name  string
		disp  BudgetLimitDisposition
		state Status
		want  bool
	}{
		{"active+keep", BudgetKeepActive, StatusActive, false},
		{"active+clear", BudgetClearActive, StatusActive, false}, // NEVER clears active
		{"complete+clear", BudgetClearActive, StatusComplete, true},
		{"usage_limited+clear", BudgetClearActive, StatusUsageLimited, true},
		{"budget_limited+clear", BudgetClearActive, StatusBudgetLimited, true},
		{"budget_limited+keep", BudgetKeepActive, StatusBudgetLimited, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldClearActiveGoal(tt.state, tt.disp); got != tt.want {
				t.Errorf("ShouldClearActiveGoal(%q, %d) = %v, want %v",
					tt.state, tt.disp, got, tt.want)
			}
		})
	}
}

// --- Store tests ---

func TestStore_PutAndGet(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	g := &Goal{
		SessionID: "session-1",
		GoalID:    "goal-1",
		Objective: "test objective",
		Status:    StatusActive,
	}
	if err := s.Put(ctx, g); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.GoalID != "goal-1" || got.Objective != "test objective" {
		t.Errorf("Get = %+v, want GoalID=goal-1 Objective=test objective", got)
	}
}

func TestStore_PutUpdateExisting(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	g := &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj1", Status: StatusActive}
	if err := s.Put(ctx, g); err != nil {
		t.Fatal(err)
	}

	g.Objective = "obj2"
	g.Status = StatusComplete
	if err := s.Put(ctx, g); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Objective != "obj2" || got.Status != StatusComplete {
		t.Errorf("after update: %+v", got)
	}
}

func TestStore_GetNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	got, err := s.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("Get = %v, want nil", got)
	}
}

func TestStore_Update(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj1", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}

	obj := "updated objective"
	st := StatusPaused
	updated, err := s.Update(ctx, "s1", Update{
		Objective: &obj,
		Status:    &st,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Objective != "updated objective" || updated.Status != StatusPaused {
		t.Errorf("Update = %+v", updated)
	}

	// Verify nil fields don't overwrite
	tok := int64(500)
	ptr := &tok
	updated2, err := s.Update(ctx, "s1", Update{
		TokenBudget: &ptr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated2.TokenBudget == nil || *updated2.TokenBudget != 500 {
		t.Errorf("TokenBudget = %v, want 500", updated2.TokenBudget)
	}
	if updated2.Objective != "updated objective" {
		t.Errorf("Objective overwritten: %q", updated2.Objective)
	}
}

func TestStore_UpdateNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	g, err := s.Update(ctx, "nonexistent", Update{})
	if err != nil {
		t.Fatal(err)
	}
	if g != nil {
		t.Error("expected nil goal for nonexistent session")
	}
}

func TestStore_Complete(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj", Status: StatusActive})

	g, err := s.Complete(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusComplete {
		t.Errorf("Status = %q, want %q", g.Status, StatusComplete)
	}
}

func TestStore_PauseResume(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj", Status: StatusActive})

	g, err := s.Pause(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusPaused {
		t.Errorf("after Pause: %q", g.Status)
	}

	g, err = s.Resume(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusActive {
		t.Errorf("after Resume: %q", g.Status)
	}
}

func TestStore_BlockAndResume(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj", Status: StatusActive})

	g, err := s.Block(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusBlocked {
		t.Errorf("after Block: %q", g.Status)
	}

	g, err = s.Resume(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusActive {
		t.Errorf("after Resume from blocked: %q", g.Status)
	}

	// Resume from paused should go to active too
	s.Pause(ctx, "s1")
	g, _ = s.Resume(ctx, "s1")
	if g.Status != StatusActive {
		t.Errorf("after Resume from paused: %q", g.Status)
	}
}

func TestStore_UsageLimit(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj", Status: StatusActive})

	g, err := s.UsageLimit(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusUsageLimited {
		t.Errorf("Status = %q, want %q", g.Status, StatusUsageLimited)
	}
}

func TestStore_Delete(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj", Status: StatusActive})

	if _, err := s.Delete(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "s1")
	if got != nil {
		t.Errorf("Get after Delete = %v, want nil", got)
	}
}

func TestStore_PutIfComplete(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	// Complete goal: insert succeeds
	g := &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj", Status: StatusComplete}
	ok, err := s.PutIfComplete(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("PutIfComplete should return true for complete goal")
	}

	// New non-complete goal: INSERT always succeeds (no conflict)
	g2 := &Goal{SessionID: "s2", GoalID: "g2", Objective: "new", Status: StatusActive}
	ok, err = s.PutIfComplete(ctx, g2)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("PutIfComplete should succeed for new row (no conflict)")
	}
	got, _ := s.Get(ctx, "s2")
	if got == nil {
		t.Fatal("new row should have been inserted")
	}
}

func TestStore_ListActive(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "a", Status: StatusActive})
	s.Put(ctx, &Goal{SessionID: "s2", GoalID: "g2", Objective: "b", Status: StatusPaused})
	s.Put(ctx, &Goal{SessionID: "s3", GoalID: "g3", Objective: "c", Status: StatusComplete})

	active, err := s.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Errorf("ListActive returned %d goals, want 2", len(active))
	}
}

func TestStore_ResumeFromUsageLimited(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj", Status: StatusUsageLimited})

	g, err := s.Resume(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusActive {
		t.Errorf("after Resume from usage_limited: %q", g.Status)
	}
}

func TestStore_CompleteCannotTransitionToNonTerminal(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	s.Put(ctx, &Goal{SessionID: "s1", GoalID: "g1", Objective: "obj", Status: StatusComplete})

	// Pause uses WHERE status IN ('active','paused'), so complete goal is unaffected
	g, err := s.Pause(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g != nil {
		t.Error("expected nil goal Pausing a completed goal")
	}
}

// --- Steering tests ---

func TestSteering_ContinuationPrompt(t *testing.T) {
	steer := NewSteering()
	g := &Goal{
		GoalID:    "g1",
		Objective: "fix the login bug",
		Status:    StatusActive,
	}
	prompt := steer.ContinuationPrompt(g)
	if !strings.Contains(prompt, "fix the login bug") {
		t.Errorf("prompt should contain objective: %q", prompt)
	}
	if !strings.Contains(prompt, "Continue working toward") {
		t.Errorf("prompt should have continuation header: %q", prompt)
	}
}

func TestSteering_BudgetLimitWarning(t *testing.T) {
	steer := NewSteering()
	budget := int64(1000)
	used := int64(950)

	t.Run("with budget", func(t *testing.T) {
		g := &Goal{
			GoalID:      "g1",
			Objective:   "test",
			Status:      StatusActive,
			TokenBudget: &budget,
			TokensUsed:  used,
		}
		warn := steer.BudgetLimitWarning(g)
		if !strings.Contains(warn, "Token budget: 1000") {
			t.Errorf("warning should mention budget: %q", warn)
		}
	})

	t.Run("nil budget", func(t *testing.T) {
		g := &Goal{
			GoalID:    "g2",
			Objective: "test",
			Status:    StatusActive,
		}
		warn := steer.BudgetLimitWarning(g)
		// Should not panic, and budget_limited goals with zero or nil budget still get a warning.
		if warn == "" {
			t.Error("expected non-empty warning even with nil budget")
		}
	})
}

func TestSteering_ObjectiveChanged(t *testing.T) {
	steer := NewSteering()
	g := &Goal{
		GoalID:    "g1",
		Objective: "new objective",
		Status:    StatusActive,
	}
	prompt := steer.ObjectiveChanged(g)
	if !strings.Contains(prompt, "new objective") {
		t.Errorf("prompt should contain new objective: %q", prompt)
	}
}

// --- Accounting tests ---

func TestAccounting_RecordUsage(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	a := NewAccounting(s)

	s.Put(ctx, &Goal{
		SessionID:   "s1",
		GoalID:      "g1",
		Objective:   "test",
		Status:      StatusActive,
		TokenBudget: int64Ptr(100),
	})

	outcome, err := a.RecordUsage(ctx, "s1", 30, 10, AccountingActiveOnly, strPtr("g1"))
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Updated {
		t.Error("expected Updated=true")
	}
	if outcome.BudgetExceeded {
		t.Error("expected BudgetExceeded=false (30 < 100)")
	}
}

func TestAccounting_RecordUsageBudgetExceeded(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	a := NewAccounting(s)

	s.Put(ctx, &Goal{
		SessionID:   "s1",
		GoalID:      "g1",
		Objective:   "test",
		Status:      StatusActive,
		TokenBudget: int64Ptr(100),
	})

	outcome, err := a.RecordUsage(ctx, "s1", 120, 10, AccountingActiveOnly, strPtr("g1"))
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Updated {
		t.Error("expected Updated=true")
	}
	if !outcome.BudgetExceeded {
		t.Error("expected BudgetExceeded=true (120 >= 100)")
	}

	// Verify the goal was marked budget_limited
	g, _ := s.Get(ctx, "s1")
	if g.Status != StatusBudgetLimited {
		t.Errorf("status = %q, want %q", g.Status, StatusBudgetLimited)
	}
}

func TestAccounting_RecordUsageNoGoal(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	a := NewAccounting(s)

	outcome, err := a.RecordUsage(ctx, "nonexistent", 30, 10, AccountingActiveOnly, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Updated {
		t.Error("expected Updated=false for nonexistent goal")
	}
	if outcome.BudgetExceeded {
		t.Error("expected BudgetExceeded=false")
	}
}

func TestAccounting_RecordUsageErrorPropagated(t *testing.T) {
	// Use a closed DB to force an error
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = NewStore(db)
	if err == nil {
		t.Error("expected error creating store with closed DB")
	}
}

func TestAccounting_RecordUsageNoBudget(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	a := NewAccounting(s)

	s.Put(ctx, &Goal{
		SessionID: "s1",
		GoalID:    "g1",
		Objective: "test",
		Status:    StatusActive,
	})

	outcome, err := a.RecordUsage(ctx, "s1", 9999, 10, AccountingActiveOnly, strPtr("g1"))
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Updated {
		t.Error("expected Updated=true")
	}
	if outcome.BudgetExceeded {
		t.Error("expected BudgetExceeded=false (no budget set)")
	}
}

func TestAccountingModeToSQL(t *testing.T) {
	tests := []struct {
		mode AccountingMode
		part string
	}{
		{AccountingActiveStatusOnly, "active"},
		{AccountingActiveOnly, "active"},
		{AccountingActiveOrComplete, "active"},
		{AccountingActiveOrStopped, "active"},
		{"invalid", "active"},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			sql := modeToSQL(tt.mode)
			if !strings.Contains(sql, tt.part) {
				t.Errorf("modeToSQL(%q) = %q, should contain %q", tt.mode, sql, tt.part)
			}
		})
	}
}

func FuzzGoalTokenDelta(f *testing.F) {
	f.Add(int64(0), int64(0), int64(0))
	f.Add(int64(100), int64(50), int64(20))
	f.Add(int64(-10), int64(5), int64(0))
	f.Fuzz(func(t *testing.T, input, output, cached int64) {
		GoalTokenDelta(&TokenUsage{
			InputTokens:       input,
			OutputTokens:      output,
			CachedInputTokens: cached,
		})
	})
}

func FuzzModeToSQL(f *testing.F) {
	f.Add("active")
	f.Add("active_or_complete")
	f.Add("active_or_stopped")
	f.Add("")
	f.Add("invalid\x00")
	f.Add("  ")
	f.Fuzz(func(t *testing.T, mode string) {
		modeToSQL(AccountingMode(mode))
	})
}

func int64Ptr(v int64) *int64 { return &v }
func strPtr(v string) *string { return &v }

package workflow

import (
	"context"
	"testing"
)

func TestJournal_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/journal.json"

	j1, err := NewJournal(path, "wf-1")
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}

	j1.RecordResult(PhaseResult{
		PhaseID: "phase-1",
		Status:  PhaseCompleted,
		Output:  "done",
		Turns:   3,
		Tokens:  500,
	})
	j1.SetCurrentPhase(1)
	j1.Save()

	// Load
	j2, err := NewJournal(path, "wf-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if j2.CurrentPhase != 1 {
		t.Errorf("expected current phase 1, got %d", j2.CurrentPhase)
	}
	if len(j2.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(j2.Results))
	}
	if j2.Results[0].PhaseID != "phase-1" {
		t.Error("wrong phase ID")
	}
	if j2.TotalTurns != 3 {
		t.Errorf("expected 3 turns, got %d", j2.TotalTurns)
	}
}

func TestJournal_PauseResume(t *testing.T) {
	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", "wf-1")

	if j.IsPaused() {
		t.Error("should not be paused initially")
	}

	j.SetPaused(true)
	if !j.IsPaused() {
		t.Error("should be paused")
	}

	j.SetPaused(false)
	if j.IsPaused() {
		t.Error("should not be paused")
	}
}

func TestJournal_CanResume(t *testing.T) {
	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", "wf-1")

	if j.CanResume() {
		t.Error("should not be resumable initially")
	}

	j.RecordResult(PhaseResult{PhaseID: "p1", Status: PhaseCompleted})
	if !j.CanResume() {
		t.Error("should be resumable after results")
	}
}

func TestJournal_Reset(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/journal.json"
	j, _ := NewJournal(path, "wf-1")

	j.RecordResult(PhaseResult{PhaseID: "p1", Status: PhaseCompleted})
	j.Save()

	j.Reset()

	if j.CurrentPhase != 0 {
		t.Error("expected phase 0 after reset")
	}
	if len(j.Results) != 0 {
		t.Error("expected no results after reset")
	}
}

func TestExecutor_RunSimple(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Prompt: "Do task 1"},
			{ID: "p2", Name: "Phase 2", Prompt: "Do task 2"},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	executed := []string{}
	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		executed = append(executed, phase.ID)
		return &PhaseResult{
			PhaseID: phase.ID,
			Status:  PhaseCompleted,
			Output:  "result for " + phase.ID,
			Turns:   1,
		}, nil
	})

	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(executed) != 2 {
		t.Fatalf("expected 2 phases executed, got %d", len(executed))
	}
}

func TestExecutor_ConditionSkip(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Prompt: "Do task 1"},
			{ID: "p2", Name: "Phase 2", Prompt: "Do task 2", Condition: "false"},
			{ID: "p3", Name: "Phase 3", Prompt: "Do task 3"},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	executed := []string{}
	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		executed = append(executed, phase.ID)
		return &PhaseResult{
			PhaseID: phase.ID,
			Status:  PhaseCompleted,
		}, nil
	})

	exec.Run(context.Background())

	if len(executed) != 2 {
		t.Fatalf("expected 2 phases (p2 skipped), got %d", len(executed))
	}
	if executed[0] != "p1" || executed[1] != "p3" {
		t.Errorf("expected p1 and p3, got %v", executed)
	}
}

func TestExecutor_ConditionDependsOn(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Prompt: "Do task 1"},
			{ID: "p2", Name: "Phase 2", Prompt: "Do task 2", Condition: "p1.status == completed"},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	executed := []string{}
	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		executed = append(executed, phase.ID)
		return &PhaseResult{
			PhaseID: phase.ID,
			Status:  PhaseCompleted,
		}, nil
	})

	exec.Run(context.Background())

	// Both should run because p1 completed
	if len(executed) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(executed))
	}
}

func TestExecutor_ConditionNotMet(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Prompt: "Do task 1"},
			{ID: "p2", Name: "Phase 2", Prompt: "Do task 2", Condition: "p1.status == failed"},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	executed := []string{}
	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		executed = append(executed, phase.ID)
		return &PhaseResult{
			PhaseID: phase.ID,
			Status:  PhaseCompleted,
		}, nil
	})

	exec.Run(context.Background())

	// p1 runs, p2 is skipped (p1 completed, not failed)
	if len(executed) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(executed))
	}
}

func TestExecutor_PauseAfter(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Prompt: "Do task 1", PauseAfter: true},
			{ID: "p2", Name: "Phase 2", Prompt: "Do task 2"},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	executed := []string{}
	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		executed = append(executed, phase.ID)
		return &PhaseResult{PhaseID: phase.ID, Status: PhaseCompleted}, nil
	})

	// First run should pause after p1
	err := exec.Run(context.Background())
	if err == nil {
		t.Fatal("expected pause error")
	}
	if len(executed) != 1 {
		t.Fatalf("expected 1 phase, got %d", len(executed))
	}

	// Resume should continue with p2
	err = exec.Resume(context.Background())
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if len(executed) != 2 {
		t.Fatalf("expected 2 phases after resume, got %d", len(executed))
	}
}

func TestExecutor_SkipOnError(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Prompt: "Do task 1", SkipOnError: true},
			{ID: "p2", Name: "Phase 2", Prompt: "Do task 2"},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	executed := []string{}
	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		if phase.ID == "p1" {
			executed = append(executed, phase.ID)
			return nil, context.DeadlineExceeded
		}
		executed = append(executed, phase.ID)
		return &PhaseResult{PhaseID: phase.ID, Status: PhaseCompleted}, nil
	})

	err := exec.Run(context.Background())
	if err != nil {
		t.Fatalf("expected no error with SkipOnError, got %v", err)
	}
	if len(executed) != 2 {
		t.Fatalf("expected both phases, got %d", len(executed))
	}
}

func TestExecutor_BudgetExhausted(t *testing.T) {
	wf := &Workflow{
		ID:              "test-wf",
		Name:            "Test Workflow",
		MaxTotalTurns:   2,
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Prompt: "Do task 1"},
			{ID: "p2", Name: "Phase 2", Prompt: "Do task 2"},
			{ID: "p3", Name: "Phase 3", Prompt: "Do task 3"},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		return &PhaseResult{PhaseID: phase.ID, Status: PhaseCompleted, Turns: 2}, nil
	})

	err := exec.Run(context.Background())
	if err == nil {
		t.Fatal("expected budget error")
	}
}

func TestExecutor_OutputSchema(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{
				ID:     "p1",
				Name:   "Phase 1",
				Prompt: "Do task 1",
				OutputSchema: map[string]string{
					"status": "string",
					"count":  "number",
				},
			},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		return &PhaseResult{
			PhaseID: phase.ID,
			Status:  PhaseCompleted,
			Output:  `{"status": "ok", "count": 42}`,
		}, nil
	})

	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestExecutor_OutputSchema_Invalid(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{
				ID:     "p1",
				Name:   "Phase 1",
				Prompt: "Do task 1",
				OutputSchema: map[string]string{
					"status": "string",
				},
			},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		return &PhaseResult{
			PhaseID: phase.ID,
			Status:  PhaseCompleted,
			Output:  `{"count": 42}`, // missing "status"
		}, nil
	})

	err := exec.Run(context.Background())
	if err == nil {
		t.Fatal("expected schema validation error")
	}
}

func TestExecutor_Resume(t *testing.T) {
	wf := &Workflow{
		ID:   "test-wf",
		Name: "Test Workflow",
		Phases: []Phase{
			{ID: "p1", Name: "Phase 1", Prompt: "Do task 1"},
			{ID: "p2", Name: "Phase 2", Prompt: "Do task 2"},
		},
	}

	dir := t.TempDir()
	j, _ := NewJournal(dir+"/journal.json", wf.ID)

	executed := []string{}
	exec := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		executed = append(executed, phase.ID)
		return &PhaseResult{PhaseID: phase.ID, Status: PhaseCompleted}, nil
	})

	// Run p1, save journal
	exec.Run(context.Background())
	executed1 := len(executed)

	// Create new executor with same journal
	exec2 := NewExecutor(wf, j, func(ctx context.Context, phase Phase) (*PhaseResult, error) {
		executed = append(executed, phase.ID)
		return &PhaseResult{PhaseID: phase.ID, Status: PhaseCompleted}, nil
	})

	exec2.Run(context.Background())

	// Should not re-run p1
	if len(executed) != executed1 {
		t.Errorf("expected %d total executions, got %d", executed1, len(executed))
	}
}

func TestPhaseStatus_String(t *testing.T) {
	if PhasePending.String() != "pending" {
		t.Error("bad string")
	}
	if PhaseRunning.String() != "running" {
		t.Error("bad string")
	}
	if PhaseCompleted.String() != "completed" {
		t.Error("bad string")
	}
	if PhaseFailed.String() != "failed" {
		t.Error("bad string")
	}
	if PhaseSkipped.String() != "skipped" {
		t.Error("bad string")
	}
	if PhasePaused.String() != "paused" {
		t.Error("bad string")
	}
}

func TestDefaultConditionEvaluator(t *testing.T) {
	results := []PhaseResult{
		{PhaseID: "p1", Status: PhaseCompleted, Output: "done"},
	}

	// Literal true
	ok, _ := defaultConditionEvaluator("true", results)
	if !ok {
		t.Error("expected true")
	}

	// Literal false
	ok, _ = defaultConditionEvaluator("false", results)
	if ok {
		t.Error("expected false")
	}

	// Phase status check
	ok, _ = defaultConditionEvaluator("p1.status == completed", results)
	if !ok {
		t.Error("expected true for completed status")
	}

	ok, _ = defaultConditionEvaluator("p1.status == failed", results)
	if ok {
		t.Error("expected false for failed check on completed phase")
	}

	ok, _ = defaultConditionEvaluator("p1.status != failed", results)
	if !ok {
		t.Error("expected true for != failed")
	}

	// Phase not run
	ok, _ = defaultConditionEvaluator("p2.status == completed", results)
	if ok {
		t.Error("expected false for unrun phase")
	}

	// Empty condition
	ok, _ = defaultConditionEvaluator("", results)
	if !ok {
		t.Error("expected true for empty condition")
	}
}

func TestValidateOutputSchema(t *testing.T) {
	schema := map[string]string{
		"name":  "string",
		"age":   "number",
		"admin": "bool",
		"tags":  "array",
		"meta":  "object",
	}

	output := `{
		"name": "test",
		"age": 30,
		"admin": true,
		"tags": ["a", "b"],
		"meta": {"key": "val"}
	}`

	if err := validateOutputSchema(output, schema); err != nil {
		t.Errorf("expected valid: %v", err)
	}

	// Missing field
	badOutput := `{"name": "test"}`
	if err := validateOutputSchema(badOutput, schema); err == nil {
		t.Error("expected error for missing fields")
	}

	// Wrong type
	wrongType := `{"name": 123, "age": 30, "admin": true, "tags": [], "meta": {}}`
	if err := validateOutputSchema(wrongType, schema); err == nil {
		t.Error("expected error for wrong type")
	}

	// Non-JSON output should pass
	if err := validateOutputSchema("plain text", schema); err != nil {
		t.Errorf("expected non-JSON to pass: %v", err)
	}
}

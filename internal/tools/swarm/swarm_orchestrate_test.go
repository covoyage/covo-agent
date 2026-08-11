package swarm

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// taskLabel extracts the single-letter task title from the sub-task text the
// runner receives ("Task: <title>\n\n..."). Checks the prefix only so that
// dependency outputs embedded in the prompt don't cause false matches.
func taskLabel(full string) string {
	for _, l := range []string{"A", "B", "C"} {
		if strings.HasPrefix(full, "Task: "+l+"\n") {
			return l
		}
	}
	return "?"
}

// newTestOrchestrator builds an orchestrator with a runner that records the
// order in which tasks start/finish.
func newTestOrchestrator(t *testing.T, onRun func(label string)) *SwarmOrchestrator {
	t.Helper()
	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		if onRun != nil {
			onRun(taskLabel(task))
		}
		return "result for: " + task, nil
	}
	return NewSwarmOrchestrator(runner, nil, func() []string { return nil })
}

func TestSwarmOrchestratorRespectsDependencies(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	started := map[string]time.Time{}
	finished := map[string]time.Time{}

	orch := newTestOrchestrator(t, func(label string) {
		mu.Lock()
		started[label] = time.Now()
		order = append(order, label)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		finished[label] = time.Now()
		mu.Unlock()
	})

	// Plan: B depends on A; C is independent.
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "test",
		Tasks: []OrchestrationTask{
			{ID: "a", Title: "A", Description: "task A", Status: "queued"},
			{ID: "b", Title: "B", Description: "task B", Status: "queued", DependsOn: []string{"a"}},
			{ID: "c", Title: "C", Description: "task C", Status: "queued"},
		},
	}

	res, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["completed"].(int) != 3 {
		t.Fatalf("expected 3 completed, got %v", m["completed"])
	}

	// B must not start until A has finished.
	mu.Lock()
	defer mu.Unlock()
	aFin, ok1 := finished["A"]
	bStart, ok2 := started["B"]
	if !ok1 || !ok2 {
		t.Fatalf("missing timing data: %v %v", finished, started)
	}
	if bStart.Before(aFin) {
		t.Errorf("dependency violated: B started (%v) before A finished (%v)", bStart, aFin)
	}
}

func TestSwarmOrchestratorBlocksOnFailedDependency(t *testing.T) {
	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		if taskLabel(task) == "A" {
			return "", context.Canceled // make A fail
		}
		return "ok", nil
	}
	orch := NewSwarmOrchestrator(runner, nil, func() []string { return nil })
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "test",
		Tasks: []OrchestrationTask{
			{ID: "a", Title: "A", Description: "task A", Status: "queued"},
			{ID: "b", Title: "B", Description: "task B", Status: "queued", DependsOn: []string{"a"}},
		},
	}

	res, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["failed"].(int) != 1 {
		t.Errorf("expected 1 failed (A), got %v", m["failed"])
	}
	if m["blocked"].(int) != 1 {
		t.Errorf("expected 1 blocked (B), got %v", m["blocked"])
	}
}

// TestSwarmOrchestratorConcurrentStatus exercises runPlan while planStatus is
// read concurrently — intended to be run with -race to catch data races on the
// shared task state.
func TestSwarmOrchestratorConcurrentStatus(t *testing.T) {
	orch := newTestOrchestrator(t, func(task string) {
		time.Sleep(5 * time.Millisecond)
	})
	tasks := make([]OrchestrationTask, 20)
	for i := range tasks {
		tasks[i] = OrchestrationTask{ID: string(rune('a' + i)), Title: "T", Description: "t", Status: "queued"}
	}
	orch.plans["p1"] = &OrchestrationPlan{Goal: "g", Tasks: tasks}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_, _ = orch.planStatus("p1")
				_, _ = orch.planResults("p1")
			}
		}
	}()

	if _, err := orch.runPlan(context.Background(), "p1"); err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	close(done)
}

// TestSwarmOrchestratorCreatePlanWithTasks verifies the plan action accepts an
// explicit task breakdown with dependencies, assigns ids, and that the plan
// then executes honouring those dependencies.
func TestSwarmOrchestratorCreatePlanWithTasks(t *testing.T) {
	orch := newTestOrchestrator(t, nil)

	res, err := orch.createPlan("build feature", []OrchestrationTask{
		{Title: "A", Description: "task A"}, // id auto-assigned
		{ID: "b", Title: "B", Description: "task B", DependsOn: []string{"a"}},
	})
	if err != nil {
		t.Fatalf("createPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["task_count"].(int) != 2 {
		t.Fatalf("expected 2 tasks, got %v", m["task_count"])
	}
	planID := m["plan_id"].(string)

	// Dangling dependency must be dropped, real one kept.
	orch.mu.Lock()
	plan := orch.plans[planID]
	if len(plan.Tasks) != 2 {
		orch.mu.Unlock()
		t.Fatalf("expected 2 tasks stored, got %d", len(plan.Tasks))
	}
	if plan.Tasks[0].ID == "" {
		orch.mu.Unlock()
		t.Fatal("first task id should have been auto-assigned")
	}
	orch.mu.Unlock()

	runRes, err := orch.runPlan(context.Background(), planID)
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	rm := runRes.(map[string]any)
	if rm["completed"].(int) != 2 {
		t.Errorf("expected 2 completed, got %v", rm["completed"])
	}
}

func TestSwarmOrchestratorCreatePlanDefaultsToSingleTask(t *testing.T) {
	orch := newTestOrchestrator(t, nil)
	res, err := orch.createPlan("just do it", nil)
	if err != nil {
		t.Fatalf("createPlan: %v", err)
	}
	if res.(map[string]any)["task_count"].(int) != 1 {
		t.Errorf("expected single default task, got %v", res.(map[string]any)["task_count"])
	}
}

// TestSwarmOrchestratorCreatePlanRejectsDuplicateTaskIDs verifies Bug 1:
// createPlan must reject duplicate explicit task IDs instead of silently
// overwriting them (which would make statusOf/outputOf match only the first).
func TestSwarmOrchestratorCreatePlanRejectsDuplicateTaskIDs(t *testing.T) {
	orch := newTestOrchestrator(t, nil)

	_, err := orch.createPlan("dup ids", []OrchestrationTask{
		{ID: "a", Title: "A", Description: "first"},
		{ID: "a", Title: "A duplicate", Description: "second"},
	})
	if err == nil {
		t.Fatal("expected error for duplicate task IDs, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate task ID") {
		t.Errorf("expected duplicate task ID error, got: %v", err)
	}

	// Auto-assigned IDs (no explicit id) must still be unique and not trip the
	// check.
	res, err := orch.createPlan("auto ids", []OrchestrationTask{
		{Title: "A"},
		{Title: "B"},
	})
	if err != nil {
		t.Fatalf("auto-assigned ids should not collide: %v", err)
	}
	if res.(map[string]any)["task_count"].(int) != 2 {
		t.Errorf("expected 2 tasks with auto ids, got %v", res.(map[string]any)["task_count"])
	}
}

// TestSwarmOrchestratorDetectsCyclicDependency verifies Bug 2: when tasks form
// a dependency cycle (A depends on B, B depends on A) neither can ever run, so
// they must be detected and marked "blocked" instead of being silently left
// forever "queued" (and missing from the final tally).
func TestSwarmOrchestratorDetectsCyclicDependency(t *testing.T) {
	ran := 0
	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		ran++
		return "ok", nil
	}
	orch := NewSwarmOrchestrator(runner, nil, func() []string { return nil })
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "cyclic",
		Tasks: []OrchestrationTask{
			{ID: "a", Title: "A", Description: "task A", Status: "queued", DependsOn: []string{"b"}},
			{ID: "b", Title: "B", Description: "task B", Status: "queued", DependsOn: []string{"a"}},
		},
	}

	res, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["blocked"].(int) != 2 {
		t.Errorf("expected 2 blocked (cyclic deps), got blocked=%v completed=%v failed=%v",
			m["blocked"], m["completed"], m["failed"])
	}
	if m["completed"].(int) != 0 {
		t.Errorf("expected 0 completed, got %v", m["completed"])
	}
	if ran != 0 {
		t.Errorf("expected no task to actually run, got %d runs", ran)
	}

	// Both tasks must carry the cyclic-dependency error.
	orch.mu.Lock()
	defer orch.mu.Unlock()
	plan := orch.plans["p1"]
	for _, tk := range plan.Tasks {
		if tk.Status != "blocked" {
			t.Errorf("task %q status=%q, want blocked", tk.ID, tk.Status)
		}
		if !strings.Contains(tk.Error, "cyclic or unsatisfiable dependency") {
			t.Errorf("task %q error=%q, want cyclic dependency message", tk.ID, tk.Error)
		}
	}
}

// TestSwarmOrchestratorCyclicAlongsideCompletableTasks verifies Bug 2 mixed
// with the normal path: an independent task completes while the cyclic pair is
// marked blocked, and the counts add up.
func TestSwarmOrchestratorCyclicAlongsideCompletableTasks(t *testing.T) {
	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		return "ok", nil
	}
	orch := NewSwarmOrchestrator(runner, nil, func() []string { return nil })
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "mixed",
		Tasks: []OrchestrationTask{
			{ID: "c", Title: "C", Description: "independent", Status: "queued"},
			{ID: "a", Title: "A", Description: "task A", Status: "queued", DependsOn: []string{"b"}},
			{ID: "b", Title: "B", Description: "task B", Status: "queued", DependsOn: []string{"a"}},
		},
	}

	res, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["completed"].(int) != 1 {
		t.Errorf("expected 1 completed (C), got %v", m["completed"])
	}
	if m["blocked"].(int) != 2 {
		t.Errorf("expected 2 blocked (A,B cyclic), got %v", m["blocked"])
	}
}

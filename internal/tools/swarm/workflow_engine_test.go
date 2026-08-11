package swarm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// --- validateStructuredOutput tests ---

func TestValidateStructuredOutput_Text(t *testing.T) {
	out, err := validateStructuredOutput("hello world", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Errorf("expected same output, got %q", out)
	}
}

func TestValidateStructuredOutput_EmptyFormat(t *testing.T) {
	out, err := validateStructuredOutput("hello world", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Errorf("expected same output, got %q", out)
	}
}

func TestValidateStructuredOutput_JSON(t *testing.T) {
	out, err := validateStructuredOutput(`{"key": "value"}`, "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return the validated JSON
	var js map[string]string
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if js["key"] != "value" {
		t.Errorf("expected key=value, got %v", js)
	}
}

func TestValidateStructuredOutput_JSONFencedBlock(t *testing.T) {
	input := "Here's the result:\n```json\n{\"name\": \"test\"}\n```\nDone."
	out, err := validateStructuredOutput(input, "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var js map[string]string
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatalf("extracted output is not valid JSON: %v", err)
	}
	if js["name"] != "test" {
		t.Errorf("expected name=test, got %v", js)
	}
}

func TestValidateStructuredOutput_JSONPlainFence(t *testing.T) {
	input := "Result:\n```\n{\"x\": 42}\n```\n"
	out, err := validateStructuredOutput(input, "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var js map[string]int
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatalf("extracted output is not valid JSON: %v", err)
	}
	if js["x"] != 42 {
		t.Errorf("expected x=42, got %v", js)
	}
}

func TestValidateStructuredOutput_InvalidJSON(t *testing.T) {
	_, err := validateStructuredOutput("not json at all", "json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("expected JSON validation error, got: %v", err)
	}
}

func TestValidateStructuredOutput_UnknownFormat(t *testing.T) {
	_, err := validateStructuredOutput("data", "yaml")
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

// --- buildTaskPromptWithInputs tests ---

func TestBuildTaskPromptWithInputs_NoDeps(t *testing.T) {
	task := OrchestrationTask{Title: "My Task", Description: "Do something"}
	prompt := buildTaskPromptWithInputs(task, "goal", nil)

	if !strings.Contains(prompt, "Task: My Task") {
		t.Error("expected task title in prompt")
	}
	if !strings.Contains(prompt, "Do something") {
		t.Error("expected task description in prompt")
	}
	if strings.Contains(prompt, "Inputs from Previous") {
		t.Error("should not have inputs section when no deps")
	}
}

func TestBuildTaskPromptWithInputs_WithDeps(t *testing.T) {
	task := OrchestrationTask{
		Title:       "Implement",
		Description: "Build the feature",
		DependsOn:   []string{"design"},
	}
	depOutputs := map[string]string{
		"design": `{"api": "GET /users"}`,
	}

	prompt := buildTaskPromptWithInputs(task, "goal", depOutputs)

	if !strings.Contains(prompt, "Inputs from Previous Tasks") {
		t.Error("expected inputs section")
	}
	if !strings.Contains(prompt, `{"api": "GET /users"}`) {
		t.Error("expected dependency output in prompt")
	}
	if !strings.Contains(prompt, "Output from task \"design\"") {
		t.Error("expected dependency task ID label")
	}
	if !strings.Contains(prompt, "Your Task") {
		t.Error("expected 'Your Task' section")
	}
}

func TestBuildTaskPromptWithInputs_TruncatesLongOutput(t *testing.T) {
	task := OrchestrationTask{
		Title:     "T",
		DependsOn: []string{"dep"},
	}
	longOutput := strings.Repeat("x", 20000)
	depOutputs := map[string]string{"dep": longOutput}

	prompt := buildTaskPromptWithInputs(task, "goal", depOutputs)

	if strings.Contains(prompt, strings.Repeat("x", 10000)) {
		t.Error("expected long output to be truncated")
	}
	if !strings.Contains(prompt, "[truncated]") {
		t.Error("expected truncation marker")
	}
}

// --- WorkflowJournal tests ---

func TestWorkflowJournal_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	j := NewWorkflowJournal(dir)

	plan := &OrchestrationPlan{
		Goal: "test goal",
		Tasks: []OrchestrationTask{
			{ID: "t1", Title: "Task 1", Status: "completed", Result: "done"},
			{ID: "t2", Title: "Task 2", Status: "queued"},
		},
	}

	if err := j.Save(plan, "plan_1"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := j.Load("plan_1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded plan, got nil")
	}
	if loaded.Goal != "test goal" {
		t.Errorf("expected goal 'test goal', got %q", loaded.Goal)
	}
	if len(loaded.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(loaded.Tasks))
	}
	if loaded.Tasks[0].Status != "completed" {
		t.Errorf("expected task 1 completed, got %q", loaded.Tasks[0].Status)
	}
	if loaded.Tasks[0].Result != "done" {
		t.Errorf("expected result 'done', got %q", loaded.Tasks[0].Result)
	}
}

func TestWorkflowJournal_LoadNonExistent(t *testing.T) {
	dir := t.TempDir()
	j := NewWorkflowJournal(dir)

	loaded, err := j.Load("nonexistent")
	if err != nil {
		t.Fatalf("expected nil error for non-existent journal, got: %v", err)
	}
	if loaded != nil {
		t.Errorf("expected nil plan for non-existent journal, got %v", loaded)
	}
}

func TestWorkflowJournal_Delete(t *testing.T) {
	dir := t.TempDir()
	j := NewWorkflowJournal(dir)

	plan := &OrchestrationPlan{Goal: "g", Tasks: []OrchestrationTask{{ID: "t1", Title: "T1"}}}
	_ = j.Save(plan, "plan_x")

	// Verify it exists
	loaded, _ := j.Load("plan_x")
	if loaded == nil {
		t.Fatal("expected plan to exist before delete")
	}

	j.Delete("plan_x")

	// Verify it's gone
	loaded, _ = j.Load("plan_x")
	if loaded != nil {
		t.Error("expected nil after delete")
	}
}

func TestWorkflowJournal_SanitizesPlanID(t *testing.T) {
	dir := t.TempDir()
	j := NewWorkflowJournal(dir)

	// Plan ID with special characters that could be path-unsafe
	plan := &OrchestrationPlan{Goal: "g", Tasks: []OrchestrationTask{{ID: "t1", Title: "T1"}}}
	if err := j.Save(plan, "plan_1/../../../etc"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The file should be sanitized — no path traversal
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "..") {
			t.Errorf("found path-unsafe filename: %s", e.Name())
		}
	}
}

// --- End-to-end orchestration with output passing ---

func TestSwarmOrchestrator_PassesDependencyOutput(t *testing.T) {
	var capturedTaskB string

	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		if strings.HasPrefix(task, "Task: B\n") {
			capturedTaskB = task
		}
		// Task A returns structured output
		if strings.HasPrefix(task, "Task: A\n") {
			return `{"design": "API spec"}`, nil
		}
		return "result", nil
	}

	orch := NewSwarmOrchestrator(runner, nil, func() []string { return nil })
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "build feature",
		Tasks: []OrchestrationTask{
			{ID: "a", Title: "A", Description: "Design API", Status: "queued", OutputFormat: "json"},
			{ID: "b", Title: "B", Description: "Implement", Status: "queued", DependsOn: []string{"a"}},
		},
	}

	res, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["completed"].(int) != 2 {
		t.Fatalf("expected 2 completed, got %v", m["completed"])
	}

	// Task B should have received A's output as input
	if capturedTaskB == "" {
		t.Fatal("task B was not captured")
	}
	if !strings.Contains(capturedTaskB, "Inputs from Previous Tasks") {
		t.Error("expected task B prompt to contain dependency inputs")
	}
	if !strings.Contains(capturedTaskB, `{"design": "API spec"}`) {
		t.Error("expected task B prompt to contain A's JSON output")
	}
}

func TestSwarmOrchestrator_StructuredOutputValidation_Failure(t *testing.T) {
	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		// Return non-JSON for a task that requires JSON output
		return "this is not json", nil
	}

	orch := NewSwarmOrchestrator(runner, nil, func() []string { return nil })
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "test",
		Tasks: []OrchestrationTask{
			{ID: "t1", Title: "T1", Description: "produce JSON", Status: "queued", OutputFormat: "json"},
		},
	}

	res, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["failed"].(int) != 1 {
		t.Errorf("expected 1 failed (JSON validation), got failed=%v completed=%v", m["failed"], m["completed"])
	}
}

func TestSwarmOrchestrator_StructuredOutputValidation_Success(t *testing.T) {
	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		return `{"result": "success"}`, nil
	}

	orch := NewSwarmOrchestrator(runner, nil, func() []string { return nil })
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "test",
		Tasks: []OrchestrationTask{
			{ID: "t1", Title: "T1", Description: "produce JSON", Status: "queued", OutputFormat: "json"},
		},
	}

	res, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["completed"].(int) != 1 {
		t.Errorf("expected 1 completed, got %v", m["completed"])
	}

	// Verify OutputJSON is populated
	plan := orch.plans["p1"]
	if plan.Tasks[0].OutputJSON == "" {
		t.Error("expected OutputJSON to be populated")
	}
}

// --- Journal resume test ---

func TestSwarmOrchestrator_JournalResume(t *testing.T) {
	dir := t.TempDir()
	j := NewWorkflowJournal(dir)

	runCount := 0
	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		runCount++
		return "result", nil
	}

	orch := NewSwarmOrchestrator(runner, nil, func() []string { return nil })
	orch.SetJournal(j)
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "test",
		Tasks: []OrchestrationTask{
			{ID: "t1", Title: "T1", Description: "task 1", Status: "queued"},
			{ID: "t2", Title: "T2", Description: "task 2", Status: "queued"},
		},
	}

	// Simulate a partial journal: t1 already completed
	partial := &OrchestrationPlan{
		Goal: "test",
		Tasks: []OrchestrationTask{
			{ID: "t1", Title: "T1", Status: "completed", Result: "old result"},
			{ID: "t2", Title: "T2", Status: "queued"},
		},
	}
	_ = j.Save(partial, "p1")

	// Run — should skip t1 (already completed in journal) and only run t2
	res, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}
	m := res.(map[string]any)
	if m["completed"].(int) != 2 {
		t.Errorf("expected 2 completed, got %v", m["completed"])
	}

	// Only t2 should have been actually executed (t1 was resumed from journal)
	if runCount != 1 {
		t.Errorf("expected 1 actual run (t2 only), got %d", runCount)
	}

	// Journal should be cleaned up after successful completion
	loaded, _ := j.Load("p1")
	if loaded != nil {
		t.Error("expected journal to be deleted after completion")
	}
}

func TestSwarmOrchestrator_JournalCheckpointPersisted(t *testing.T) {
	dir := t.TempDir()
	j := NewWorkflowJournal(dir)

	runner := func(ctx context.Context, task string, toolsetNames []string, maxTurns int) (string, error) {
		return "done", nil
	}

	orch := NewSwarmOrchestrator(runner, nil, func() []string { return nil })
	orch.SetJournal(j)
	orch.plans["p1"] = &OrchestrationPlan{
		Goal: "test",
		Tasks: []OrchestrationTask{
			{ID: "t1", Title: "T1", Description: "task 1", Status: "queued"},
			{ID: "t2", Title: "T2", Description: "task 2", Status: "queued", DependsOn: []string{"t1"}},
		},
	}

	// Run to completion
	_, err := orch.runPlan(context.Background(), "p1")
	if err != nil {
		t.Fatalf("runPlan: %v", err)
	}

	// After completion, journal should be cleaned up
	loaded, _ := j.Load("p1")
	if loaded != nil {
		t.Error("expected journal to be cleaned up after successful completion")
	}
}

func TestNewWorkflowJournal_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "workflows")
	j := NewWorkflowJournal(dir)

	// Directory should be created
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("expected journal directory to be created")
	}

	// Should be able to save
	plan := &OrchestrationPlan{Goal: "g", Tasks: []OrchestrationTask{{ID: "t1", Title: "T1"}}}
	if err := j.Save(plan, "p1"); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// --- Bug-fix regression tests ---

// TestExtractJSONBlock_CRLFLineEndings verifies Bug 3: Windows-style \r\n line
// endings no longer prevent fenced JSON extraction.
func TestExtractJSONBlock_CRLFLineEndings(t *testing.T) {
	input := "Result:\r\n```json\r\n{\"a\": 1}\r\n```\r\nDone."
	out := extractJSONBlock(input)
	if out != `{"a": 1}` {
		t.Errorf("expected extracted JSON with \\r\\n fences, got %q", out)
	}
}

// TestExtractJSONBlock_UppercaseJSONFence verifies Bug 3: an uppercase (or
// mixed-case) ```JSON fence is accepted case-insensitively.
func TestExtractJSONBlock_UppercaseJSONFence(t *testing.T) {
	cases := map[string]string{
		"uppercase": "Result:\n```JSON\n{\"b\": 2}\n```\n",
		"mixedcase": "```Json\n{\"c\": 3}\n```\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			out := extractJSONBlock(input)
			if out == "" {
				t.Fatalf("expected JSON extraction, got empty string for input %q", input)
			}
			var js map[string]int
			if err := json.Unmarshal([]byte(out), &js); err != nil {
				t.Fatalf("extracted content is not valid JSON: %v (out=%q)", err, out)
			}
		})
	}
}

// TestExtractJSONBlock_PrefersJSONFenceOverPlain verifies the json-fence-first
// preference is preserved after the rewrite (regression guard).
func TestExtractJSONBlock_PrefersJSONFenceOverPlain(t *testing.T) {
	input := "```\nplain\n```\n```json\n{\"k\": 1}\n```\n"
	out := extractJSONBlock(input)
	if out != `{"k": 1}` {
		t.Errorf("expected json-fenced block to win over plain fence, got %q", out)
	}
}

// TestBuildTaskPromptWithInputs_TruncatesCJKRunes verifies Bug 5: rune-aware
// truncation never splits a multi-byte UTF-8 sequence (the old byte-based
// slice could corrupt CJK content).
func TestBuildTaskPromptWithInputs_TruncatesCJKRunes(t *testing.T) {
	task := OrchestrationTask{
		Title:     "T",
		DependsOn: []string{"dep"},
	}

	// 6000 CJK runes = 18000 bytes. Under the 8000-rune limit, so the content
	// must be preserved whole (the old byte-based code truncated at 8000 bytes).
	cjk := strings.Repeat("你", 6000)
	prompt := buildTaskPromptWithInputs(task, "goal", map[string]string{"dep": cjk})
	if !utf8.ValidString(prompt) {
		t.Errorf("prompt is not valid UTF-8")
	}
	if !strings.Contains(prompt, cjk) {
		t.Error("expected full CJK content (under rune limit) to be preserved")
	}

	// 10000 CJK runes = 30000 bytes — exceeds the 8000-rune limit, so it must be
	// truncated, and the truncated result must still be valid UTF-8.
	big := strings.Repeat("你", 10000)
	prompt2 := buildTaskPromptWithInputs(task, "goal", map[string]string{"dep": big})
	if !utf8.ValidString(prompt2) {
		t.Errorf("truncated CJK prompt is not valid UTF-8")
	}
	if !strings.Contains(prompt2, "[truncated]") {
		t.Error("expected truncation marker for CJK content over the rune limit")
	}
	if strings.Contains(prompt2, big) {
		t.Error("expected the oversized CJK content to be truncated, not passed through")
	}
}

// TestWorkflowJournal_NoTmpRemainsAfterSuccess verifies Bug 4 (normal path): a
// successful Save leaves no .tmp file behind.
func TestWorkflowJournal_NoTmpRemainsAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	j := NewWorkflowJournal(dir)

	plan := &OrchestrationPlan{Goal: "g", Tasks: []OrchestrationTask{{ID: "t1", Title: "T1"}}}
	if err := j.Save(plan, "plan_ok"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("unexpected tmp file left behind after successful Save: %s", e.Name())
		}
	}
}

// TestWorkflowJournal_CleansUpTmpOnRenameFailure verifies Bug 4 (failure path):
// when os.Rename fails, the orphaned .tmp file is removed rather than left on
// disk. The rename is forced to fail by making the target path a non-empty
// directory (renaming a file over a non-empty directory fails on Unix).
func TestWorkflowJournal_CleansUpTmpOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	j := NewWorkflowJournal(dir)

	// The sanitized path for plan_id "plan_fail" is <dir>/plan_fail.json.
	// Make it a non-empty directory so the rename step cannot replace it.
	targetDir := filepath.Join(dir, "plan_fail.json")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "blocker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile blocker: %v", err)
	}

	plan := &OrchestrationPlan{Goal: "g", Tasks: []OrchestrationTask{{ID: "t1", Title: "T1"}}}
	err := j.Save(plan, "plan_fail")
	if err == nil {
		t.Skip("rename did not fail on this platform; cannot verify tmp cleanup on the failure path")
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Fatalf("expected a rename error, got: %v", err)
	}

	// The temp file must have been cleaned up by the defer.
	tmpPath := filepath.Join(dir, "plan_fail.json.tmp")
	if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
		t.Errorf("expected tmp file %s to be removed after rename failure, stat err=%v", tmpPath, statErr)
	}
}

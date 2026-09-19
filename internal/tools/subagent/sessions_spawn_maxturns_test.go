package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// spawnToolForTest builds a sessions_spawn tool with a capturing raw runner.
func spawnToolForTest(t *testing.T, capture func(maxTurns int)) *agentcore.Tool {
	t.Helper()
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		capture(maxTurns)
		return "done", nil
	}
	return BuildSessionsSpawnTool(runner, nil,
		func() []string { return nil },
		func() []agentcore.Message { return nil },
		nil, nil)
}

// TestSessionsSpawnMaxTurnsUnlimitedByDefault verifies that omitting
// max_turns passes no budget (0) to the runner instead of coercing a cap.
func TestSessionsSpawnMaxTurnsUnlimitedByDefault(t *testing.T) {
	var got int
	tool := spawnToolForTest(t, func(maxTurns int) { got = maxTurns })

	args := `{"task": "analyze the codebase"}`
	res, err := tool.Func(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Func: %v", err)
	}
	if res.(map[string]any)["status"] != "completed" {
		t.Fatalf("unexpected result: %v", res)
	}
	if got != 0 {
		t.Fatalf("expected maxTurns=0 (unlimited) when omitted, got %d", got)
	}
}

// TestSessionsSpawnMaxTurnsExplicit verifies an explicit max_turns is passed
// through verbatim so the model can bound cost when it chooses to.
func TestSessionsSpawnMaxTurnsExplicit(t *testing.T) {
	var got int
	tool := spawnToolForTest(t, func(maxTurns int) { got = maxTurns })

	args := `{"task": "quick lookup", "max_turns": 7}`
	if _, err := tool.Func(context.Background(), json.RawMessage(args)); err != nil {
		t.Fatalf("Func: %v", err)
	}
	if got != 7 {
		t.Fatalf("expected maxTurns=7 passed through, got %d", got)
	}
}

func TestSessionsSpawnInjectsRoleSystemPrompt(t *testing.T) {
	var gotTask string
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		gotTask = task
		return "done", nil
	}
	tool := BuildSessionsSpawnTool(runner, nil,
		func() []string { return nil },
		func() []agentcore.Message { return nil },
		nil, nil)

	args := `{"task": "map the auth package", "role": "explorer", "background": false}`
	if _, err := tool.Func(context.Background(), json.RawMessage(args)); err != nil {
		t.Fatalf("Func: %v", err)
	}
	if !strings.Contains(gotTask, "read-only file explorer") {
		t.Fatalf("expected explorer system prompt in task, got %q", gotTask)
	}
	if !strings.Contains(gotTask, "map the auth package") {
		t.Fatalf("expected original task in spawned prompt, got %q", gotTask)
	}
}

// TestSpawnUnknownPersonaSoftDegradation verifies that an invented persona
// name does not fail the spawn: the child runs without the persona and the
// result carries a warning listing the available names.
func TestSpawnUnknownPersonaSoftDegradation(t *testing.T) {
	var gotTask string
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		gotTask = task
		return "done", nil
	}
	tool := BuildSessionsSpawnTool(runner, nil,
		func() []string { return nil },
		func() []agentcore.Message { return nil },
		nil, nil)

	res, err := tool.Func(context.Background(), json.RawMessage(`{"task": "analyze", "persona": "analytical"}`))
	if err != nil {
		t.Fatalf("Func: %v", err)
	}
	out := res.(map[string]any)
	if out["status"] != "completed" {
		t.Fatalf("expected spawn to proceed, got %v", out)
	}
	warnings, _ := out["persona_warnings"].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"analytical" not found`) {
		t.Fatalf("expected persona warning, got %v", warnings)
	}
	if !strings.Contains(warnings[0], "concise") || !strings.Contains(warnings[0], "architect") {
		t.Fatalf("warning should list available personas: %v", warnings)
	}
	// The task runs WITHOUT the persona reminder prepended.
	if strings.Contains(gotTask, "<system-reminder>") {
		t.Fatalf("failed persona must not inject a reminder, task: %q", gotTask)
	}
}

// TestSpawnDefaultBackgroundParallel verifies the default: two
// sequential spawn calls (no explicit background) both return immediately
// and their children run CONCURRENTLY, not serialized.
func TestSpawnDefaultBackgroundParallel(t *testing.T) {
	registry := NewSubagentRegistry()
	release := make(chan struct{})
	var running int32
	concurrent := int32(0)

	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		n := atomic.AddInt32(&running, 1)
		for {
			old := atomic.LoadInt32(&concurrent)
			if n <= old || atomic.CompareAndSwapInt32(&concurrent, old, n) {
				break
			}
		}
		<-release // block until both children are inside the runner
		atomic.AddInt32(&running, -1)
		return "done " + task, nil
	}
	tool := BuildSessionsSpawnTool(runner, nil,
		func() []string { return nil },
		func() []agentcore.Message { return nil },
		registry, nil)

	// Two sequential calls (as a serial tool executor would issue them).
	res1, err := tool.Func(context.Background(), json.RawMessage(`{"task": "analyze A"}`))
	if err != nil {
		t.Fatalf("spawn 1: %v", err)
	}
	res2, err := tool.Func(context.Background(), json.RawMessage(`{"task": "analyze B"}`))
	if err != nil {
		t.Fatalf("spawn 2: %v", err)
	}
	if res1.(map[string]any)["status"] != "started" || res2.(map[string]any)["status"] != "started" {
		t.Fatalf("expected both spawns to return started, got %v %v", res1, res2)
	}

	// Both children are inside the runner at the same time → parallel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&concurrent) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	if got := atomic.LoadInt32(&concurrent); got < 2 {
		t.Fatalf("expected 2 concurrent children, observed max %d", got)
	}
}

// TestSpawnPanicDoesNotLeakRunningEntry verifies that a child runner panic
// still flips the registry entry to a terminal state and fires the
// completion notification — a leaked "running" entry would keep the footer
// strip alive forever.
func TestSpawnPanicDoesNotLeakRunningEntry(t *testing.T) {
	registry := NewSubagentRegistry()
	completed := make(chan string, 1)
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		panic("boom: child exploded")
	}
	tool := BuildSessionsSpawnTool(runner, nil,
		func() []string { return nil },
		func() []agentcore.Message { return nil },
		registry, func(childID, task, output string, err error) {
			completed <- childID
		})

	res, err := tool.Func(context.Background(), json.RawMessage(`{"task": "explode"}`))
	if err != nil {
		t.Fatalf("Func: %v", err)
	}
	id, _ := res.(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("expected background id, got %v", res)
	}

	select {
	case got := <-completed:
		if got != id {
			t.Fatalf("onComplete id = %s, want %s", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onComplete not fired after runner panic")
	}

	run, ok := registry.Get(id)
	if !ok {
		t.Fatal("registry entry missing")
	}
	if run.Status != "failed" {
		t.Fatalf("expected failed status after panic, got %q", run.Status)
	}
}

// TestSpawnLenientArgTypes verifies that stringified flag/number arguments
// (a common model failure mode) are coerced instead of failing the spawn.
func TestSpawnLenientArgTypes(t *testing.T) {
	registry := NewSubagentRegistry()
	gotMaxTurns := make(chan int, 1)
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		gotMaxTurns <- maxTurns
		return "done", nil
	}
	tool := BuildSessionsSpawnTool(runner, nil,
		func() []string { return nil },
		func() []agentcore.Message { return nil },
		registry, nil)

	// Old strict parsing failed on: "background":"true", "max_turns":"7".
	args := `{"task": "t", "background": "true", "max_turns": "7"}`
	res, err := tool.Func(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("lenient parse failed: %v", err)
	}
	if res.(map[string]any)["status"] != "started" {
		t.Fatalf("expected started, got %v", res)
	}
	select {
	case got := <-gotMaxTurns:
		if got != 7 {
			t.Fatalf("maxTurns coercion failed: %d", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner not called within timeout")
	}
}

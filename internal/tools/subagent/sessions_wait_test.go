package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// --- normalizeContextMode ---

func TestNormalizeContextMode(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantN   int
		wantErr bool
	}{
		{in: "", want: "isolated"},
		{in: "isolated", want: "isolated"},
		{in: "state", want: "state"},
		{in: "full", want: "full"},
		{in: "turns:5", want: "turns", wantN: 5},
		{in: "turns:0", wantErr: true},
		{in: "turns:-3", wantErr: true},
		{in: "turns:abc", wantErr: true},
		{in: "banana", wantErr: true},
	}
	for _, tc := range cases {
		mode, n, err := normalizeContextMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
			continue
		}
		if mode != tc.want || n != tc.wantN {
			t.Errorf("%q: got (%q, %d), want (%q, %d)", tc.in, mode, n, tc.want, tc.wantN)
		}
	}

	// Cap: turns:N above maxForkTurns is clamped, not rejected.
	_, n, err := normalizeContextMode("turns:99999999")
	if err != nil || n != maxForkTurns {
		t.Fatalf("expected clamp to %d, got n=%d err=%v", maxForkTurns, n, err)
	}
}

// --- sessions_wait mailbox semantics ---

func waitToolForTest() (*agentcore.Tool, *SubagentRegistry) {
	registry := NewSubagentRegistry()
	return BuildSessionsWaitTool(registry), registry
}

func TestRegistryReadSnapshots(t *testing.T) {
	registry := NewSubagentRegistry()
	id := registry.Start("snapshot task", 1)
	if !registry.SendInput(id, "original input") {
		t.Fatal("input not sent")
	}
	running, _ := registry.Get(id)
	registry.Complete(id, false)
	if running.Status != "running" || running.EndedAt != nil {
		t.Fatal("previous snapshot changed after completion")
	}
	completed, _ := registry.Get(id)
	endedAt := *completed.EndedAt
	for _, snapshot := range []*SubagentRun{completed, registry.List(false)[0]} {
		snapshot.Status = "changed"
		snapshot.InputMessages[0] = "changed"
		*snapshot.EndedAt = endedAt.Add(time.Hour)
	}
	current, _ := registry.Get(id)
	if current.Status != "completed" || current.InputMessages[0] != "original input" || !current.EndedAt.Equal(endedAt) {
		t.Fatalf("snapshot mutation changed registry: %+v", current)
	}
	if missing, ok := registry.Get("missing"); ok || missing != nil {
		t.Fatal("missing run returned a snapshot")
	}
}

func TestRegistryConcurrentSnapshotAccess(t *testing.T) {
	registry := NewSubagentRegistry()
	id := registry.Start("concurrent task", 1)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 200; iteration++ {
				registry.SendInput(id, "input")
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for iteration := 0; iteration < 200; iteration++ {
			if run, ok := registry.Get(id); ok {
				runSummary(run)
			}
			for _, run := range registry.List(false) {
				runSummary(run)
			}
		}
	}()
	close(start)
	workers.Wait()
	workers.Add(1)
	go func() {
		defer workers.Done()
		for iteration := 0; iteration < 200; iteration++ {
			registry.Complete(id, false)
		}
	}()
	for iteration := 0; iteration < 200; iteration++ {
		summarizeRuns(registry, []string{id})
		splitUnseen(registry, &waitSeen{seen: make(map[string]bool)})
	}
	workers.Wait()
}

func callWait(t *testing.T, tool *agentcore.Tool, args string, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, err := tool.Func(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("wait Func: %v", err)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", raw)
	}
	return m
}

// TestWaitReportsCompletionExactlyOnce verifies mailbox semantics: the first
// wait after a completion reports it; the second does not.
func TestWaitReportsCompletionExactlyOnce(t *testing.T) {
	tool, registry := waitToolForTest()
	id := registry.Start("analyze codebase", 1)

	go func() {
		time.Sleep(100 * time.Millisecond)
		registry.Complete(id, false)
	}()

	first := callWait(t, tool, `{"timeout_seconds": 5}`, 10*time.Second)
	updated, _ := first["updated"].([]map[string]any)
	if len(updated) != 1 || updated[0]["id"] != id {
		t.Fatalf("expected first wait to report %s, got %v", id, first)
	}
	if updated[0]["status"] != "completed" {
		t.Fatalf("expected status completed, got %v", updated[0]["status"])
	}

	// Second wait: nothing new → timeout with an empty update set.
	second := callWait(t, tool, `{"timeout_seconds": 1}`, 10*time.Second)
	if second["timed_out"] != true {
		t.Fatalf("expected second wait to time out without new updates, got %v", second)
	}
	if updated, _ := second["updated"].([]map[string]any); len(updated) != 0 {
		t.Fatalf("second wait re-reported completions: %v", updated)
	}
}

// TestWaitImmediateForPreexistingCompletion verifies that a child which
// completed before the first wait call is reported immediately.
func TestWaitImmediateForPreexistingCompletion(t *testing.T) {
	tool, registry := waitToolForTest()
	id := registry.Start("quick task", 1)
	registry.Complete(id, false)

	start := time.Now()
	result := callWait(t, tool, `{"timeout_seconds": 10}`, 10*time.Second)
	if time.Since(start) > 2*time.Second {
		t.Fatal("wait should return immediately for a preexisting completion")
	}
	updated, _ := result["updated"].([]map[string]any)
	if len(updated) != 1 || updated[0]["id"] != id {
		t.Fatalf("expected immediate report of %s, got %v", id, result)
	}
}

// TestWaitTargetedMode verifies agent_ids blocks until all listed agents are
// terminal and rejects unknown ids.
func TestWaitTargetedMode(t *testing.T) {
	tool, registry := waitToolForTest()
	idA := registry.Start("task a", 1)
	idB := registry.Start("task b", 1)
	registry.Complete(idA, false)

	go func() {
		time.Sleep(100 * time.Millisecond)
		registry.Complete(idB, false)
	}()

	result := callWait(t, tool,
		`{"agent_ids": ["`+idA+`", "`+idB+`"], "timeout_seconds": 5}`, 10*time.Second)
	if result["all_completed"] != true {
		t.Fatalf("expected all_completed, got %v", result)
	}
	agents, _ := result["agents"].([]map[string]any)
	if len(agents) != 2 {
		t.Fatalf("expected 2 agent summaries, got %v", agents)
	}

	if _, err := tool.Func(context.Background(), json.RawMessage(`{"agent_ids": ["nope"]}`)); err == nil {
		t.Fatal("expected unknown agent id to error")
	}
}

func TestWaitFinishedDoesNotMeanSucceeded(t *testing.T) {
	tool, registry := waitToolForTest()
	failed := registry.Start("failure", 1)
	registry.CompleteWithError(failed, fmt.Errorf("provider: password=private-value request failed"))
	completed := registry.Start("success", 1)
	registry.Complete(completed, false)
	result := callWait(t, tool, `{"agent_ids":["`+failed+`","`+completed+`"]}`, time.Second)
	if result["all_finished"] != true || result["all_succeeded"] != false || result["all_completed"] != false {
		t.Fatalf("failure represented as task success: %+v", result)
	}
	agents := result["agents"].([]map[string]any)
	if agents[0]["succeeded"] != false || !strings.Contains(agents[0]["error"].(string), "provider:") || strings.Contains(agents[0]["error"].(string), "private-value") {
		t.Fatalf("missing safe child failure: %+v", agents)
	}
}

// TestWaitTimeoutListsRunning verifies the timeout path reports what is
// still running.
func TestWaitTimeoutListsRunning(t *testing.T) {
	tool, registry := waitToolForTest()
	id := registry.Start("long task", 1)
	defer registry.Interrupt(id)

	result := callWait(t, tool, `{"timeout_seconds": 1}`, 10*time.Second)
	if result["timed_out"] != true {
		t.Fatalf("expected timeout, got %v", result)
	}
	running, _ := result["running"].([]map[string]any)
	if len(running) != 1 || running[0]["id"] != id {
		t.Fatalf("expected running list with %s, got %v", id, result)
	}
}

// TestBatchRejectsInvalidContextMode verifies batch pre-validation rejects
// malformed context modes before spawning anything.
func TestBatchRejectsInvalidContextMode(t *testing.T) {
	tool := buildBatchToolForTest(t)
	args := `{"tasks": [{"id": "a", "task": "x", "context_mode": "turns:abc"}]}`
	if _, err := tool.Func(context.Background(), json.RawMessage(args)); err == nil {
		t.Fatal("expected invalid context_mode to be rejected")
	}
}

func buildBatchToolForTest(t *testing.T) *agentcore.Tool {
	t.Helper()
	runner := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "done", nil
	}
	return BuildSessionsSpawnBatchTool(runner, nil,
		func() []string { return nil },
		func() []agentcore.Message { return nil })
}

// TestWaitToolDescriptionMentionsMailbox guards the summary-only contract in
// the tool description.
func TestWaitToolDescriptionMentionsMailbox(t *testing.T) {
	tool, _ := waitToolForTest()
	if !strings.Contains(tool.Description, "STATUS SUMMARY") {
		t.Fatalf("expected summary-only contract in description:\n%s", tool.Description)
	}
}

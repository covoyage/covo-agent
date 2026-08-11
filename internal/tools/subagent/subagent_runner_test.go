package subagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDelegateToolsetsForRole(t *testing.T) {
	tests := []struct {
		name           string
		parent         []string
		child          []string
		restrictParent bool
		orchestrator   bool
		want           []string
	}{
		{
			name:           "leaf strips delegation",
			parent:         []string{"filesystem", "delegation", "web"},
			child:          []string{"filesystem", "delegation"},
			restrictParent: true,
			orchestrator:   false,
			want:           []string{"filesystem"},
		},
		{
			name:           "orchestrator retains delegation",
			parent:         []string{"filesystem", "delegation", "web"},
			child:          []string{"filesystem", "delegation"},
			restrictParent: true,
			orchestrator:   true,
			want:           []string{"filesystem", "delegation"},
		},
		{
			name:           "always strips clarify",
			parent:         []string{"filesystem", "clarify", "delegation"},
			child:          []string{"filesystem", "clarify", "delegation"},
			restrictParent: true,
			orchestrator:   true,
			want:           []string{"filesystem", "delegation"},
		},
		{
			name:           "not in parent scope",
			parent:         []string{"filesystem", "git"},
			child:          []string{"filesystem", "web"},
			restrictParent: true,
			orchestrator:   false,
			want:           []string{"filesystem"},
		},
		{
			name:           "no parent restriction",
			parent:         []string{"filesystem"},
			child:          []string{"filesystem", "web", "delegation"},
			restrictParent: false,
			orchestrator:   false,
			want:           []string{"filesystem", "web"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DelegateToolsetsForRole(tt.parent, tt.child, tt.restrictParent, tt.orchestrator)
			if len(got) != len(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d]=%q, want[%d]=%q", i, got[i], i, tt.want[i])
				}
			}
		})
	}
}

func TestSubagentRunner_Timeout(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{
		DefaultTimeout: 50 * time.Millisecond,
		Logger:         nil, // use default
	})

	// Spawn that blocks forever
	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Second):
			return "done", nil
		}
	}

	start := time.Now()
	_, err := runner.Run(context.Background(), spawn, "test task", nil, SubagentRunOptions{
		MaxTurns: 10,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("expected cancellation error, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestSubagentRunner_Heartbeat(t *testing.T) {
	var beatCount atomic.Int32

	runner := NewSubagentRunner(SubagentRunnerConfig{
		HeartbeatInterval: 20 * time.Millisecond,
		HeartbeatFn: func() {
			beatCount.Add(1)
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		time.Sleep(80 * time.Millisecond)
		return "done", nil
	}

	output, err := runner.Run(context.Background(), spawn, "heartbeat test", nil, SubagentRunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "done" {
		t.Errorf("got %q, want %q", output, "done")
	}

	beats := beatCount.Load()
	if beats < 2 {
		t.Errorf("expected at least 2 heartbeats, got %d", beats)
	}
}

func TestSubagentRunner_StaleDetection(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{
		HeartbeatInterval: 10 * time.Millisecond,
		StaleTimeout:      30 * time.Millisecond,
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		// Block until context is done (simulates stuck child)
		<-ctx.Done()
		return "", ctx.Err()
	}

	start := time.Now()
	_, err := runner.Run(context.Background(), spawn, "stale test", nil, SubagentRunOptions{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected stale error, got nil")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("expected stale error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("stale detection took too long: %v", elapsed)
	}
}

func TestSubagentRunner_SuccessPath(t *testing.T) {
	var beatCount atomic.Int32

	runner := NewSubagentRunner(SubagentRunnerConfig{
		DefaultTimeout:    5 * time.Second,
		HeartbeatInterval: 50 * time.Millisecond,
		HeartbeatFn: func() {
			beatCount.Add(1)
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		time.Sleep(120 * time.Millisecond) // let heartbeat fire (2x interval)
		if task != "hello world" {
			return "", errors.New("wrong task")
		}
		if maxTurns != 10 {
			return "", errors.New("wrong maxTurns")
		}
		return "result from child", nil
	}

	output, err := runner.Run(context.Background(), spawn, "hello world", []string{"filesystem"}, SubagentRunOptions{
		Goal:     "greeting task",
		MaxTurns: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "result from child" {
		t.Errorf("got %q, want %q", output, "result from child")
	}
	if beatCount.Load() < 1 {
		t.Error("expected at least one heartbeat")
	}
}

func TestSubagentRunner_ProgressCallback(t *testing.T) {
	var events []SubagentProgressEvent

	runner := NewSubagentRunner(SubagentRunnerConfig{
		ProgressCallback: func(e SubagentProgressEvent) {
			events = append(events, e)
		},
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "done", nil
	}

	_, err := runner.Run(context.Background(), spawn, "progress test", nil, SubagentRunOptions{
		Goal:       "test goal",
		ChildIndex: 2,
		ChildCount: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (start + complete), got %d", len(events))
	}
	if events[0].Type != "start" {
		t.Errorf("first event should be 'start', got %q", events[0].Type)
	}
	if events[0].ChildIndex != 2 {
		t.Errorf("ChildIndex = %d, want 2", events[0].ChildIndex)
	}
	if events[0].ChildCount != 5 {
		t.Errorf("ChildCount = %d, want 5", events[0].ChildCount)
	}
	lastEvent := events[len(events)-1]
	if lastEvent.Type != "complete" {
		t.Errorf("last event should be 'complete', got %q", lastEvent.Type)
	}
}

func TestSubagentRunner_DiagnosticOnTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	logsDir := filepath.Join(tmpDir, "logs")

	runner := NewSubagentRunner(SubagentRunnerConfig{
		DefaultTimeout: 10 * time.Millisecond,
		HomeDir:        tmpDir,
	})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	_, err := runner.Run(context.Background(), spawn, "task for diagnostic", []string{"web", "filesystem"}, SubagentRunOptions{
		Goal: "diagnostic test goal",
	})
	if err == nil {
		t.Fatal("expected error")
	}

	// Check diagnostic file was written
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		t.Fatalf("logs dir not created: %v", err)
	}

	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "subagent-timeout-") {
			found = true
			data, err := os.ReadFile(filepath.Join(logsDir, e.Name()))
			if err != nil {
				t.Errorf("cannot read diagnostic: %v", err)
			}
			content := string(data)
			if !strings.Contains(content, "diagnostic test goal") {
				t.Error("diagnostic missing goal")
			}
			if !strings.Contains(content, "task for diagnostic") {
				t.Error("diagnostic missing task")
			}
			if !strings.Contains(content, "filesystem") {
				t.Error("diagnostic missing toolsets")
			}
			if !strings.Contains(content, "Stack Trace") {
				t.Error("diagnostic missing stack trace")
			}
		}
	}
	if !found {
		t.Error("no diagnostic file found")
	}
}

func TestSubagentDepthContext(t *testing.T) {
	ctx := context.Background()

	if d := SubagentDepthFromContext(ctx); d != 0 {
		t.Errorf("default depth = %d, want 0", d)
	}

	ctx = WithSubagentDepth(ctx, 3)
	if d := SubagentDepthFromContext(ctx); d != 3 {
		t.Errorf("depth = %d, want 3", d)
	}
}

func TestSubagentOrchestratorContext(t *testing.T) {
	ctx := context.Background()

	if IsSubagentOrchestrator(ctx) {
		t.Error("default should not be orchestrator")
	}

	ctx = WithSubagentOrchestrator(ctx)
	if !IsSubagentOrchestrator(ctx) {
		t.Error("should be orchestrator after WithSubagentOrchestrator")
	}
}

func TestSubagentProgressContext(t *testing.T) {
	ctx := context.Background()

	if cb := SubagentProgressFromContext(ctx); cb != nil {
		t.Error("default progress callback should be nil")
	}

	dummy := func(e SubagentProgressEvent) {}
	ctx = WithSubagentProgress(ctx, dummy)
	if cb := SubagentProgressFromContext(ctx); cb == nil {
		t.Error("progress callback should not be nil after WithSubagentProgress")
	}
}

func TestSubagentGoalContext(t *testing.T) {
	ctx := context.Background()

	if g := SubagentGoalFromContext(ctx); g != "" {
		t.Errorf("default goal = %q, want empty", g)
	}

	ctx = WithSubagentGoal(ctx, "build feature X")
	if g := SubagentGoalFromContext(ctx); g != "build feature X" {
		t.Errorf("goal = %q, want 'build feature X'", g)
	}
}

func TestSubagentRunner_ErrorPath(t *testing.T) {
	runner := NewSubagentRunner(SubagentRunnerConfig{})

	spawn := func(ctx context.Context, task string, toolsets []string, maxTurns int) (string, error) {
		return "", errors.New("child agent exploded")
	}

	var events []SubagentProgressEvent
	runner.cfg.ProgressCallback = func(e SubagentProgressEvent) {
		events = append(events, e)
	}

	_, err := runner.Run(context.Background(), spawn, "risky task", nil, SubagentRunOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "child agent exploded") {
		t.Errorf("got error %q, want child agent exploded", err)
	}

	// Last event should be "error"
	lastEvent := events[len(events)-1]
	if lastEvent.Type != "error" {
		t.Errorf("last event should be 'error', got %q", lastEvent.Type)
	}
	if !strings.Contains(lastEvent.Error, "child agent exploded") {
		t.Errorf("error event missing error msg: %+v", lastEvent)
	}
}

package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
)

type blockingProvider struct{}

func (*blockingProvider) Complete(ctx context.Context, _ *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*blockingProvider) Stream(ctx context.Context, _ *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	ch := make(chan agentcore.StreamDelta, 1)
	go func() {
		<-ctx.Done()
		select {
		case ch <- agentcore.StreamDelta{Err: ctx.Err()}:
		default:
		}
		close(ch)
	}()
	return ch, nil
}

func makeTestAgent(t *testing.T, provider agentcore.Provider) *agent.CovoAgent {
	t.Helper()
	homeDir := t.TempDir()
	factory := NewAgentFactory(agent.CovoAgentConfig{
		HomeDir:    homeDir,
		WorkingDir: homeDir,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, NewRuntimeState())
	ca, err := factory.New(AgentRequest{
		Mode:         agent.ModeGeneral,
		Provider:     provider,
		ProviderName: "test",
		Model:        "test",
	})
	if err != nil {
		t.Fatalf("create test agent: %v", err)
	}
	return ca
}

func findTask(tasks []TaskSummary, id string) (TaskSummary, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return TaskSummary{}, false
}

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "seconds", in: 42 * time.Second, want: "42s"},
		{name: "minutes", in: 2*time.Minute + 3*time.Second, want: "2m3s"},
		{name: "hours", in: time.Hour + 2*time.Minute + 3*time.Second, want: "1h2m3s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fmtDuration(tc.in); got != tc.want {
				t.Fatalf("fmtDuration(%s) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBackgroundManagerStartFailedCreateAgent(t *testing.T) {
	mgr := NewBackgroundManager()
	var notified string
	id := mgr.Start("do work", func() *agent.CovoAgent { return nil }, func(msg string) {
		notified = msg
	})

	task, ok := findTask(mgr.List(), id)
	if !ok {
		t.Fatalf("task %s not found", id)
	}
	if task.Status != TaskFailed {
		t.Fatalf("status = %s, want %s", task.Status, TaskFailed)
	}
	if task.Error != "failed to create agent" {
		t.Fatalf("error = %q", task.Error)
	}
	if notified != "[bg/"+id+"] failed to create agent" {
		t.Fatalf("notify = %q", notified)
	}
}

func TestBackgroundManagerListSortsAndTruncatesOutputRunes(t *testing.T) {
	now := time.Now()
	longOutput := strings.Repeat("你", 205)
	mgr := &BackgroundManager{
		tasks: map[string]*backgroundTask{
			"bg-1": {
				ID:          "bg-1",
				Input:       "older",
				Status:      TaskCompleted,
				Output:      "short",
				Turns:       2,
				startedAt:   now.Add(-2 * time.Minute),
				completedAt: now.Add(-90 * time.Second),
			},
			"bg-2": {
				ID:        "bg-2",
				Input:     "newer",
				Status:    TaskFailed,
				Output:    longOutput,
				Error:     "boom",
				Turns:     5,
				startedAt: now.Add(-time.Minute),
			},
		},
	}

	tasks := mgr.List()
	if len(tasks) != 2 {
		t.Fatalf("len(tasks) = %d", len(tasks))
	}
	if tasks[0].ID != "bg-2" || tasks[1].ID != "bg-1" {
		t.Fatalf("unexpected order: %s then %s", tasks[0].ID, tasks[1].ID)
	}
	if !utf8.ValidString(tasks[0].Output) {
		t.Fatal("truncated output is not valid UTF-8")
	}
	if got := len([]rune(tasks[0].Output)); got != 203 {
		t.Fatalf("truncated rune length = %d, want 203", got)
	}
}

func TestBackgroundManagerCancelStopsRunningTask(t *testing.T) {
	mgr := NewBackgroundManager()
	id := mgr.Start("wait for cancel", func() *agent.CovoAgent {
		return makeTestAgent(t, &blockingProvider{})
	}, nil)

	if err := mgr.Cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := findTask(mgr.List(), id)
		if !ok {
			t.Fatalf("task %s not found", id)
		}
		if task.Status == TaskCancelled || task.Status == TaskCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	task, _ := findTask(mgr.List(), id)
	t.Fatalf("status = %s, want %s or %s", task.Status, TaskCancelled, TaskCompleted)
}

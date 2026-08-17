package app

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covo-agent/internal/telemetry"
)

// TaskStatus represents the status of a background task.
type TaskStatus string

const (
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

// TaskSummary is a read-only summary of a background task.
type TaskSummary struct {
	ID          string
	Input       string
	Status      TaskStatus
	Output      string
	Error       string
	Turns       int64
	CurrentTurn int64
	Runtime     string
	StartedAt   time.Time
}

type backgroundTask struct {
	ID          string
	Input       string
	Status      TaskStatus
	Output      string
	Error       string
	Turns       int64
	agent       *agentcore.Agent
	covoAgent   *agent.CovoAgent
	cancel      context.CancelFunc
	startedAt   time.Time
	completedAt time.Time
}

type BackgroundManager struct {
	mu      sync.Mutex
	tasks   map[string]*backgroundTask
	counter int
}

func NewBackgroundManager() *BackgroundManager {
	return &BackgroundManager{
		tasks: make(map[string]*backgroundTask),
	}
}

func (m *BackgroundManager) Start(input string, createAgent func() *agent.CovoAgent, notify func(string)) string {
	m.mu.Lock()
	m.counter++
	id := fmt.Sprintf("bg-%d", m.counter)
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	ca := createAgent()
	if ca == nil {
		cancel()
		m.mu.Lock()
		m.tasks[id] = &backgroundTask{
			ID: id, Input: input, Status: TaskFailed, Error: "failed to create agent",
			startedAt: time.Now(), completedAt: time.Now(),
		}
		m.mu.Unlock()
		if notify != nil {
			notify(fmt.Sprintf("[bg/%s] failed to create agent", id))
		}
		return id
	}

	bt := &backgroundTask{
		ID: id, Input: input, Status: TaskRunning,
		startedAt: time.Now(),
		agent:     ca.Core(), covoAgent: ca, cancel: cancel,
	}

	m.mu.Lock()
	m.tasks[id] = bt
	m.mu.Unlock()

	safego.SafeGo(func() {
		defer cancel()
		// Flush this task's spans promptly; do NOT shut down the pipeline —
		// the interactive session keeps running in this process.
		defer telemetry.FlushOtel(context.Background())

		output, err := ca.RunDirectWithSession(ctx, input, "bg-"+bt.ID)

		// Close the agent BEFORE marking the task finished: Close waits out
		// background work (e.g. the async snapshot baseline), so once an
		// observer sees a terminal status the agent is fully torn down and
		// nothing writes into its data dir anymore.
		ca.Close()

		m.mu.Lock()
		bt.Output = output
		bt.Turns = ca.Core().State().Turn()
		bt.completedAt = time.Now()
		if err != nil {
			if ctx.Err() != nil {
				bt.Status = TaskCancelled
			} else {
				bt.Status = TaskFailed
				bt.Error = err.Error()
			}
		} else {
			bt.Status = TaskCompleted
		}
		m.mu.Unlock()

		if notify != nil {
			prefix := fmt.Sprintf("[bg/%s] ", id)
			switch bt.Status {
			case TaskCompleted:
				notify(prefix + "completed")
			case TaskFailed:
				notify(prefix + "failed: " + bt.Error)
			case TaskCancelled:
				notify(prefix + "cancelled")
			}
		}
	}, nil)

	return id
}

func (m *BackgroundManager) Steer(id, instructions string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bt, ok := m.tasks[id]
	if !ok {
		return fmt.Errorf("task %q not found", id)
	}
	if bt.Status != TaskRunning {
		return fmt.Errorf("task %q is not running (status: %s)", id, bt.Status)
	}

	bt.agent.Steer(agentcore.Message{Role: agentcore.RoleUser, Content: instructions})
	return nil
}

func (m *BackgroundManager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	bt, ok := m.tasks[id]
	if !ok {
		return fmt.Errorf("task %q not found", id)
	}
	bt.cancel()
	return nil
}

func (m *BackgroundManager) List() []TaskSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]TaskSummary, 0, len(m.tasks))
	for id, bt := range m.tasks {
		runtime := ""
		if !bt.startedAt.IsZero() {
			end := bt.completedAt
			if end.IsZero() {
				end = time.Now()
			}
			runtime = fmtDuration(end.Sub(bt.startedAt))
		}

		currentTurn := bt.Turns
		if bt.Status == TaskRunning && bt.agent != nil {
			currentTurn = bt.agent.State().Turn()
		}

		result = append(result, TaskSummary{
			ID: id, Input: bt.Input, Status: bt.Status,
			Output: truncateRunes(bt.Output, 200),
			Error:  bt.Error, Turns: bt.Turns,
			CurrentTurn: currentTurn, Runtime: runtime, StartedAt: bt.startedAt,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.After(result[j].StartedAt)
	})

	return result
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

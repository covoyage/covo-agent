package agent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CommandPriority ranks queued work. now jumps the line; later waits
// behind next.
type CommandPriority string

const (
	PriorityNow   CommandPriority = "now"
	PriorityNext  CommandPriority = "next"
	PriorityLater CommandPriority = "later"
)

// CommandMode names why a command was admitted.
type CommandMode string

const (
	ModePrompt             CommandMode = "prompt"
	ModeTargetContinuation CommandMode = "target-continuation"
	ModeTaskNotification   CommandMode = "task-notification"
	ModeSubagentMessage    CommandMode = "subagent-message"
	ModeControlOnly        CommandMode = "control-only"
)

// RuntimeCommand is one unit of work for a session.
type RuntimeCommand struct {
	ID        string
	Owner     string
	Mode      CommandMode
	Priority  CommandPriority
	Input     string
	CreatedAt time.Time
	done      chan commandResult
}

type commandResult struct {
	output string
	err    error
}

// CommandQueue serializes session turns without dropping a second client.
// Claim still records the current owner; enqueue waits instead of rejecting.
type CommandQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	now      []*RuntimeCommand
	next     []*RuntimeCommand
	later    []*RuntimeCommand
	seq      uint64
	closed   bool
	draining bool
}

func NewCommandQueue() *CommandQueue {
	q := &CommandQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *CommandQueue) Enqueue(owner string, mode CommandMode, priority CommandPriority, input string) *RuntimeCommand {
	if q == nil {
		return &RuntimeCommand{Owner: owner, Mode: mode, Priority: priority, Input: input, done: make(chan commandResult, 1)}
	}
	if owner == "" {
		owner = "default"
	}
	if mode == "" {
		mode = ModePrompt
	}
	if priority == "" {
		priority = PriorityNext
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		cmd := &RuntimeCommand{Owner: owner, Mode: mode, Priority: priority, Input: input, done: make(chan commandResult, 1)}
		cmd.done <- commandResult{err: fmt.Errorf("session command queue closed")}
		close(cmd.done)
		return cmd
	}
	q.seq++
	cmd := &RuntimeCommand{
		ID:        fmt.Sprintf("cmd-%d", q.seq),
		Owner:     owner,
		Mode:      mode,
		Priority:  priority,
		Input:     input,
		CreatedAt: time.Now(),
		done:      make(chan commandResult, 1),
	}
	switch priority {
	case PriorityNow:
		q.now = append(q.now, cmd)
	case PriorityLater:
		q.later = append(q.later, cmd)
	default:
		q.next = append(q.next, cmd)
	}
	q.cond.Signal()
	return cmd
}

func (q *CommandQueue) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.now) + len(q.next) + len(q.later)
}

func (q *CommandQueue) popLocked() *RuntimeCommand {
	if len(q.now) > 0 {
		cmd := q.now[0]
		q.now = q.now[1:]
		return cmd
	}
	if len(q.next) > 0 {
		cmd := q.next[0]
		q.next = q.next[1:]
		return cmd
	}
	if len(q.later) > 0 {
		cmd := q.later[0]
		q.later = q.later[1:]
		return cmd
	}
	return nil
}

func (q *CommandQueue) dequeue(ctx context.Context) (*RuntimeCommand, error) {
	if q == nil {
		return nil, fmt.Errorf("nil command queue")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if q.closed {
			return nil, fmt.Errorf("session command queue closed")
		}
		if cmd := q.popLocked(); cmd != nil {
			return cmd, nil
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		q.cond.Wait()
	}
}

func (q *CommandQueue) tryStartWorker() bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.draining {
		return false
	}
	q.draining = true
	return true
}

func (q *CommandQueue) endWorker() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.draining = false
	pending := len(q.now)+len(q.next)+len(q.later) > 0
	q.mu.Unlock()
	if pending {
		q.cond.Signal()
	}
}

func (q *CommandQueue) tryDequeue() (*RuntimeCommand, error) {
	if q == nil {
		return nil, fmt.Errorf("nil command queue")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, fmt.Errorf("session command queue closed")
	}
	return q.popLocked(), nil
}

func (q *CommandQueue) Close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.draining = false
	pending := append(append(q.now, q.next...), q.later...)
	q.now, q.next, q.later = nil, nil, nil
	for _, cmd := range pending {
		cmd.finish("", fmt.Errorf("session command queue closed"))
	}
	q.cond.Broadcast()
}

func (c *RuntimeCommand) Wait(ctx context.Context) (string, error) {
	if c == nil {
		return "", fmt.Errorf("nil command")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-c.done:
		return r.output, r.err
	}
}

func (c *RuntimeCommand) finish(output string, err error) {
	if c == nil || c.done == nil {
		return
	}
	select {
	case c.done <- commandResult{output: output, err: err}:
	default:
	}
}

func commandPriorityFor(mode CommandMode) CommandPriority {
	switch mode {
	case ModeControlOnly, ModeSubagentMessage:
		return PriorityNow
	case ModeTaskNotification, ModeTargetContinuation:
		return PriorityLater
	default:
		return PriorityNext
	}
}

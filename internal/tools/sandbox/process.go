package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
	ossandbox "github.com/covoyage/covo-agent/internal/sandbox/ossandbox"
	"github.com/covoyage/covonaut/agentcore"
)

const (
	maxOutputChars     = 200_000
	finishedTTL        = 30 * time.Minute
	maxProcesses       = 64
	defaultWaitTimeout = 180
)

type ProcessSession struct {
	ID           string
	Command      string
	CWD          string
	PID          int
	process      *os.Process
	stdin        io.WriteCloser
	startedAt    time.Time
	exited       bool
	exitCode     int
	outputBuffer string
	waitDone     chan struct{}
	mu           sync.Mutex
}

type pollState struct {
	count     int
	lastPoll  time.Time
	hasOutput bool
}

// backoffSchedule is the escalating delay (ms) after consecutive no-output polls.
var backoffScheduleMs = []int{5000, 10000, 30000, 60000}

func calculateBackoffMs(consecutiveNoOutputPolls int) int {
	if consecutiveNoOutputPolls < 0 {
		return backoffScheduleMs[0]
	}
	idx := consecutiveNoOutputPolls
	if idx >= len(backoffScheduleMs) {
		idx = len(backoffScheduleMs) - 1
	}
	return backoffScheduleMs[idx]
}

type ProcessStore struct {
	mu         sync.RWMutex
	running    map[string]*ProcessSession
	finished   map[string]*ProcessSession
	counter    int
	pollCounts map[string]*pollState
}

func NewProcessStore() *ProcessStore {
	return &ProcessStore{
		running:    make(map[string]*ProcessSession),
		finished:   make(map[string]*ProcessSession),
		pollCounts: make(map[string]*pollState),
	}
}

func (s *ProcessStore) recordPoll(id string, hasNewOutput bool) (retryMs int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if hasNewOutput {
		s.pollCounts[id] = &pollState{count: 0, lastPoll: now, hasOutput: true}
		return backoffScheduleMs[0]
	}
	ps := s.pollCounts[id]
	if ps == nil {
		ps = &pollState{}
		s.pollCounts[id] = ps
	}
	retryMs = calculateBackoffMs(ps.count)
	ps.count++
	ps.lastPoll = now
	ps.hasOutput = false
	return retryMs
}

func (s *ProcessStore) resetPoll(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pollCounts, id)
}

func (s *ProcessStore) NextID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	return fmt.Sprintf("proc-%d", s.counter)
}

func (s *ProcessStore) Spawn(command string, cwd string) (*ProcessSession, error) {
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	resolvedCwd := cwd
	if resolvedCwd == "" {
		resolvedCwd, _ = os.Getwd()
	}
	if strings.HasPrefix(resolvedCwd, "~") {
		home, _ := os.UserHomeDir()
		resolvedCwd = strings.Replace(resolvedCwd, "~", home, 1)
	}

	shell, ok := os.LookupEnv("SHELL")
	if !ok {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-c", command)
	cmd.Dir = resolvedCwd

	// Apply child process network restriction if the OS-level sandbox requires it.
	if ossandbox.ShouldRestrictChildNetwork() {
		if err := ossandbox.ApplyChildNetworkRestriction(cmd); err != nil {
			return nil, fmt.Errorf("apply child network restriction: %w", err)
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	configureProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	id := s.NextID()
	session := &ProcessSession{
		ID:        id,
		Command:   command,
		CWD:       resolvedCwd,
		PID:       cmd.Process.Pid,
		process:   cmd.Process,
		stdin:     stdin,
		startedAt: time.Now(),
		waitDone:  make(chan struct{}),
	}

	safego.SafeGo(func() {
		s.readerLoop(session, cmd, stdout)
	}, nil)

	s.mu.Lock()
	s.pruneIfNeeded()
	s.running[id] = session
	s.mu.Unlock()

	return session, nil
}

func (s *ProcessStore) readerLoop(session *ProcessSession, cmd *exec.Cmd, stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			session.mu.Lock()
			session.outputBuffer += chunk
			if len(session.outputBuffer) > maxOutputChars {
				session.outputBuffer = session.outputBuffer[len(session.outputBuffer)-maxOutputChars:]
			}
			session.mu.Unlock()
		}
		if err != nil {
			break
		}
	}

	_ = cmd.Wait()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	session.mu.Lock()
	session.exited = true
	session.exitCode = exitCode
	session.mu.Unlock()
	close(session.waitDone)

	s.moveToFinished(session)
}

func (s *ProcessStore) moveToFinished(session *ProcessSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, session.ID)
	s.finished[session.ID] = session
	s.pruneIfNeeded()
}

func (s *ProcessStore) Get(id string) *ProcessSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if session, ok := s.running[id]; ok {
		return session
	}
	return s.finished[id]
}

func (s *ProcessStore) List() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []map[string]any
	now := time.Now()

	for _, session := range s.running {
		session.mu.Lock()
		entry := s.sessionToMap(session, now)
		session.mu.Unlock()
		result = append(result, entry)
	}
	for _, session := range s.finished {
		session.mu.Lock()
		entry := s.sessionToMap(session, now)
		session.mu.Unlock()
		result = append(result, entry)
	}
	return result
}

func (s *ProcessStore) sessionToMap(session *ProcessSession, now time.Time) map[string]any {
	entry := map[string]any{
		"session_id":     session.ID,
		"command":        session.Command[:min(len(session.Command), 200)],
		"cwd":            session.CWD,
		"pid":            session.PID,
		"started_at":     session.startedAt.Format(time.RFC3339),
		"uptime_seconds": int(now.Sub(session.startedAt).Seconds()),
		"status":         map[bool]string{true: "exited", false: "running"}[session.exited],
	}
	if session.exited {
		entry["exit_code"] = session.exitCode
		preview := session.outputBuffer
		if len(preview) > 200 {
			preview = preview[len(preview)-200:]
		}
		entry["output_preview"] = preview
	}
	return entry
}

func notFoundResult(id string) map[string]any {
	return map[string]any{"status": "not_found", "error": fmt.Sprintf("No process with ID %s", id)}
}

func (s *ProcessStore) Poll(id string) map[string]any {
	session := s.Get(id)
	if session == nil {
		return notFoundResult(id)
	}

	session.mu.Lock()
	exited := session.exited
	preview := session.outputBuffer
	if len(preview) > 1000 {
		preview = preview[len(preview)-1000:]
	}
	newOutput := len(preview) > 0 && !exited
	session.mu.Unlock()

	var retryMs int
	if exited {
		s.resetPoll(id)
	} else {
		retryMs = s.recordPoll(id, newOutput)
	}

	result := map[string]any{
		"session_id":     session.ID,
		"command":        session.Command,
		"status":         map[bool]string{true: "exited", false: "running"}[exited],
		"pid":            session.PID,
		"uptime_seconds": int(time.Since(session.startedAt).Seconds()),
		"output_preview": preview,
	}
	if exited {
		result["exit_code"] = session.exitCode
	} else if retryMs > 0 {
		result["retry_in_ms"] = retryMs
	}
	return result
}

func (s *ProcessStore) ReadLog(id string, offset, limit int) map[string]any {
	session := s.Get(id)
	if session == nil {
		return notFoundResult(id)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	lines := strings.Split(session.outputBuffer, "\n")
	totalLines := len(lines)

	var selected []string
	if offset == 0 && limit > 0 {
		start := totalLines - limit
		if start < 0 {
			start = 0
		}
		selected = lines[start:]
	} else {
		if offset >= totalLines {
			selected = []string{}
		} else {
			end := offset + limit
			if end > totalLines {
				end = totalLines
			}
			selected = lines[offset:end]
		}
	}

	return map[string]any{
		"session_id":  session.ID,
		"status":      map[bool]string{true: "exited", false: "running"}[session.exited],
		"output":      strings.Join(selected, "\n"),
		"total_lines": totalLines,
		"showing":     fmt.Sprintf("%d lines", len(selected)),
	}
}

func (s *ProcessStore) Wait(id string, timeout int) map[string]any {
	session := s.Get(id)
	if session == nil {
		return notFoundResult(id)
	}

	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}

	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	select {
	case <-session.waitDone:
		session.mu.Lock()
		defer session.mu.Unlock()
		output := session.outputBuffer
		if len(output) > 2000 {
			output = output[len(output)-2000:]
		}
		return map[string]any{
			"status":    "exited",
			"exit_code": session.exitCode,
			"output":    output,
		}
	case <-timer.C:
		session.mu.Lock()
		defer session.mu.Unlock()
		output := session.outputBuffer
		if len(output) > 1000 {
			output = output[len(output)-1000:]
		}
		return map[string]any{
			"status":       "timeout",
			"output":       output,
			"timeout_note": fmt.Sprintf("Waited %ds, process still running", timeout),
		}
	}
}

func (s *ProcessStore) Kill(id string) map[string]any {
	session := s.Get(id)
	if session == nil {
		return notFoundResult(id)
	}

	session.mu.Lock()
	if session.exited {
		session.mu.Unlock()
		return map[string]any{"status": "already_exited", "exit_code": session.exitCode}
	}
	proc := session.process
	session.mu.Unlock()

	terminationErr := terminateProcessTree(session.PID, proc, 3*time.Second)

	session.mu.Lock()
	session.exited = true
	session.exitCode = -15
	session.mu.Unlock()
	// waitDone may already be closed by readerLoop; closing again panics.
	// Use a select to safely close only if not already closed.
	select {
	case <-session.waitDone:
	default:
		close(session.waitDone)
	}
	s.moveToFinished(session)

	result := map[string]any{"status": "killed", "session_id": session.ID}
	if terminationErr != nil {
		result["warning"] = terminationErr.Error()
	}
	return result
}

func (s *ProcessStore) WriteStdin(id string, data string) map[string]any {
	session := s.Get(id)
	if session == nil {
		return notFoundResult(id)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.exited {
		return map[string]any{"status": "already_exited", "error": "Process has already finished"}
	}
	if session.stdin == nil {
		return map[string]any{"status": "error", "error": "Stdin not available"}
	}

	if _, err := session.stdin.Write([]byte(data)); err != nil {
		return map[string]any{"status": "error", "error": err.Error()}
	}
	return map[string]any{"status": "ok", "bytes_written": len(data)}
}

func (s *ProcessStore) SubmitStdin(id string, data string) map[string]any {
	return s.WriteStdin(id, data+"\n")
}

func (s *ProcessStore) CloseStdin(id string) map[string]any {
	session := s.Get(id)
	if session == nil {
		return notFoundResult(id)
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.exited {
		return map[string]any{"status": "already_exited", "error": "Process has already finished"}
	}
	if session.stdin == nil {
		return map[string]any{"status": "error", "error": "Stdin not available"}
	}

	if err := session.stdin.Close(); err != nil {
		return map[string]any{"status": "error", "error": err.Error()}
	}
	session.stdin = nil
	return map[string]any{"status": "ok", "message": "stdin closed"}
}

func (s *ProcessStore) pruneIfNeeded() {
	now := time.Now()
	for sid, session := range s.finished {
		if now.Sub(session.startedAt) > finishedTTL {
			delete(s.finished, sid)
		}
	}

	total := len(s.running) + len(s.finished)
	if total >= maxProcesses && len(s.finished) > 0 {
		var oldestID string
		var oldestTime time.Time
		for sid, session := range s.finished {
			if oldestID == "" || session.startedAt.Before(oldestTime) {
				oldestID = sid
				oldestTime = session.startedAt
			}
		}
		if oldestID != "" {
			delete(s.finished, oldestID)
		}
	}
}

func BuildProcessTool(store *ProcessStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "process",
		Description: strings.Join([]string{
			"Manage background processes (spawned via the 'spawn' action).",
			"Actions:",
			"  'list'  — Show all running and recently-finished processes",
			"  'spawn' — Start a new background process (returns session_id)",
			"  'poll'  — Check status and get latest output",
			"  'log'   — Read full output with pagination (offset/limit by lines)",
			"  'wait'  — Block until process exits or timeout",
			"  'kill'  — Terminate a running process (sends SIGTERM, then SIGKILL)",
			"  'write' — Send raw data to process stdin (no newline appended)",
			"  'submit' — Send data + newline to process stdin (for answering prompts)",
			"  'close' — Close stdin / send EOF",
			"",
			"For long-running tasks like dev servers, build watchers, or data processing.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "spawn", "poll", "log", "wait", "kill", "write", "submit", "close"},
					"description": "Action to perform.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Process session ID (returned by spawn). Required for all actions except 'list' and 'spawn'.",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to run (for 'spawn' action).",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory (for 'spawn' action). Defaults to current directory.",
				},
				"data": map[string]any{
					"type":        "string",
					"description": "Text to send to process stdin (for 'write' and 'submit' actions).",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Max seconds to wait (for 'wait' action). Default: 180.",
					"minimum":     1,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Line offset for 'log' action (0-indexed). Default: 0 (last N lines).",
					"minimum":     0,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max lines for 'log' action. Default: 200.",
					"minimum":     1,
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action    string `json:"action"`
				SessionID string `json:"session_id"`
				Command   string `json:"command"`
				CWD       string `json:"cwd"`
				Data      string `json:"data"`
				Timeout   int    `json:"timeout"`
				Offset    int    `json:"offset"`
				Limit     int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch params.Action {
			case "list":
				return map[string]any{"processes": store.List()}, nil

			case "spawn":
				if params.Command == "" {
					return nil, fmt.Errorf("command is required for spawn action")
				}
				session, err := store.Spawn(params.Command, params.CWD)
				if err != nil {
					return nil, fmt.Errorf("spawn failed: %w", err)
				}
				return map[string]any{
					"status":     "spawned",
					"session_id": session.ID,
					"pid":        session.PID,
					"command":    session.Command,
				}, nil

			case "poll":
				if params.SessionID == "" {
					return nil, fmt.Errorf("session_id is required for poll action")
				}
				return store.Poll(params.SessionID), nil

			case "log":
				if params.SessionID == "" {
					return nil, fmt.Errorf("session_id is required for log action")
				}
				if params.Limit <= 0 {
					params.Limit = 200
				}
				return store.ReadLog(params.SessionID, params.Offset, params.Limit), nil

			case "wait":
				if params.SessionID == "" {
					return nil, fmt.Errorf("session_id is required for wait action")
				}
				return store.Wait(params.SessionID, params.Timeout), nil

			case "kill":
				if params.SessionID == "" {
					return nil, fmt.Errorf("session_id is required for kill action")
				}
				result := store.Kill(params.SessionID)
				store.resetPoll(params.SessionID)
				return result, nil

			case "write":
				if params.SessionID == "" {
					return nil, fmt.Errorf("session_id is required for write action")
				}
				return store.WriteStdin(params.SessionID, params.Data), nil

			case "submit":
				if params.SessionID == "" {
					return nil, fmt.Errorf("session_id is required for submit action")
				}
				return store.SubmitStdin(params.SessionID, params.Data), nil

			case "close":
				if params.SessionID == "" {
					return nil, fmt.Errorf("session_id is required for close action")
				}
				return store.CloseStdin(params.SessionID), nil

			default:
				return nil, fmt.Errorf("unknown action: %s (use: list, spawn, poll, log, wait, kill, write, submit, close)", params.Action)
			}
		},
	}
}

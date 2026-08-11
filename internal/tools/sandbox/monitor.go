package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

type MonitorSession struct {
	ID          string
	Command     string
	Description string
	StartedAt   time.Time
	MaxEvents   int
	IdleTimeout time.Duration
}

type monitorRequest struct {
	Action      string `json:"action"`
	Command     string `json:"command"`
	Description string `json:"description"`
	MaxEvents   int    `json:"max_events"`
	IdleTimeout int    `json:"idle_timeout"`
	SessionID   string `json:"session_id"`
}

func BuildMonitorTool(store *ProcessStore) *agentcore.Tool {
	monitorSessions := make(map[string]*MonitorSession)

	return &agentcore.Tool{
		Name: "monitor",
		Description: strings.Join([]string{
			"Monitor a long-running process and stream its output line-by-line.",
			"The monitor captures stdout/stderr as structured events and auto-stops",
			"after max_events lines or idle_timeout seconds of inactivity.",
			"",
			"Actions:",
			"- start: spawn a command and begin monitoring",
			"- status: check the current output of a running monitor",
			"- stop: terminate a running monitor",
			"",
			"Use this for: watching build output, tailing logs, running tests",
			"with live output, or any long-running process where you want to",
			"see incremental progress.",
			"",
			"Parameters for 'start':",
			"- command: the shell command to run",
			"- description: human-readable label for this monitor",
			"- max_events: max output lines before auto-stop (default: 50)",
			"- idle_timeout: seconds of no output before auto-stop (default: 30)",
			"",
			"Parameters for 'status'/'stop':",
			"- session_id: the monitor session ID returned by 'start'",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action to perform: start, status, or stop.",
					"enum":        []string{"start", "status", "stop"},
				},
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to run (required for 'start' action).",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Human-readable label for this monitor (e.g., 'build', 'test run').",
				},
				"max_events": map[string]any{
					"type":        "integer",
					"description": "Maximum number of output lines before auto-stop (default: 50, max: 500).",
					"default":     50,
					"maximum":     500,
				},
				"idle_timeout": map[string]any{
					"type":        "integer",
					"description": "Seconds of no output before auto-stop (default: 30, max: 300).",
					"default":     30,
					"maximum":     300,
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Monitor session ID (required for 'status' and 'stop' actions).",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var req monitorRequest
			if err := json.Unmarshal(args, &req); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch req.Action {
			case "start":
				return handleMonitorStart(ctx, req, store, monitorSessions)
			case "status":
				return handleMonitorStatus(req, store, monitorSessions)
			case "stop":
				return handleMonitorStop(req, store, monitorSessions)
			default:
				return nil, fmt.Errorf("unknown action: %s (must be start, status, or stop)", req.Action)
			}
		},
	}
}

func handleMonitorStart(ctx context.Context, req monitorRequest, store *ProcessStore, sessions map[string]*MonitorSession) (any, error) {
	if strings.TrimSpace(req.Command) == "" {
		return nil, fmt.Errorf("command is required for 'start' action")
	}

	if req.MaxEvents <= 0 {
		req.MaxEvents = 50
	}
	if req.MaxEvents > 500 {
		req.MaxEvents = 500
	}
	if req.IdleTimeout <= 0 {
		req.IdleTimeout = 30
	}
	if req.IdleTimeout > 300 {
		req.IdleTimeout = 300
	}

	proc, err := store.Spawn(req.Command, "")
	if err != nil {
		return nil, fmt.Errorf("spawn monitor: %w", err)
	}

	sessions[proc.ID] = &MonitorSession{
		ID:          proc.ID,
		Command:     req.Command,
		Description: req.Description,
		StartedAt:   time.Now(),
		MaxEvents:   req.MaxEvents,
		IdleTimeout: time.Duration(req.IdleTimeout) * time.Second,
	}

	// Start auto-stop goroutine
	go monitorAutoStop(ctx, proc.ID, store, sessions, req.MaxEvents, time.Duration(req.IdleTimeout)*time.Second)

	return map[string]any{
		"session_id":   proc.ID,
		"command":      req.Command,
		"description":  req.Description,
		"max_events":   req.MaxEvents,
		"idle_timeout": req.IdleTimeout,
		"status":       "started",
		"hint":         "Use monitor action='status' with session_id to check output. Use monitor action='stop' to terminate.",
	}, nil
}

func handleMonitorStatus(req monitorRequest, store *ProcessStore, sessions map[string]*MonitorSession) (any, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required for 'status' action")
	}

	proc := store.Get(req.SessionID)
	if proc == nil {
		return nil, fmt.Errorf("monitor session %q not found (may have expired or been stopped)", req.SessionID)
	}

	result := store.ReadLog(req.SessionID, 0, 500)
	result["action"] = "status"

	ms, ok := sessions[req.SessionID]
	if ok {
		result["description"] = ms.Description
		result["command"] = ms.Command
	}

	return result, nil
}

func handleMonitorStop(req monitorRequest, store *ProcessStore, sessions map[string]*MonitorSession) (any, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required for 'stop' action")
	}

	result := store.Kill(req.SessionID)
	delete(sessions, req.SessionID)
	result["action"] = "stop"
	return result, nil
}

func monitorAutoStop(ctx context.Context, sessionID string, store *ProcessStore, sessions map[string]*MonitorSession, maxEvents int, idleTimeout time.Duration) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	prevLineCount := 0
	idleStart := time.Time{}
	eventCount := 0

	for {
		select {
		case <-ctx.Done():
			store.Kill(sessionID)
			delete(sessions, sessionID)
			return
		case <-ticker.C:
			status := store.Poll(sessionID)
			if status == nil {
				delete(sessions, sessionID)
				return
			}

			statusStr, _ := status["status"].(string)
			if statusStr == "exited" {
				delete(sessions, sessionID)
				return
			}

			preview, _ := status["output_preview"].(string)
			lineCount := strings.Count(preview, "\n")

			if lineCount > prevLineCount {
				eventCount += lineCount - prevLineCount
				prevLineCount = lineCount
				idleStart = time.Time{}
			} else if idleStart.IsZero() {
				idleStart = time.Now()
			}

			if eventCount >= maxEvents {
				store.Kill(sessionID)
				delete(sessions, sessionID)
				return
			}

			if !idleStart.IsZero() && time.Since(idleStart) >= idleTimeout {
				store.Kill(sessionID)
				delete(sessions, sessionID)
				return
			}
		}
	}
}

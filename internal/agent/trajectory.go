package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

func ConvertScratchpadToThink(content string) string {
	if content == "" || !strings.Contains(content, "<REASONING_SCRATCHPAD>") {
		return content
	}
	content = strings.ReplaceAll(content, "<REASONING_SCRATCHPAD>", " thinking")
	content = strings.ReplaceAll(content, "</REASONING_SCRATCHPAD>", " response")
	return content
}

func HasIncompleteScratchpad(content string) bool {
	if content == "" {
		return false
	}
	hasOpen := strings.Contains(content, "<REASONING_SCRATCHPAD>")
	hasClose := strings.Contains(content, "</REASONING_SCRATCHPAD>")
	return hasOpen && !hasClose
}

type TrajectoryEntry struct {
	Role       string               `json:"role"`
	Content    string               `json:"content,omitempty"`
	ToolCalls  []TrajectoryToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	Name       string               `json:"name,omitempty"`
	Timestamp  time.Time            `json:"timestamp"`
}

type TrajectoryToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type TrajectoryRecord struct {
	Conversations []TrajectoryEntry `json:"conversations"`
	Timestamp     time.Time         `json:"timestamp"`
	Model         string            `json:"model"`
	Completed     bool              `json:"completed"`
	SessionID     string            `json:"session_id,omitempty"`
}

type TrajectoryRecorder struct {
	mu        sync.Mutex
	entries   []TrajectoryEntry
	model     string
	sessionID string
	outputDir string
}

func NewTrajectoryRecorder(model, sessionID, outputDir string) *TrajectoryRecorder {
	return &TrajectoryRecorder{
		model:     model,
		sessionID: sessionID,
		outputDir: outputDir,
	}
}

func (r *TrajectoryRecorder) RecordUser(content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, TrajectoryEntry{
		Role:      "user",
		Content:   content,
		Timestamp: time.Now(),
	})
}

func (r *TrajectoryRecorder) RecordAssistant(content string, toolCalls []TrajectoryToolCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, TrajectoryEntry{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
		Timestamp: time.Now(),
	})
}

func (r *TrajectoryRecorder) RecordToolResult(toolCallID, toolName, content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, TrajectoryEntry{
		Role:       "tool",
		ToolCallID: toolCallID,
		Name:       toolName,
		Content:    content,
		Timestamp:  time.Now(),
	})
}

func (r *TrajectoryRecorder) RecordSystem(content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, TrajectoryEntry{
		Role:      "system",
		Content:   content,
		Timestamp: time.Now(),
	})
}

func (r *TrajectoryRecorder) Snapshot() []TrajectoryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := make([]TrajectoryEntry, len(r.entries))
	copy(snap, r.entries)
	return snap
}

func (r *TrajectoryRecorder) Save(completed bool) error {
	r.mu.Lock()
	entries := make([]TrajectoryEntry, len(r.entries))
	copy(entries, r.entries)
	r.mu.Unlock()

	record := TrajectoryRecord{
		Conversations: entries,
		Timestamp:     time.Now(),
		Model:         r.model,
		Completed:     completed,
		SessionID:     r.sessionID,
	}

	filename := "trajectory_samples.jsonl"
	if !completed {
		filename = "failed_trajectories.jsonl"
	}
	if r.outputDir != "" {
		filename = fmt.Sprintf("%s/%s", r.outputDir, filename)
	}

	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func (r *TrajectoryRecorder) FormatAsShareGPT() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	var conversations []map[string]any
	for _, entry := range r.entries {
		msg := map[string]any{
			"from": entry.Role,
		}
		if entry.Content != "" {
			msg["value"] = entry.Content
		}
		if len(entry.ToolCalls) > 0 {
			var tcs []map[string]any
			for _, tc := range entry.ToolCalls {
				tcs = append(tcs, map[string]any{
					"id":   tc.ID,
					"name": tc.Name,
					"args": tc.Arguments,
				})
			}
			msg["tool_calls"] = tcs
		}
		if entry.ToolCallID != "" {
			msg["tool_call_id"] = entry.ToolCallID
			msg["from"] = "tool"
		}
		if entry.Name != "" {
			msg["name"] = entry.Name
		}
		conversations = append(conversations, msg)
	}
	return conversations
}

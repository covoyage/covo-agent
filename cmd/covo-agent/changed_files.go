package main

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/chat"

	agentpanels "github.com/covoyage/covo-agent/internal/tui/panels"
)

// FileChangeAction describes how a file was changed.
type FileChangeAction string

const (
	ActionCreated  FileChangeAction = "created"
	ActionModified FileChangeAction = "modified"
	ActionDeleted  FileChangeAction = "deleted"
)

// FileChangeEntry tracks a single file modification event.
type FileChangeEntry struct {
	Path   string
	Action FileChangeAction
	Tool   string
	Time   time.Time
}

// ChangedFilesTracker tracks all files modified during a session by
// listening to tool-call events from file-mutating tools.
type ChangedFilesTracker struct {
	mu      sync.RWMutex
	entries map[string]*FileChangeEntry // path -> latest entry
	order   []string                    // insertion order (unique paths)
	// pending captures tool-call info from the Start event so we can
	// access tool name and arguments at the End event.
	pending map[string]*pendingToolCall
}

type pendingToolCall struct {
	toolName string
	argsJSON string
}

// NewChangedFilesTracker creates a tracker. Pass the *agentcore.Agent to
// start listening for tool-call events.
func NewChangedFilesTracker(agent *agentcore.Agent) *ChangedFilesTracker {
	t := &ChangedFilesTracker{
		entries: make(map[string]*FileChangeEntry),
		pending: make(map[string]*pendingToolCall),
	}
	t.Rebind(agent)
	return t
}

// Rebind attaches the tracker to a new agent (e.g., after mode switch or
// /clear). The old agent's event listeners become dead references, but
// the accumulated file-change history is preserved so the tree still
// reflects all modifications across the session.
func (t *ChangedFilesTracker) Rebind(agent *agentcore.Agent) {
	if agent == nil {
		return
	}
	agent.On(agentcore.EventToolCallStart, func(e agentcore.Event) {
		ev := e.(*agentcore.ToolCallStartEvent)
		t.mu.Lock()
		// Evict orphaned entries if the map has grown too large.
		if len(t.pending) >= 128 {
			t.pending = make(map[string]*pendingToolCall)
		}
		t.pending[ev.ToolCall.ID] = &pendingToolCall{
			toolName: ev.ToolCall.Name,
			argsJSON: ev.ToolCall.Arguments,
		}
		t.mu.Unlock()
	})
	agent.On(agentcore.EventToolCallEnd, func(e agentcore.Event) {
		ev := e.(*agentcore.ToolCallEndEvent)
		if ev.Err != nil {
			t.mu.Lock()
			delete(t.pending, ev.ToolCallID)
			t.mu.Unlock()
			return
		}
		t.mu.Lock()
		pc := t.pending[ev.ToolCallID]
		delete(t.pending, ev.ToolCallID)
		t.mu.Unlock()
		if pc == nil {
			return
		}
		t.recordFromToolCall(pc.toolName, pc.argsJSON, ev.Result)
	})
}

// fileMutatingToolPaths maps tool names to the argument key that holds the file path.
var fileMutatingToolPaths = map[string]string{
	"write_file":  "path",
	"write":       "path",
	"edit_block":  "path",
	"edit":        "file_path",
	"append_file": "file_path",
	"move":        "path",
	"patch":       "path",
	"delete_file": "path",
}

func (t *ChangedFilesTracker) recordFromToolCall(toolName, argsJSON, result string) {
	// Patch tools extract file paths from patch content rather than a
	// single path argument, so handle them before the map lookup below.
	if toolName == "patch" || toolName == "apply_patch" {
		var args map[string]any
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return
		}
		paths := extractPatchPaths(args)
		for _, p := range paths {
			t.recordEntry(p, ActionModified, toolName)
		}
		return
	}

	paramKey, ok := fileMutatingToolPaths[toolName]
	if !ok {
		return
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return
	}

	path, _ := args[paramKey].(string)
	if path == "" {
		// Some tools use "path" as a fallback
		if p, ok := args["path"].(string); ok {
			path = p
		}
	}
	if path == "" {
		return
	}

	// Determine action: if file existed before (tool is write/edit) → modified,
	// if new → created, if delete_file → deleted.
	action := ActionModified
	switch toolName {
	case "delete_file":
		action = ActionDeleted
	case "write_file", "write":
		// Check if the result indicates a new file
		var r map[string]any
		if json.Unmarshal([]byte(result), &r) == nil {
			if created, ok := r["created"].(bool); ok && created {
				action = ActionCreated
			}
		}
		// If we can't determine from result, check if we've seen this file before
		if action == ActionModified {
			t.mu.RLock()
			_, seen := t.entries[path]
			t.mu.RUnlock()
			if !seen {
				// Check if file existed before by looking at the result for bytes_written
				// without a prior entry — treat as created if no prior entry
				action = ActionCreated
			}
		}
	}

	t.recordEntry(path, action, toolName)
}

func extractPatchPaths(args map[string]any) []string {
	patchContent, _ := args["patch"].(string)
	if patchContent == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(patchContent, "\n") {
		line = strings.TrimSpace(line)
		for _, p := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if strings.HasPrefix(line, p) {
				path := strings.TrimSpace(line[len(p):])
				if path != "" {
					paths = append(paths, path)
				}
			}
		}
	}
	return paths
}

func (t *ChangedFilesTracker) recordEntry(path string, action FileChangeAction, tool string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry := &FileChangeEntry{
		Path:   path,
		Action: action,
		Tool:   tool,
		Time:   time.Now(),
	}

	if _, exists := t.entries[path]; !exists {
		t.order = append(t.order, path)
	}
	t.entries[path] = entry
}

// Entries returns a copy of all tracked file changes in insertion order.
func (t *ChangedFilesTracker) Entries() []FileChangeEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]FileChangeEntry, 0, len(t.order))
	for _, path := range t.order {
		if entry, ok := t.entries[path]; ok {
			result = append(result, *entry)
		}
	}
	return result
}

// Reset clears all tracked changes (e.g., on /new or /clear).
func (t *ChangedFilesTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = make(map[string]*FileChangeEntry)
	t.order = nil
}

// openChangedFilesPanel creates and displays the changed files panel.
func openChangedFilesPanel(tracker *ChangedFilesTracker, workingDir string) {
	if tracker == nil {
		return
	}
	entries := func() []agentpanels.FileChange {
		tracked := tracker.Entries()
		changes := make([]agentpanels.FileChange, 0, len(tracked))
		for _, entry := range tracked {
			changes = append(changes, agentpanels.FileChange{
				Path:   entry.Path,
				Action: string(entry.Action),
				Tool:   entry.Tool,
			})
		}
		return changes
	}
	panel := agentpanels.NewChangedFilesPanel(entries, workingDir)
	var ov chat.OverlayRef
	closeOverlay := func() {
		loadUIBus().ClosePanel(ov)
	}
	panel.SetOnCancel(closeOverlay)
	ov = loadUIBus().ShowPanel(panel, 70, 70)
}

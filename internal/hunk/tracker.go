// Package hunk tracks file changes with source attribution (Agent vs External).
//
// It watches the workspace for file modifications and records whether each
// change was made by the agent (via tool calls) or by external processes
// (user's editor, other tools, VCS operations). This enables:
//   - Conflict detection (agent and user editing the same file simultaneously)
//   - Selective rewind (undo only agent changes, preserve user edits)
//   - Audit trail (which tool call produced which hunk)
package hunk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Source identifies who made a file change.
type Source int

const (
	SourceAgent   Source = iota // changed by agent tool call
	SourceExternal              // changed by user/external process
	SourceUnknown               // origin cannot be determined
)

func (s Source) String() string {
	switch s {
	case SourceAgent:
		return "agent"
	case SourceExternal:
		return "external"
	default:
		return "unknown"
	}
}

// Hunk represents a single tracked file change.
type Hunk struct {
	ID         string    `json:"id"`
 FilePath   string    `json:"file_path"`
	Source     Source    `json:"source"`
	ToolName   string    `json:"tool_name,omitempty"`   // for agent changes
	ToolCallID string    `json:"tool_call_id,omitempty"` // for agent changes
	HashBefore string    `json:"hash_before,omitempty"`
	HashAfter  string    `json:"hash_after,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	LinesAdded int       `json:"lines_added"`
	LinesDeleted int     `json:"lines_deleted"`
}

// FileState tracks the last-known state of a file.
type FileState struct {
	Hash     string
	ModTime  time.Time
	Size     int64
	Source   Source // who last modified it
}

// Tracker monitors file changes and attributes them to sources.
type Tracker struct {
	mu          sync.Mutex
	workspace   string
	fileStates  map[string]*FileState  // path -> state
	hunks       []Hunk                 // chronological log
	maxHunks    int
}

// NewTracker creates a hunk tracker for the given workspace.
func NewTracker(workspace string) *Tracker {
	return &Tracker{
		workspace:  workspace,
		fileStates: make(map[string]*FileState),
		maxHunks:   10000,
	}
}

// RecordAgentEdit records a file change made by an agent tool call.
// Call this BEFORE and AFTER the agent writes to a file.
func (t *Tracker) RecordAgentEdit(path, toolName, toolCallID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	hash, info, err := hashAndStat(abs)
	if err != nil {
		return err
	}

	before := t.fileStates[abs]
	hunk := Hunk{
		ID:         generateID(abs, time.Now()),
		FilePath:   abs,
		Source:     SourceAgent,
		ToolName:   toolName,
		ToolCallID: toolCallID,
		HashAfter:  hash,
		Timestamp:  time.Now(),
	}

	if before != nil {
		hunk.HashBefore = before.Hash
		hunk.LinesAdded, hunk.LinesDeleted = estimateLineDelta(before.Size, info.Size())
	} else {
		hunk.LinesAdded = int(info.Size() / 50) // rough estimate
	}

	t.hunks = append(t.hunks, hunk)
	t.trimHunks()
	t.fileStates[abs] = &FileState{
		Hash:    hash,
		ModTime: info.ModTime(),
		Size:    info.Size(),
		Source:  SourceAgent,
	}

	return nil
}

// CheckExternalChanges scans the workspace for files modified since the last
// known state and records them as external changes. Returns the list of
// newly detected external changes.
func (t *Tracker) CheckExternalChanges() ([]Hunk, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var newHunks []Hunk

	err := filepath.Walk(t.workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Skip .git directory
		if strings.Contains(path, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			return nil
		}

		abs, _ := filepath.Abs(path)
		known, exists := t.fileStates[abs]

		if !exists {
			// New file — record as external if not agent-created
			hash, _, err := hashAndStat(abs)
			if err != nil {
				return nil
			}
			hunk := Hunk{
				ID:        generateID(abs, time.Now()),
				FilePath:  abs,
				Source:    SourceExternal,
				HashAfter: hash,
				Timestamp: time.Now(),
			}
			t.hunks = append(t.hunks, hunk)
			newHunks = append(newHunks, hunk)
			t.fileStates[abs] = &FileState{
				Hash:    hash,
				ModTime: info.ModTime(),
				Size:    info.Size(),
				Source:  SourceExternal,
			}
			return nil
		}

		// Check if modified since last known state
		if info.ModTime().After(known.ModTime) || info.Size() != known.Size {
			hash, _, err := hashAndStat(abs)
			if err != nil {
				return nil
			}
			if hash == known.Hash {
				// Content unchanged, just metadata
				known.ModTime = info.ModTime()
				return nil
			}

			// If the last source was agent, this is an external modification
			source := SourceExternal
			if known.Source == SourceAgent {
				source = SourceExternal // someone modified after agent
			}

			hunk := Hunk{
				ID:         generateID(abs, time.Now()),
				FilePath:   abs,
				Source:     source,
				HashBefore: known.Hash,
				HashAfter:  hash,
				Timestamp:  time.Now(),
				LinesAdded: estimateAdded(known.Size, info.Size()),
				LinesDeleted: estimateDeleted(known.Size, info.Size()),
			}
			t.hunks = append(t.hunks, hunk)
			newHunks = append(newHunks, hunk)
			t.fileStates[abs] = &FileState{
				Hash:    hash,
				ModTime: info.ModTime(),
				Size:    info.Size(),
				Source:  source,
			}
		}

		return nil
	})

	t.trimHunks()
	return newHunks, err
}

// GetHunks returns all tracked hunks, optionally filtered by source.
func (t *Tracker) GetHunks(source Source) []Hunk {
	t.mu.Lock()
	defer t.mu.Unlock()

	if source == SourceUnknown {
		result := make([]Hunk, len(t.hunks))
		copy(result, t.hunks)
		return result
	}

	var result []Hunk
	for _, h := range t.hunks {
		if h.Source == source {
			result = append(result, h)
		}
	}
	return result
}

// GetFileHistory returns all hunks for a specific file.
func (t *Tracker) GetFileHistory(path string) []Hunk {
	t.mu.Lock()
	defer t.mu.Unlock()

	abs, _ := filepath.Abs(path)
	var result []Hunk
	for _, h := range t.hunks {
		if h.FilePath == abs {
			result = append(result, h)
		}
	}
	return result
}

// HasConflict checks if a file was modified by both agent and external
// sources, indicating a potential conflict.
func (t *Tracker) HasConflict(path string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	abs, _ := filepath.Abs(path)
	hasAgent := false
	hasExternal := false
	for _, h := range t.hunks {
		if h.FilePath == abs {
			if h.Source == SourceAgent {
				hasAgent = true
			}
			if h.Source == SourceExternal {
				hasExternal = true
			}
		}
	}
	return hasAgent && hasExternal
}

// GetConflicts returns all files with both agent and external modifications.
func (t *Tracker) GetConflicts() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	fileSources := make(map[string]map[Source]bool)
	for _, h := range t.hunks {
		if fileSources[h.FilePath] == nil {
			fileSources[h.FilePath] = make(map[Source]bool)
		}
		fileSources[h.FilePath][h.Source] = true
	}

	var conflicts []string
	for path, sources := range fileSources {
		if sources[SourceAgent] && sources[SourceExternal] {
			conflicts = append(conflicts, path)
		}
	}
	return conflicts
}

// Reset clears all tracking state.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fileStates = make(map[string]*FileState)
	t.hunks = nil
}

// SetMaxHunks adjusts the maximum number of hunks to retain.
func (t *Tracker) SetMaxHunks(max int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maxHunks = max
	t.trimHunks()
}

func (t *Tracker) trimHunks() {
	if len(t.hunks) > t.maxHunks {
		t.hunks = t.hunks[len(t.hunks)-t.maxHunks:]
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func hashAndStat(path string) (string, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:16]), info, nil
}

func generateID(path string, t time.Time) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", path, t.UnixNano())))
	return hex.EncodeToString(h[:8])
}

func estimateLineDelta(oldSize, newSize int64) (added, deleted int) {
	if newSize > oldSize {
		added = int((newSize - oldSize) / 50)
	} else if oldSize > newSize {
		deleted = int((oldSize - newSize) / 50)
	}
	return
}

func estimateAdded(oldSize, newSize int64) int {
	if newSize > oldSize {
		return int((newSize - oldSize) / 50)
	}
	return 0
}

func estimateDeleted(oldSize, newSize int64) int {
	if oldSize > newSize {
		return int((oldSize - newSize) / 50)
	}
	return 0
}

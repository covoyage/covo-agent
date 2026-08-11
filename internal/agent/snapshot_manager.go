package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/snapshot"
)

// fileMutatingTools is the set of tools that change file contents and should
// trigger a snapshot after execution.
var fileMutatingTools = map[string]bool{
	"write_file":  true,
	"edit_block":  true,
	"edit":        true,
	"move":        true,
	"patch":       true,
	"apply_patch": true,
	"append_file": true,
	"bash":        true, // bash may write files via shell redirection
}

// SnapshotManager coordinates file-level snapshots and revert for a CovoAgent
// session. It wraps the snapshot.Service and maintains a history of snapshots
// taken during the session, enabling /undo, revert-to-point, and /rewind (which
// restores both chat history and workspace files in one action).
//
// Each Entry records a MessageIndex that aligns the snapshot with a position in
// the conversation, so /rewind can find the correct workspace state for any
// selected conversation turn.
type SnapshotManager struct {
	mu       sync.Mutex
	service  *snapshot.Service
	entries  []snapshot.Entry
	storeDir string
	// revertSnapshot stores the tree hash captured just before the most recent
	// revert, so unrevert can restore to that state.
	revertSnapshot string
	// reverted tracks whether we're currently in a reverted state.
	reverted bool
}

// NewSnapshotManager creates a manager wrapping the given snapshot service.
func NewSnapshotManager(svc *snapshot.Service) *SnapshotManager {
	return &SnapshotManager{service: svc}
}

// Service returns the underlying snapshot service (may be nil or disabled).
func (m *SnapshotManager) Service() *snapshot.Service { return m.service }

// Enabled reports whether snapshot tracking is available.
func (m *SnapshotManager) Enabled() bool {
	return m != nil && m.service != nil && m.service.Enabled()
}

// ShouldSnapshot returns true if the given tool name is a file-mutating tool
// that should trigger a snapshot.
func ShouldSnapshot(toolName string) bool {
	return fileMutatingTools[toolName]
}

// SetStoreDir configures a directory for JSON persistence of snapshot entries.
// When set, entries are saved to snapshots.json and loaded on startup.
func (m *SnapshotManager) SetStoreDir(dir string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storeDir = dir
	m.loadPersistedLocked()
}

// Track captures a snapshot after a tool execution and records it in the
// session history. msgIdx is the current conversation message count, used by
// FindClosest to align workspace state with chat history for /rewind.
func (m *SnapshotManager) Track(toolName string, msgIdx int) error {
	if !m.Enabled() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	prevHash := ""
	if len(m.entries) > 0 {
		prevHash = m.entries[len(m.entries)-1].Hash
	}

	hash, err := m.service.Track()
	if err != nil {
		return fmt.Errorf("snapshot track: %w", err)
	}
	if hash == "" {
		return nil
	}

	entry := snapshot.Entry{
		Hash:         hash,
		ToolName:     toolName,
		Timestamp:    time.Now().Unix(),
		MessageIndex: msgIdx,
	}

	// Compute changed files since previous snapshot.
	if prevHash != "" && hash != prevHash {
		patch, err := m.service.Patch(prevHash)
		if err == nil {
			entry.Files = patch.Files
		}
	}

	m.entries = append(m.entries, entry)
	m.reverted = false // new activity clears reverted state
	m.persistLocked()
	return nil
}

// FindClosest returns a copy of the entry whose MessageIndex is closest to
// (but not greater than) the given target index. This is used by /rewind to
// find the workspace state that corresponds to a selected conversation turn.
// Returns false if no entries exist.
func (m *SnapshotManager) FindClosest(targetMsgIdx int) (snapshot.Entry, bool) {
	if m == nil {
		return snapshot.Entry{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) == 0 {
		return snapshot.Entry{}, false
	}

	var bestIdx int = -1
	for i := range m.entries {
		if m.entries[i].MessageIndex <= targetMsgIdx {
			if bestIdx == -1 || m.entries[i].MessageIndex >= m.entries[bestIdx].MessageIndex {
				bestIdx = i
			}
		}
	}

	// If no entry is <= target, return the first entry (earliest state).
	if bestIdx == -1 {
		bestIdx = 0
	}
	return m.entries[bestIdx], true
}

// Get returns a copy of the entry at the given index, or false if out of range.
func (m *SnapshotManager) Get(index int) (snapshot.Entry, bool) {
	if m == nil {
		return snapshot.Entry{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.entries) {
		return snapshot.Entry{}, false
	}
	return m.entries[index], true
}

// Undo reverts the most recent snapshot, restoring files to the state before
// the last file-mutating tool call. If already at the earliest snapshot, does
// nothing. Returns the number of files reverted.
func (m *SnapshotManager) Undo() (int, error) {
	if !m.Enabled() {
		return 0, fmt.Errorf("snapshot service not available")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) < 2 {
		return 0, fmt.Errorf("no snapshot to undo (need at least 2, have %d)", len(m.entries))
	}

	// The last entry is the current state. The one before it is where we
	// revert to. We revert the files changed in the last entry.
	last := m.entries[len(m.entries)-1]
	target := m.entries[len(m.entries)-2]

	if len(last.Files) == 0 {
		return 0, fmt.Errorf("last snapshot recorded no file changes")
	}

	// Capture current state for potential unrevert.
	currentHash, _ := m.service.Track()
	m.revertSnapshot = currentHash

	// Revert files to the target snapshot state.
	patches := []snapshot.Patch{{Hash: target.Hash, Files: last.Files}}
	if err := m.service.Revert(patches); err != nil {
		return 0, fmt.Errorf("undo revert: %w", err)
	}

	// Remove the last entry (we've undone it).
	m.entries = m.entries[:len(m.entries)-1]
	m.reverted = true
	m.persistLocked()

	return len(last.Files), nil
}

// RevertTo reverts to the snapshot at the given index, restoring all files
// changed since that snapshot. Returns the number of files reverted.
func (m *SnapshotManager) RevertTo(index int) (int, error) {
	if !m.Enabled() {
		return 0, fmt.Errorf("snapshot service not available")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if index < 0 || index >= len(m.entries)-1 {
		return 0, fmt.Errorf("invalid snapshot index %d (range 0-%d)", index, len(m.entries)-2)
	}

	target := m.entries[index]

	// Collect all files changed from index to the end.
	var allFiles []string
	seen := make(map[string]bool)
	for i := index + 1; i < len(m.entries); i++ {
		for _, f := range m.entries[i].Files {
			if !seen[f] {
				seen[f] = true
				allFiles = append(allFiles, f)
			}
		}
	}

	if len(allFiles) == 0 {
		return 0, fmt.Errorf("no files to revert")
	}

	// Capture current state for unrevert.
	currentHash, _ := m.service.Track()
	m.revertSnapshot = currentHash

	// Revert all changed files to the target snapshot.
	patches := []snapshot.Patch{{Hash: target.Hash, Files: allFiles}}
	if err := m.service.Revert(patches); err != nil {
		return 0, fmt.Errorf("revert to %d: %w", index, err)
	}

	// Truncate entries to the target.
	m.entries = m.entries[:index+1]
	m.reverted = true
	m.persistLocked()

	return len(allFiles), nil
}

// Unrevert restores the working tree to the state captured just before the
// most recent revert/undo. No-op if no revert has been performed.
func (m *SnapshotManager) Unrevert() error {
	if !m.Enabled() {
		return fmt.Errorf("snapshot service not available")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.reverted || m.revertSnapshot == "" {
		return fmt.Errorf("nothing to unrevert")
	}

	if err := m.service.Restore(m.revertSnapshot); err != nil {
		return fmt.Errorf("unrevert: %w", err)
	}

	// Re-track to add the restored state as a new entry.
	hash, err := m.service.Track()
	if err != nil {
		return fmt.Errorf("unrevert re-track: %w", err)
	}
	m.entries = append(m.entries, snapshot.Entry{
		Hash:         hash,
		ToolName:     "unrevert",
		Timestamp:    time.Now().Unix(),
		MessageIndex: 0, // unrevert doesn't align to a specific message
	})
	m.reverted = false
	m.revertSnapshot = ""
	m.persistLocked()
	return nil
}

// Restore restores the working tree to the given snapshot hash.
// This is used by /rewind to restore workspace files when rewinding the
// conversation.
func (m *SnapshotManager) Restore(hash string) error {
	if !m.Enabled() {
		return fmt.Errorf("snapshot service not available")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.service.Restore(hash)
}

// Diff returns a unified diff between the snapshot at the given index and the
// current working tree state. If index is -1 or omitted, diffs against the
// previous snapshot (i.e., shows changes since the last snapshot).
func (m *SnapshotManager) Diff(index int) (string, error) {
	if !m.Enabled() {
		return "", fmt.Errorf("snapshot service not available")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) == 0 {
		return "", fmt.Errorf("no snapshots available")
	}

	var fromHash string
	if index < 0 {
		// Diff against the last snapshot.
		fromHash = m.entries[len(m.entries)-1].Hash
	} else {
		if index < 0 || index >= len(m.entries) {
			return "", fmt.Errorf("invalid snapshot index %d (range 0-%d)", index, len(m.entries)-1)
		}
		fromHash = m.entries[index].Hash
	}

	// Track to refresh the index with current file state.
	if _, err := m.service.Track(); err != nil {
		return "", fmt.Errorf("diff: track: %w", err)
	}

	diff, err := m.service.Diff(fromHash)
	if err != nil {
		return "", fmt.Errorf("diff: %w", err)
	}
	return diff, nil
}

// DiffBetween returns a unified diff between two snapshot indices.
func (m *SnapshotManager) DiffBetween(fromIdx, toIdx int) (string, error) {
	if !m.Enabled() {
		return "", fmt.Errorf("snapshot service not available")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) == 0 {
		return "", fmt.Errorf("no snapshots available")
	}
	if fromIdx < 0 || fromIdx >= len(m.entries) {
		return "", fmt.Errorf("invalid from index %d", fromIdx)
	}
	if toIdx < 0 || toIdx >= len(m.entries) {
		return "", fmt.Errorf("invalid to index %d", toIdx)
	}

	fromHash := m.entries[fromIdx].Hash
	toHash := m.entries[toIdx].Hash

	// Use the snapshot service's git to diff between two tree hashes.
	// We need to temporarily set the index to the to-hash state.
	// Since Diff() diffs fromHash against the current index, we first
	// need to read-tree the toHash into the index.
	if err := m.service.RestoreIndex(toHash); err != nil {
		return "", fmt.Errorf("diff: restore index: %w", err)
	}

	diff, err := m.service.Diff(fromHash)
	if err != nil {
		// Even on error, try to restore the index to the working tree state.
		_, _ = m.service.Track()
		return "", fmt.Errorf("diff: %w", err)
	}

	// Restore the index to reflect the current working tree state.
	// RestoreIndex left the index pointing at toHash; without this,
	// subsequent Diff/Patch calls would see stale index data until
	// the next Track() call.
	// Non-fatal: the diff is already computed; the next Track corrects the index.
	_, _ = m.service.Track()

	return diff, nil
}

// List returns the snapshot history for display.
func (m *SnapshotManager) List() []snapshot.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]snapshot.Entry, len(m.entries))
	copy(out, m.entries)
	return out
}

// FormatList returns a human-readable summary of the snapshot history.
func (m *SnapshotManager) FormatList() string {
	entries := m.List()
	if len(entries) == 0 {
		return "(no snapshots)"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("── Snapshots (%d) ──\n", len(entries)))
	for i, e := range entries {
		ts := time.Unix(e.Timestamp, 0).Format("15:04:05")
		tool := e.ToolName
		if tool == "" {
			tool = "(initial)"
		}
		fileCount := len(e.Files)
		b.WriteString(fmt.Sprintf("  [%d] %s msg#%d %s (%d files)\n", i, ts, e.MessageIndex, tool, fileCount))
	}
	if m.reverted {
		b.WriteString("  ⚠ currently in reverted state (use /unrevert to restore)\n")
	}
	b.WriteString("\n  Use /checkpoint restore <index> to restore chat + workspace")
	return b.String()
}

// --- persistence ---

// maxSnapshotEntries caps the number of snapshot entries retained in memory
// and on disk. Older entries are pruned automatically when new ones are
// recorded, preventing unbounded growth during long sessions.
const maxSnapshotEntries = 100

func (m *SnapshotManager) persistLocked() {
	if m.storeDir == "" {
		return
	}
	// Auto-prune: keep only the most recent maxSnapshotEntries.
	if len(m.entries) > maxSnapshotEntries {
		m.entries = m.entries[len(m.entries)-maxSnapshotEntries:]
	}
	_ = os.MkdirAll(m.storeDir, 0755)
	path := filepath.Join(m.storeDir, "snapshots.json")
	data, err := json.Marshal(m.entries)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func (m *SnapshotManager) loadPersistedLocked() {
	if m.storeDir == "" {
		return
	}
	path := filepath.Join(m.storeDir, "snapshots.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var loaded []snapshot.Entry
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}
	m.entries = loaded
}

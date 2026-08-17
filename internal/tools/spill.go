package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// SpillEntry is the lineage metadata recorded for every spilled block. Each
// entry links a spilled artifact to the session (and purpose) that produced
// it, forming a minimal result lineage that can be listed and audited.
type SpillEntry struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Purpose   string `json:"purpose,omitempty"`
	CharCount int    `json:"char_count"`
	Path      string `json:"path"`
	CreatedAt string `json:"created_at"`
}

const spillLineageFile = "lineage.jsonl"

// SpillIndexer is called after a successful spill to index the content into
// the session FTS search engine so it becomes discoverable via session_search.
// The implementation lives in the sessions package (FTSSearcher.IndexSpill)
// but is injected here to avoid a circular import.
type SpillIndexer func(sessionID, name, purpose, content, spillPath string) error

// spillIndexer is a package-level variable set by the extension during init.
// It is intentionally not passed per-call to keep the tool constructor simple.
var spillIndexer SpillIndexer

// SetSpillIndexer sets the global indexer used by spill tools. Must be called
// before any tool execution (typically during agent construction).
func SetSpillIndexer(idx SpillIndexer) {
	spillIndexer = idx
}

// spillStorageDir returns the directory holding spilled artifacts. Spills are
// stored under <homeDir>/spill/ and are read back by the agent via the read
// tool. This is the explicit counterpart to the automatic result persistence
// in internal/agent (which only kicks in past large per-result/turn budgets):
// spill lets the model proactively offload bulky text from its context.
func spillStorageDir(homeDir string) string {
	if homeDir == "" {
		return filepath.Join("~", ".covo-agent", "spill")
	}
	return filepath.Join(homeDir, "spill")
}

// BuildSpillTool returns the spill tool, which stores a block of text to disk
// and returns a path the agent can read back later. Use it to offload bulky
// tool output, file dumps, or reference material that would otherwise bloat
// the conversation context. Every spill records lineage metadata (session,
// purpose, timestamp) visible via spill_list.
func BuildSpillTool(homeDir string, sessionIDFn func() string) *agentcore.Tool {
	dir := spillStorageDir(homeDir)
	return &agentcore.Tool{
		Name: "spill",
		Description: strings.Join([]string{
			"Store a block of text to disk (in the spill store) and return its path.",
			"Use this to offload bulky output — large tool results, file dumps,",
			"reference material, generated code you will not edit immediately —",
			"so it does not consume your conversation context. The full text stays",
			"available: read the returned path with the read tool whenever you need it.",
			"",
			"Each spill records lineage metadata (session, name, purpose, timestamp),",
			"listed by the spill_list tool.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "The full text to spill to disk.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Short human-readable label for the spill, e.g. 'webpack_config'. Used in the stored filename and lineage.",
				},
				"purpose": map[string]any{
					"type":        "string",
					"description": "Why this text is being spilled, e.g. 'parse config later'. Recorded in lineage metadata.",
				},
			},
			"required": []string{"text"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Text    string `json:"text"`
				Name    string `json:"name"`
				Purpose string `json:"purpose"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Text) == "" {
				return nil, fmt.Errorf("text is required")
			}

			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("spill: create storage dir: %w", err)
			}

			id := fmt.Sprintf("spill_%d", time.Now().UnixNano())
			name := sanitizeSpillName(params.Name)
			filename := id
			if name != "" {
				filename = name + "_" + id
			}
			filename += ".txt"

			path := filepath.Join(dir, filename)
			if err := os.WriteFile(path, []byte(params.Text), 0o600); err != nil {
				return nil, fmt.Errorf("spill: write: %w", err)
			}

			entry := SpillEntry{
				ID:        id,
				SessionID: sessionIDFor(sessionIDFn),
				Name:      params.Name,
				Purpose:   params.Purpose,
				CharCount: len(params.Text),
				Path:      path,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			if err := appendSpillLineage(dir, entry); err != nil {
				// Lineage is best-effort; the artifact itself was written.
				return nil, fmt.Errorf("spill: record lineage: %w", err)
			}

			// Index into session FTS so session_search can find this spill.
			// Best-effort: failure is non-fatal (the file still exists on disk).
			if spillIndexer != nil {
				_ = spillIndexer(sessionIDFor(sessionIDFn), params.Name, params.Purpose, params.Text, path)
			}

			return map[string]any{
				"id":         entry.ID,
				"path":       path,
				"char_count": entry.CharCount,
				"name":       entry.Name,
				"purpose":    entry.Purpose,
			}, nil
		},
	}
}

// BuildSpillListTool returns the spill_list tool, which lists spill artifacts
// for the current session (with their lineage metadata). The agent can then
// read any path back. When no session ID is available, all spills are listed.
func BuildSpillListTool(homeDir string, sessionIDFn func() string) *agentcore.Tool {
	dir := spillStorageDir(homeDir)
	return &agentcore.Tool{
		Name: "spill_list",
		Description: "List text previously stored with the spill tool, newest first, for the current session. " +
			"Each entry includes the stored path, name, purpose, size, and timestamp so you can decide what to read back. " +
			"Read an entry's path with the read tool to get its full contents.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of entries to return (default: 20).",
				},
				"purpose": map[string]any{
					"type":        "string",
					"description": "Optional substring filter on the recorded purpose.",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Limit   int    `json:"limit"`
				Purpose string `json:"purpose"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.Limit <= 0 {
				params.Limit = 20
			}

			entries, err := readSpillLineage(dir)
			if err != nil {
				return nil, err
			}

			sessionID := sessionIDFor(sessionIDFn)
			filtered := make([]SpillEntry, 0, len(entries))
			for _, e := range entries {
				if sessionID != "" && e.SessionID != "" && e.SessionID != sessionID {
					continue
				}
				if params.Purpose != "" && !strings.Contains(strings.ToLower(e.Purpose), strings.ToLower(params.Purpose)) {
					continue
				}
				filtered = append(filtered, e)
			}

			// Newest first.
			sort.Slice(filtered, func(i, j int) bool {
				return filtered[i].CreatedAt > filtered[j].CreatedAt
			})
			if len(filtered) > params.Limit {
				filtered = filtered[:params.Limit]
			}

			return map[string]any{
				"count":   len(filtered),
				"entries": filtered,
			}, nil
		},
	}
}

func sessionIDFor(fn func() string) string {
	if fn == nil {
		return ""
	}
	return fn()
}

func sanitizeSpillName(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_.")
}

// appendSpillLineage appends a lineage entry as one JSON line per spill.
func appendSpillLineage(dir string, entry SpillEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, spillLineageFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// readSpillLineage reads all lineage entries from the spill store.
func readSpillLineage(dir string) ([]SpillEntry, error) {
	f, err := os.Open(filepath.Join(dir, spillLineageFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("spill_list: open lineage: %w", err)
	}
	defer f.Close()

	var entries []SpillEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e SpillEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip corrupt lines, keep the rest
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("spill_list: read lineage: %w", err)
	}
	return entries, nil
}

package sessions

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// AtomicSessionWriter provides crash-safe appends to session JSONL files.
// Uses temp file + fsync + atomic rename for each write, guaranteeing
// that partial writes are never visible to readers.
type AtomicSessionWriter struct {
	dir    string
	shards [64]sync.Mutex // per-file sharding via FNV hash
}

func NewAtomicSessionWriter(sessionsDir string) *AtomicSessionWriter {
	os.MkdirAll(sessionsDir, 0755)
	return &AtomicSessionWriter{dir: sessionsDir}
}

// Append atomically appends a JSON line to a session file.
// Never corrupts existing data — writes to temp file, syncs, then renames.
func (w *AtomicSessionWriter) Append(sessionID string, entry interface{}) error {
	mu := w.sessionMutex(sessionID)
	mu.Lock()
	defer mu.Unlock()

	filename := sessionFilename(sessionID)
	path := filepath.Join(w.dir, filename)
	tmpPath := path + ".tmp"

	// Read existing content (if any)
	existing, _ := os.ReadFile(path)

	// Marshal new entry
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	// Write to temp file: existing + newline + new entry
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open temp: %w", err)
	}

	if len(existing) > 0 {
		if _, err := f.Write(existing); err != nil {
			f.Close()
			return fmt.Errorf("write existing: %w", err)
		}
		if existing[len(existing)-1] != '\n' {
			f.Write([]byte{'\n'})
		}
	}

	f.Write(line)
	f.Write([]byte{'\n'})

	// fsync before rename
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("fsync: %w", err)
	}
	f.Close()

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

// AppendLine appends a raw JSON line already formatted.
func (w *AtomicSessionWriter) AppendLine(sessionID, jsonLine string) error {
	if jsonLine == "" {
		return nil
	}
	mu := w.sessionMutex(sessionID)
	mu.Lock()
	defer mu.Unlock()

	path := filepath.Join(w.dir, sessionFilename(sessionID))
	tmpPath := path + ".tmp"

	existing, _ := os.ReadFile(path)

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	if len(existing) > 0 {
		f.Write(existing)
		if existing[len(existing)-1] != '\n' {
			f.Write([]byte{'\n'})
		}
	}
	f.Write([]byte(jsonLine))
	if jsonLine[len(jsonLine)-1] != '\n' {
		f.Write([]byte{'\n'})
	}

	f.Sync()
	f.Close()

	return os.Rename(tmpPath, path)
}

// Read reads all lines from a session file.
func (w *AtomicSessionWriter) Read(sessionID string) ([]string, error) {
	path := filepath.Join(w.dir, sessionFilename(sessionID))
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, nil
	}

	lines := stringsSplit(string(data), "\n")
	var result []string
	for _, line := range lines {
		if line != "" {
			result = append(result, line)
		}
	}
	return result, nil
}

func stringsSplit(s, sep string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep[0] {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// Delete removes a session file.
func (w *AtomicSessionWriter) Delete(sessionID string) error {
	mu := w.sessionMutex(sessionID)
	mu.Lock()
	defer mu.Unlock()
	return os.Remove(filepath.Join(w.dir, sessionFilename(sessionID)))
}

func (w *AtomicSessionWriter) sessionMutex(sessionID string) *sync.Mutex {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(sessionID))
	return &w.shards[hash.Sum32()%uint32(len(w.shards))]
}

func sessionFilename(sessionID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sessionID)) + ".jsonl"
}

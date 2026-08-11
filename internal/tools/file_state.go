package tools

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type FileStateRegistry struct {
	mu     sync.RWMutex
	reads  map[string][]FileAccess
	writes map[string]FileAccess
	locks  map[string]*sync.Mutex
}

type FileAccess struct {
	Path    string
	AgentID string
	ReadAt  time.Time
	Mtime   time.Time
	Partial bool
}

func NewFileStateRegistry() *FileStateRegistry {
	return &FileStateRegistry{
		reads:  make(map[string][]FileAccess),
		writes: make(map[string]FileAccess),
		locks:  make(map[string]*sync.Mutex),
	}
}

func (r *FileStateRegistry) LockPath(path string) func() {
	r.mu.Lock()
	lk, ok := r.locks[path]
	if !ok {
		lk = &sync.Mutex{}
		r.locks[path] = lk
	}
	r.mu.Unlock()
	lk.Lock()
	return lk.Unlock
}

func (r *FileStateRegistry) RecordRead(agentID, path string, partial bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stat, _ := os.Stat(path)
	mtime := time.Time{}
	if stat != nil {
		mtime = stat.ModTime()
	}
	r.reads[path] = append(r.reads[path], FileAccess{
		Path: path, AgentID: agentID, ReadAt: time.Now(), Mtime: mtime, Partial: partial,
	})
}

func (r *FileStateRegistry) RecordWrite(agentID, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes[path] = FileAccess{Path: path, AgentID: agentID, ReadAt: time.Now()}
}

func (r *FileStateRegistry) CheckStale(agentID, path string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lastWrite, hasWrite := r.writes[path]
	if !hasWrite {
		return false
	}
	if lastWrite.AgentID == agentID {
		return false
	}
	accesses := r.reads[path]
	for i := len(accesses) - 1; i >= 0; i-- {
		if accesses[i].AgentID == agentID {
			return lastWrite.ReadAt.After(accesses[i].ReadAt)
		}
	}
	return true
}

// CheckpointManager uses git for file snapshots.
type CheckpointManager struct {
	mu     sync.Mutex
	gitDir string
}

func NewCheckpointManager(dataDir string) *CheckpointManager {
	return &CheckpointManager{gitDir: dataDir}
}

func (cm *CheckpointManager) Snapshot(workdir string) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if _, err := runGit(workdir, "add", "--all"); err != nil {
		return "", err
	}
	return runGit(workdir, "write-tree")
}

func (cm *CheckpointManager) Diff(workdir, snapshotHash string) ([]string, error) {
	out, err := runGit(workdir, "diff", "--name-only", snapshotHash)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (cm *CheckpointManager) Restore(workdir, snapshotHash string) error {
	_, err := runGit(workdir, "checkout", snapshotHash, "--", ".")
	return err
}

func runGit(workdir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workdir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	var result []string
	for _, l := range lines {
		if l != "" {
			result = append(result, l)
		}
	}
	return result
}

package app

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// GitBranchTracker periodically captures the current branch and dirty-file count.
type GitBranchTracker struct {
	mu       sync.RWMutex
	branch   string
	dir      string
	stop     chan struct{}
	stopOnce sync.Once
}

func NewGitBranchTracker(dir string) *GitBranchTracker {
	tracker := &GitBranchTracker{dir: dir, stop: make(chan struct{})}
	go tracker.loop()
	return tracker
}

func (tracker *GitBranchTracker) loop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	tracker.refresh()
	for {
		select {
		case <-tracker.stop:
			return
		case <-ticker.C:
			tracker.refresh()
		}
	}
}

func (tracker *GitBranchTracker) refresh() {
	branch := readGitBranch(tracker.dir)
	tracker.mu.Lock()
	tracker.branch = branch
	tracker.mu.Unlock()
}

func (tracker *GitBranchTracker) Branch() string {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	return tracker.branch
}

func (tracker *GitBranchTracker) Stop() {
	tracker.stopOnce.Do(func() { close(tracker.stop) })
}

func readGitBranch(dir string) string {
	command := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(output))
	if branch == "HEAD" {
		return ""
	}

	diffCommand := exec.Command("git", "-C", dir, "diff", "--stat")
	diffOutput, _ := diffCommand.Output()
	changed := 0
	if len(diffOutput) > 0 {
		changed = len(strings.Split(strings.TrimSpace(string(diffOutput)), "\n"))
	}
	if changed > 0 {
		return fmt.Sprintf("⎇ %s (+%d)", branch, changed)
	}
	return fmt.Sprintf("⎇ %s", branch)
}

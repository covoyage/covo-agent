package tools

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type checkpointSection struct {
	Topic       string
	Goal        string
	Directives  []string
	TaskTree    string
	CurrentWork string
	Files       []string
	Learnings   []string
	Errors      []string
	Resources   []string
	Decisions   []string
	Notes       []string
}

func (c *checkpointSection) Render() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Topic\n%s\n\n", c.Topic))
	b.WriteString(fmt.Sprintf("## Goal\n%s\n\n", c.Goal))
	if len(c.Directives) > 0 {
		b.WriteString("## Directives\n")
		for _, d := range c.Directives {
			b.WriteString(fmt.Sprintf("- %s\n", d))
		}
		b.WriteString("\n")
	}
	if c.TaskTree != "" {
		b.WriteString(fmt.Sprintf("## Task Tree\n%s\n\n", c.TaskTree))
	}
	if c.CurrentWork != "" {
		b.WriteString(fmt.Sprintf("## Current Work\n%s\n\n", c.CurrentWork))
	}
	if len(c.Files) > 0 {
		b.WriteString("## Files\n")
		for _, f := range c.Files {
			b.WriteString(fmt.Sprintf("- %s\n", f))
		}
		b.WriteString("\n")
	}
	if len(c.Learnings) > 0 {
		b.WriteString("## Learnings\n")
		for _, l := range c.Learnings {
			b.WriteString(fmt.Sprintf("- %s\n", l))
		}
		b.WriteString("\n")
	}
	if len(c.Errors) > 0 {
		b.WriteString("## Errors\n")
		for _, e := range c.Errors {
			b.WriteString(fmt.Sprintf("- %s\n", e))
		}
		b.WriteString("\n")
	}
	if len(c.Decisions) > 0 {
		b.WriteString("## Decisions\n")
		for _, d := range c.Decisions {
			b.WriteString(fmt.Sprintf("- %s\n", d))
		}
		b.WriteString("\n")
	}
	if len(c.Notes) > 0 {
		b.WriteString("## Notes\n")
		for _, n := range c.Notes {
			b.WriteString(fmt.Sprintf("- %s\n", n))
		}
		b.WriteString("\n")
	}
	return b.String()
}

type CheckpointWriter struct {
	mu     sync.Mutex
	dir    string
	budget int
}

func NewCheckpointWriter(dataDir string) *CheckpointWriter {
	return &CheckpointWriter{dir: dataDir, budget: 20}
}

func (cw *CheckpointWriter) Write(sessionID string, section *checkpointSection) (string, error) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	os.MkdirAll(cw.dir, 0755)
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-%s-checkpoint.md", sessionID, timestamp)
	path := cw.dir + "/" + filename
	if err := os.WriteFile(path, []byte(section.Render()), 0644); err != nil {
		return "", err
	}
	cw.prune()
	return path, nil
}

func (cw *CheckpointWriter) prune() {
	entries, _ := os.ReadDir(cw.dir)
	type entry struct {
		name string
		time time.Time
	}
	var checkpoints []entry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "-checkpoint.md") {
			info, _ := e.Info()
			if info != nil {
				checkpoints = append(checkpoints, entry{e.Name(), info.ModTime()})
			}
		}
	}
	if len(checkpoints) > cw.budget {
		for i := 0; i < len(checkpoints); i++ {
			for j := i + 1; j < len(checkpoints); j++ {
				if checkpoints[j].time.After(checkpoints[i].time) {
					checkpoints[i], checkpoints[j] = checkpoints[j], checkpoints[i]
				}
			}
		}
		for i := cw.budget; i < len(checkpoints); i++ {
			os.Remove(cw.dir + "/" + checkpoints[i].name)
		}
	}
}

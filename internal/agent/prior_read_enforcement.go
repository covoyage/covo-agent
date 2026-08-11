package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

type ReadTracker struct {
	mu   sync.RWMutex
	read map[string]bool
}

func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		read: make(map[string]bool),
	}
}

func (t *ReadTracker) RecordRead(filePath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.read[filepath.Clean(filePath)] = true
}

func (t *ReadTracker) HasRead(filePath string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.read[filepath.Clean(filePath)]
}

func (t *ReadTracker) Snapshot() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var files []string
	for f := range t.read {
		files = append(files, f)
	}
	return files
}

func (t *ReadTracker) PriorReadAfterHook() agentcore.AfterHook {
	return func(ctx context.Context, hc *agentcore.HookContext, result string, err error) {
		if hc.ToolName != "read" {
			return
		}
		var args map[string]any
		if err := json.Unmarshal(hc.Arguments, &args); err != nil {
			return
		}
		path, _ := args["path"].(string)
		if path == "" {
			path, _ = args["file_path"].(string)
		}
		if path != "" {
			t.RecordRead(path)
		}
	}
}

func (t *ReadTracker) PriorReadBeforeHook() agentcore.BeforeHook {
	return func(ctx context.Context, hc *agentcore.HookContext) error {
		paramKey, ok := fileWriteToolNames[hc.ToolName]
		if !ok {
			return nil
		}
		var args map[string]any
		if err := json.Unmarshal(hc.Arguments, &args); err != nil {
			return nil
		}
		path, _ := args[paramKey].(string)
		if path == "" {
			return nil
		}
		// New-file creation is exempt: a file that does not exist yet has no
		// prior content to read, so requiring a read first would make initial
		// creation impossible. Only enforce prior-read when overwriting/editing
		// an existing file.
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			return nil
		}
		if !t.HasRead(path) {
			return fmt.Errorf(
				"%q has not been read in this session yet. "+
					"Read the file first with the read tool before editing — this ensures edits are "+
					"based on actual content, not guesswork. Do NOT use bash/shell redirection "+
					"(>, tee, heredoc) to work around this.",
				path,
			)
		}
		return nil
	}
}

// truncatingRedirectRe matches output-redirection targets that OVERWRITE a file
// (single `>`, optionally with a leading fd number or `&`). Appends (`>>`) are
// intentionally excluded because appending does not depend on prior content.
// Targets that are descriptors/devices (e.g. `2>&1`, `> /dev/null`) do not match
// because the capture group forbids `&` and the /dev/ prefix is filtered later.
var truncatingRedirectRe = regexp.MustCompile(`(?:^|[^>])(?:[0-9]+|&)?>(?:\s*)("[^"]+"|'[^']+'|[^\s>&;|]+)`)

// teeWriteRe matches `tee` invocations that truncate their target file(s). The
// `-a`/`--append` flag is excluded so log appends are not enforced.
var teeWriteRe = regexp.MustCompile(`\btee\b((?:\s+-[a-zA-Z-]+)*)\s+("[^"]+"|'[^']+'|[^\s>&;|]+)`)

// shellWriteTargets returns the file paths a shell command would create or
// overwrite via output redirection or `tee`. It is a heuristic covering the
// common ways an agent might write a file from bash (`echo ... > f`,
// `cat > f <<EOF`, `tee f`), which would otherwise bypass the file-tool
// prior-read enforcement.
func shellWriteTargets(command string) []string {
	var targets []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		raw = strings.Trim(raw, `"'`)
		if raw == "" || strings.HasPrefix(raw, "/dev/") {
			return
		}
		targets = append(targets, raw)
	}
	for _, m := range truncatingRedirectRe.FindAllStringSubmatch(command, -1) {
		add(m[1])
	}
	for _, m := range teeWriteRe.FindAllStringSubmatch(command, -1) {
		if strings.Contains(m[1], "-a") || strings.Contains(m[1], "--append") {
			continue
		}
		add(m[2])
	}
	return targets
}

// ShellWritePriorReadViolation reports the first existing, unread file that the
// given shell command would overwrite. workingDir is used to resolve relative
// paths. New files (not yet on disk) are exempt, mirroring the file-tool hook.
func (t *ReadTracker) ShellWritePriorReadViolation(command, workingDir string) (string, bool) {
	for _, target := range shellWriteTargets(command) {
		path := target
		if !filepath.IsAbs(path) {
			path = filepath.Join(workingDir, path)
		}
		if _, err := os.Stat(path); err != nil {
			continue // missing/unreadable → treat as new-file creation, exempt
		}
		if !t.HasRead(path) {
			return path, true
		}
	}
	return "", false
}

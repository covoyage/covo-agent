package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var hintFilenames = []string{
	"AGENTS.md", "agents.md",
	"CLAUDE.md", "claude.md",
	".cursorrules",
}

const maxHintChars = 8000
const maxAncestorWalk = 5

var pathArgKeys = map[string]bool{
	"path":      true,
	"file_path": true,
	"workdir":   true,
}

var commandTools = map[string]bool{
	"terminal": true,
}

type SubdirectoryHintTracker struct {
	workingDir string
	loadedDirs map[string]bool
}

func NewSubdirectoryHintTracker(workingDir string) *SubdirectoryHintTracker {
	abs, _ := filepath.Abs(workingDir)
	t := &SubdirectoryHintTracker{
		workingDir: abs,
		loadedDirs: make(map[string]bool),
	}
	t.loadedDirs[abs] = true
	return t
}

func (t *SubdirectoryHintTracker) CheckToolCall(toolName string, toolArgs map[string]any) string {
	dirs := t.extractDirectories(toolName, toolArgs)
	if len(dirs) == 0 {
		return ""
	}

	var allHints []string
	for _, d := range dirs {
		hints := t.loadHintsForDirectory(d)
		if hints != "" {
			allHints = append(allHints, hints)
		}
	}

	if len(allHints) == 0 {
		return ""
	}

	return "\n\n" + strings.Join(allHints, "\n\n")
}

func (t *SubdirectoryHintTracker) extractDirectories(toolName string, args map[string]any) []string {
	candidates := make(map[string]bool)

	for key := range pathArgKeys {
		val, ok := args[key]
		if !ok {
			continue
		}
		strVal, ok := val.(string)
		if !ok || strings.TrimSpace(strVal) == "" {
			continue
		}
		t.addPathCandidate(strVal, candidates)
	}

	if commandTools[toolName] {
		cmd, ok := args["command"]
		if ok {
			cmdStr, ok := cmd.(string)
			if ok {
				t.extractPathsFromCommand(cmdStr, candidates)
			}
		}
	}

	result := make([]string, 0, len(candidates))
	for d := range candidates {
		result = append(result, d)
	}
	return result
}

func (t *SubdirectoryHintTracker) addPathCandidate(rawPath string, candidates map[string]bool) {
	if strings.HasPrefix(rawPath, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			rawPath = filepath.Join(home, rawPath[1:])
		}
	}

	p := rawPath
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.workingDir, p)
	}

	p, err := filepath.Abs(p)
	if err != nil {
		return
	}

	info, err := os.Stat(p)
	if err == nil && !info.IsDir() {
		p = filepath.Dir(p)
	}

	for i := 0; i < maxAncestorWalk; i++ {
		if t.loadedDirs[p] {
			break
		}
		if t.isValidSubdir(p) {
			candidates[p] = true
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
}

func (t *SubdirectoryHintTracker) extractPathsFromCommand(cmd string, candidates map[string]bool) {
	tokens := strings.Fields(cmd)
	for _, token := range tokens {
		if strings.HasPrefix(token, "-") {
			continue
		}
		if !strings.Contains(token, "/") && !strings.Contains(token, ".") {
			continue
		}
		if strings.HasPrefix(token, "http://") || strings.HasPrefix(token, "https://") || strings.HasPrefix(token, "git@") {
			continue
		}
		t.addPathCandidate(token, candidates)
	}
}

func (t *SubdirectoryHintTracker) isValidSubdir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	if t.loadedDirs[path] {
		return false
	}

	rel, err := filepath.Rel(t.workingDir, path)
	if err != nil {
		return false
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}

func (t *SubdirectoryHintTracker) loadHintsForDirectory(directory string) string {
	t.loadedDirs[directory] = true

	rel, err := filepath.Rel(t.workingDir, directory)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}

	for _, filename := range hintFilenames {
		hintPath := filepath.Join(directory, filename)
		info, err := os.Stat(hintPath)
		if err != nil || info.IsDir() {
			continue
		}

		data, err := os.ReadFile(hintPath)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}

		if len(content) > maxHintChars {
			content = content[:maxHintChars] + fmt.Sprintf("\n\n[...truncated %s: %d chars total]", filename, len(content))
		}

		relPath := hintPath
		if r, err := filepath.Rel(t.workingDir, hintPath); err == nil {
			relPath = r
		} else if home, err := os.UserHomeDir(); err == nil {
			if r, err := filepath.Rel(home, hintPath); err == nil {
				relPath = "~/" + r
			}
		}

		return fmt.Sprintf("[Subdirectory context discovered: %s]\n%s", relPath, content)
	}

	return ""
}

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/diff"
)

const (
	diffPreviewContextLines = 3
	diffPreviewMaxLines     = 40
	maxStartedDiffPreviews  = 64
)

type diffPreviewer struct {
	workingDir string
	emit       func([]diff.FileDiff)
	mu         sync.Mutex
	started    map[string]*diffToolCallContext
}

type diffToolCallContext struct {
	toolName string
	oldFiles map[string][]byte
}

// BindDiffPreviewer observes mutating tool calls and emits structured diffs.
func BindDiffPreviewer(agent *agentcore.Agent, workingDir string, emit func([]diff.FileDiff)) {
	previewer := &diffPreviewer{
		workingDir: workingDir,
		emit:       emit,
		started:    make(map[string]*diffToolCallContext),
	}

	agent.On(agentcore.EventToolCallStart, func(event agentcore.Event) {
		toolCall := event.(*agentcore.ToolCallStartEvent)
		context := previewer.captureOldContent(toolCall.ToolCall.Name, toolCall.ToolCall.Arguments)
		previewer.mu.Lock()
		if len(previewer.started) >= maxStartedDiffPreviews {
			previewer.started = make(map[string]*diffToolCallContext)
		}
		previewer.started[toolCall.ToolCall.ID] = context
		previewer.mu.Unlock()
	})

	agent.On(agentcore.EventToolCallEnd, func(event agentcore.Event) {
		toolCall := event.(*agentcore.ToolCallEndEvent)
		previewer.mu.Lock()
		context := previewer.started[toolCall.ToolCallID]
		delete(previewer.started, toolCall.ToolCallID)
		previewer.mu.Unlock()
		if context == nil || toolCall.Err != nil {
			return
		}
		previews := previewer.generateDiffs(context, toolCall.Result)
		if len(previews) > 0 && previewer.emit != nil {
			previewer.emit(previews)
		}
	})
}

func (previewer *diffPreviewer) captureOldContent(toolName, arguments string) *diffToolCallContext {
	context := &diffToolCallContext{toolName: toolName, oldFiles: make(map[string][]byte)}
	for _, path := range previewer.extractFilePaths(toolName, arguments) {
		if path == "" {
			continue
		}
		if content, err := os.ReadFile(previewer.resolvePath(path)); err == nil {
			context.oldFiles[path] = content
		}
	}
	return context
}

func (previewer *diffPreviewer) extractFilePaths(toolName, arguments string) []string {
	switch toolName {
	case "write_file", "write", "edit_block":
		return []string{extractDiffJSONField(arguments, "path")}
	case "edit":
		path := extractDiffJSONField(arguments, "file_path")
		if path == "" {
			path = extractDiffJSONField(arguments, "path")
		}
		return []string{path}
	case "append_file":
		return []string{extractDiffJSONField(arguments, "file_path")}
	case "patch", "apply_patch":
		return extractPatchFilePaths(arguments)
	default:
		return nil
	}
}

func extractPatchFilePaths(arguments string) []string {
	patchContent := extractDiffJSONField(arguments, "patch")
	if patchContent == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(patchContent, "\n") {
		if path := extractV4AFilePath(strings.TrimSpace(line)); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func extractV4AFilePath(line string) string {
	for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	if strings.HasPrefix(line, "*** Move File: ") {
		rest := strings.TrimSpace(line[len("*** Move File: "):])
		parts := strings.Split(rest, " -> ")
		if len(parts) == 2 {
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}

func (previewer *diffPreviewer) resolvePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if strings.HasPrefix(path, "~") {
		homeDir, _ := os.UserHomeDir()
		return strings.Replace(path, "~", homeDir, 1)
	}
	return filepath.Join(previewer.workingDir, path)
}

func (previewer *diffPreviewer) generateDiffs(context *diffToolCallContext, result string) []diff.FileDiff {
	if len(context.oldFiles) == 0 {
		if preview, ok := diffFromToolResult(context.toolName, result); ok {
			return []diff.FileDiff{preview}
		}
		return nil
	}

	paths := make([]string, 0, len(context.oldFiles))
	for path := range context.oldFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	previews := make([]diff.FileDiff, 0, len(paths))
	for _, path := range paths {
		oldContent := context.oldFiles[path]
		newContent, err := os.ReadFile(previewer.resolvePath(path))
		if err != nil || string(oldContent) == string(newContent) {
			continue
		}
		unified := diff.Unified(string(oldContent), string(newContent), filepath.Base(path), diffPreviewContextLines, diffPreviewMaxLines)
		if unified != "" {
			previews = append(previews, diff.FileDiff{Path: path, Unified: unified})
		}
	}
	return previews
}

func diffFromToolResult(toolName, result string) (diff.FileDiff, bool) {
	if toolName != "edit_block" {
		return diff.FileDiff{}, false
	}
	var response struct {
		Diff string `json:"diff"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(result), &response); err != nil || response.Diff == "" {
		return diff.FileDiff{}, false
	}
	if response.Path == "" {
		response.Path = "file"
	}
	return diff.FileDiff{Path: response.Path, Unified: response.Diff}, true
}

func extractDiffJSONField(arguments, field string) string {
	var values map[string]any
	if err := json.Unmarshal([]byte(arguments), &values); err != nil {
		return ""
	}
	value, _ := values[field].(string)
	return value
}

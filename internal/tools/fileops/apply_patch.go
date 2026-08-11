package fileops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

type patchOpType string

const (
	patchAdd    patchOpType = "add"
	patchUpdate patchOpType = "update"
	patchDelete patchOpType = "delete"
	patchMove   patchOpType = "move"
)

type patchLine struct {
	prefix  string // " ", "-", "+"
	content string
}

type patchHunk struct {
	contextHint string
	lines       []patchLine
}

type patchOp struct {
	op       patchOpType
	filePath string
	newPath  string
	hunks    []patchHunk
}

type validationError struct {
	opIndex int
	message string
}

func parseV4APatch(patchContent string) ([]patchOp, error) {
	lines := strings.Split(patchContent, "\n")

	startIdx := -1
	endIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "*** Begin Patch" || trimmed == "***Begin Patch" {
			startIdx = i
		} else if trimmed == "*** End Patch" || trimmed == "***End Patch" {
			endIdx = i
			break
		}
	}

	if startIdx == -1 {
		// Try parsing without explicit begin marker — scan for *** markers
		for _, line := range lines {
			if strings.Contains(line, "***") && (strings.Contains(line, "File:") || strings.Contains(line, " Patch")) {
				startIdx = 0
				break
			}
		}
	}
	if startIdx == -1 {
		startIdx = 0
	}
	if endIdx == -1 {
		endIdx = len(lines)
	}

	var ops []patchOp
	var currentOp *patchOp
	var currentHunk *patchHunk

	updateRe := regexp.MustCompile(`^\*\*\*\s*Update\s+File:\s*(.+)$`)
	addRe := regexp.MustCompile(`^\*\*\*\s*Add\s+File:\s*(.+)$`)
	deleteRe := regexp.MustCompile(`^\*\*\*\s*Delete\s+File:\s*(.+)$`)
	moveRe := regexp.MustCompile(`^\*\*\*\s*Move\s+File:\s*(.+?)\s*->\s*(.+)$`)
	hintRe := regexp.MustCompile(`^@@\s*(.+?)\s*@@`)

	for i := startIdx + 1; i < endIdx; i++ {
		line := lines[i]

		if matches := updateRe.FindStringSubmatch(line); matches != nil {
			if currentOp != nil {
				ops = append(ops, *currentOp)
			}
			currentOp = &patchOp{op: patchUpdate, filePath: strings.TrimSpace(matches[1])}
			currentHunk = nil
		} else if matches := addRe.FindStringSubmatch(line); matches != nil {
			if currentOp != nil {
				ops = append(ops, *currentOp)
			}
			currentOp = &patchOp{op: patchAdd, filePath: strings.TrimSpace(matches[1])}
			currentHunk = &patchHunk{}
		} else if matches := deleteRe.FindStringSubmatch(line); matches != nil {
			if currentOp != nil {
				ops = append(ops, *currentOp)
			}
			currentOp = &patchOp{op: patchDelete, filePath: strings.TrimSpace(matches[1])}
			ops = append(ops, *currentOp)
			currentOp = nil
			currentHunk = nil
		} else if matches := moveRe.FindStringSubmatch(line); matches != nil {
			if currentOp != nil {
				ops = append(ops, *currentOp)
			}
			currentOp = &patchOp{
				op:       patchMove,
				filePath: strings.TrimSpace(matches[1]),
				newPath:  strings.TrimSpace(matches[2]),
			}
			ops = append(ops, *currentOp)
			currentOp = nil
			currentHunk = nil
		} else if strings.HasPrefix(line, "@@") {
			if currentOp != nil {
				if currentHunk != nil && len(currentHunk.lines) > 0 {
					currentOp.hunks = append(currentOp.hunks, *currentHunk)
				}
				hint := ""
				if m := hintRe.FindStringSubmatch(line); m != nil {
					hint = m[1]
				}
				currentHunk = &patchHunk{contextHint: hint}
			}
		} else if currentOp != nil && line != "" {
			if currentHunk == nil {
				currentHunk = &patchHunk{}
			}
			if strings.HasPrefix(line, "+") {
				currentHunk.lines = append(currentHunk.lines, patchLine{"+", line[1:]})
			} else if strings.HasPrefix(line, "-") {
				currentHunk.lines = append(currentHunk.lines, patchLine{"-", line[1:]})
			} else if strings.HasPrefix(line, " ") {
				currentHunk.lines = append(currentHunk.lines, patchLine{" ", line[1:]})
			} else if strings.HasPrefix(line, "\\") {
				// "\ No newline at end of file" — skip
			} else {
				currentHunk.lines = append(currentHunk.lines, patchLine{" ", line})
			}
		}
	}

	if currentOp != nil {
		if currentHunk != nil && len(currentHunk.lines) > 0 {
			currentOp.hunks = append(currentOp.hunks, *currentHunk)
		}
		ops = append(ops, *currentOp)
	}

	// Basic structural validation
	for i, op := range ops {
		switch op.op {
		case patchUpdate:
			if len(op.hunks) == 0 {
				return nil, fmt.Errorf("UPDATE %q: no hunks found (op %d)", op.filePath, i)
			}
		case patchAdd:
			if len(op.hunks) == 0 {
				return nil, fmt.Errorf("ADD %q: no content found (op %d)", op.filePath, i)
			}
		case patchMove:
			if op.newPath == "" {
				return nil, fmt.Errorf("MOVE %q: missing destination (op %d)", op.filePath, i)
			}
		}
	}

	return ops, nil
}

// Validate all operations against current file state without writing anything.
// Returns errors only — empty slice means all operations are valid.
func validateOps(ops []patchOp, workspaceDir string) []validationError {
	var errs []validationError

	for i, op := range ops {
		path := resolvePatchPath(op.filePath, workspaceDir)

		switch op.op {
		case patchUpdate:
			content, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					errs = append(errs, validationError{i, fmt.Sprintf("File not found: %s", op.filePath)})
				} else {
					errs = append(errs, validationError{i, fmt.Sprintf("Cannot read %s: %v", op.filePath, err)})
				}
				continue
			}
			text := string(content)
			simulated := text

			for _, hunk := range op.hunks {
				searchLines := make([]string, 0)
				replaceLines := make([]string, 0)
				for _, line := range hunk.lines {
					if line.prefix == " " {
						searchLines = append(searchLines, line.content)
						replaceLines = append(replaceLines, line.content)
					} else if line.prefix == "-" {
						searchLines = append(searchLines, line.content)
					} else if line.prefix == "+" {
						replaceLines = append(replaceLines, line.content)
					}
				}

				if len(searchLines) > 0 {
					searchPattern := strings.Join(searchLines, "\n")
					replacement := strings.Join(replaceLines, "\n")
					newSimulated, found := applyHunk(simulated, searchPattern, replacement, hunk.contextHint)
					if !found {
						errs = append(errs, validationError{i, fmt.Sprintf("Hunk not found in %s: %q", op.filePath, truncate(searchPattern, 80))})
						break
					}
					simulated = newSimulated
				}
			}

		case patchDelete:
			if _, err := os.Stat(path); os.IsNotExist(err) {
				errs = append(errs, validationError{i, fmt.Sprintf("File not found for deletion: %s", op.filePath)})
			}

		case patchMove:
			dstPath := resolvePatchPath(op.newPath, workspaceDir)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				errs = append(errs, validationError{i, fmt.Sprintf("Source file not found: %s", op.filePath)})
			}
			if _, err := os.Stat(dstPath); err == nil {
				errs = append(errs, validationError{i, fmt.Sprintf("Destination already exists: %s", op.newPath)})
			}

		case patchAdd:
			// No pre-validation needed for adds
		}
	}

	return errs
}

// applyHunk tries to find and replace searchPattern in text. Returns (newText, found).
// Falls back to context-hint-anchored search then fuzzy search.
func applyHunk(text, searchPattern, replacement, contextHint string) (string, bool) {
	idx := strings.Index(text, searchPattern)
	if idx >= 0 {
		return text[:idx] + replacement + text[idx+len(searchPattern):], true
	}

	// Try context-hint-anchored search
	if contextHint != "" {
		hintIdx := strings.Index(text, contextHint)
		if hintIdx >= 0 {
			windowStart := hintIdx - 300
			if windowStart < 0 {
				windowStart = 0
			}
			windowEnd := hintIdx + len(contextHint) + 500
			if windowEnd > len(text) {
				windowEnd = len(text)
			}
			window := text[windowStart:windowEnd]
			windowIdx := strings.Index(window, searchPattern)
			if windowIdx >= 0 {
				absIdx := windowStart + windowIdx
				return text[:absIdx] + replacement + text[absIdx+len(searchPattern):], true
			}
		}
	}

	// Fuzzy: try whitespace-normalized matching
	searchNorm := normalizeWhitespace(searchPattern)
	textNorm := normalizeWhitespace(text)
	fuzzyIdx := strings.Index(textNorm, searchNorm)
	if fuzzyIdx >= 0 {
		// Map back to original text — approximate by character position
		origIdx := runeOffsetToByteOffset(text, fuzzyIdx, textNorm)
		if origIdx >= 0 {
			return text[:origIdx] + replacement + text[origIdx+len(searchPattern):], true
		}
	}

	// Try individual line matching
	searchLines := strings.Split(searchPattern, "\n")
	if len(searchLines) == 1 {
		textLines := strings.Split(text, "\n")
		for li, line := range textLines {
			trimmed := strings.TrimSpace(line)
			if trimmed == strings.TrimSpace(searchLines[0]) {
				before := strings.Join(textLines[:li], "\n")
				after := strings.Join(textLines[li+1:], "\n")
				result := before
				if result != "" {
					result += "\n"
				}
				result += replacement
				if after != "" {
					result += "\n" + after
				}
				return result, true
			}
		}
	}

	return text, false
}

func normalizeWhitespace(s string) string {
	// Collapse all whitespace sequences to single space, trim
	re := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(re.ReplaceAllString(s, " "))
}

func runeOffsetToByteOffset(text string, runeOffset int, normalized string) int {
	// Map from position in normalized text back to position in original text.
	// This is approximate — we scan forward in both simultaneously, skipping
	// whitespace runs in the original text.
	ti := 0
	ni := 0
	textRunes := []rune(text)
	normRunes := []rune(normalized)

	for ni < runeOffset && ti < len(textRunes) {
		// Skip whitespace in original text
		for ti < len(textRunes) && textRunes[ti] == ' ' || textRunes[ti] == '\t' || textRunes[ti] == '\n' || textRunes[ti] == '\r' {
			ti++
		}
		// Skip whitespace in normalized text
		for ni < len(normRunes) && (normRunes[ni] == ' ') {
			ni++
		}

		if ti >= len(textRunes) || ni >= len(normRunes) {
			break
		}
		ti++
		ni++
	}

	if ti < len(textRunes) {
		return len(string(textRunes[:ti]))
	}
	return -1
}

// applyPatchOps applies the operations (assumes validation already passed).
func applyPatchOps(ops []patchOp, workspaceDir string) (map[string]any, error) {
	var filesModified []string
	var filesCreated []string
	var filesDeleted []string
	var errors []string

	for _, op := range ops {
		switch op.op {
		case patchAdd:
			path := resolvePatchPath(op.filePath, workspaceDir)
			content := extractAddContent(op.hunks)

			mutErr := withFileMutationLock(path, func() error {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					return err
				}
				return os.WriteFile(path, []byte(content), 0644)
			})
			if mutErr != nil {
				errors = append(errors, fmt.Sprintf("Failed to write %s: %v", path, mutErr))
				continue
			}
			notifyAfterWrite(path, "apply_patch", "")
			filesCreated = append(filesCreated, op.filePath)

		case patchUpdate:
			path := resolvePatchPath(op.filePath, workspaceDir)

			mutErr := withFileMutationLock(path, func() error {
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				newContent := string(content)
				for _, hunk := range op.hunks {
					searchLines := make([]string, 0)
					replaceLines := make([]string, 0)

					for _, line := range hunk.lines {
						if line.prefix == " " {
							searchLines = append(searchLines, line.content)
							replaceLines = append(replaceLines, line.content)
						} else if line.prefix == "-" {
							searchLines = append(searchLines, line.content)
						} else if line.prefix == "+" {
							replaceLines = append(replaceLines, line.content)
						}
					}

					if len(searchLines) > 0 {
						searchPattern := strings.Join(searchLines, "\n")
						replacement := strings.Join(replaceLines, "\n")
						newContent, _ = applyHunk(newContent, searchPattern, replacement, hunk.contextHint)
					} else {
						insertText := strings.Join(replaceLines, "\n")
						if hunk.contextHint != "" {
							hintIdx := strings.Index(newContent, hunk.contextHint)
							if hintIdx >= 0 {
								eol := strings.Index(newContent[hintIdx:], "\n")
								if eol >= 0 {
									pos := hintIdx + eol + 1
									newContent = newContent[:pos] + insertText + "\n" + newContent[pos:]
								} else {
									newContent = newContent + "\n" + insertText
								}
							} else {
								newContent = strings.TrimRight(newContent, "\n") + "\n" + insertText + "\n"
							}
						} else {
							newContent = strings.TrimRight(newContent, "\n") + "\n" + insertText + "\n"
						}
					}
				}

				return os.WriteFile(path, []byte(newContent), 0644)
			})
			if mutErr != nil {
				errors = append(errors, fmt.Sprintf("Failed to write %s: %v", path, mutErr))
				continue
			}
			notifyAfterWrite(path, "apply_patch", "")
			filesModified = append(filesModified, op.filePath)

		case patchDelete:
			path := resolvePatchPath(op.filePath, workspaceDir)
			mutErr := withFileMutationLock(path, func() error {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
				return nil
			})
			if mutErr != nil {
				errors = append(errors, fmt.Sprintf("Failed to delete %s: %v", path, mutErr))
				continue
			}
			filesDeleted = append(filesDeleted, op.filePath)

		case patchMove:
			srcPath := resolvePatchPath(op.filePath, workspaceDir)
			dstPath := resolvePatchPath(op.newPath, workspaceDir)
			mutErr := withFileMutationLock(srcPath, func() error {
				if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
					return err
				}
				return os.Rename(srcPath, dstPath)
			})
			if mutErr != nil {
				errors = append(errors, fmt.Sprintf("Failed to move %s -> %s: %v", op.filePath, op.newPath, mutErr))
				continue
			}
			filesModified = append(filesModified, fmt.Sprintf("%s -> %s", op.filePath, op.newPath))
		}
	}

	result := map[string]any{
		"files_modified": filesModified,
		"files_created":  filesCreated,
		"files_deleted":  filesDeleted,
	}
	if len(errors) > 0 {
		result["status"] = "partial"
		result["errors"] = errors
	} else {
		result["status"] = "ok"
	}
	return result, nil
}

func extractAddContent(hunks []patchHunk) string {
	var lines []string
	for _, hunk := range hunks {
		for _, line := range hunk.lines {
			if line.prefix == "+" {
				lines = append(lines, line.content)
			}
		}
	}
	return strings.Join(lines, "\n")
}

func resolvePatchPath(filePath, workspaceDir string) string {
	if strings.HasPrefix(filePath, "~") {
		home, _ := os.UserHomeDir()
		filePath = strings.Replace(filePath, "~", home, 1)
	}
	return resolveReadPath(filePath, workspaceDir)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func BuildApplyPatchTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "apply_patch",
		Description: strings.Join([]string{
			"Apply a structured V4A format patch to modify files in the workspace.",
			"",
			"The V4A patch format supports:",
			"  *** Begin Patch",
			"  *** Update File: path/to/file.py",
			"    @@ context hint @@",
			"    context line (space prefix)",
			"   -removed line (minus prefix)",
			"   +added line (plus prefix)",
			"  *** Add File: path/to/new.py",
			"   +new file content",
			"  *** Delete File: path/to/old.py",
			"  *** Move File: old/path.py -> new/path.py",
			"  *** End Patch",
			"",
			"Lines with a space prefix are context for matching.",
			"Lines with '-' are removed. Lines with '+' are added.",
			"Context hints (@@ hint @@) help disambiguate when the same pattern appears multiple times.",
			"The path is relative to the current workspace directory.",
			"",
			"Uses a two-phase validate-then-apply approach: all hunks are validated",
			"before any file is modified. If validation fails, no files are changed.",
			"",
			"For simple single-file edits, use 'edit_block' instead.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patch": map[string]any{
					"type":        "string",
					"description": "The V4A patch content (between *** Begin Patch and *** End Patch markers).",
				},
				"workspace_dir": map[string]any{
					"type":        "string",
					"description": "Optional workspace root directory. Defaults to current directory.",
				},
			},
			"required": []string{"patch"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Patch        string `json:"patch"`
				WorkspaceDir string `json:"workspace_dir"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Patch) == "" {
				return nil, fmt.Errorf("patch content is required")
			}

			workspaceDir := params.WorkspaceDir
			if workspaceDir == "" {
				workspaceDir, _ = os.Getwd()
			}
			if strings.HasPrefix(workspaceDir, "~") {
				home, _ := os.UserHomeDir()
				workspaceDir = strings.Replace(workspaceDir, "~", home, 1)
			}

			ops, err := parseV4APatch(params.Patch)
			if err != nil {
				return nil, fmt.Errorf("patch parse error: %w", err)
			}
			if len(ops) == 0 {
				return map[string]any{
					"status": "no_changes",
					"note":   "No patch operations found. Use *** Begin Patch / *** End Patch markers.",
				}, nil
			}

			// Phase 1: Validate without modifying files
			validationErrs := validateOps(ops, workspaceDir)
			if len(validationErrs) > 0 {
				errMsgs := make([]string, len(validationErrs))
				for i, ve := range validationErrs {
					errMsgs[i] = ve.message
				}
				return nil, fmt.Errorf("patch validation failed (no files modified):\n  - %s", strings.Join(errMsgs, "\n  - "))
			}

			// Phase 2: Apply
			result, err := applyPatchOps(ops, workspaceDir)
			if err != nil {
				return nil, fmt.Errorf("patch apply error: %w", err)
			}
			return result, nil
		},
	}
}

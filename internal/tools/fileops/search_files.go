package fileops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// buildSearchFilesTool creates a unified file search tool that combines
// filename matching (glob) and content search (grep) into a single call.
// This reduces the number of round-trips when the LLM needs to locate files
// or code — instead of calling grep then glob separately, it can do both in
// one shot and get ranked, deduplicated results.
func BuildSearchFilesTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "search_files",
		Description: strings.Join([]string{
			"Unified file search: find files by name pattern AND/OR search file contents.",
			"Combines glob (filename matching) and grep (content search) into one call.",
			"",
			"Parameters:",
			"- 'path': Base directory to search in (default: current working directory).",
			"- 'pattern': Content search pattern (regex). If empty, only filename matching is performed.",
			"- 'glob': Filename glob pattern (e.g. '**/*.go', '*.{ts,tsx}'). If empty, only content search.",
			"- 'case_insensitive': Case-insensitive content search (default: false).",
			"- 'max_results': Maximum results to return (default: 50).",
			"- 'context_lines': Lines of context around content matches (default: 2).",
			"",
			"If both 'pattern' and 'glob' are provided, only files matching the glob",
			"are searched for content. If only 'glob' is provided, matching file paths",
			"are returned. If only 'pattern' is provided, all files are searched.",
			"",
			"Results include: file path, matched lines (for content search), and a relevance score.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Base directory to search in (default: cwd).",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "Content search pattern (regex). Empty = filename-only search.",
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "Filename glob pattern (e.g. '**/*.go'). Empty = search all files.",
				},
				"case_insensitive": map[string]any{
					"type":        "boolean",
					"description": "Case-insensitive content search (default: false).",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum results to return (default: 50).",
				},
				"context_lines": map[string]any{
					"type":        "integer",
					"description": "Lines of context around content matches (default: 2).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Path            string `json:"path"`
				Pattern         string `json:"pattern"`
				Glob            string `json:"glob"`
				CaseInsensitive bool   `json:"case_insensitive"`
				MaxResults      int    `json:"max_results"`
				ContextLines    int    `json:"context_lines"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if params.Path == "" {
				params.Path = "."
			}
			absPath, err := filepath.Abs(resolveReadPath(params.Path, ""))
			if err != nil {
				return nil, fmt.Errorf("resolve path: %w", err)
			}
			if info, err := os.Stat(absPath); err != nil || !info.IsDir() {
				return nil, fmt.Errorf("path is not a directory: %s", absPath)
			}

			if params.MaxResults <= 0 {
				params.MaxResults = 50
			}
			if params.ContextLines <= 0 {
				params.ContextLines = 2
			}
			if params.ContextLines > 10 {
				params.ContextLines = 10
			}

			hasContentSearch := strings.TrimSpace(params.Pattern) != ""
			hasGlobSearch := strings.TrimSpace(params.Glob) != ""
			if !hasContentSearch && !hasGlobSearch {
				return nil, fmt.Errorf("at least one of 'pattern' or 'glob' must be provided")
			}

			start := time.Now()

			// Phase 1: Glob matching (if glob pattern provided)
			var globMatches []string
			if hasGlobSearch {
				globMatches = globSearch(absPath, params.Glob)
			}

			// Phase 2: Content search (if pattern provided)
			var contentResults []contentMatch
			if hasContentSearch {
				searchFiles := globMatches
				if !hasGlobSearch {
					searchFiles = nil // grep will search all files
				}
				contentResults = grepSearch(absPath, params.Pattern, params.CaseInsensitive, params.ContextLines, searchFiles)
			}

			// Phase 3: Merge and deduplicate results
			seen := make(map[string]bool)
			var results []searchResult

			// Add content matches first (higher priority)
			for _, cm := range contentResults {
				if seen[cm.File] {
					continue
				}
				seen[cm.File] = true
				results = append(results, searchResult{
					File:         cm.File,
					Score:        cm.Score,
					MatchedLines: cm.Lines,
					MatchType:    "content",
				})
				if len(results) >= params.MaxResults {
					break
				}
			}

			// Add glob-only matches
			if len(results) < params.MaxResults {
				for _, fp := range globMatches {
					relPath := relPath(absPath, fp)
					if seen[relPath] {
						continue
					}
					seen[relPath] = true
					results = append(results, searchResult{
						File:      relPath,
						Score:     1,
						MatchType: "filename",
					})
					if len(results) >= params.MaxResults {
						break
					}
				}
			}

			// Sort by score descending
			sort.Slice(results, func(i, j int) bool {
				return results[i].Score > results[j].Score
			})

			elapsed := time.Since(start)

			return map[string]any{
				"status":     "ok",
				"query":      params.Pattern,
				"glob":       params.Glob,
				"path":       absPath,
				"total":      len(results),
				"truncated":  len(results) >= params.MaxResults,
				"elapsed_ms": elapsed.Milliseconds(),
				"results":    results,
			}, nil
		},
	}
}

type contentMatch struct {
	File  string        `json:"file"`
	Score float64       `json:"score"`
	Lines []matchedLine `json:"matched_lines"`
}

type matchedLine struct {
	LineNumber int      `json:"line_number"`
	Content    string   `json:"content"`
	Context    []string `json:"context,omitempty"`
}

type searchResult struct {
	File         string        `json:"file"`
	Score        float64       `json:"score"`
	MatchedLines []matchedLine `json:"matched_lines,omitempty"`
	MatchType    string        `json:"match_type"`
}

// globSearch finds files matching a glob pattern starting from root.
func globSearch(root, pattern string) []string {
	// Handle ** patterns by walking the directory tree
	var matches []string

	if strings.Contains(pattern, "**") {
		// Use filepath.Walk for ** patterns
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			relPath := relPath(root, path)
			if matchGlob(relPath, pattern) {
				matches = append(matches, relPath)
			}
			return nil
		})
	} else {
		// Use filepath.Glob for simple patterns
		fullPattern := filepath.Join(root, pattern)
		paths, _ := filepath.Glob(fullPattern)
		for _, p := range paths {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				matches = append(matches, relPath(root, p))
			}
		}
		// Also try with ** prefix if no results
		if len(matches) == 0 {
			deepPattern := filepath.Join(root, "**", pattern)
			paths, _ := filepath.Glob(deepPattern)
			for _, p := range paths {
				if info, err := os.Stat(p); err == nil && !info.IsDir() {
					matches = append(matches, relPath(root, p))
				}
			}
		}
	}

	// Filter out common ignored directories
	matches = filterIgnored(matches)
	return matches
}

// matchGlob checks if a path matches a glob pattern with ** support.
func matchGlob(path, pattern string) bool {
	// Normalize patterns
	if strings.HasPrefix(pattern, "**/") {
		// ** matches any number of directories
		suffix := pattern[3:]
		return strings.HasSuffix(path, suffix) || filepath.Base(path) == suffix
	}
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	if matched {
		return true
	}
	// Try matching just the basename
	matched, _ = filepath.Match(pattern, filepath.Base(path))
	return matched
}

// grepSearch searches file contents for a pattern using ripgrep (if available)
// or falls back to Go's regexp.
func grepSearch(root, pattern string, caseInsensitive bool, contextLines int, limitFiles []string) []contentMatch {
	// Try ripgrep first — it's much faster
	if rgPath, err := exec.LookPath("rg"); err == nil {
		return grepWithRipgrep(rgPath, root, pattern, caseInsensitive, contextLines, limitFiles)
	}
	// Fall back to grep
	if grepPath, err := exec.LookPath("grep"); err == nil {
		return grepWithGrep(grepPath, root, pattern, caseInsensitive, contextLines, limitFiles)
	}
	// Last resort: Go regex search
	return grepWithGo(root, pattern, caseInsensitive, contextLines, limitFiles)
}

func grepWithRipgrep(rgPath, root, pattern string, caseInsensitive bool, contextLines int, limitFiles []string) []contentMatch {
	args := []string{"--json", "--no-heading", "-n"}
	if caseInsensitive {
		args = append(args, "-i")
	}
	if contextLines > 0 {
		args = append(args, fmt.Sprintf("-C%d", contextLines))
	}
	args = append(args, "--")
	args = append(args, pattern)

	// If limitFiles is non-empty, search only those files; otherwise search root.
	if len(limitFiles) > 0 {
		for _, f := range limitFiles {
			args = append(args, filepath.Join(root, f))
		}
	} else {
		args = append(args, root)
	}

	cmd := exec.Command(rgPath, args...)
	output, err := cmd.Output()
	if err != nil {
		// rg returns exit code 1 when no matches found — not an error
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil
		}
		return nil
	}

	return parseRipgrepJSON(output, root, contextLines)
}

func grepWithGrep(grepPath, root, pattern string, caseInsensitive bool, contextLines int, limitFiles []string) []contentMatch {
	args := []string{"-rn", "--include=*"}
	if caseInsensitive {
		args = append(args, "-i")
	}
	if contextLines > 0 {
		args = append(args, fmt.Sprintf("-C%d", contextLines))
	}
	args = append(args, "--")
	args = append(args, pattern)

	if len(limitFiles) > 0 {
		for _, f := range limitFiles {
			args = append(args, filepath.Join(root, f))
		}
	} else {
		args = append(args, root)
	}

	cmd := exec.Command(grepPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	return parseGrepOutput(string(output), root, contextLines)
}

func grepWithGo(root, pattern string, caseInsensitive bool, contextLines int, limitFiles []string) []contentMatch {
	// Simple fallback: walk files and search line by line
	// This is slower but works without external tools
	limitSet := make(map[string]bool)
	for _, f := range limitFiles {
		limitSet[f] = true
	}
	results := []contentMatch{}

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rp := relPath(root, path)
		if isIgnoredPath(rp) {
			return nil
		}
		// If limitFiles is non-empty, only search those files
		if len(limitFiles) > 0 && !limitSet[rp] {
			return nil
		}
		// Skip binary files (basic check)
		if isBinaryExt(filepath.Ext(path)) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		searchPattern := pattern
		if caseInsensitive {
			searchPattern = strings.ToLower(pattern)
		}

		var matches []matchedLine
		for i, line := range lines {
			checkLine := line
			if caseInsensitive {
				checkLine = strings.ToLower(checkLine)
			}
			if strings.Contains(checkLine, searchPattern) {
				ml := matchedLine{
					LineNumber: i + 1,
					Content:    strings.TrimSpace(line),
				}
				// Add context
				if contextLines > 0 {
					for j := max(0, i-contextLines); j <= min(len(lines)-1, i+contextLines); j++ {
						if j != i {
							ml.Context = append(ml.Context, strings.TrimSpace(lines[j]))
						}
					}
				}
				matches = append(matches, ml)
			}
		}

		if len(matches) > 0 {
			results = append(results, contentMatch{
				File:  rp,
				Score: float64(len(matches)),
				Lines: matches,
			})
		}
		return nil
	})

	return results
}

// parseRipgrepJSON parses ripgrep --json output.
func parseRipgrepJSON(output []byte, root string, contextLines int) []contentMatch {
	fileMatches := make(map[string]*contentMatch)

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				LineNumber int `json:"line_number"`
				Lines      struct {
					Text string `json:"text"`
				} `json:"lines"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "match" {
			continue
		}
		filePath := relPath(root, entry.Data.Path.Text)
		if isIgnoredPath(filePath) {
			continue
		}

		if fileMatches[filePath] == nil {
			fileMatches[filePath] = &contentMatch{
				File:  filePath,
				Score: 0,
			}
		}
		fileMatches[filePath].Score++
		fileMatches[filePath].Lines = append(fileMatches[filePath].Lines, matchedLine{
			LineNumber: entry.Data.LineNumber,
			Content:    strings.TrimSpace(entry.Data.Lines.Text),
		})
	}

	results := make([]contentMatch, 0, len(fileMatches))
	for _, cm := range fileMatches {
		results = append(results, *cm)
	}
	return results
}

// parseGrepOutput parses grep -rn output.
func parseGrepOutput(output, root string, contextLines int) []contentMatch {
	fileMatches := make(map[string]*contentMatch)

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		// Format: path:linenum:content  or  path-linenum-content (with -C)
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			// Try with dash separator (context lines)
			parts = strings.SplitN(line, "-", 3)
			if len(parts) < 3 {
				continue
			}
		}
		filePath := relPath(root, parts[0])
		if isIgnoredPath(filePath) {
			continue
		}
		lineNum := 0
		fmt.Sscanf(parts[1], "%d", &lineNum)
		content := strings.TrimSpace(parts[2])

		if fileMatches[filePath] == nil {
			fileMatches[filePath] = &contentMatch{
				File:  filePath,
				Score: 0,
			}
		}
		fileMatches[filePath].Score++
		fileMatches[filePath].Lines = append(fileMatches[filePath].Lines, matchedLine{
			LineNumber: lineNum,
			Content:    content,
		})
	}

	results := make([]contentMatch, 0, len(fileMatches))
	for _, cm := range fileMatches {
		results = append(results, *cm)
	}
	return results
}

// filterIgnored removes files in common ignored directories.
func filterIgnored(paths []string) []string {
	var result []string
	for _, p := range paths {
		if !isIgnoredPath(p) {
			result = append(result, p)
		}
	}
	return result
}

// isIgnoredPath checks if a path is in a commonly ignored directory.
func isIgnoredPath(p string) bool {
	ignoredDirs := []string{
		"node_modules/", ".git/", "vendor/", "dist/", "build/",
		".next/", "__pycache__/", ".venv/", ".tox/", ".eggs/",
		".mypy_cache/", ".pytest_cache/", "coverage/", ".nyc_output/",
	}
	for _, dir := range ignoredDirs {
		if strings.Contains(p+"/", dir) {
			return true
		}
	}
	return false
}

// isBinaryExt returns true for common binary file extensions.
func isBinaryExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp",
		".pdf", ".zip", ".tar", ".gz", ".bz2", ".7z", ".rar",
		".exe", ".dll", ".so", ".dylib", ".bin", ".dat",
		".mp3", ".mp4", ".avi", ".mov", ".mkv", ".wav", ".flv",
		".ttf", ".otf", ".woff", ".woff2", ".eot",
		".sqlite", ".db", ".mdb":
		return true
	}
	return false
}

// relPath returns the path relative to root, or the original path if it
// can't be made relative.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

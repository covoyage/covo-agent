package agent

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var refPattern = regexp.MustCompile(
	// Support both "@file:path" (colon) and "@file path" (no colon, autocomplete style).
	// The colon-less form requires a directory-like path (contains / or .).
	`@(?:(?P<simple>diff|staged|problems)\b|(?P<kind>file|folder|git|url):(?P<value>(?:` + "`[^`\n]+`" + `|"[^"\n]+"|'[^'\n]+')` + `(?::\d+(?:-\d+)?)?|\S+)|(?P<kind2>file|folder)(?P<value2>\S+))`,
)

var trailingPunctuation = ",.;!?"

type ContextReference struct {
	Raw       string
	Kind      string
	Target    string
	Start     int
	End       int
	LineStart int
	LineEnd   int
}

type ContextReferenceResult struct {
	Message         string
	OriginalMessage string
	References      []ContextReference
	Warnings        []string
	InjectedTokens  int
	Expanded        bool
	Blocked         bool
}

func ParseContextReferences(message string) []ContextReference {
	var refs []ContextReference
	if message == "" {
		return refs
	}

	for _, m := range refPattern.FindAllStringSubmatchIndex(message, -1) {
		if m == nil {
			continue
		}
		matchStart := m[0]
		matchEnd := m[1]

		// Go RE2 does not support negative lookbehind (?<![...]).
		// Filter out false positives where '@' is preceded by a word
		// character or '/', e.g. user@example.com or a/b@c.
		if matchStart > 0 {
			prev := message[matchStart-1]
			if prev == '/' || (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') || prev == '_' {
				continue
			}
		}

		simpleStart := m[2]
		simpleEnd := m[3]
		kindStart := m[4]
		kindEnd := m[5]
		valueStart := m[6]
		valueEnd := m[7]
		kind2Start := -1
		kind2End := -1
		value2Start := -1
		value2End := -1
		if len(m) > 10 {
			kind2Start = m[8]
			kind2End = m[9]
			value2Start = m[10]
			value2End = m[11]
		}

		if simpleStart >= 0 && simpleEnd >= 0 {
			refs = append(refs, ContextReference{
				Raw:   message[matchStart:matchEnd],
				Kind:  message[simpleStart:simpleEnd],
				Start: matchStart,
				End:   matchEnd,
			})
			continue
		}

		// Colon-less format: @filepath or @folderpath (autocomplete style)
		if kind2Start >= 0 && kind2End >= 0 && value2Start >= 0 && value2End >= 0 {
			kind := message[kind2Start:kind2End]
			value := stripTrailingPunctuation(message[value2Start:value2End])
			target := stripReferenceWrappers(value)
			lineStart := 0
			lineEnd := 0
			if kind == "file" {
				target, lineStart, lineEnd = parseFileReferenceValue(value)
			}
			refs = append(refs, ContextReference{
				Raw:       message[matchStart:matchEnd],
				Kind:      kind,
				Target:    target,
				Start:     matchStart,
				End:       matchEnd,
				LineStart: lineStart,
				LineEnd:   lineEnd,
			})
			continue
		}

		kind := message[kindStart:kindEnd]
		value := stripTrailingPunctuation(message[valueStart:valueEnd])
		target := stripReferenceWrappers(value)
		lineStart := 0
		lineEnd := 0

		if kind == "file" {
			target, lineStart, lineEnd = parseFileReferenceValue(value)
		}

		refs = append(refs, ContextReference{
			Raw:       message[matchStart:matchEnd],
			Kind:      kind,
			Target:    target,
			Start:     matchStart,
			End:       matchEnd,
			LineStart: lineStart,
			LineEnd:   lineEnd,
		})
	}

	return refs
}

func PreprocessContextReferences(message string, cwd string, contextLength int64) ContextReferenceResult {
	refs := ParseContextReferences(message)
	if len(refs) == 0 {
		return ContextReferenceResult{
			Message:         message,
			OriginalMessage: message,
		}
	}

	cwdPath, _ := filepath.Abs(cwd)
	var warnings []string
	var blocks []string
	injectedTokens := 0

	for _, ref := range refs {
		warning, block := expandReference(ref, cwdPath)
		if warning != "" {
			warnings = append(warnings, warning)
		}
		if block != "" {
			blocks = append(blocks, block)
			injectedTokens += estimateTokens(block)
		}
	}

	hardLimit := int(max(1, contextLength*50/100))
	softLimit := int(max(1, contextLength*25/100))

	if injectedTokens > hardLimit {
		warnings = append(warnings,
			fmt.Sprintf("@ context injection refused: %d tokens exceeds the 50%% hard limit (%d).", injectedTokens, hardLimit))
		return ContextReferenceResult{
			Message:         message,
			OriginalMessage: message,
			References:      refs,
			Warnings:        warnings,
			InjectedTokens:  injectedTokens,
			Expanded:        false,
			Blocked:         true,
		}
	}

	if injectedTokens > softLimit {
		warnings = append(warnings,
			fmt.Sprintf("@ context injection warning: %d tokens exceeds the 25%% soft limit (%d).", injectedTokens, softLimit))
	}

	stripped := removeReferenceTokens(message, refs)
	final := stripped

	if len(warnings) > 0 {
		warnSection := "\n--- Context Warnings ---\n"
		for _, w := range warnings {
			warnSection += "- " + w + "\n"
		}
		final += warnSection
	}
	if len(blocks) > 0 {
		final += "\n\n--- Attached Context ---\n\n" + strings.Join(blocks, "\n\n")
	}

	return ContextReferenceResult{
		Message:         strings.TrimSpace(final),
		OriginalMessage: message,
		References:      refs,
		Warnings:        warnings,
		InjectedTokens:  injectedTokens,
		Expanded:        len(blocks) > 0 || len(warnings) > 0,
		Blocked:         false,
	}
}

// problemsProvider is set by CovoAgent during initialization to provide
// LSP workspace diagnostics for @problems references. When nil, @problems
// falls back to a graceful "not available" message.
var problemsProvider func(cwd string) string

// SetProblemsProvider installs the LSP diagnostic provider for @problems.
// Called once during CovoAgent initialization.
func SetProblemsProvider(f func(cwd string) string) {
	problemsProvider = f
}

func expandReference(ref ContextReference, cwd string) (string, string) {
	switch ref.Kind {
	case "file":
		return expandFileReference(ref, cwd)
	case "folder":
		return expandFolderReference(ref, cwd)
	case "diff":
		return expandGitReference(ref, cwd, []string{"diff"}, "git diff")
	case "staged":
		return expandGitReference(ref, cwd, []string{"diff", "--staged"}, "git diff --staged")
	case "problems":
		return expandProblemsReference(ref, cwd)
	case "git":
		count := 1
		if ref.Target != "" {
			if n, ok := parseInt(ref.Target); ok {
				count = clamp(n, 1, 10)
			}
		}
		return expandGitReference(ref, cwd, []string{"log", fmt.Sprintf("-%d", count), "-p"}, fmt.Sprintf("git log -%d -p", count))
	case "url":
		return expandURLReference(ref)
	default:
		return fmt.Sprintf("%s: unsupported reference type", ref.Raw), ""
	}
}

func expandURLReference(ref ContextReference) (string, string) {
	url := strings.TrimSpace(ref.Target)
	if url == "" {
		return fmt.Sprintf("%s: empty URL", ref.Raw), ""
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Sprintf("%s: URL must start with http:// or https://", ref.Raw), ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Sprintf("%s: %v", ref.Raw, err), ""
	}
	req.Header.Set("User-Agent", "Covo-Agent/1.0")
	req.Header.Set("Accept", "text/html,text/plain,application/json;q=0.9")

	// Use the SSRF-guarded client so an @url reference cannot be pointed at
	// cloud metadata endpoints, loopback, or internal/private hosts. The
	// SafeRoundTripper validates the URL (and any redirect target) against the
	// blocklist before the request is made.
	resp, err := NewSafeClient().Do(req)
	if err != nil {
		return fmt.Sprintf("%s: %v", ref.Raw, err), ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return fmt.Sprintf("%s: %v", ref.Raw, err), ""
	}

	text := string(body)
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") || strings.HasPrefix(text, "<!DOCTYPE") || strings.HasPrefix(text, "<html") {
		text = stripHTMLTags(text)
	}
	if len(text) > 50000 {
		text = text[:50000] + "\n... [truncated]"
	}

	return "", fmt.Sprintf("--- @url: %s ---\n%s", url, text)
}

func stripHTMLTags(htmlStr string) string {
	inTag := false
	var buf strings.Builder
	buf.Grow(len(htmlStr) / 2)
	for _, ch := range htmlStr {
		if ch == '<' {
			inTag = true
			continue
		}
		if ch == '>' {
			inTag = false
			continue
		}
		if !inTag {
			buf.WriteRune(ch)
		}
	}
	result := buf.String()
	lines := strings.Split(result, "\n")
	var clean []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			clean = append(clean, line)
		}
	}
	return strings.Join(clean, "\n")
}

func expandFileReference(ref ContextReference, cwd string) (string, string) {
	path := filepath.Join(cwd, ref.Target)
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Sprintf("%s: cannot resolve path", ref.Raw), ""
	}

	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Sprintf("%s: file not found", ref.Raw), ""
	}
	if info.IsDir() {
		return fmt.Sprintf("%s: path is not a file", ref.Raw), ""
	}
	if isBinaryFile(abs) {
		return fmt.Sprintf("%s: binary files are not supported", ref.Raw), ""
	}

	text, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Sprintf("%s: cannot read file", ref.Raw), ""
	}
	content := string(text)

	if ref.LineStart > 0 && ref.LineEnd == 0 {
		ref.LineEnd = ref.LineStart
	}
	if ref.LineStart > 0 {
		lines := strings.Split(content, "\n")
		startIdx := ref.LineStart - 1
		endIdx := ref.LineEnd
		if startIdx < 0 {
			startIdx = 0
		}
		if endIdx > len(lines) {
			endIdx = len(lines)
		}
		if startIdx < len(lines) {
			content = strings.Join(lines[startIdx:endIdx], "\n")
		}
	}

	lang := codeFenceLanguage(abs)
	tokens := estimateTokens(content)
	return "", fmt.Sprintf("📄 %s (%d tokens)\n```%s\n%s\n```", ref.Raw, tokens, lang, content)
}

func expandFolderReference(ref ContextReference, cwd string) (string, string) {
	path := filepath.Join(cwd, ref.Target)
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Sprintf("%s: cannot resolve path", ref.Raw), ""
	}

	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Sprintf("%s: folder not found", ref.Raw), ""
	}
	if !info.IsDir() {
		return fmt.Sprintf("%s: path is not a folder", ref.Raw), ""
	}

	listing := buildFolderListing(abs, cwd)
	tokens := estimateTokens(listing)
	return "", fmt.Sprintf("📁 %s (%d tokens)\n%s", ref.Raw, tokens, listing)
}

func expandProblemsReference(ref ContextReference, cwd string) (string, string) {
	if problemsProvider == nil {
		return fmt.Sprintf("%s: LSP problems not available (no language server configured)", ref.Raw), ""
	}
	content := problemsProvider(cwd)
	if content == "" {
		content = "(no problems detected in workspace)"
	}
	tokens := estimateTokens(content)
	return "", fmt.Sprintf("🔍 %s (%d tokens)\n%s", ref.Raw, tokens, content)
}

func expandGitReference(ref ContextReference, cwd string, args []string, label string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	output, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		if stderr == "" {
			stderr = "git command failed"
		}
		return fmt.Sprintf("%s: %s", ref.Raw, stderr), ""
	}

	content := strings.TrimSpace(string(output))
	if content == "" {
		content = "(no output)"
	}
	tokens := estimateTokens(content)
	return "", fmt.Sprintf("🧾 %s (%d tokens)\n```diff\n%s\n```", label, tokens, content)
}

func stripTrailingPunctuation(value string) string {
	stripped := strings.TrimRight(value, trailingPunctuation)
	for len(stripped) > 0 {
		last := stripped[len(stripped)-1]
		if last == ')' || last == ']' || last == '}' {
			closer := string(last)
			opener := map[byte]byte{')': '(', ']': '[', '}': '{'}[last]
			openCount := strings.Count(stripped, string(opener))
			closeCount := strings.Count(stripped, closer)
			if closeCount > openCount {
				stripped = stripped[:len(stripped)-1]
				continue
			}
		}
		break
	}
	return stripped
}

func stripReferenceWrappers(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if first == last && (first == '`' || first == '"' || first == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func parseFileReferenceValue(value string) (string, int, int) {
	unwrapped := stripReferenceWrappers(value)

	re := regexp.MustCompile(`^(.+?):(\d+)(?:-(\d+))?$`)
	m := re.FindStringSubmatch(unwrapped)
	if m != nil {
		path := m[1]
		start, _ := parseInt(m[2])
		end := start
		if m[3] != "" {
			end, _ = parseInt(m[3])
		}
		return path, start, end
	}

	return unwrapped, 0, 0
}

func removeReferenceTokens(message string, refs []ContextReference) string {
	var pieces []string
	cursor := 0
	for _, ref := range refs {
		pieces = append(pieces, message[cursor:ref.Start])
		cursor = ref.End
	}
	pieces = append(pieces, message[cursor:])
	text := strings.Join(pieces, "")
	text = regexp.MustCompile(`\s{2,}`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`\s+([,.;:!?])`).ReplaceAllString(text, "$1")
	return strings.TrimSpace(text)
}

func isBinaryFile(path string) bool {
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType != "" && !strings.HasPrefix(mimeType, "text/") {
		ext := strings.ToLower(filepath.Ext(path))
		textExts := map[string]bool{
			".py": true, ".md": true, ".txt": true, ".json": true,
			".yaml": true, ".yml": true, ".toml": true, ".js": true, ".ts": true,
			".jsx": true, ".tsx": true, ".go": true, ".rs": true, ".c": true,
			".h": true, ".cpp": true, ".hpp": true, ".java": true, ".kt": true,
			".rb": true, ".php": true, ".css": true, ".html": true, ".xml": true,
			".csv": true, ".log": true, ".sh": true, ".bash": true, ".zsh": true,
			".cfg": true, ".ini": true, ".env": true, ".sql": true, ".proto": true,
			".graphql": true, ".vue": true, ".svelte": true, ".lua": true,
			".swift": true, ".scala": true, ".clj": true, ".ex": true,
			".exs": true, ".erl": true, ".hrl": true, ".hs": true, ".ml": true,
			".dart": true, ".r": true, ".jl": true, ".tf": true, ".tfvars": true,
		}
		if textExts[ext] {
			return false
		}
		return true
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	chunk := data
	if len(chunk) > 4096 {
		chunk = chunk[:4096]
	}
	return strings.Contains(string(chunk), "\x00")
}

func buildFolderListing(path string, cwd string) string {
	relPath, _ := filepath.Rel(cwd, path)
	var sb strings.Builder
	sb.WriteString(relPath)
	sb.WriteString("/\n")

	entries := iterVisibleEntries(path, cwd, 200)
	for _, entry := range entries {
		rel, _ := filepath.Rel(cwd, entry)
		depth := len(strings.Split(rel, string(filepath.Separator))) - len(strings.Split(relPath, string(filepath.Separator))) - 1
		if depth < 0 {
			depth = 0
		}
		indent := strings.Repeat("  ", depth)
		info, err := os.Stat(entry)
		if err != nil {
			continue
		}
		if info.IsDir() {
			sb.WriteString(fmt.Sprintf("%s- %s/\n", indent, filepath.Base(entry)))
		} else {
			meta := fileMetadata(entry)
			sb.WriteString(fmt.Sprintf("%s- %s (%s)\n", indent, filepath.Base(entry), meta))
		}
	}

	if len(entries) >= 200 {
		sb.WriteString("- ...\n")
	}
	return sb.String()
}

func iterVisibleEntries(path string, cwd string, limit int) []string {
	rgEntries := rgFiles(path, cwd, limit)
	if rgEntries != nil {
		var output []string
		seenDirs := make(map[string]bool)
		for _, rel := range rgEntries {
			full := filepath.Join(cwd, rel)
			dir := filepath.Dir(full)
			for dir != cwd && dir != path {
				if !seenDirs[dir] {
					seenDirs[dir] = true
					output = append(output, dir)
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
			output = append(output, full)
		}
		sort.Slice(output, func(i, j int) bool {
			iDir, _ := os.Stat(output[i])
			jDir, _ := os.Stat(output[j])
			iIsDir := iDir != nil && iDir.IsDir()
			jIsDir := jDir != nil && jDir.IsDir()
			if iIsDir != jIsDir {
				return iIsDir
			}
			return output[i] < output[j]
		})
		deduped := make([]string, 0, len(output))
		seen := make(map[string]bool)
		for _, entry := range output {
			if _, err := os.Stat(entry); err == nil && !seen[entry] {
				seen[entry] = true
				deduped = append(deduped, entry)
			}
		}
		return deduped
	}

	var output []string
	filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".") || name == "__pycache__" || name == "node_modules" || name == ".git" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(output) >= limit {
			return filepath.SkipAll
		}
		output = append(output, p)
		return nil
	})
	return output
}

func rgFiles(path string, cwd string, limit int) []string {
	relPath, err := filepath.Rel(cwd, path)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rg", "--files", relPath)
	cmd.Dir = cwd
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, line)
		if len(files) >= limit {
			break
		}
	}
	return files
}

func fileMetadata(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "unknown"
	}
	size := info.Size()

	if isBinaryFile(path) {
		return fmt.Sprintf("%d bytes", size)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("%d bytes", size)
	}
	lineCount := strings.Count(string(data), "\n") + 1
	return fmt.Sprintf("%d lines", lineCount)
}

func codeFenceLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	mapping := map[string]string{
		".py":       "python",
		".js":       "javascript",
		".ts":       "typescript",
		".tsx":      "tsx",
		".jsx":      "jsx",
		".json":     "json",
		".md":       "markdown",
		".sh":       "bash",
		".yml":      "yaml",
		".yaml":     "yaml",
		".toml":     "toml",
		".go":       "go",
		".rs":       "rust",
		".c":        "c",
		".h":        "c",
		".cpp":      "cpp",
		".hpp":      "cpp",
		".java":     "java",
		".kt":       "kotlin",
		".rb":       "ruby",
		".css":      "css",
		".html":     "html",
		".xml":      "xml",
		".sql":      "sql",
		".proto":    "protobuf",
		".vue":      "vue",
		".swift":    "swift",
		".scala":    "scala",
		".lua":      "lua",
		".dart":     "dart",
		".r":        "r",
		".tf":       "hcl",
		".makefile": "makefile",
	}
	if lang, ok := mapping[ext]; ok {
		return lang
	}
	base := strings.ToLower(filepath.Base(path))
	if lang, ok := mapping[base]; ok {
		return lang
	}
	if base == "makefile" || base == "dockerfile" {
		return "dockerfile"
	}
	return ""
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return len(text) / 4
}

func parseInt(s string) (int, bool) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func clamp(n, minVal, maxVal int) int {
	if n < minVal {
		return minVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

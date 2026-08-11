package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// nonCodeExtensions lists file extensions that don't carry verifiable runtime
// behavior. Editing these files should NOT trigger a verification nudge.
var nonCodeExtensions = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".csv": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".xml": true, ".html": true, ".htm": true, ".css": true,
	".scss": true, ".less": true, ".svg": true, ".png": true,
	".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".lock": true, ".sum": true, ".mod": true, ".env": true,
	".gitignore": true, ".dockerignore": true, ".editorconfig": true,
	".prettierrc": true, ".eslintrc": true, ".license": true,
	"LICENSE": true, "LICENSE.md": true, "README": true,
	"README.md": true, "CHANGELOG": true, "CHANGELOG.md": true,
}

// fileModifyingTools lists tool names that can modify files on disk.
var fileModifyingTools = map[string]bool{
	"write_file":  true,
	"edit":        true,
	"edit_block":  true,
	"patch":       true,
	"apply_patch": true,
	"append_file": true,
	"delete":      true,
	"delete_file": true,
	"move":        true,
}

// testCommandPatterns lists substrings that indicate a test/verification command.
var testCommandPatterns = []string{
	"go test", "go vet", "go build", "golangci-lint",
	"pytest", "python -m pytest", "unittest", "nose",
	"cargo test", "cargo clippy", "cargo build",
	"npm test", "npm run test", "yarn test", "pnpm test",
	"npx jest", "vitest", "mocha",
	"make test", "make check", "make lint", "make build",
	"dart test", "flutter test",
	"rspec", "minitest",
	"phpunit",
	"mvn test", "gradle test",
	"dotnet test",
	"rake test",
	"bun test",
	"turbo test",
}

// verificationGuardHook nudges the model to verify (test/build) after editing
// code files. When the model tries to stop without running any verification
// commands, it injects a FollowUp message listing the modified files.
//
// Non-code files (.md, .txt, .json, etc.) are excluded — editing documentation
// should not trigger a verification nudge.
type verificationGuardHook struct {
	agentcore.BaseLifecycleHook
	ca *CovoAgent

	// filesModified tracks file paths modified in the current turn.
	filesModified []string
	// testsRan tracks whether a test/build command was executed in the current turn.
	testsRan bool
}

func newVerificationGuardHook(ca *CovoAgent) *verificationGuardHook {
	return &verificationGuardHook{ca: ca}
}

// AfterToolExecution scans tool calls for file modifications and test commands.
func (h *verificationGuardHook) AfterToolExecution(_ context.Context, _ *agentcore.AgentRunContext, tec *agentcore.ToolExecutionContext) {
	for _, tc := range tec.ToolCalls {
		if fileModifyingTools[tc.Name] {
			if path := extractFilePath(tc.Arguments); path != "" {
				h.filesModified = append(h.filesModified, path)
			}
		}
		if tc.Name == "bash" || tc.Name == "sandbox" || tc.Name == "execute_code" || tc.Name == "process" {
			if looksLikeTestCommand(tc.Arguments) {
				h.testsRan = true
			}
		}
	}
}

// AfterTurn checks if the model is stopping after editing code without testing.
func (h *verificationGuardHook) AfterTurn(ctx context.Context, arc *agentcore.AgentRunContext, info agentcore.TurnInfo) {
	if info.HadToolCalls {
		return // model is still working; keep state for the stop check
	}

	// Model wants to stop — check if code was modified without testing.
	codeFiles := h.nonDocModifiedFiles()
	testsRan := h.testsRan
	h.reset()

	if len(codeFiles) == 0 || testsRan {
		return // no code files modified, or tests already ran
	}

	// Cap the file list to keep the nudge concise.
	if len(codeFiles) > 8 {
		codeFiles = codeFiles[:8]
	}

	arc.Agent.FollowUp(agentcore.Message{
		Role:    agentcore.RoleSystem,
		Content: buildVerificationNudge(codeFiles),
	})
}

// reset clears the per-turn tracking state.
func (h *verificationGuardHook) reset() {
	h.filesModified = nil
	h.testsRan = false
}

// nonDocModifiedFiles returns only the code files (excluding docs/config).
func (h *verificationGuardHook) nonDocModifiedFiles() []string {
	var codeFiles []string
	seen := map[string]bool{}
	for _, f := range h.filesModified {
		if seen[f] {
			continue
		}
		seen[f] = true
		if !isNonCodeFile(f) {
			codeFiles = append(codeFiles, f)
		}
	}
	return codeFiles
}

// isNonCodeFile returns true if the file extension (or basename) indicates a
// non-code file that doesn't carry verifiable runtime behavior.
func isNonCodeFile(path string) bool {
	base := filepath.Base(path)
	if nonCodeExtensions[base] {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	return nonCodeExtensions[ext]
}

// extractFilePath pulls the file path from a tool call's JSON arguments.
func extractFilePath(args string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	// Common key names across file-modifying tools.
	for _, key := range []string{"path", "file_path", "filePath", "target"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// looksLikeTestCommand checks if a bash/sandbox tool call argument contains a
// test/build/lint command pattern.
func looksLikeTestCommand(args string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return false
	}
	// Check "command" and "cmd" keys.
	for _, key := range []string{"command", "cmd", "script"} {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				if containsTestPattern(s) {
					return true
				}
			}
		}
	}
	return false
}

// containsTestPattern checks if a command string contains any test pattern.
func containsTestPattern(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, pat := range testCommandPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// buildVerificationNudge composes the nudge message shown when the model
// tries to stop after editing code without testing.
func buildVerificationNudge(files []string) string {
	var b strings.Builder
	b.WriteString("You modified code files but did not run any tests or build commands:\n")
	for _, f := range files {
		b.WriteString("  - " + f + "\n")
	}
	b.WriteString("\nRun the relevant tests or build to verify your changes before finishing.")
	return b.String()
}

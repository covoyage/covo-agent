package swarm

// Workflow Engine — deterministic script-style orchestration primitives that
// extend the SwarmOrchestrator with:
//
//  1. Output passing — dependency results are injected into dependent task
//     prompts, enabling real pipelines (task B sees task A's output).
//  2. Structured output validation — tasks can declare an output_format
//     ("json" or "text"); JSON outputs are validated and extracted before
//     being passed downstream.
//  3. Journal persistence — plan state is saved to disk after every task
//     completion, so interrupted workflows can resume from the last
//     consistent checkpoint instead of restarting from scratch.
//  4. Phase/pipeline primitives — tasks can be grouped into named phases,
//     and pipelines chain outputs in sequence.
//
// All features are opt-in; plans without the new fields behave exactly as
// before (backward compatible).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Structured output validation
// ---------------------------------------------------------------------------

// validateStructuredOutput checks whether a task's raw output conforms to its
// declared output format. Returns the validated/extracted output (which may
// differ from raw for JSON) and an error if validation fails.
//
// outputFormat values:
//   - "" or "text": no validation, raw is returned as-is.
//   - "json":  raw must be valid JSON. If it contains a fenced code block,
//     the block content is extracted first.
func validateStructuredOutput(raw, outputFormat string) (string, error) {
	if outputFormat == "" || outputFormat == "text" {
		return raw, nil
	}
	if outputFormat != "json" {
		return raw, fmt.Errorf("unknown output_format %q", outputFormat)
	}

	// Extract JSON from fenced code block if present.
	extracted := extractJSONBlock(raw)
	if extracted == "" {
		extracted = strings.TrimSpace(raw)
	}

	var js any
	if err := json.Unmarshal([]byte(extracted), &js); err != nil {
		return raw, fmt.Errorf("output is not valid JSON: %w", err)
	}
	return extracted, nil
}

// extractJSONBlock finds the first ```json ... ``` or ``` ... ``` fenced block
// in the text and returns its content. Returns "" if no block is found.
//
// The opening fence language tag is matched case-insensitively (so ```JSON and
// ```Json are accepted) and Windows-style \r\n line endings are normalized
// before matching.
func extractJSONBlock(text string) string {
	// Normalize Windows-style line endings so \n-based fences match.
	text = strings.ReplaceAll(text, "\r\n", "\n")

	// findFirst returns the content of the first fenced code block whose
	// language tag matches wantLang (compared case-insensitively). An empty
	// wantLang matches a plain fence with no language tag.
	findFirst := func(wantLang string) string {
		s := text
		for {
			i := strings.Index(s, "```")
			if i < 0 {
				return ""
			}
			after := s[i+3:]
			nl := strings.IndexByte(after, '\n')
			if nl < 0 {
				return ""
			}
			lang := strings.TrimSpace(after[:nl])
			if strings.EqualFold(lang, wantLang) {
				body := after[nl+1:]
				if end := strings.Index(body, "```"); end >= 0 {
					return strings.TrimSpace(body[:end])
				}
				// No closing fence for this opening; stop searching.
				return ""
			}
			s = after
		}
	}
	// Prefer ```json blocks first, then fall back to plain ``` blocks.
	if c := findFirst("json"); c != "" {
		return c
	}
	return findFirst("")
}

// ---------------------------------------------------------------------------
// Output passing — build a task prompt that includes dependency outputs
// ---------------------------------------------------------------------------

// buildTaskPromptWithInputs constructs the task description with dependency
// outputs prepended, enabling pipeline-style data flow.
//
// depOutputs maps dependency task ID -> that task's (validated) output.
func buildTaskPromptWithInputs(task OrchestrationTask, goal string, depOutputs map[string]string) string {
	base := fmt.Sprintf("Task: %s\n\nDescription: %s\n\nPart of plan: %s",
		task.Title, task.Description, goal)

	if len(depOutputs) == 0 {
		return base
	}

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n---\n## Inputs from Previous Tasks\n\n")
	for _, depID := range task.DependsOn {
		if out, ok := depOutputs[depID]; ok && strings.TrimSpace(out) != "" {
			// Truncate very long outputs to keep the prompt manageable. Use
			// rune-aware truncation so multi-byte CJK/emoji content is never
			// split mid-sequence (which would produce invalid UTF-8).
			const maxRunes = 8000
			display := truncateRunesWithSuffix(out, maxRunes, "\n…[truncated]")
			b.WriteString(fmt.Sprintf("### Output from task %q\n```\n%s\n```\n\n", depID, display))
		}
	}
	b.WriteString("---\n## Your Task\nComplete the task described above, using the inputs from previous tasks as needed.")
	return b.String()
}

// ---------------------------------------------------------------------------
// Journal — persistent execution state for resume
// ---------------------------------------------------------------------------

// WorkflowJournal persists orchestration plan state to disk so that
// interrupted workflows can resume from the last checkpoint.
type WorkflowJournal struct {
	mu  sync.Mutex
	dir string // directory for journal files
}

// NewWorkflowJournal creates a journal writer rooted at dir.
// The directory is created if it doesn't exist.
func NewWorkflowJournal(dir string) *WorkflowJournal {
	_ = os.MkdirAll(dir, 0o755)
	return &WorkflowJournal{dir: dir}
}

// Save writes the plan state to disk atomically (write to temp, then rename).
func (j *WorkflowJournal) Save(plan *OrchestrationPlan, planID string) error {
	if j == nil || j.dir == "" {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("journal marshal: %w", err)
	}

	path := j.path(planID)
	tmp := path + ".tmp"
	// Ensure the temp file never outlives Save: on a successful rename the
	// file no longer exists at tmp (rename moves it), so this Remove is a
	// no-op; on any failure path it cleans up the orphaned temp file.
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("journal write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("journal rename: %w", err)
	}
	return nil
}

// Load reads a previously saved plan state. Returns nil, nil if no journal
// exists (first run).
func (j *WorkflowJournal) Load(planID string) (*OrchestrationPlan, error) {
	if j == nil || j.dir == "" {
		return nil, nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := os.ReadFile(j.path(planID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal read: %w", err)
	}

	var plan OrchestrationPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("journal unmarshal: %w", err)
	}
	return &plan, nil
}

// Delete removes the journal file for a completed plan.
func (j *WorkflowJournal) Delete(planID string) {
	if j == nil || j.dir == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = os.Remove(j.path(planID))
}

func (j *WorkflowJournal) path(planID string) string {
	// Sanitize planID for filesystem safety.
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, planID)
	return filepath.Join(j.dir, safe+".json")
}

// truncateRunesWithSuffix truncates a string to maxRunes runes and appends a suffix.
func truncateRunesWithSuffix(s string, maxRunes int, suffix string) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= len(suffix) {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-len(suffix)]) + suffix
}

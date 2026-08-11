// Package workflow provides a declarative workflow engine for orchestrating
// multi-phase agent tasks with conditions, budgets, pause/resume, and
// output schemas.
//
// Workflows are defined as a series of phases, each with:
//   - A prompt to execute
//   - Optional conditions (evaluate before running)
//   - An optional budget (max turns, max tokens)
//   - Pause/resume support
//   - An output schema for validation
package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Phase represents a single step in a workflow.
type Phase struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Prompt      string            `json:"prompt"`
	Condition   string            `json:"condition,omitempty"`   // expression evaluated against context
	MaxTurns    int               `json:"max_turns,omitempty"`   // 0 = unlimited
	MaxTokens   int64             `json:"max_tokens,omitempty"`  // 0 = unlimited
	OutputSchema map[string]string `json:"output_schema,omitempty"` // field name → expected type
	PauseAfter  bool              `json:"pause_after,omitempty"` // pause for user review after this phase
	SkipOnError bool              `json:"skip_on_error,omitempty"`
}

// Workflow defines a multi-phase task.
type Workflow struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Phases      []Phase `json:"phases"`
	MaxTotalTurns    int   `json:"max_total_turns,omitempty"`
	MaxTotalTokens   int64 `json:"max_total_tokens,omitempty"`
}

// PhaseResult captures the outcome of a phase execution.
type PhaseResult struct {
	PhaseID   string          `json:"phase_id"`
	Status    PhaseStatus     `json:"status"`
	Output    string          `json:"output"`
	Parsed    map[string]any  `json:"parsed,omitempty"`
	Turns     int             `json:"turns"`
	Tokens    int64           `json:"tokens"`
	Error     string          `json:"error,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   time.Time       `json:"ended_at"`
}

// PhaseStatus represents the execution status of a phase.
type PhaseStatus int

const (
	PhasePending   PhaseStatus = iota
	PhaseRunning
	PhaseCompleted
	PhaseFailed
	PhaseSkipped
	PhasePaused
)

func (s PhaseStatus) String() string {
	switch s {
	case PhasePending:
		return "pending"
	case PhaseRunning:
		return "running"
	case PhaseCompleted:
		return "completed"
	case PhaseFailed:
		return "failed"
	case PhaseSkipped:
		return "skipped"
	case PhasePaused:
		return "paused"
	default:
		return "unknown"
	}
}

// Journal persists workflow execution state for crash recovery.
type Journal struct {
	mu          sync.Mutex
	path        string
	WorkflowID  string                  `json:"workflow_id"`
	StartedAt   time.Time               `json:"started_at"`
	CurrentPhase int                    `json:"current_phase"`
	Results     []PhaseResult           `json:"results"`
	TotalTurns  int                     `json:"total_turns"`
	TotalTokens int64                   `json:"total_tokens"`
	Paused      bool                    `json:"paused"`
}

// NewJournal creates or loads a journal at the given path.
func NewJournal(path, workflowID string) (*Journal, error) {
	j := &Journal{
		path:       path,
		WorkflowID: workflowID,
		StartedAt:  time.Now(),
		Results:    []PhaseResult{},
	}

	// Try to load existing journal
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, j); err != nil {
			return nil, fmt.Errorf("load journal: %w", err)
		}
	}

	return j, nil
}

// Save persists the journal to disk.
func (j *Journal) Save() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(j.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	return os.WriteFile(j.path, data, 0o644)
}

// RecordResult adds a phase result to the journal.
func (j *Journal) RecordResult(result PhaseResult) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Results = append(j.Results, result)
	j.TotalTurns += result.Turns
	j.TotalTokens += result.Tokens
}

// SetCurrentPhase updates the current phase index.
func (j *Journal) SetCurrentPhase(idx int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.CurrentPhase = idx
}

// SetPaused marks the workflow as paused/unpaused.
func (j *Journal) SetPaused(paused bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Paused = paused
}

// IsPaused returns whether the workflow is paused.
func (j *Journal) IsPaused() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Paused
}

// GetResults returns a copy of all phase results.
func (j *Journal) GetResults() []PhaseResult {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := make([]PhaseResult, len(j.Results))
	copy(result, j.Results)
	return result
}

// CanResume checks if the workflow can be resumed from the journal.
func (j *Journal) CanResume() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.Results) > 0 || j.CurrentPhase > 0
}

// Reset clears the journal for a fresh start.
func (j *Journal) Reset() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.StartedAt = time.Now()
	j.CurrentPhase = 0
	j.Results = nil
	j.TotalTurns = 0
	j.TotalTokens = 0
	j.Paused = false
	return os.Remove(j.path)
}

// Executor runs a workflow phase by phase.
type Executor struct {
	workflow  *Workflow
	journal   *Journal
	runPhase  func(ctx context.Context, phase Phase) (*PhaseResult, error)
	evaluateCondition func(condition string, results []PhaseResult) (bool, error)
}

// NewExecutor creates a workflow executor.
// runPhase is the callback that executes each phase (typically via a subagent).
func NewExecutor(wf *Workflow, journal *Journal, runPhase func(ctx context.Context, phase Phase) (*PhaseResult, error)) *Executor {
	return &Executor{
		workflow:  wf,
		journal:   journal,
		runPhase:  runPhase,
		evaluateCondition: defaultConditionEvaluator,
	}
}

// Run executes the workflow, optionally resuming from the journal.
func (e *Executor) Run(ctx context.Context) error {
	startPhase := 0
	if e.journal.CanResume() && !e.journal.IsPaused() {
		startPhase = e.journal.CurrentPhase
	}

	for i := startPhase; i < len(e.workflow.Phases); i++ {
		// Check pause state
		if e.journal.IsPaused() {
			return fmt.Errorf("workflow paused at phase %d", i)
		}

		phase := e.workflow.Phases[i]
		e.journal.SetCurrentPhase(i)

		// Evaluate condition
		if phase.Condition != "" {
			results := e.journal.GetResults()
			shouldRun, err := e.evaluateCondition(phase.Condition, results)
			if err != nil {
				return fmt.Errorf("phase %s condition error: %w", phase.ID, err)
			}
		if !shouldRun {
			e.journal.RecordResult(PhaseResult{
				PhaseID:   phase.ID,
				Status:    PhaseSkipped,
				StartedAt: time.Now(),
				EndedAt:   time.Now(),
			})
			e.journal.SetCurrentPhase(i + 1)
			e.journal.Save()
			continue
		}
		}

		// Check budget
		if e.workflow.MaxTotalTurns > 0 && e.journal.TotalTurns >= e.workflow.MaxTotalTurns {
			return fmt.Errorf("total turns budget exhausted (%d)", e.workflow.MaxTotalTurns)
		}
		if e.workflow.MaxTotalTokens > 0 && e.journal.TotalTokens >= e.workflow.MaxTotalTokens {
			return fmt.Errorf("total tokens budget exhausted (%d)", e.workflow.MaxTotalTokens)
		}

		// Execute phase
		result, err := e.runPhase(ctx, phase)
		if err != nil {
			if phase.SkipOnError {
				e.journal.RecordResult(PhaseResult{
				PhaseID:   phase.ID,
				Status:    PhaseSkipped,
				Error:     err.Error(),
				StartedAt: time.Now(),
				EndedAt:   time.Now(),
			})
				e.journal.SetCurrentPhase(i + 1)
				e.journal.Save()
				continue
			}
			return fmt.Errorf("phase %s failed: %w", phase.ID, err)
		}

		// Validate output schema
		if phase.OutputSchema != nil && result.Output != "" {
			if err := validateOutputSchema(result.Output, phase.OutputSchema); err != nil {
				result.Status = PhaseFailed
				result.Error = fmt.Sprintf("output schema validation failed: %v", err)
				e.journal.RecordResult(*result)
				e.journal.Save()
				return fmt.Errorf("phase %s output validation: %w", phase.ID, err)
			}
		}

		e.journal.RecordResult(*result)
		e.journal.SetCurrentPhase(i + 1)
		e.journal.Save()

		// Pause if requested
		if phase.PauseAfter {
			e.journal.SetPaused(true)
			e.journal.Save()
			return fmt.Errorf("workflow paused after phase %s — call resume to continue", phase.ID)
		}
	}

	return nil
}

// Resume continues a paused workflow.
func (e *Executor) Resume(ctx context.Context) error {
	e.journal.SetPaused(false)
	e.journal.Save()
	return e.Run(ctx)
}

// defaultConditionEvaluator evaluates simple conditions against phase results.
// Supported syntax:
//   - "true" / "false" → literal
//   - "phase_id.status == completed" → check phase status
//   - "phase_id.status != failed" → negation
func defaultConditionEvaluator(condition string, results []PhaseResult) (bool, error) {
	condition = trim(condition)
	if condition == "" || condition == "true" {
		return true, nil
	}
	if condition == "false" {
		return false, nil
	}

	// Parse: phaseID.status == completed
	// or: phaseID.status != failed
	for _, op := range []string{"==", "!="} {
		idx := indexOf(condition, op)
		if idx > 0 {
			left := trim(condition[:idx])
			right := trim(condition[idx+len(op):])

			// Parse left: phaseID.field
			dotIdx := indexOf(left, ".")
			if dotIdx <= 0 {
				continue
			}
			phaseID := left[:dotIdx]
			field := left[dotIdx+1:]

			// Find the result for this phase
			var result *PhaseResult
			for i := range results {
				if results[i].PhaseID == phaseID {
					result = &results[i]
					break
				}
			}
			if result == nil {
				// Phase not yet run — condition is false
				return false, nil
			}

			var actual string
			switch field {
			case "status":
				actual = result.Status.String()
			case "output":
				actual = result.Output
			case "error":
				actual = result.Error
			default:
				return false, fmt.Errorf("unknown field: %s", field)
			}

			if op == "==" {
				return actual == right, nil
			}
			return actual != right, nil
		}
	}

	return false, fmt.Errorf("unable to evaluate condition: %s", condition)
}

// validateOutputSchema validates that the output matches the expected schema.
func validateOutputSchema(output string, schema map[string]string) error {
	// Try to parse as JSON
	var data map[string]any
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		// If output is not JSON, skip schema validation (might be plain text)
		return nil
	}

	for field, expectedType := range schema {
		val, ok := data[field]
		if !ok {
			return fmt.Errorf("missing required field: %s", field)
		}

		switch expectedType {
		case "string":
			if _, ok := val.(string); !ok {
				return fmt.Errorf("field %s: expected string, got %T", field, val)
			}
		case "number", "int", "float":
			if _, ok := val.(float64); !ok {
				return fmt.Errorf("field %s: expected number, got %T", field, val)
			}
		case "bool":
			if _, ok := val.(bool); !ok {
				return fmt.Errorf("field %s: expected bool, got %T", field, val)
			}
		case "array":
			if _, ok := val.([]any); !ok {
				return fmt.Errorf("field %s: expected array, got %T", field, val)
			}
		case "object":
			if _, ok := val.(map[string]any); !ok {
				return fmt.Errorf("field %s: expected object, got %T", field, val)
			}
		}
	}

	return nil
}

// Helper functions
func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

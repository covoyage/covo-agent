package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

type PlanStepStatus string

const (
	PlanPending    PlanStepStatus = "pending"
	PlanInProgress PlanStepStatus = "in_progress"
	PlanCompleted  PlanStepStatus = "completed"
)

type PlanStep struct {
	Step   string         `json:"step"`
	Status PlanStepStatus `json:"status"`
}

type PlanStore struct {
	mu          sync.RWMutex
	steps       []PlanStep
	explanation string
}

func NewPlanStore() *PlanStore {
	return &PlanStore{}
}

func (s *PlanStore) Update(steps []PlanStep, explanation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	inProgress := 0
	for _, step := range steps {
		if step.Status == PlanInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return fmt.Errorf("at most one step can be in_progress, found %d", inProgress)
	}

	s.steps = steps
	s.explanation = explanation
	return nil
}

func (s *PlanStore) Read() ([]PlanStep, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	steps := make([]PlanStep, len(s.steps))
	copy(steps, s.steps)
	return steps, s.explanation
}

func (s *PlanStore) Summary() (pending, inProgress, completed int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, step := range s.steps {
		switch step.Status {
		case PlanPending:
			pending++
		case PlanInProgress:
			inProgress++
		case PlanCompleted:
			completed++
		}
	}
	return
}

// FormatForInjection renders the active plan for system prompt injection.
func (s *PlanStore) FormatForInjection() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.steps) == 0 {
		return ""
	}

	p, ip, c := 0, 0, 0
	for _, step := range s.steps {
		switch step.Status {
		case PlanPending:
			p++
		case PlanInProgress:
			ip++
		case PlanCompleted:
			c++
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Current plan (%d/%d completed):\n", c, len(s.steps)))
	for _, step := range s.steps {
		var icon string
		switch step.Status {
		case PlanPending:
			icon = "☐"
		case PlanInProgress:
			icon = "◐"
		case PlanCompleted:
			icon = "☑"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", icon, step.Step))
	}
	return b.String()
}

func BuildUpdatePlanTool(store *PlanStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "update_plan",
		Description: strings.Join([]string{
			"Ordered plan for the current task (not a durable tracker; use todo for this turn, kanban/goal to persist).",
			"Update a structured plan with ordered steps and their statuses. The TUI footer shows PLAN n/m plus the in-progress step.",
			"",
			"Each step has a status: pending, in_progress, or completed.",
			"At most ONE step can be in_progress at a time.",
			"At most 50 steps. Steps should be ordered from first to last.",
			"Use only for substantial work with at least two sequential steps.",
			"",
			"Call without 'plan' to read the current plan.",
			"Call with 'plan' to update the plan (replaces the entire plan).",
			"Call with an empty plan array to clear.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plan": map[string]any{
					"type":        "array",
					"description": "Ordered list of plan steps. Omit to read current plan.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"step": map[string]any{
								"type":        "string",
								"description": "Short description of the plan step.",
							},
							"status": map[string]any{
								"type":        "string",
								"description": "Step status: pending, in_progress, or completed.",
								"enum":        []string{"pending", "in_progress", "completed"},
							},
						},
						"required": []string{"step", "status"},
					},
				},
				"explanation": map[string]any{
					"type":        "string",
					"description": "Optional short note explaining what changed in the plan.",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Plan        []PlanStep `json:"plan"`
				Explanation string     `json:"explanation"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if params.Plan == nil {
				// Read mode (plan omitted)
				steps, explanation := store.Read()
				p, ip, c := store.Summary()
				result := map[string]any{
					"steps":   steps,
					"summary": map[string]int{"pending": p, "in_progress": ip, "completed": c, "total": len(steps)},
				}
				if explanation != "" {
					result["explanation"] = explanation
				}
				return result, nil
			}

			if len(params.Plan) > 50 {
				return nil, fmt.Errorf("plan has at most 50 steps")
			}

			// Validate
			for i, step := range params.Plan {
				if strings.TrimSpace(step.Step) == "" {
					return nil, fmt.Errorf("plan[%d]: step is required", i)
				}
				switch step.Status {
				case PlanPending, PlanInProgress, PlanCompleted:
					// valid
				default:
					return nil, fmt.Errorf("plan[%d]: invalid status %q (must be pending, in_progress, or completed)", i, step.Status)
				}
			}

			if err := store.Update(params.Plan, params.Explanation); err != nil {
				return nil, err
			}

			steps, _ := store.Read()
			p, ip, c := store.Summary()
			result := map[string]any{
				"status":  "updated",
				"steps":   steps,
				"summary": map[string]int{"pending": p, "in_progress": ip, "completed": c, "total": len(steps)},
			}
			if params.Explanation != "" {
				result["explanation"] = params.Explanation
			}
			return result, nil
		},
	}
}

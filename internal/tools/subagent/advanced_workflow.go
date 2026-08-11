package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/workflow"
	"github.com/covoyage/covonaut/agentcore"
)

// BuildAdvancedWorkflowTool returns a tool that orchestrates multi-phase
// workflows with conditions, budgets, pause/resume, and journal persistence.
func BuildAdvancedWorkflowTool(runner SpawnRunner, registry *SubagentRegistry, homeDir string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "advanced_workflow",
		Description: strings.Join([]string{
			"Orchestrate a multi-phase workflow with advanced features:",
			"- Conditional phase execution (skip phases based on prior results)",
			"- Turn/token budgets per phase and total",
			"- Pause/resume support (persisted to journal)",
			"- Output schema validation",
			"- Crash recovery via journal persistence",
			"",
			"Use this for complex multi-step tasks that need budget control",
			"and the ability to pause for user review between phases.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Workflow name for journal tracking.",
				},
				"phases": map[string]any{
					"type":        "array",
					"description": "Ordered phases to execute.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "Unique phase identifier.",
							},
							"name": map[string]any{
								"type":        "string",
								"description": "Human-readable phase name.",
							},
							"prompt": map[string]any{
								"type":        "string",
								"description": "Task prompt for this phase.",
							},
							"condition": map[string]any{
								"type":        "string",
								"description": "Optional condition expression (e.g. 'phase_id.status == completed').",
							},
							"max_turns": map[string]any{
								"type":        "integer",
								"description": "Max agent turns for this phase (0 = unlimited).",
							},
							"pause_after": map[string]any{
								"type":        "boolean",
								"description": "Pause for user review after this phase.",
							},
						},
						"required": []string{"id", "prompt"},
					},
				},
				"max_total_turns": map[string]any{
					"type":        "integer",
					"description": "Total turn budget across all phases (0 = unlimited).",
				},
			},
			"required": []string{"phases"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Name          string `json:"name"`
				Phases        []struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					Prompt      string `json:"prompt"`
					Condition   string `json:"condition"`
					MaxTurns    int    `json:"max_turns"`
					PauseAfter  bool   `json:"pause_after"`
				} `json:"phases"`
				MaxTotalTurns int `json:"max_total_turns"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("parse arguments: %w", err)
			}
			if len(params.Phases) == 0 {
				return nil, fmt.Errorf("at least one phase is required")
			}

			// Build workflow
			wf := &workflow.Workflow{
				ID:          fmt.Sprintf("wf-%d", time.Now().UnixMilli()),
				Name:        params.Name,
				Phases:      make([]workflow.Phase, len(params.Phases)),
				MaxTotalTurns: params.MaxTotalTurns,
			}
			for i, p := range params.Phases {
				wf.Phases[i] = workflow.Phase{
					ID:         p.ID,
					Name:       p.Name,
					Prompt:     p.Prompt,
					Condition:  p.Condition,
					MaxTurns:   p.MaxTurns,
					PauseAfter: p.PauseAfter,
				}
			}

			// Create journal for crash recovery
			journalDir := filepath.Join(homeDir, "workflows")
			os.MkdirAll(journalDir, 0o755)
			journalPath := filepath.Join(journalDir, wf.ID+".json")
			journal, err := workflow.NewJournal(journalPath, wf.ID)
			if err != nil {
				return nil, fmt.Errorf("create journal: %w", err)
			}

			// Create executor with subagent runner
			executor := workflow.NewExecutor(wf, journal, func(ctx context.Context, phase workflow.Phase) (*workflow.PhaseResult, error) {
				// Track in subagent registry
				var subID string
				if registry != nil {
					subID = registry.Start(phase.Prompt, 1)
				}

				output, err := runner(ctx, phase.Prompt, nil, phase.MaxTurns)
				if err != nil {
					if registry != nil && subID != "" {
						registry.Complete(subID, true)
					}
					return nil, err
				}

				if registry != nil && subID != "" {
					registry.Complete(subID, false)
				}

				return &workflow.PhaseResult{
					PhaseID:   phase.ID,
					Status:    workflow.PhaseCompleted,
					Output:    output,
					StartedAt: time.Now(),
					EndedAt:   time.Now(),
				}, nil
			})

			// Run the workflow
			if err := executor.Run(ctx); err != nil {
				return nil, fmt.Errorf("workflow execution: %w", err)
			}

			// Collect results
			results := journal.GetResults()
			completed := 0
			for _, r := range results {
				if r.Status == workflow.PhaseCompleted {
					completed++
				}
			}

			return map[string]any{
				"workflow_id":   wf.ID,
				"total_phases":  len(results),
				"completed":     completed,
				"all_completed": completed == len(results),
				"results":       results,
			}, nil
		},
	}
}

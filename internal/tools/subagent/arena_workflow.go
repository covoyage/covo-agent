package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// --- Dashboard ---

func BuildDashboardTool(registry *SubagentRegistry) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "dashboard",
		Description: strings.Join([]string{
			"Show a live monitoring panel of all subagents: active, completed, and failed.",
			"Use this to track parallel work, identify stuck agents, and decide next actions.",
			"",
			"Displays: id, task, status, depth, elapsed time, staleness flag.",
			"Stale agents (running >5min without heartbeat) are flagged.",
		}, "\n"),
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			runs := registry.List(false)

			type row struct {
				ID      string `json:"id"`
				Task    string `json:"task"`
				Status  string `json:"status"`
				Depth   int    `json:"depth"`
				Elapsed string `json:"elapsed"`
				Stale   bool   `json:"stale,omitempty"`
			}

			var rows []row
			active, completed, failed, stale := 0, 0, 0, 0

			for _, r := range runs {
				elapsed := time.Since(r.StartedAt).Truncate(time.Second).String()
				isStale := r.Status == "running" && time.Since(r.StartedAt) > 5*time.Minute
				if isStale {
					stale++
				}

				task := r.Task
				if len(task) > 60 {
					task = task[:60] + "..."
				}

				rows = append(rows, row{
					ID:      r.ID,
					Task:    task,
					Status:  r.Status,
					Depth:   r.Depth,
					Elapsed: elapsed,
					Stale:   isStale,
				})

				switch r.Status {
				case "running":
					active++
				case "completed":
					completed++
				case "failed":
					failed++
				}
			}

			sort.Slice(rows, func(i, j int) bool {
				if rows[i].Status == "running" && rows[j].Status != "running" {
					return true
				}
				return false
			})

			return map[string]any{
				"subagents": rows,
				"active":    active,
				"completed": completed,
				"failed":    failed,
				"stale":     stale,
				"total":     len(rows),
			}, nil
		},
	}
}

// --- Workflow ---

func BuildWorkflowTool(runner SpawnRunner, registry *SubagentRegistry) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "workflow",
		Description: strings.Join([]string{
			"Orchestrate a sequential pipeline of subagent phases. Each phase runs only",
			"after the previous phase completes. Output from earlier phases is available",
			"via {{phase_label}} references in later phase tasks.",
			"",
			"If any phase fails, remaining phases are skipped and marked accordingly.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"phases": map[string]any{
					"type":        "array",
					"description": "Ordered phases to execute sequentially.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"label": map[string]any{
								"type":        "string",
								"description": "Phase label used for output referencing.",
							},
							"toolsets": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Toolset names for this phase.",
							},
							"task": map[string]any{
								"type":        "string",
								"description": "Task description. Use {{label}} to reference prior output.",
							},
						},
						"required": []string{"label", "task"},
					},
				},
			},
			"required": []string{"phases"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Phases []struct {
					Label    string   `json:"label"`
					Toolsets []string `json:"toolsets"`
					Task     string   `json:"task"`
				} `json:"phases"`
			}
			json.Unmarshal(args, &params)
			if len(params.Phases) == 0 {
				return nil, fmt.Errorf("at least one phase required")
			}

			type phaseResult struct {
				Label  string `json:"label"`
				Output string `json:"output,omitempty"`
				Error  string `json:"error,omitempty"`
				Status string `json:"status"`
			}

			var results []phaseResult
			prevOutputs := map[string]string{}

			for _, phase := range params.Phases {
				task := phase.Task
				for label, output := range prevOutputs {
					task = strings.ReplaceAll(task,
						fmt.Sprintf("{{%s}}", label), output)
				}

				// Track in subagent registry for dashboard visibility
				var subID string
				if registry != nil {
					subID = registry.Start(task, 1)
				}

				result := phaseResult{
					Label:  phase.Label,
					Status: "running",
				}

				output, err := runner(ctx, task, phase.Toolsets, 0)
				if err != nil {
					result.Status = "failed"
					result.Error = err.Error()
					results = append(results, result)
					if registry != nil && subID != "" {
						registry.Complete(subID, true)
					}
					// Skip remaining
					for i := len(results); i < len(params.Phases); i++ {
						results = append(results, phaseResult{
							Label:  params.Phases[i].Label,
							Status: "skipped",
						})
					}
					break
				}

				result.Status = "completed"
				result.Output = output
				results = append(results, result)
				prevOutputs[phase.Label] = output

				if registry != nil && subID != "" {
					registry.Complete(subID, false)
				}
			}

			completed := 0
			for _, r := range results {
				if r.Status == "completed" {
					completed++
				}
			}

			return map[string]any{
				"phases":        results,
				"total":         len(params.Phases),
				"completed":     completed,
				"all_completed": completed == len(params.Phases),
			}, nil
		},
	}
}

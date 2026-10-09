package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/workflow"
	"github.com/covoyage/covonaut/agentcore"
)

// BuildScriptWorkflowTool compiles a checkable workflow script, then runs
// the recovered phases through the existing executor.
func BuildScriptWorkflowTool(runner SpawnRunner, registry *SubagentRegistry, homeDir string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "script_workflow",
		Description: strings.Join([]string{
			"Compile and run a checkable workflow script.",
			"Allowed statements: name(\"...\"), phase(\"id\", \"prompt\"), ask(\"question\"), spawn(\"id\", \"role\").",
			"The compiler recovers a site graph, rejects unknown statements and duplicate ids,",
			"then executes recovered phases as subagents. ask() sites pause for later user input.",
			"Prefer this over advanced_workflow when the plan should be inspectable before running.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"script": map[string]any{
					"type":        "string",
					"description": "Workflow script source. Required unless resuming with workflow_id.",
				},
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "If true, only compile and return the graph; do not execute.",
				},
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Resume a paused script journal.",
				},
				"answers": map[string]any{
					"type":        "object",
					"description": "Answers for ask() sites, keyed by site id.",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Script     string            `json:"script"`
				DryRun     bool              `json:"dry_run"`
				WorkflowID string            `json:"workflow_id"`
				Answers    map[string]string `json:"answers"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("parse arguments: %w", err)
			}
			if strings.TrimSpace(params.Script) == "" && params.WorkflowID == "" {
				return nil, fmt.Errorf("script or workflow_id is required")
			}
			run, err := workflow.ExecuteScript(ctx, workflow.ScriptExecOptions{
				Source:     params.Script,
				HomeDir:    homeDir,
				WorkflowID: params.WorkflowID,
				Answers:    params.Answers,
				DryRun:     params.DryRun || runner == nil,
				RunPhase: func(ctx context.Context, phase workflow.Phase) (*workflow.PhaseResult, error) {
					var subID string
					if registry != nil {
						subID = registry.Start(phase.Prompt, 1)
					}
					phaseCtx, prompt, toolsets := ApplySpawnRole(ctx, phase.Prompt, phase.Role, phase.Toolsets)
					output, runErr := runner(phaseCtx, prompt, toolsets, phase.MaxTurns)
					if registry != nil && subID != "" {
						registry.Complete(subID, runErr != nil)
					}
					if runErr != nil {
						return nil, runErr
					}
					return &workflow.PhaseResult{
						PhaseID:   phase.ID,
						Status:    workflow.PhaseCompleted,
						Output:    output,
						StartedAt: time.Now(),
						EndedAt:   time.Now(),
					}, nil
				},
			})
			if err != nil {
				return nil, err
			}
			result := map[string]any{
				"status":      run.Status,
				"name":        run.Analysis.Name,
				"sites":       run.Analysis.Sites,
				"mermaid":     run.Analysis.Mermaid,
				"ask_count":   run.Analysis.AskCount,
				"spawn_count": run.Analysis.SpawnCount,
				"diagnostics": run.Analysis.Diagnostics,
			}
			if run.WorkflowID != "" {
				result["workflow_id"] = run.WorkflowID
			}
			if run.PendingAsk != nil {
				result["pending_ask"] = run.PendingAsk
			}
			if len(run.Results) > 0 {
				result["results"] = run.Results
			}
			return result, nil
		},
	}
}

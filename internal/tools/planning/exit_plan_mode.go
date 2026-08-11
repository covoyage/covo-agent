package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

type ExitPlanModeResult struct {
	Plan     string `json:"plan"`
	Awaiting string `json:"awaiting"`
}

// PhaseTransitioner is called when the user approves an exit_plan_mode
// request, transitioning the agent from Plan to Act execution phase.
// This allows mutating tools to be unblocked.
type PhaseTransitioner func()

func BuildExitPlanModeTool(store *PlanStore, transitioner PhaseTransitioner) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "exit_plan_mode",
		Description: strings.Join([]string{
			"Use this tool when you have finished presenting your plan and are ready to code.",
			"This will prompt the user to approve your plan before you start implementing.",
			"",
			"## IMPORTANT: Only use this when the task requires planning implementation steps",
			"for a task that involves writing code. For research tasks where you're",
			"gathering information, searching files, or reading files to understand",
			"the codebase - do NOT use this tool.",
			"",
			"## Before Using:",
			"- Ensure your plan is complete and unambiguous",
			"- If you have unresolved questions, use 'clarify' first",
			"- Do NOT use 'clarify' to ask \"Is this plan okay?\" - use THIS tool",
			"",
			"## Examples:",
			"1. Task: \"Search for vim mode implementation\" -> Do NOT use exit_plan_mode",
			"2. Task: \"Help me implement yank mode for vim\" -> Use exit_plan_mode after planning",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plan": map[string]any{
					"type":        "string",
					"description": "The plan you came up with, that you want to run by the user for approval. Supports markdown. Should be concise.",
				},
			},
			"required": []string{"plan"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Plan string `json:"plan"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Plan) == "" {
				return nil, fmt.Errorf("plan is required")
			}

			// Transition to Act mode if a transitioner is configured.
			// The TUI layer handles user approval; if approved, this
			// transitioner is called to unblock mutating tools.
			if transitioner != nil {
				transitioner()
			}

			return ExitPlanModeResult{
				Plan:     params.Plan,
				Awaiting: "approval",
			}, nil
		},
	}
}

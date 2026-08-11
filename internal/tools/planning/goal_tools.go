package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/goal"
	"github.com/covoyage/covonaut/agentcore"
)

func BuildGetGoalTool(store *goal.Store, sessionIDFn func() string) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "get_goal",
		Description: "Retrieve the current goal state for this session. Returns the goal with status, objective, token usage, and budget.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			sessionID := sessionIDFn()
			if sessionID == "" {
				return map[string]any{"status": "no_active_session"}, nil
			}
			g, err := store.Get(ctx, sessionID)
			if err != nil {
				return nil, fmt.Errorf("get goal: %w", err)
			}
			if g == nil {
				return map[string]any{"status": "no_active_goal"}, nil
			}
			return map[string]any{
				"id":                g.GoalID,
				"objective":         g.Objective,
				"status":            string(g.Status),
				"token_budget":      g.TokenBudget,
				"tokens_used":       g.TokensUsed,
				"time_used_seconds": g.TimeUsedSeconds,
				"created_at":        g.CreatedAt.Format(time.RFC3339),
				"updated_at":        g.UpdatedAt.Format(time.RFC3339),
			}, nil
		},
	}
}

func BuildCreateGoalTool(store *goal.Store, sessionIDFn func() string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "create_goal",
		Description: strings.Join([]string{
			"Create a new session-level goal. The goal persists across turns, is tracked for token budget, and is verified by an independent judge before the agent can stop.",
			"Only one goal can be active per session. Setting a new goal replaces the previous one.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objective": map[string]any{
					"type":        "string",
					"description": "The goal objective — what should be accomplished.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Optional token budget for this goal (default: unlimited).",
				},
			},
			"required": []string{"objective"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Objective   string `json:"objective"`
				TokenBudget *int64 `json:"token_budget"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Objective) == "" {
				return nil, fmt.Errorf("objective is required")
			}

			sessionID := sessionIDFn()
			if sessionID == "" {
				return nil, fmt.Errorf("no active session")
			}

			g := &goal.Goal{
				SessionID:   sessionID,
				Objective:   strings.TrimSpace(params.Objective),
				Status:      goal.StatusActive,
				TokenBudget: params.TokenBudget,
			}
			if err := store.Put(ctx, g); err != nil {
				return nil, fmt.Errorf("create goal: %w", err)
			}
			return map[string]any{
				"id":        g.GoalID,
				"objective": g.Objective,
				"status":    string(g.Status),
				"message":   "Goal created. Use get_goal to check progress.",
			}, nil
		},
	}
}

func BuildUpdateGoalTool(store *goal.Store, sessionIDFn func() string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "update_goal",
		Description: strings.Join([]string{
			"Update the status of the current session goal.",
			"Status can be: 'completed' (goal achieved), 'blocked' (needs intervention).",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"description": "New status: 'completed' or 'blocked'.",
					"enum":        []string{"completed", "blocked"},
				},
			},
			"required": []string{"status"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			sessionID := sessionIDFn()
			if sessionID == "" {
				return nil, fmt.Errorf("no active session")
			}

			var g *goal.Goal
			var err error
			switch params.Status {
			case "completed":
				g, err = store.Complete(ctx, sessionID)
				if err != nil {
					return nil, fmt.Errorf("complete goal: %w", err)
				}
				if g == nil {
					return nil, fmt.Errorf("no active goal to complete")
				}
			case "blocked":
				g, err = store.Block(ctx, sessionID)
				if err != nil {
					return nil, fmt.Errorf("block goal: %w", err)
				}
				if g == nil {
					// Check if there's no goal at all
					existing, _ := store.Get(ctx, sessionID)
					if existing == nil {
						return nil, fmt.Errorf("no goal to block")
					}
					// Goal exists but can't transition from current status
					return nil, fmt.Errorf("goal cannot be blocked from current status (%s)", existing.Status)
				}
			default:
				return nil, fmt.Errorf("status must be 'completed' or 'blocked'")
			}

			return map[string]any{
				"id":        g.GoalID,
				"objective": g.Objective,
				"status":    string(g.Status),
				"message":   fmt.Sprintf("Goal marked %s", params.Status),
			}, nil
		},
	}
}

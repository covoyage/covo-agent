package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// AskUserFunc presents a structured question to the user and blocks until the
// user responds. options is the list of suggested answers the model provided
// (may be empty); defaultValue is the answer to use when no interactive user
// is present (headless/cron/oneshot) or the user declines to answer.
type AskUserFunc func(ctx context.Context, question string, options []string, defaultValue string) (string, error)

// buildAskUserTool returns the ask_user tool, which lets the agent ask the
// user a structured question (optionally with suggested answers) and block
// until an answer is received. This complements human_handoff (free-form
// input) with a decision-oriented interface: the model enumerates options and
// a default, so headless/cron runs can proceed without a human present.
func buildAskUserTool(callback AskUserFunc) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "ask_user",
		Description: strings.Join([]string{
			"Ask the user a question and block until they answer.",
			"Use this when you need the user to make a decision or pick between",
			"alternatives before you can continue. Prefer it over human_handoff",
			"whenever you can enumerate the possible answers.",
			"",
			"Always provide a 'default': when no interactive user is present",
			"(headless, cron, oneshot) or the user skips the question, that value",
			"is used automatically so the run does not hang. Without a default the",
			"tool fails in non-interactive contexts — so think hard about what a",
			"reasonable fallback is before calling it.",
			"",
			"The user's answer (their chosen option or free-form reply) is returned",
			"as the tool result.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question to ask the user. Be specific and self-contained: the user sees only this text, not your reasoning.",
				},
				"options": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Suggested answers the user can pick from (optional, but recommended). The user may also type their own answer.",
				},
				"default": map[string]any{
					"type":        "string",
					"description": "Fallback answer used when no interactive user is present (headless/cron/oneshot) or the user skips the question. Strongly recommended — without it the tool fails in non-interactive contexts.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Maximum seconds to wait for the user. On timeout the default is used (or the tool fails if no default is given). 0 = wait indefinitely.",
				},
			},
			"required": []string{"question"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Question       string   `json:"question"`
				Options        []string `json:"options"`
				Default        string   `json:"default"`
				TimeoutSeconds int      `json:"timeout_seconds"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Question) == "" {
				return nil, fmt.Errorf("question is required")
			}

			if params.TimeoutSeconds > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Duration(params.TimeoutSeconds)*time.Second)
				defer cancel()
			}

			answer := ""
			var err error
			if callback != nil {
				answer, err = callback(ctx, params.Question, params.Options, params.Default)
			} else {
				err = fmt.Errorf("no interactive user available")
			}

			// Fall back to the default when the user is unavailable, skipped
			// the question, or the interaction failed.
			if err != nil || strings.TrimSpace(answer) == "" {
				if params.Default != "" {
					return map[string]any{
						"response":     params.Default,
						"used_default": true,
					}, nil
				}
				if err != nil {
					return nil, fmt.Errorf("ask_user: %w", err)
				}
				return nil, fmt.Errorf("ask_user: no answer provided and no default given")
			}

			return map[string]any{
				"response":     strings.TrimSpace(answer),
				"used_default": false,
			}, nil
		},
	}
}

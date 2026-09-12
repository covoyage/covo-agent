package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// AskFunc presents a question and blocks until the user answers.
// Same shape as tools.AskUserFunc so the TUI callback can be reused.
type AskFunc func(ctx context.Context, question string, options []string, defaultValue string) (string, error)

type ClarifyOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type ClarifyResult struct {
	Question      string          `json:"question"`
	Header        string          `json:"header,omitempty"`
	Choices       []string        `json:"choices,omitempty"`
	Options       []ClarifyOption `json:"options,omitempty"`
	DefaultChoice string          `json:"default_choice,omitempty"`
	MultiSelect   bool            `json:"multi_select,omitempty"`
}

func BuildClarifyTool(ask AskFunc) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "clarify",
		Description: strings.Join([]string{
			"Ask the user a clarifying question and wait for their answer before proceeding.",
			"Blocks until the user responds (same interaction as ask_user).",
			"Use this when:",
			"- There are multiple valid interpretations of the request",
			"- You need to make a decision that depends on user preference",
			"- A critical detail is missing and you cannot safely assume",
			"",
			"For simple multiple-choice questions, use 'choices' (up to 4 string options).",
			"For richer prompts where each option needs explanation, use 'options' with label+description pairs.",
			"For open-ended questions, omit both choices and options.",
			"Set 'header' to a short label (max 12 chars) displayed as a chip/tag.",
			"Set 'default_choice' so headless/cron runs do not hang.",
			"",
			"The user's answer is returned as 'response'.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question to ask the user. Be clear and concise.",
				},
				"header": map[string]any{
					"type":        "string",
					"description": "Short label displayed as a chip/tag (max 12 chars). Examples: \"Auth method\", \"Library\", \"Approach\".",
					"maxLength":   12,
				},
				"choices": map[string]any{
					"type":        "array",
					"description": "Simple string choices for a multiple-choice question (max 4). Use 'options' instead when each choice needs a description.",
					"items":       map[string]any{"type": "string"},
					"maxItems":    4,
				},
				"options": map[string]any{
					"type":        "array",
					"description": "Structured options with label and description. Each option has a label (display text) and optional description (explanation). Max 4. Prefer this over 'choices' when options have trade-offs that need explanation.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"label":       map[string]any{"type": "string", "description": "Display text for this option (1-5 words)."},
							"description": map[string]any{"type": "string", "description": "Explanation of what this option means or trade-offs."},
						},
						"required": []string{"label"},
					},
					"maxItems": 4,
				},
				"multi_select": map[string]any{
					"type":        "boolean",
					"description": "Set to true to allow the user to select multiple options instead of just one. Use when choices are not mutually exclusive.",
				},
				"default_choice": map[string]any{
					"type":        "string",
					"description": "Default choice if the user just presses Enter.",
				},
			},
			"required": []string{"question"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Question      string          `json:"question"`
				Header        string          `json:"header"`
				Choices       []string        `json:"choices"`
				Options       []ClarifyOption `json:"options"`
				DefaultChoice string          `json:"default_choice"`
				MultiSelect   bool            `json:"multi_select"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if strings.TrimSpace(params.Question) == "" {
				return nil, fmt.Errorf("question is required")
			}

			if len(params.Header) > 12 {
				params.Header = params.Header[:12]
			}

			if len(params.Choices) > 4 {
				params.Choices = params.Choices[:4]
			}
			if len(params.Options) > 4 {
				params.Options = params.Options[:4]
			}

			opts := params.Choices
			if len(opts) == 0 {
				for _, o := range params.Options {
					if label := strings.TrimSpace(o.Label); label != "" {
						opts = append(opts, label)
					}
				}
			}

			question := params.Question
			if params.Header != "" {
				question = "[" + params.Header + "] " + question
			}

			answer := ""
			var err error
			if ask != nil {
				answer, err = ask(ctx, question, opts, params.DefaultChoice)
			} else {
				err = fmt.Errorf("no interactive user available")
			}
			if err != nil || strings.TrimSpace(answer) == "" {
				if params.DefaultChoice != "" {
					return map[string]any{
						"response":     params.DefaultChoice,
						"used_default": true,
						"question":     params.Question,
					}, nil
				}
				if err != nil {
					return nil, fmt.Errorf("clarify: %w", err)
				}
				return nil, fmt.Errorf("clarify: no answer provided and no default_choice given")
			}

			return map[string]any{
				"response":     strings.TrimSpace(answer),
				"used_default": false,
				"question":     params.Question,
			}, nil
		},
	}
}

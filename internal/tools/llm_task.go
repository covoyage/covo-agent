package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func buildLLMTaskTool(provider agentcore.Provider, defaultModel string) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "llm_task",
		Description: strings.Join([]string{
			"Call a language model independently with a custom prompt.",
			"Use this for subtasks that benefit from a separate LLM invocation:",
			"- Summarization or analysis of large text",
			"- Translation",
			"- Data extraction and structured output",
			"- Asking a different model a separate question",
			"",
			"The response is returned as text. No tools are available in this call.",
			"If no model is specified, the current agent model is used.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "The user prompt / instruction for the LLM.",
				},
				"system": map[string]any{
					"type":        "string",
					"description": "Optional system prompt to set context and behavior.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model identifier (e.g. 'gpt-4o', 'claude-sonnet-4-20250514'). Defaults to the current agent model.",
				},
				"temperature": map[string]any{
					"type":        "number",
					"description": "Optional sampling temperature (0.0 to 2.0). Defaults to 0.7.",
				},
				"max_tokens": map[string]any{
					"type":        "integer",
					"description": "Optional maximum tokens in the response.",
					"minimum":     1,
				},
			},
			"required": []string{"prompt"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Prompt      string  `json:"prompt"`
				System      string  `json:"system"`
				Model       string  `json:"model"`
				Temperature float64 `json:"temperature"`
				MaxTokens   int     `json:"max_tokens"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Prompt) == "" {
				return nil, fmt.Errorf("prompt is required")
			}
			if params.Temperature == 0 {
				params.Temperature = 0.7
			}

			model := params.Model
			if model == "" {
				model = defaultModel
			}

			messages := []agentcore.Message{
				{Role: agentcore.RoleUser, Content: params.Prompt},
			}
			if params.System != "" {
				messages = append([]agentcore.Message{
					{Role: agentcore.RoleSystem, Content: params.System},
				}, messages...)
			}

			req := &agentcore.ProviderRequest{
				Model:       model,
				Messages:    messages,
				Temperature: params.Temperature,
			}
			if params.MaxTokens > 0 {
				req.MaxTokens = int64(params.MaxTokens)
			}

			if provider == nil {
				return nil, fmt.Errorf("llm_task: no provider configured")
			}

			resp, err := provider.Complete(ctx, req)
			if err != nil {
				return map[string]any{
					"status": "error",
					"error":  err.Error(),
				}, nil
			}
			if resp == nil {
				return map[string]any{
					"status": "error",
					"error":  "no response from provider",
				}, nil
			}

			return map[string]any{
				"status": "ok",
				"output": resp.Content,
				"model":  model,
				"usage": map[string]any{
					"input_tokens":  resp.Usage.PromptTokens,
					"output_tokens": resp.Usage.CompletionTokens,
				},
			}, nil
		},
	}
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/safego"
)

const (
	moaMaxRetries    = 2
	moaTimeoutPerRef = 120 * time.Second
	moaMaxRefChars   = 6000 // max chars per reference response to avoid context overflow
)

func moaDefaultModelList(primary string) []string {
	if env := strings.TrimSpace(os.Getenv("COVO_MOA_MODELS")); env != "" {
		return splitModelList(env)
	}
	primary = strings.TrimSpace(primary)
	if primary == "" {
		return nil
	}
	return []string{primary}
}

func splitModelList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

var moaAggregatorSystemPrompt = `You have been provided with responses from various models to the latest user query.
Your task is to synthesize these responses into a single, high-quality response.
It is critical to critically evaluate the information provided in each response,
recognizing agreements, contradictions, and ambiguities.
Some responses may be incomplete, incorrect, or entirely missing — filter those out.
Your synthesized response should be well-structured, accurate, and directly address the user's query.
Do NOT mention that you are synthesizing multiple model outputs.`

type MoAResult struct {
	Model   string `json:"model"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
	Latency string `json:"latency"`
}

type MoAConfig struct {
	Provider     agentcore.Provider
	DefaultModel string
}

func buildMoATool(cfg MoAConfig) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "moa",
		Description: strings.Join([]string{
			"Mixture of Agents — queries one or more LLMs in parallel and synthesizes the best answer.",
			"",
			"Default models: the current agent model (override with models= or COVO_MOA_MODELS).",
			"Does not assume gpt-4o / Claude / Gemini exist on the current provider.",
			"",
			"Parameters:",
			"- query (required): The question or task",
			"- models (optional): Comma-separated model IDs on the current provider",
			"- system_prompt (optional): Custom system prompt for all models",
			"- aggregator_model (optional): Model to use for final synthesis (default: main model)",
			"",
			"Example:",
			`  {"query":"Explain quantum computing in simple terms"}`,
			`  {"query":"Review this code","system_prompt":"You are a senior code reviewer"}`,
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The question or task to process",
				},
				"models": map[string]any{
					"type":        "string",
					"description": "Comma-separated model IDs on the current provider (or set COVO_MOA_MODELS)",
				},
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "Optional system prompt override",
				},
				"aggregator_model": map[string]any{
					"type":        "string",
					"description": "Model to use for final synthesis (default: the agent's main model)",
				},
			},
			"required": []string{"query"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			startTime := time.Now()

			var params struct {
				Query           string `json:"query"`
				Models          string `json:"models"`
				SystemPrompt    string `json:"system_prompt"`
				AggregatorModel string `json:"aggregator_model"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if params.Query == "" {
				return nil, fmt.Errorf("query is required")
			}

			provider := cfg.Provider
			if provider == nil {
				return map[string]any{
					"success": false,
					"error":   "no LLM provider configured",
				}, nil
			}

			systemPrompt := params.SystemPrompt
			if systemPrompt == "" {
				systemPrompt = "You are a helpful assistant. Provide clear, accurate, and well-structured responses."
			}

			modelList := splitModelList(params.Models)
			if len(modelList) == 0 {
				modelList = moaDefaultModelList(cfg.DefaultModel)
			}
			if len(modelList) == 0 {
				return map[string]any{
					"success": false,
					"error":   "no reference models: pass models= or set COVO_MOA_MODELS",
				}, nil
			}

			aggregatorModel := params.AggregatorModel
			if aggregatorModel == "" {
				aggregatorModel = cfg.DefaultModel
			}
			if aggregatorModel == "" {
				aggregatorModel = modelList[0]
			}

			// Phase 1: parallel reference model calls
			type refResult struct {
				model   string
				content string
				err     error
				latency time.Duration
			}

			var wg sync.WaitGroup
			results := make(chan refResult, len(modelList))

			for _, m := range modelList {
				model := strings.TrimSpace(m)
				if model == "" {
					continue
				}
				wg.Add(1)
				mdl := model
				safego.SafeGo(func() {
					defer wg.Done()
					start := time.Now()

					var lastErr error
					var content string
					for attempt := 0; attempt <= moaMaxRetries; attempt++ {
						if attempt > 0 {
							time.Sleep(time.Duration(attempt) * 2 * time.Second)
						}
						refCtx, cancel := context.WithTimeout(ctx, moaTimeoutPerRef)
						resp, err := provider.Complete(refCtx, &agentcore.ProviderRequest{
							Model: mdl,
							Messages: []agentcore.Message{
								{Role: agentcore.RoleSystem, Content: systemPrompt},
								{Role: agentcore.RoleUser, Content: params.Query},
							},
							Temperature: 0.6,
							MaxTokens:   8192,
						})
						cancel()

						if err != nil {
							lastErr = err
							continue
						}
						content = strings.TrimSpace(resp.Content)
						if content == "" {
							lastErr = fmt.Errorf("empty response")
							continue
						}
						lastErr = nil
						break
					}

					results <- refResult{
						model:   mdl,
						content: content,
						err:     lastErr,
						latency: time.Since(start),
					}
				}, nil)
			}

			wg.Wait()
			close(results)

			// Collect reference results
			var successful []refResult
			var refModels []string
			var refDetails []MoAResult
			for r := range results {
				detail := MoAResult{
					Model:   r.model,
					Latency: r.latency.Round(time.Millisecond).String(),
				}
				if r.err != nil {
					detail.Error = r.err.Error()
				} else {
					detail.Content = r.content
					successful = append(successful, r)
					refModels = append(refModels, r.model)
				}
				refDetails = append(refDetails, detail)
			}

			if len(successful) == 0 {
				return map[string]any{
					"success":    false,
					"error":      "all reference models failed",
					"references": refDetails,
				}, nil
			}

			// Phase 2: aggregation
			var aggParts []string
			for i, r := range successful {
				content := r.content
				if len(content) > moaMaxRefChars {
					content = content[:moaMaxRefChars] + "\n[...truncated]"
				}
				aggParts = append(aggParts, fmt.Sprintf("%d. <%s>\n%s\n</%s>", i+1, r.model, content, r.model))
			}

			aggMessages := []agentcore.Message{
				{Role: agentcore.RoleSystem, Content: moaAggregatorSystemPrompt + "\n\n" + strings.Join(aggParts, "\n\n")},
				{Role: agentcore.RoleUser, Content: params.Query},
			}

			var aggResp *agentcore.ProviderResponse
			var aggErr error
			for attempt := 0; attempt <= 1; attempt++ {
				if attempt > 0 {
					time.Sleep(2 * time.Second)
				}
				aggCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
				aggResp, aggErr = provider.Complete(aggCtx, &agentcore.ProviderRequest{
					Model:       aggregatorModel,
					Messages:    aggMessages,
					Temperature: 0.4,
					MaxTokens:   16384,
				})
				cancel()
				if aggErr != nil {
					continue
				}
				if strings.TrimSpace(aggResp.Content) != "" {
					break
				}
				aggErr = fmt.Errorf("empty response")
			}

			if aggErr != nil {
				return map[string]any{
					"success":    false,
					"error":      fmt.Sprintf("aggregation failed: %s", aggErr),
					"references": refDetails,
				}, nil
			}

			return map[string]any{
				"success":           true,
				"response":          strings.TrimSpace(aggResp.Content),
				"models_used":       refModels,
				"aggregator_model":  aggregatorModel,
				"reference_details": refDetails,
				"total_time":        time.Since(startTime).Round(time.Millisecond).String(),
			}, nil
		},
	}
}

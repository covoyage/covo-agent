package context

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/agentcore"
)

// ContextCompressor summarizes long conversation histories to stay within token limits.
type ContextCompressor struct {
	mu               sync.RWMutex
	thresholdPct     float64
	maxContextTokens int

	// Per-fragment token budgets
	FragmentLimiter *FragmentTokenLimiter

	// Compression history
	compressedCount int
}

func NewContextCompressor(maxContextTokens int, thresholdPct float64) *ContextCompressor {
	if thresholdPct <= 0 {
		thresholdPct = 50
	}
	if maxContextTokens <= 0 {
		maxContextTokens = 200000
	}
	return &ContextCompressor{
		thresholdPct:     thresholdPct,
		maxContextTokens: maxContextTokens,
	}
}

func (c *ContextCompressor) ThresholdPct() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.thresholdPct
}

func (c *ContextCompressor) SetThreshold(pct float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.thresholdPct = pct
}

func (c *ContextCompressor) ShouldCompress(estimatedTokens, maxTokens int) bool {
	if maxTokens <= 0 {
		maxTokens = c.maxContextTokens
	}
	// Check all fragment budgets first
	if c.FragmentLimiter != nil {
		for _, b := range c.FragmentLimiter.Budgets() {
			// If any fragment approaches its budget, trigger compression
			if float64(estimatedTokens)*0.3 > float64(b.MaxTokens) {
				return true
			}
		}
	}
	pct := float64(estimatedTokens) / float64(maxTokens) * 100
	return pct >= c.ThresholdPct()
}

// BuildCompressionPrompt creates a system-user message pair that tells the model
// to summarize all prior conversation.
func (c *ContextCompressor) BuildCompressionPrompt() []map[string]string {
	return []map[string]string{
		{
			"role":    "system",
			"content": compressionSystemPrompt,
		},
		{
			"role": "user",
			"content": strings.Join([]string{
				"Summarize the entire conversation above into a comprehensive",
				"but concise record. Preserve all:",
				"- Key decisions made and their reasoning",
				"- Code changes, file paths, and architectural decisions",
				"- Open tasks, blockers, and next steps",
				"- User preferences, conventions, and explicit requests",
				"- Technical details that might be needed to continue work",
				"",
				"Format:",
				"## Decisions",
				"- [decision] — why + context",
				"",
				"## Code Changes",
				"- [file]: [what changed] — [why]",
				"",
				"## Current State",
				"- What's done, what's in progress, what's blocked",
				"",
				"## User Preferences",
				"- Any explicit conventions, styles, or preferences stated",
				"",
				"Write in prose, not bullet-only. Be comprehensive but brief.",
			}, "\n"),
		},
	}
}

const compressionSystemPrompt = `You are compressing a long conversation into a compact summary.
This summary will REPLACE the full conversation history, so it must retain all
critical information. Preserve technical details, file paths, decisions, and
user preferences exactly. Do NOT omit anything that would be needed to resume
this conversation from where it left off.`

// --- Fragment Token Budget ---

type FragmentBudget struct {
	Name      string `json:"name"`
	MaxTokens int    `json:"max_tokens"`
}

type FragmentTokenLimiter struct {
	budgets []FragmentBudget
}

func NewFragmentTokenLimiter() *FragmentTokenLimiter {
	return &FragmentTokenLimiter{
		budgets: []FragmentBudget{
			{Name: "system_prompt", MaxTokens: 8000},
			{Name: "skill_instructions", MaxTokens: 3000},
			{Name: "memory_context", MaxTokens: 2000},
			{Name: "tool_descriptions", MaxTokens: 6000},
			{Name: "turn_history", MaxTokens: 16000},
			{Name: "user_message", MaxTokens: 4000},
			{Name: "tool_result", MaxTokens: 8000},
		},
	}
}

func (l *FragmentTokenLimiter) Check(name string, estimatedTokens int) error {
	for _, b := range l.budgets {
		if b.Name == name && estimatedTokens > b.MaxTokens {
			return fmt.Errorf("fragment %q exceeds budget: %d > %d tokens", name, estimatedTokens, b.MaxTokens)
		}
	}
	return nil
}

func (l *FragmentTokenLimiter) Budget(name string) int {
	for _, b := range l.budgets {
		if b.Name == name {
			return b.MaxTokens
		}
	}
	return 0
}

func (l *FragmentTokenLimiter) Budgets() []FragmentBudget {
	return append([]FragmentBudget(nil), l.budgets...)
}

func (l *FragmentTokenLimiter) ShouldTrim(name string, estimatedTokens int) bool {
	budget := l.Budget(name)
	return budget > 0 && estimatedTokens > budget
}

func BuildContextCompressTool(compressor *ContextCompressor) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "context_compress",
		Description: strings.Join([]string{
			"Check if the conversation context is approaching token limits and",
			"trigger automatic summarization of older messages.",
			"Use this when the conversation is long and you're running out of context window.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"estimated_tokens": map[string]any{
					"type":        "integer",
					"description": "Estimated current token count in the conversation.",
				},
				"max_tokens": map[string]any{
					"type":        "integer",
					"description": "Maximum context window size (default: model limit).",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				EstimatedTokens int `json:"estimated_tokens"`
				MaxTokens       int `json:"max_tokens"`
			}
			json.Unmarshal(args, &params)

			if params.EstimatedTokens <= 0 {
				return map[string]any{
					"should_compress": false,
					"reason":          "no token estimate provided",
				}, nil
			}

			should := compressor.ShouldCompress(params.EstimatedTokens, params.MaxTokens)
			prompt := compressor.BuildCompressionPrompt()

			return map[string]any{
				"should_compress":    should,
				"estimated_tokens":   params.EstimatedTokens,
				"threshold_pct":      compressor.ThresholdPct(),
				"compression_prompt": prompt,
				"compression_count":  compressor.compressedCount,
			}, nil
		},
	}
}

func BuildContextCompressConfigTool(compressor *ContextCompressor) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "context_compress_config",
		Description: strings.Join([]string{
			"View or update the context compression threshold.",
			"The threshold is the percentage of context window used that triggers compression.",
			"Default: 50%%. Lower values compress earlier; higher values compress later.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"threshold_pct": map[string]any{
					"type":        "number",
					"description": "New threshold percentage (1-100). Omit to view current.",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				ThresholdPct float64 `json:"threshold_pct"`
			}
			json.Unmarshal(args, &params)

			if params.ThresholdPct > 0 {
				if params.ThresholdPct < 1 || params.ThresholdPct > 100 {
					return nil, fmt.Errorf("threshold must be between 1 and 100")
				}
				compressor.SetThreshold(params.ThresholdPct)
			}

			return map[string]any{
				"threshold_pct": compressor.ThresholdPct(),
				"compressions":  compressor.compressedCount,
			}, nil
		},
	}
}

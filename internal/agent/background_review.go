package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
)

const memoryReviewPrompt = `Review the conversation above and consider saving to memory if appropriate.

Focus on:
1. Has the user revealed things about themselves — their persona, desires, preferences, or personal details worth remembering?
2. Has the user expressed expectations about how you should behave, their work style, or ways they want you to operate?

If something stands out, save it using the memory tool. If nothing is worth saving, just say "Nothing to save." and stop.`

const skillReviewPrompt = `Review the conversation above and update the skill library. Be ACTIVE — most sessions produce at least one skill update, even if small.

Signals to look for (any one of these warrants action):
  • User corrected your style, tone, format, legibility, or verbosity. Frustration signals like "stop doing X", "this is too verbose", "don't format like this", "why are you explaining", "you always do Y and I hate it", or an explicit "remember this" are FIRST-CLASS skill signals.
  • User corrected your workflow, approach, or sequence of steps.
  • Non-trivial technique, fix, workaround, debugging path, or tool-usage pattern emerged.
  • A skill that got loaded or consulted this session turned out to be wrong, missing a step, or outdated.

Do NOT capture:
  • Environment-dependent failures: missing binaries, "command not found", unconfigured credentials.
  • Negative claims about tools or features.
  • Session-specific transient errors that resolved before the conversation ended.
  • One-off task narratives.

If genuinely nothing stands out, say "Nothing to save." and stop.`

const combinedReviewPrompt = `Review the conversation above and update two things:

**Memory**: who the user is. Did the user reveal persona, desires, preferences, personal details, or expectations about how you should behave?

**Skills**: how to do this class of task. Be ACTIVE — most sessions produce at least one skill update.

Signals that warrant a skill update:
  • User corrected your style, tone, format, or approach.
  • Non-trivial technique, fix, workaround, or debugging path emerged.
  • A skill that was loaded turned out wrong, missing, or outdated.

Do NOT capture:
  • Environment-dependent failures.
  • Negative claims about tools or features.
  • Session-specific transient errors.
  • One-off task narratives.

Act on whichever dimension has real signal. If genuinely nothing stands out on either, say "Nothing to save." and stop.`

type ReviewType string

const (
	ReviewMemory   ReviewType = "memory"
	ReviewSkill    ReviewType = "skill"
	ReviewCombined ReviewType = "combined"
)

type ReviewAction struct {
	Type    string `json:"type"`
	Target  string `json:"target"`
	Message string `json:"message"`
	Success bool   `json:"success"`
}

type ReviewResult struct {
	Actions []ReviewAction
	Summary string
	Error   error
}

type LLMReviewer interface {
	Review(ctx context.Context, systemPrompt, userPrompt string, conversation []map[string]any) (string, error)
}

// funcReviewer adapts a plain function to the LLMReviewer interface.
type funcReviewer struct {
	fn func(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

func (f *funcReviewer) Review(ctx context.Context, systemPrompt, userPrompt string, _ []map[string]any) (string, error) {
	return f.fn(ctx, systemPrompt, userPrompt)
}

// NewBackgroundReviewerFromFunc creates a BackgroundReviewer from a plain LLM call function.
// This avoids the caller needing to implement the full LLMReviewer interface.
func NewBackgroundReviewerFromFunc(fn func(ctx context.Context, systemPrompt, userPrompt string) (string, error), reviewType ReviewType) *BackgroundReviewer {
	return NewBackgroundReviewer(&funcReviewer{fn: fn}, reviewType)
}

type BackgroundReviewer struct {
	mu          sync.Mutex
	reviewer    LLMReviewer
	reviewType  ReviewType
	lastReview  time.Time
	minInterval time.Duration
	results     []ReviewResult
}

func NewBackgroundReviewer(reviewer LLMReviewer, reviewType ReviewType) *BackgroundReviewer {
	return &BackgroundReviewer{
		reviewer:    reviewer,
		reviewType:  reviewType,
		minInterval: 5 * time.Minute,
	}
}

func (r *BackgroundReviewer) SetMinInterval(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minInterval = d
}

func (r *BackgroundReviewer) reviewPrompt() string {
	switch r.reviewType {
	case ReviewMemory:
		return memoryReviewPrompt
	case ReviewSkill:
		return skillReviewPrompt
	case ReviewCombined:
		return combinedReviewPrompt
	default:
		return combinedReviewPrompt
	}
}

func (r *BackgroundReviewer) SpawnReview(conversation []map[string]any) {
	r.mu.Lock()
	elapsed := time.Since(r.lastReview)
	if elapsed < r.minInterval {
		r.mu.Unlock()
		return
	}
	r.lastReview = time.Now()
	r.mu.Unlock()

	safego.SafeGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		response, err := r.reviewer.Review(ctx, r.reviewPrompt(), "", conversation)

		result := ReviewResult{
			Summary: response,
			Error:   err,
		}

		if err == nil && response != "" && !strings.Contains(strings.ToLower(response), "nothing to save") {
			result.Actions = r.parseActions(response)
		}

		r.mu.Lock()
		r.results = append(r.results, result)
		r.mu.Unlock()
	}, nil)
}

func (r *BackgroundReviewer) parseActions(response string) []ReviewAction {
	var actions []ReviewAction

	if strings.Contains(response, "Memory updated") || strings.Contains(response, "memory updated") {
		actions = append(actions, ReviewAction{
			Type:    "memory",
			Target:  "memory",
			Message: "Memory updated",
			Success: true,
		})
	}

	if strings.Contains(response, "Skill updated") || strings.Contains(response, "skill updated") ||
		strings.Contains(response, "SKILL.md") {
		actions = append(actions, ReviewAction{
			Type:    "skill",
			Target:  "skill",
			Message: "Skill updated",
			Success: true,
		})
	}

	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			var action ReviewAction
			if err := json.Unmarshal([]byte(line), &action); err == nil && action.Message != "" {
				actions = append(actions, action)
			}
		}
	}

	return actions
}

func (r *BackgroundReviewer) RecentResults(limit int) []ReviewResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if limit <= 0 || limit > len(r.results) {
		limit = len(r.results)
	}
	start := len(r.results) - limit
	if start < 0 {
		start = 0
	}
	results := make([]ReviewResult, limit)
	copy(results, r.results[start:])
	return results
}

func (r *BackgroundReviewer) SummarizeActions(conversation []map[string]any) []string {
	var actions []string

	for _, msg := range conversation {
		role, _ := msg["role"].(string)
		if role != "tool" {
			continue
		}

		content, _ := msg["content"].(string)
		if content == "" {
			continue
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(content), &data); err != nil {
			continue
		}

		success, _ := data["success"].(bool)
		if !success {
			continue
		}

		message, _ := data["message"].(string)
		target, _ := data["target"].(string)

		msgLower := strings.ToLower(message)
		switch {
		case strings.Contains(msgLower, "created"):
			actions = append(actions, message)
		case strings.Contains(msgLower, "updated"):
			actions = append(actions, message)
		case strings.Contains(msgLower, "added") || (target != "" && strings.Contains(msgLower, "add")):
			label := target
			switch target {
			case "memory":
				label = "Memory"
			case "user":
				label = "User profile"
			}
			if label != "" {
				actions = append(actions, label+" updated")
			}
		}
	}

	return actions
}

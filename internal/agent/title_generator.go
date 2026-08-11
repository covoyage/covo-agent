package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

const titlePrompt = "Generate a short, descriptive title (3-7 words) for a conversation that starts with the following exchange. The title should capture the main topic or intent. Return ONLY the title text, nothing else. No quotes, no punctuation at the end, no prefixes."

const summaryPrompt = "Summarize the key outcomes, decisions, and artifacts from this session in 2-3 sentences. Focus on what was accomplished, built, or decided. Be concise and factual."

func GenerateTitle(ctx context.Context, provider agentcore.Provider, model string, userMsg, assistantMsg string) (string, error) {
	userSnippet := userMsg
	if len(userSnippet) > 500 {
		userSnippet = userSnippet[:500]
	}
	assistantSnippet := assistantMsg
	if len(assistantSnippet) > 500 {
		assistantSnippet = assistantSnippet[:500]
	}

	req := &agentcore.ProviderRequest{
		Model: model,
		Messages: []agentcore.Message{
			{Role: "system", Content: titlePrompt},
			{Role: "user", Content: fmt.Sprintf("User: %s\n\nAssistant: %s", userSnippet, assistantSnippet)},
		},
		MaxTokens:   500,
		Temperature: 0.3,
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("title generation: %w", err)
	}

	title := strings.TrimSpace(resp.Content)
	title = strings.Trim(title, `"'`)
	title = strings.TrimPrefix(title, "Title: ")
	title = strings.TrimPrefix(title, "title: ")

	if len(title) > 80 {
		title = title[:77] + "..."
	}

	if title == "" {
		return "", fmt.Errorf("empty title generated")
	}

	return title, nil
}

func (ca *CovoAgent) MaybeAutoTitle(ctx context.Context, userMsg, assistantMsg string) {
	if ca.sessionMgr == nil {
		return
	}

	sessionID := ca.sessionMgr.CurrentID()
	if sessionID == "" || userMsg == "" {
		return
	}

	// Set a quick fallback title from first user message immediately
	fallback := truncateTitle(userMsg)
	if err := ca.sessionMgr.RenameSession(ctx, sessionID, fallback); err != nil {
		slog.Debug("fallback auto-title set failed", "error", err)
	}

	// Also try LLM-generated title in background (overwrites fallback when ready)
	if assistantMsg != "" {
		safego.SafeGo(func() {
			titleCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			var title string
			var err error
			if ca.auxClient != nil {
				// Use auxiliary client (routes to configured title model/provider)
				userSnippet := userMsg
				if len(userSnippet) > 500 {
					userSnippet = userSnippet[:500]
				}
				assistantSnippet := assistantMsg
				if len(assistantSnippet) > 500 {
					assistantSnippet = assistantSnippet[:500]
				}
				title, err = ca.auxClient.Complete(titleCtx, TaskTitle, titlePrompt,
					fmt.Sprintf("User: %s\n\nAssistant: %s", userSnippet, assistantSnippet),
					500, 0.3)
			} else {
				// Fallback: use main provider directly
				provider := ca.cfg.Provider
				if provider == nil {
					return
				}
				title, err = GenerateTitle(titleCtx, provider, ca.model, userMsg, assistantMsg)
			}
			if err != nil {
				slog.Warn("auto-title generation failed", "error", err)
				return
			}

			// Clean up the title (same post-processing as GenerateTitle)
			title = strings.TrimSpace(title)
			title = strings.Trim(title, `"'`)
			title = strings.TrimPrefix(title, "Title: ")
			title = strings.TrimPrefix(title, "title: ")
			if len(title) > 80 {
				title = title[:77] + "..."
			}
			if title == "" {
				slog.Warn("auto-title: empty title generated")
				return
			}

			if err := ca.sessionMgr.RenameSession(context.Background(), sessionID, title); err != nil {
				slog.Debug("auto-title LLM set failed", "error", err)
			}
		}, nil)
	}
}

func (ca *CovoAgent) MaybeAutoSummary(ctx context.Context, userMsg, assistantMsg string) {
	if ca.sessionMgr == nil {
		return
	}
	sessionID := ca.sessionMgr.CurrentID()
	if sessionID == "" {
		return
	}

	safego.SafeGo(func() {
		summaryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var summary string
		var err error
		if ca.auxClient != nil {
			// Use auxiliary client (routes to configured title model/provider)
			userSnippet := userMsg
			if len(userSnippet) > 1000 {
				userSnippet = userSnippet[:1000]
			}
			assistantSnippet := assistantMsg
			if len(assistantSnippet) > 2000 {
				assistantSnippet = assistantSnippet[:2000]
			}
			summary, err = ca.auxClient.Complete(summaryCtx, TaskTitle, summaryPrompt,
				fmt.Sprintf("Conversation:\nUser: %s\n\nAssistant: %s", userSnippet, assistantSnippet),
				300, 0.3)
		} else {
			// Fallback: use main provider directly
			provider := ca.cfg.Provider
			if provider == nil {
				return
			}
			summary, err = GenerateSummary(summaryCtx, provider, ca.model, userMsg, assistantMsg)
		}
		if err != nil {
			slog.Debug("auto-summary generation failed", "error", err)
			return
		}

		// Clean up the summary
		summary = strings.TrimSpace(summary)
		if summary == "" {
			return
		}
		if len(summary) > 300 {
			summary = summary[:297] + "..."
		}

		if err := ca.sessionMgr.SetSummary(context.Background(), sessionID, summary); err != nil {
			slog.Debug("auto-summary persist failed", "error", err)
		}
	}, nil)
}

func GenerateSummary(ctx context.Context, provider agentcore.Provider, model string, userMsg, assistantMsg string) (string, error) {
	userSnippet := userMsg
	if len(userSnippet) > 1000 {
		userSnippet = userSnippet[:1000]
	}
	assistantSnippet := assistantMsg
	if len(assistantSnippet) > 2000 {
		assistantSnippet = assistantSnippet[:2000]
	}

	req := &agentcore.ProviderRequest{
		Model: model,
		Messages: []agentcore.Message{
			{Role: "system", Content: summaryPrompt},
			{Role: "user", Content: fmt.Sprintf("Conversation:\nUser: %s\n\nAssistant: %s", userSnippet, assistantSnippet)},
		},
		MaxTokens:   300,
		Temperature: 0.3,
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("summary generation: %w", err)
	}

	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return "", fmt.Errorf("empty summary generated")
	}
	if len(summary) > 300 {
		summary = summary[:297] + "..."
	}
	return summary, nil
}

func truncateTitle(msg string) string {
	// Strip newlines and truncate to a reasonable title length
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 40 {
		msg = msg[:37] + "..."
	}
	return msg
}

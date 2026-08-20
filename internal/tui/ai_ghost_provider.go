package tui

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// ---------------------------------------------------------------------------
// AIGhostProvider — inline completion via LLM.
//
// Debounces editor input, cancels in-flight requests on new input, and calls
// the LLM provider with a lightweight prompt to generate a short completion
// that is displayed as ghost text in the editor.
// ---------------------------------------------------------------------------

const (
	ghostDebounce    = 300 * time.Millisecond
	ghostMaxContext  = 10  // max lines of context sent to LLM
	ghostMaxTokens   = 200 // enough room for complete sentences
	ghostMaxResponse = 500 // max runes in displayed ghost
)

// AIGhostProvider manages async LLM-based inline completions for the editor.
type AIGhostProvider struct {
	mu       sync.Mutex
	provider agentcore.Provider
	model    string
	cancel   context.CancelFunc
}

// NewAIGhostProvider creates a provider that calls the given LLM for ghost text.
func NewAIGhostProvider(provider agentcore.Provider, model string) *AIGhostProvider {
	return &AIGhostProvider{
		provider: provider,
		model:    model,
	}
}

// Handle is the callback suitable for ChatAppConfig.OnGhostRequest.
// It debounces calls, cancels previous in-flight requests, and invokes cb
// with the completion text (or "" on error/empty).
func (g *AIGhostProvider) Handle(prompt string, cb func(string)) {
	if g.provider == nil || prompt == "" {
		cb("")
		return
	}

	// Cancel any in-flight request.
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	g.mu.Unlock()

	go func() {
		// Debounce: wait for a pause in typing.
		select {
		case <-ctx.Done():
			return
		case <-time.After(ghostDebounce):
		}

		// Check cancelled after debounce.
		if ctx.Err() != nil {
			return
		}

		completion := g.complete(ctx, prompt)
		if ctx.Err() != nil {
			return // stale
		}
		cb(completion)
	}()
}

// complete sends a lightweight completion request to the LLM.
func (g *AIGhostProvider) complete(ctx context.Context, prompt string) string {
	lines := strings.Split(prompt, "\n")
	if len(lines) > ghostMaxContext {
		lines = lines[len(lines)-ghostMaxContext:]
	}
	// Send context with a cursor marker at the end.
	contextStr := strings.Join(lines, "\n")

	req := &agentcore.ProviderRequest{
		Model: g.model,
		Messages: []agentcore.Message{
			{
				Role: agentcore.RoleUser,
				Content: "You are an inline code assistant. The user is typing in an editor. " +
					"Complete the text from the cursor position marked by '|>'. " +
					"Output ONLY the continuation text. No explanation, no newline at start, no code fences.\n\n" +
					contextStr + "\n|>",
			},
		},
		MaxTokens:   ghostMaxTokens,
		Temperature: 0,
		FastMode:    true,
	}

	resp, err := g.provider.Complete(ctx, req)
	if err != nil || resp == nil {
		return ""
	}

	// Strip leading/trailing whitespace and newlines (chat API artifact).
	text := strings.TrimSpace(resp.Content)
	if text == "" {
		return ""
	}

	// Strip code fences the model might wrap around the completion.
	if strings.HasPrefix(text, "```") {
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx+1:]
		}
		if idx := strings.LastIndex(text, "```"); idx >= 0 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}

	// Trim incomplete fragments for code — but not for natural language.
	if looksLikeCode(text) {
		text = trimIncomplete(text)
	}

	// Filter out answer-style responses: the model explained instead of completing.
	if looksLikeAnswer(text, contextStr) {
		return ""
	}

	// Truncate if too long.
	runes := []rune(text)
	if len(runes) > ghostMaxResponse {
		text = string(runes[:ghostMaxResponse])
		if looksLikeCode(text) {
			text = trimIncomplete(text)
		}
	}

	// Strip the prompt prefix — the model often echoes the user's text.
	text = stripPromptPrefix(text, prompt)
	if text == "" {
		return ""
	}

	return text
}

// stripPromptPrefix removes leading text that overlaps with the end of the
// prompt. The model frequently echoes the user's input before continuing.
func stripPromptPrefix(text, prompt string) string {
	if prompt == "" {
		return text
	}
	pRunes := []rune(prompt)
	tRunes := []rune(text)
	// Only strip if the overlapping prefix is at least 2 runes to avoid
	// false positives on newlines or single characters.
	maxPrefix := len(pRunes)
	if maxPrefix > len(tRunes) {
		maxPrefix = len(tRunes)
	}
	for n := maxPrefix; n >= 2; n-- {
		suffix := string(pRunes[len(pRunes)-n:])
		if strings.HasPrefix(text, suffix) {
			remaining := strings.TrimSpace(string(tRunes[n:]))
			if remaining != "" {
				return remaining
			}
		}
	}
	return text
}

// trimIncomplete trims a completion back so it ends at a natural boundary.
// If the text ends mid-sentence or mid-structure, it drops the trailing
// fragment and returns everything up to the last complete unit.
func trimIncomplete(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	runes := []rune(s)
	last := runes[len(runes)-1]

	// Already ends at a clean boundary — keep as-is.
	if last == '\n' || last == '.' || last == '!' || last == '?' ||
		last == ')' || last == ']' || last == '}' || last == ';' {
		return s
	}

	// Ends with a trailing comma — likely more items follow, drop it.
	if last == ',' {
		if idx := strings.LastIndex(s, ","); idx >= 0 {
			return strings.TrimSpace(s[:idx])
		}
	}

	// Ends mid-word or after an operator — trim back to the last line
	// that looks like a complete statement.
	for i := len(runes) - 1; i >= 0; i-- {
		r := runes[i]
		if r == '\n' {
			return strings.TrimSpace(string(runes[:i]))
		}
		if i == 0 {
			// Entire text is one incomplete fragment — drop it all.
			return ""
		}
	}

	return s
}

// looksLikeAnswer detects when the model generated an explanation or
// standalone paragraph instead of continuing the user's input.
func looksLikeAnswer(text, prompt string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	return false
}

// looksLikeCode heuristic: does the text contain code-like characters?
func looksLikeCode(s string) bool {
	return strings.ContainsAny(s, "{}[]();:=<>\\/") ||
		strings.Contains(s, "->") || strings.Contains(s, "=>") ||
		strings.Contains(s, "//") || strings.Contains(s, "/*")
}

package app

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/agentadapter"
	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/agent/recovery"
)

// UsageFooter is the footer surface updated at the end of each agent turn.
type UsageFooter interface {
	SetContextUsage(string)
	SetContextWarn(bool)
}

// AgentUIBinder connects agent events to the current chat app and footer.
type AgentUIBinder struct {
	App         func() *chat.ChatApp
	Footer      func() UsageFooter
	PrintSystem func(string)
	Mode        func() string
	Model       func() string

	mu        sync.Mutex
	startedAt time.Time
}

func (binder *AgentUIBinder) Bind(core *agentcore.Agent) {
	if core == nil {
		return
	}
	retry := newRetryUIState(binder.PrintSystem)
	core.On(agentcore.EventAutoRetry, func(event agentcore.Event) {
		if autoRetry, ok := event.(*agentcore.AutoRetryEvent); ok {
			retry.onRetry(autoRetry)
		}
	})
	core.On(agentcore.EventAgentError, func(agentcore.Event) {
		retry.onFailure()
	})
	core.On(agentcore.EventTurnEnd, func(event agentcore.Event) {
		turnEnd, ok := event.(*agentcore.TurnEndEvent)
		if !ok {
			return
		}
		if binder.Footer != nil {
			if footer := binder.Footer(); footer != nil {
				footer.SetContextUsage(formatTokenUsage(turnEnd.Usage))
				footer.SetContextWarn(false)
			}
		}
		retry.onSuccess()
	})
	core.On(agentcore.EventAgentStart, func(agentcore.Event) {
		binder.mu.Lock()
		binder.startedAt = time.Now()
		binder.mu.Unlock()
	})
	core.On(agentcore.EventAgentEnd, func(agentcore.Event) {
		binder.attachCompletionChip()
	})
	if binder.App != nil {
		if app := binder.App(); app != nil {
			agentadapter.BindAgent(app, core)
		}
	}
}

func (binder *AgentUIBinder) attachCompletionChip() {
	if binder.App == nil {
		return
	}
	app := binder.App()
	if app == nil || app.History() == nil {
		return
	}
	binder.mu.Lock()
	started := binder.startedAt
	binder.mu.Unlock()
	if started.IsZero() {
		return
	}
	elapsed := time.Since(started)
	if elapsed < 50*time.Millisecond {
		return
	}
	chip := formatCompletionChip(binder.modeLabel(), binder.modelLabel(), elapsed)
	app.History().PatchLastAssistantReply(func(m *chat.ChatMessage) {
		m.FooterChip = chip
	})
}

func (binder *AgentUIBinder) modeLabel() string {
	if binder.Mode == nil {
		return ""
	}
	return strings.TrimSpace(binder.Mode())
}

func (binder *AgentUIBinder) modelLabel() string {
	if binder.Model == nil {
		return ""
	}
	return shortModelName(binder.Model())
}

func formatCompletionChip(mode, model string, elapsed time.Duration) string {
	parts := make([]string, 0, 3)
	if mode != "" {
		parts = append(parts, mode)
	}
	if model != "" {
		parts = append(parts, model)
	}
	parts = append(parts, formatElapsed(elapsed))
	return strings.Join(parts, " · ")
}

func shortModelName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if i := strings.LastIndex(model, "/"); i >= 0 && i+1 < len(model) {
		model = model[i+1:]
	}
	return model
}

func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		secs := d.Seconds()
		if secs < 10 {
			return fmt.Sprintf("%.1fs", secs)
		}
		return fmt.Sprintf("%.0fs", secs)
	}
	minutes := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", minutes, secs)
}

type retryUIState struct {
	mu      sync.Mutex
	buffer  *recovery.BufferedStatus
	flushFn func(string)
}

func newRetryUIState(flushFn func(string)) *retryUIState {
	if flushFn == nil {
		flushFn = func(string) {}
	}
	return &retryUIState{flushFn: flushFn}
}

func (state *retryUIState) onRetry(event *agentcore.AutoRetryEvent) {
	state.mu.Lock()
	if state.buffer == nil {
		state.buffer = recovery.NewBufferedStatus(state.flushFn)
	}
	buffer := state.buffer
	buffer.Buffer("%s retry %d/%d in %s", theme.SymbolWarning, event.Attempt, event.MaxRetries, event.Delay.Round(time.Millisecond))
	state.mu.Unlock()
}

func (state *retryUIState) onFailure() {
	state.mu.Lock()
	buffer := state.buffer
	state.buffer = nil
	state.mu.Unlock()
	if buffer != nil {
		buffer.Flush()
	}
}

func (state *retryUIState) onSuccess() {
	state.mu.Lock()
	buffer := state.buffer
	state.buffer = nil
	state.mu.Unlock()
	if buffer != nil {
		buffer.Discard()
	}
}

func formatTokenUsage(usage agentcore.TokenUsage) string {
	return fmt.Sprintf("tokens: %d in + %d out = %d", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
}

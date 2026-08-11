package app

import (
	"fmt"
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
	if binder.App != nil {
		if app := binder.App(); app != nil {
			agentadapter.BindAgent(app, core)
		}
	}
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

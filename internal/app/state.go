// Package app owns process-level runtime state and application composition.
package app

import (
	"sync/atomic"

	"github.com/covoyage/covo-agent/internal/promptqueue"
	"github.com/covoyage/covo-agent/internal/tui"
)

const (
	DefaultReasoningEffort = "medium"
	DefaultBusyInputMode   = "block"
	DefaultToolProfile     = "full"
)

// RuntimeState contains the concurrent, process-level settings shared by the
// CLI, agent callbacks, and TUI event loop.
type RuntimeState struct {
	sessionYolo   atomic.Bool
	fastMode      atomic.Bool
	footerEnabled atomic.Bool

	reasoningEffort atomic.Pointer[string]
	personality     atomic.Pointer[string]
	busyInputMode   atomic.Pointer[string]
	pendingInput    atomic.Pointer[string]
	activeProfile   atomic.Pointer[string]
	ui              atomic.Pointer[tui.UIBus]
	promptQueue     *promptqueue.Queue
}

func NewRuntimeState() *RuntimeState {
	state := &RuntimeState{}
	state.storeString(&state.reasoningEffort, DefaultReasoningEffort)
	state.storeString(&state.personality, "")
	state.storeString(&state.busyInputMode, DefaultBusyInputMode)
	state.storeString(&state.pendingInput, "")
	state.storeString(&state.activeProfile, DefaultToolProfile)
	state.promptQueue = promptqueue.New(100)
	return state
}

func (state *RuntimeState) SessionYolo() bool { return state.sessionYolo.Load() }
func (state *RuntimeState) SetSessionYolo(enabled bool) {
	state.sessionYolo.Store(enabled)
}
func (state *RuntimeState) ToggleSessionYolo() bool {
	return toggleBool(&state.sessionYolo)
}

func (state *RuntimeState) FastMode() bool { return state.fastMode.Load() }
func (state *RuntimeState) SetFastMode(enabled bool) {
	state.fastMode.Store(enabled)
}
func (state *RuntimeState) ToggleFastMode() bool {
	return toggleBool(&state.fastMode)
}

func (state *RuntimeState) FooterEnabled() bool { return state.footerEnabled.Load() }
func (state *RuntimeState) SetFooterEnabled(enabled bool) {
	state.footerEnabled.Store(enabled)
}
func (state *RuntimeState) ToggleFooterEnabled() bool {
	return toggleBool(&state.footerEnabled)
}

func (state *RuntimeState) ReasoningEffort() string {
	return loadString(&state.reasoningEffort, DefaultReasoningEffort)
}
func (state *RuntimeState) SetReasoningEffort(effort string) {
	state.storeString(&state.reasoningEffort, effort)
}

func (state *RuntimeState) Personality() string {
	return loadString(&state.personality, "")
}
func (state *RuntimeState) SetPersonality(personality string) {
	state.storeString(&state.personality, personality)
}

func (state *RuntimeState) BusyInputMode() string {
	return loadString(&state.busyInputMode, DefaultBusyInputMode)
}
func (state *RuntimeState) SetBusyInputMode(mode string) {
	state.storeString(&state.busyInputMode, mode)
}

func (state *RuntimeState) PendingInput() string {
	return loadString(&state.pendingInput, "")
}
func (state *RuntimeState) SetPendingInput(input string) {
	// Push to the prompt queue for multi-prompt support
	if state.promptQueue != nil && input != "" {
		state.promptQueue.Push(input)
	}
}

// TakePendingInput atomically returns and clears the queued input.
func (state *RuntimeState) TakePendingInput() string {
	// First try the prompt queue
	if state.promptQueue != nil && !state.promptQueue.IsEmpty() {
		entry, ok := state.promptQueue.Pop()
		if ok {
			return entry.Text
		}
	}
	// Fall back to the legacy pending input
	empty := ""
	value := state.pendingInput.Swap(&empty)
	if value == nil {
		return ""
	}
	return *value
}

// PromptQueue returns the prompt queue for direct access.
func (state *RuntimeState) PromptQueue() *promptqueue.Queue {
	return state.promptQueue
}

// HasQueuedPrompts returns true if there are prompts waiting in the queue.
func (state *RuntimeState) HasQueuedPrompts() bool {
	return state.promptQueue != nil && !state.promptQueue.IsEmpty()
}

func (state *RuntimeState) ActiveProfile() string {
	return loadString(&state.activeProfile, DefaultToolProfile)
}
func (state *RuntimeState) SetActiveProfile(profile string) {
	state.storeString(&state.activeProfile, profile)
}

func (state *RuntimeState) UI() *tui.UIBus { return state.ui.Load() }
func (state *RuntimeState) SetUI(ui *tui.UIBus) {
	state.ui.Store(ui)
}

func (*RuntimeState) storeString(target *atomic.Pointer[string], value string) {
	stored := value
	target.Store(&stored)
}

func loadString(source *atomic.Pointer[string], fallback string) string {
	if value := source.Load(); value != nil {
		return *value
	}
	return fallback
}

func toggleBool(value *atomic.Bool) bool {
	for {
		current := value.Load()
		if value.CompareAndSwap(current, !current) {
			return !current
		}
	}
}

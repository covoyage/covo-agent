// Package app owns process-level runtime state and application composition.
package app

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/covoyage/covo-agent/internal/promptqueue"
	"github.com/covoyage/covo-agent/internal/tui"
)

// PromptQueuePersister stores a session-scoped prompt queue.
type PromptQueuePersister interface {
	SavePromptQueue(ctx context.Context, sessionID string, entries []promptqueue.Entry) error
	LoadPromptQueue(ctx context.Context, sessionID string) ([]promptqueue.Entry, error)
}

const (
	DefaultReasoningEffort = "medium"
	DefaultBusyInputMode   = "queue"
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

	queueMu        sync.Mutex
	queueSessionID string
	queuePersister PromptQueuePersister
}

func NewRuntimeState() *RuntimeState {
	state := &RuntimeState{}
	state.storeString(&state.reasoningEffort, DefaultReasoningEffort)
	state.storeString(&state.personality, "")
	state.storeString(&state.busyInputMode, DefaultBusyInputMode)
	state.storeString(&state.pendingInput, "")
	state.storeString(&state.activeProfile, DefaultToolProfile)
	state.promptQueue = promptqueue.New(100)
	state.promptQueue.SetOnChange(func() {
		state.PersistPromptQueue(context.Background())
	})
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

// EnqueueResult reports what happened when a prompt was offered to the queue.
type EnqueueResult struct {
	OK     bool
	Merged bool
	Full   bool
	Entry  promptqueue.Entry
}

func (state *RuntimeState) SetPendingInput(input string) {
	state.EnqueuePrompt(input)
}

// EnqueuePrompt appends text to the prompt queue. It never drops older
// entries; a full queue is rejected (Full=true, OK=false).
func (state *RuntimeState) EnqueuePrompt(input string) EnqueueResult {
	if input == "" || state.promptQueue == nil {
		return EnqueueResult{}
	}
	entry, ok := state.promptQueue.TryPush(input)
	if !ok {
		return EnqueueResult{Full: true}
	}
	return EnqueueResult{OK: true, Entry: entry}
}

// EnqueuePromptFront inserts text at the head of the queue (interrupt / send-now).
func (state *RuntimeState) EnqueuePromptFront(input string) EnqueueResult {
	if input == "" || state.promptQueue == nil {
		return EnqueueResult{}
	}
	entry, ok := state.promptQueue.TryPushFront(input)
	if !ok {
		return EnqueueResult{Full: true}
	}
	return EnqueueResult{OK: true, Entry: entry}
}

// EnqueueMerged appends text, combining it with the last short entry when
// both sides are under the merge threshold.
func (state *RuntimeState) EnqueueMerged(input string) EnqueueResult {
	if input == "" || state.promptQueue == nil {
		return EnqueueResult{}
	}
	if state.promptQueue.CanMergeFollower(input, 200) {
		state.promptQueue.TryMerge(input, 200)
		return EnqueueResult{OK: true, Merged: true}
	}
	entry, ok := state.promptQueue.TryPush(input)
	if !ok {
		return EnqueueResult{Full: true}
	}
	return EnqueueResult{OK: true, Entry: entry}
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

// ResetPromptQueue drops all queued prompts. Used when starting a fresh
// conversation so a leftover queue cannot fire against the new transcript.
func (state *RuntimeState) ResetPromptQueue() {
	if state.promptQueue != nil {
		state.promptQueue.Clear()
	}
	empty := ""
	state.pendingInput.Store(&empty)
}

// BindPromptQueueSession attaches persistence to sessionID and replaces the
// in-memory queue with the stored entries (Replace does not re-save).
func (state *RuntimeState) BindPromptQueueSession(ctx context.Context, persister PromptQueuePersister, sessionID string) {
	state.queueMu.Lock()
	state.queuePersister = persister
	state.queueSessionID = sessionID
	state.queueMu.Unlock()

	var entries []promptqueue.Entry
	if persister != nil && sessionID != "" {
		loaded, err := persister.LoadPromptQueue(ctx, sessionID)
		if err == nil {
			entries = loaded
		}
	}
	if state.promptQueue != nil {
		state.promptQueue.Replace(entries)
	}
}

// SwitchPromptQueue saves the current queue, then loads the destination session.
func (state *RuntimeState) SwitchPromptQueue(ctx context.Context, persister PromptQueuePersister, toID string) {
	state.PersistPromptQueue(ctx)
	if persister == nil {
		state.queueMu.Lock()
		persister = state.queuePersister
		state.queueMu.Unlock()
	}
	state.BindPromptQueueSession(ctx, persister, toID)
}

// PersistPromptQueue writes the in-memory queue for the bound session.
func (state *RuntimeState) PersistPromptQueue(ctx context.Context) {
	state.queueMu.Lock()
	persister := state.queuePersister
	sessionID := state.queueSessionID
	state.queueMu.Unlock()
	if persister == nil || sessionID == "" || state.promptQueue == nil {
		return
	}
	entries := state.promptQueue.All()
	_ = persister.SavePromptQueue(ctx, sessionID, entries)
}

// PromptQueueSessionID returns the session currently bound to the prompt queue.
func (state *RuntimeState) PromptQueueSessionID() string {
	state.queueMu.Lock()
	defer state.queueMu.Unlock()
	return state.queueSessionID
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

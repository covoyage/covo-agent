package app

import (
	"context"
	"sync"
	"testing"

	"github.com/covoyage/covo-agent/internal/promptqueue"
	"github.com/covoyage/covo-agent/internal/tui"
)

func TestRuntimeStateDefaults(t *testing.T) {
	state := NewRuntimeState()
	if state.SessionYolo() || state.FastMode() || state.FooterEnabled() {
		t.Fatal("boolean runtime defaults must be disabled")
	}
	if got := state.ReasoningEffort(); got != DefaultReasoningEffort {
		t.Fatalf("ReasoningEffort() = %q", got)
	}
	if got := state.BusyInputMode(); got != DefaultBusyInputMode {
		t.Fatalf("BusyInputMode() = %q", got)
	}
	if got := state.ActiveProfile(); got != DefaultToolProfile {
		t.Fatalf("ActiveProfile() = %q", got)
	}
	if state.Personality() != "" || state.PendingInput() != "" || state.UI() != nil {
		t.Fatal("optional runtime defaults must be empty")
	}
}

func TestRuntimeStateSettersAndPendingTake(t *testing.T) {
	state := NewRuntimeState()
	state.SetSessionYolo(true)
	state.SetFastMode(true)
	state.SetReasoningEffort("high")
	state.SetPersonality("reviewer")
	state.SetBusyInputMode("queue")
	state.EnqueuePrompt("next task")
	state.SetActiveProfile("coding")
	ui := tui.NewUIBus(nil)
	state.SetUI(ui)

	if !state.SessionYolo() || !state.FastMode() {
		t.Fatal("boolean setters did not persist")
	}
	if state.ReasoningEffort() != "high" || state.Personality() != "reviewer" || state.BusyInputMode() != "queue" || state.ActiveProfile() != "coding" {
		t.Fatal("string setters did not persist")
	}
	if state.UI() != ui {
		t.Fatal("UI pointer did not persist")
	}
	if got := state.TakePendingInput(); got != "next task" {
		t.Fatalf("TakePendingInput() = %q", got)
	}
	if got := state.TakePendingInput(); got != "" {
		t.Fatalf("second TakePendingInput() = %q", got)
	}
}

func TestRuntimeStateEnqueueRejectsWhenFull(t *testing.T) {
	state := NewRuntimeState()
	q := state.PromptQueue()
	for q.Len() < q.MaxSize() {
		if res := state.EnqueuePrompt("item"); !res.OK {
			t.Fatalf("expected enqueue ok at len %d", q.Len())
		}
	}
	res := state.EnqueuePrompt("overflow")
	if !res.Full || res.OK {
		t.Fatalf("expected full rejection, got %+v", res)
	}
	if q.Len() != q.MaxSize() {
		t.Fatalf("len=%d want %d", q.Len(), q.MaxSize())
	}
	front := state.EnqueuePromptFront("interrupt")
	if !front.Full {
		t.Fatal("front enqueue should also reject when full")
	}
}

func TestRuntimeStateResetPromptQueue(t *testing.T) {
	state := NewRuntimeState()
	state.EnqueuePrompt("one")
	state.EnqueuePrompt("two")
	state.EnqueuePrompt("legacy")
	state.ResetPromptQueue()
	if state.HasQueuedPrompts() {
		t.Fatal("queue should be empty after reset")
	}
	if got := state.TakePendingInput(); got != "" {
		t.Fatalf("pending leftover %q", got)
	}
}

type memQueueStore struct {
	mu   sync.Mutex
	data map[string][]promptqueue.Entry
}

func (m *memQueueStore) SavePromptQueue(_ context.Context, sessionID string, entries []promptqueue.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]promptqueue.Entry{}
	}
	cp := append([]promptqueue.Entry(nil), entries...)
	m.data[sessionID] = cp
	return nil
}

func (m *memQueueStore) LoadPromptQueue(_ context.Context, sessionID string) ([]promptqueue.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]promptqueue.Entry(nil), m.data[sessionID]...), nil
}

func TestRuntimeStateSwitchPromptQueue(t *testing.T) {
	store := &memQueueStore{data: map[string][]promptqueue.Entry{}}
	state := NewRuntimeState()
	state.BindPromptQueueSession(context.Background(), store, "sess-a")
	if res := state.EnqueuePrompt("from-a"); !res.OK {
		t.Fatal(res)
	}
	state.SwitchPromptQueue(context.Background(), store, "sess-b")
	if state.HasQueuedPrompts() {
		t.Fatal("sess-b should start empty")
	}
	if res := state.EnqueuePrompt("from-b"); !res.OK {
		t.Fatal(res)
	}
	state.SwitchPromptQueue(context.Background(), store, "sess-a")
	got := state.TakePendingInput()
	if got != "from-a" {
		t.Fatalf("restored %q", got)
	}
}

func TestRuntimeStateEnqueueMerged(t *testing.T) {
	state := NewRuntimeState()
	if res := state.EnqueuePrompt("add tests"); !res.OK {
		t.Fatal(res)
	}
	merged := state.EnqueueMerged("also lint")
	if !merged.OK || !merged.Merged {
		t.Fatalf("expected merge, got %+v", merged)
	}
	if state.PromptQueue().Len() != 1 {
		t.Fatalf("len=%d", state.PromptQueue().Len())
	}
	got := state.TakePendingInput()
	if got == "add tests" || got == "also lint" {
		t.Fatalf("expected combined text, got %q", got)
	}
}

func TestRuntimeStateConcurrentToggles(t *testing.T) {
	state := NewRuntimeState()
	const toggles = 101
	var waitGroup sync.WaitGroup
	waitGroup.Add(toggles)
	for range toggles {
		go func() {
			defer waitGroup.Done()
			state.ToggleFastMode()
		}()
	}
	waitGroup.Wait()
	if !state.FastMode() {
		t.Fatal("odd number of atomic toggles should leave fast mode enabled")
	}
}

func TestPersonalitiesReturnsIndependentMap(t *testing.T) {
	first := Personalities()
	if _, ok := first["reviewer"]; !ok {
		t.Fatal("reviewer personality is missing")
	}
	delete(first, "reviewer")
	if _, ok := Personalities()["reviewer"]; !ok {
		t.Fatal("caller mutation changed the personality catalog")
	}
}

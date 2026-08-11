package app

import (
	"sync"
	"testing"

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
	state.SetFooterEnabled(true)
	state.SetReasoningEffort("high")
	state.SetPersonality("reviewer")
	state.SetBusyInputMode("queue")
	state.SetPendingInput("next task")
	state.SetActiveProfile("coding")
	ui := tui.NewUIBus(nil)
	state.SetUI(ui)

	if !state.SessionYolo() || !state.FastMode() || !state.FooterEnabled() {
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

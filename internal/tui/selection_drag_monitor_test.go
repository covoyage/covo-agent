package tui

import (
	"testing"
	"time"

	"github.com/covoyage/covonaut/tui/core"
)

func TestSelectionDragMonitorStartsAtEdgeAndStopsOnRelease(t *testing.T) {
	controller := NewAutoScrollController(func(AutoScrollDirection, int) {})
	monitor := &SelectionDragMonitor{controller: controller}
	monitor.Update(core.WindowSizeMsg{Height: 40})
	monitor.Update(core.MouseMsg{Action: core.MousePress, Button: 0, Row: 20})
	monitor.Update(core.MouseMsg{Action: core.MouseMotion, Button: 0, Row: 39})
	if !monitor.Active() {
		t.Fatal("edge drag did not start auto-scroll")
	}
	monitor.Update(core.MouseMsg{Action: core.MouseRelease, Button: 0, Row: 39})
	if monitor.Active() {
		t.Fatal("mouse release did not stop auto-scroll")
	}
}

func TestSelectionDragMonitorIgnoresNonPrimaryButton(t *testing.T) {
	controller := NewAutoScrollController(func(AutoScrollDirection, int) {})
	monitor := &SelectionDragMonitor{controller: controller}
	monitor.Update(core.WindowSizeMsg{Height: 40})
	monitor.Update(core.MouseMsg{Action: core.MousePress, Button: 1, Row: 20})
	monitor.Update(core.MouseMsg{Action: core.MouseMotion, Button: 1, Row: 39})
	if monitor.Active() {
		t.Fatal("non-primary drag started auto-scroll")
	}
}

func TestAutoScrollControllerChangesDirection(t *testing.T) {
	events := make(chan AutoScrollDirection, 8)
	controller := NewAutoScrollController(func(direction AutoScrollDirection, _ int) {
		events <- direction
	})
	defer controller.Stop()

	controller.Update(39, 40)
	awaitDirection(t, events, AutoScrollDown)
	controller.Update(0, 40)
	awaitDirection(t, events, AutoScrollUp)
}

func awaitDirection(t *testing.T, events <-chan AutoScrollDirection, want AutoScrollDirection) {
	t.Helper()
	select {
	case got := <-events:
		if got != want {
			t.Fatalf("direction = %v, want %v", got, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for direction %v", want)
	}
}

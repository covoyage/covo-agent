package tui

import (
	"testing"
	"time"
)

func TestHotkeyRouterDispatchesCallbacks(t *testing.T) {
	called := ""
	router := NewHotkeyRouter(HotkeyRouterConfig{
		OpenSessions:       func() { called = "sessions" },
		OpenTodos:          func() { called = "todos" },
		OpenSkillCenter:    func() { called = "skills" },
		OpenCommandPalette: func() { called = "palette" },
		OpenHistorySearch:  func() { called = "search" },
		OpenModelPicker:    func() { called = "model" },
		OpenSessionTree:    func() { called = "tree" },
		OpenEditor:         func() { called = "editor" },
		OpenChangedFiles:   func() { called = "files" },
	})
	tests := []struct {
		data string
		want string
	}{
		{"\x0f", "sessions"},
		{"\x14", "todos"},
		{"\x0b", "palette"},
		{"\x13", "search"},
		{"\x10", "model"},
		{"\x19", "tree"},
		{"\x05", "editor"},
		{"\x07", "files"},
	}
	for _, test := range tests {
		called = ""
		router.HandleInput(test.data)
		if called != test.want {
			t.Errorf("HandleInput(%q) called %q, want %q", test.data, called, test.want)
		}
	}
}

func TestHotkeyRouterRequiresDoubleQuit(t *testing.T) {
	stopped := make(chan struct{}, 1)
	warnings := 0
	router := NewHotkeyRouter(HotkeyRouterConfig{
		Stop:        func() error { stopped <- struct{}{}; return nil },
		PrintSystem: func(string) { warnings++ },
		QuitWindow:  time.Second,
	})
	router.HandleInput("\x11")
	if warnings != 1 {
		t.Fatalf("first quit warnings = %d, want 1", warnings)
	}
	select {
	case <-stopped:
		t.Fatal("first quit stopped the app")
	default:
	}
	router.HandleInput("\x11")
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("second quit did not stop the app")
	}
}

package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestRegistry_RegisterEmit(t *testing.T) {
	r := NewRegistry()

	var events []Event
	r.RegisterFunc("test", func(event Event, hctx *HookContext) {
		events = append(events, event)
	})

	r.Emit(EventBeforeTurn, &HookContext{Turn: 1})
	r.Emit(EventAfterTurn, &HookContext{Turn: 1})

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0] != EventBeforeTurn || events[1] != EventAfterTurn {
		t.Errorf("unexpected events: %v", events)
	}
}

func TestRegistry_MultipleContributors(t *testing.T) {
	r := NewRegistry()

	var count int32
	r.RegisterFunc("a", func(event Event, hctx *HookContext) {
		atomic.AddInt32(&count, 1)
	})
	r.RegisterFunc("b", func(event Event, hctx *HookContext) {
		atomic.AddInt32(&count, 1)
	})

	r.Emit(EventBeforeTurn, &HookContext{})

	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()

	var count int32
	r.RegisterFunc("a", func(event Event, hctx *HookContext) {
		atomic.AddInt32(&count, 1)
	})
	r.RegisterFunc("b", func(event Event, hctx *HookContext) {
		atomic.AddInt32(&count, 1)
	})

	r.Unregister("a")
	r.Emit(EventBeforeTurn, &HookContext{})

	if count != 1 {
		t.Fatalf("expected count 1 after unregister, got %d", count)
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	r.RegisterFunc("alpha", func(Event, *HookContext) {})
	r.RegisterFunc("beta", func(Event, *HookContext) {})

	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
}

func TestRegistry_Count(t *testing.T) {
	r := NewRegistry()
	if r.Count() != 0 {
		t.Error("expected 0")
	}
	r.RegisterFunc("a", func(Event, *HookContext) {})
	if r.Count() != 1 {
		t.Error("expected 1")
	}
}

func TestRegistry_PanicRecovery(t *testing.T) {
	r := NewRegistry()

	r.RegisterFunc("panicker", func(event Event, hctx *HookContext) {
		panic("boom")
	})

	var called bool
	r.RegisterFunc("after-panic", func(event Event, hctx *HookContext) {
		called = true
	})

	// Should not crash, and subsequent contributor should still be called
	r.Emit(EventBeforeTurn, &HookContext{})

	if !called {
		t.Error("expected subsequent contributor to be called after panic")
	}
}

func TestContributorFunc(t *testing.T) {
	var called bool
	c := NewContributor("test", func(event Event, hctx *HookContext) {
		called = true
	})

	if c.Name() != "test" {
		t.Error("bad name")
	}
	c.OnEvent(EventBeforeTurn, &HookContext{})
	if !called {
		t.Error("expected callback called")
	}
}

func TestLoggingContributor(t *testing.T) {
	var logs []string
	c := NewLoggingContributor(func(format string, args ...any) {
		logs = append(logs, format)
	})

	c.OnEvent(EventBeforeTurn, &HookContext{Turn: 1})
	c.OnEvent(EventAfterTurn, &HookContext{Turn: 1})
	c.OnEvent(EventBeforeToolCall, &HookContext{ToolName: "read_file"})
	c.OnEvent(EventOnError, &HookContext{Error: errors.New("test")})
	c.OnEvent(EventOnSessionStart, &HookContext{SessionID: "sess-1"})

	if len(logs) != 5 {
		t.Fatalf("expected 5 log entries, got %d", len(logs))
	}
}

func TestLoggingContributor_NilLogger(t *testing.T) {
	c := NewLoggingContributor(nil)
	// Should not panic
	c.OnEvent(EventBeforeTurn, &HookContext{})
}

func TestMetricsContributor(t *testing.T) {
	m := NewMetricsContributor()

	m.OnEvent(EventAfterTurn, &HookContext{})
	m.OnEvent(EventAfterTurn, &HookContext{})
	m.OnEvent(EventAfterToolCall, &HookContext{})
	m.OnEvent(EventAfterToolCall, &HookContext{})
	m.OnEvent(EventAfterToolCall, &HookContext{})
	m.OnEvent(EventOnError, &HookContext{})
	m.OnEvent(EventOnSessionStart, &HookContext{})

	snap := m.Snapshot()
	if snap.TurnCount != 2 {
		t.Errorf("expected 2 turns, got %d", snap.TurnCount)
	}
	if snap.ToolCallCount != 3 {
		t.Errorf("expected 3 tool calls, got %d", snap.ToolCallCount)
	}
	if snap.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", snap.ErrorCount)
	}
	if snap.SessionCount != 1 {
		t.Errorf("expected 1 session, got %d", snap.SessionCount)
	}
}

func TestGlobalRegistry(t *testing.T) {
	// Save and restore global registry
	original := globalRegistry
	defer func() { globalRegistry = original }()

	globalRegistry = NewRegistry()

	var called bool
	RegisterFunc("global-test", func(event Event, hctx *HookContext) {
		called = true
	})

	Emit(EventBeforeTurn, &HookContext{})

	if !called {
		t.Error("expected global contributor called")
	}

	if Global().Count() != 1 {
		t.Error("expected 1 global contributor")
	}
}

func TestEvent_String(t *testing.T) {
	tests := []struct {
		event Event
		name  string
	}{
		{EventBeforeTurn, "before_turn"},
		{EventAfterTurn, "after_turn"},
		{EventBeforeToolCall, "before_tool_call"},
		{EventAfterToolCall, "after_tool_call"},
		{EventOnError, "on_error"},
		{EventOnSessionStart, "on_session_start"},
		{EventOnSessionEnd, "on_session_end"},
	}

	for _, tt := range tests {
		if tt.event.String() != tt.name {
			t.Errorf("expected %q, got %q", tt.name, tt.event.String())
		}
	}
}

func TestHookContext(t *testing.T) {
	hctx := &HookContext{
		Ctx:        context.Background(),
		Turn:       5,
		ToolName:   "read_file",
		ToolInput:  `{"path":"test.go"}`,
		ToolResult: "contents",
		Error:      nil,
		SessionID:  "sess-1",
		Extra:      map[string]any{"custom": "value"},
	}

	if hctx.Turn != 5 {
		t.Error("bad turn")
	}
	if hctx.ToolName != "read_file" {
		t.Error("bad tool name")
	}
}

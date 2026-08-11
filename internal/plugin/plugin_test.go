package plugin

import (
	"context"
	"encoding/json"
	"testing"
)

// --- Registry tests ---

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("expected non-nil Registry")
	}
	if r.Count() != 0 {
		t.Errorf("expected 0 entries, got %d", r.Count())
	}
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()
	err := r.Register(&Entry{ID: "test1", Name: "Test 1", Category: CategoryPlatform, Enabled: true})
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	if r.Count() != 1 {
		t.Errorf("expected 1 entry, got %d", r.Count())
	}
}

func TestRegistry_Register_Duplicate(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "test1", Name: "Test 1", Category: CategoryPlatform})
	err := r.Register(&Entry{ID: "test1", Name: "Test 1 Duplicate"})
	if err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestRegistry_Register_EmptyID(t *testing.T) {
	r := NewRegistry()
	err := r.Register(&Entry{ID: "", Name: "No ID"})
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "test1", Name: "Test 1"})
	r.Unregister("test1")
	if r.Count() != 0 {
		t.Errorf("expected 0 after unregister, got %d", r.Count())
	}
}

func TestRegistry_Unregister_Empty(t *testing.T) {
	r := NewRegistry()
	r.Unregister("") // should not panic
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "test1", Name: "Test 1"})
	e := r.Get("test1")
	if e == nil {
		t.Fatal("expected non-nil entry")
	}
	if e.Name != "Test 1" {
		t.Errorf("expected name 'Test 1', got %q", e.Name)
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	r := NewRegistry()
	if r.Get("nonexistent") != nil {
		t.Error("expected nil for nonexistent entry")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "b", Name: "B", Category: CategoryPlatform})
	r.Register(&Entry{ID: "a", Name: "A", Category: CategoryPlatform})
	r.Register(&Entry{ID: "c", Name: "C", Category: CategoryTools})

	list := r.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(list))
	}
	// Should be sorted by category then name
	if list[0].Name != "A" {
		t.Errorf("expected first entry 'A', got %q", list[0].Name)
	}
}

func TestRegistry_ListByCategory(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "a", Name: "A", Category: CategoryPlatform})
	r.Register(&Entry{ID: "b", Name: "B", Category: CategoryTools})
	r.Register(&Entry{ID: "c", Name: "C", Category: CategoryPlatform})

	platforms := r.ListByCategory(CategoryPlatform)
	if len(platforms) != 2 {
		t.Fatalf("expected 2 platforms, got %d", len(platforms))
	}
}

func TestRegistry_ListEnabledByCategory(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "a", Name: "A", Category: CategoryPlatform, Enabled: true})
	r.Register(&Entry{ID: "b", Name: "B", Category: CategoryPlatform, Enabled: false})
	r.Register(&Entry{ID: "c", Name: "C", Category: CategoryPlatform, Enabled: true})

	enabled := r.ListEnabledByCategory(CategoryPlatform)
	if len(enabled) != 2 {
		t.Fatalf("expected 2 enabled, got %d", len(enabled))
	}
}

func TestRegistry_Enable(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "test1", Name: "Test", Enabled: false})
	if err := r.Enable("test1"); err != nil {
		t.Fatalf("Enable() error: %v", err)
	}
	if !r.Get("test1").Enabled {
		t.Error("expected enabled=true")
	}
}

func TestRegistry_Enable_NotFound(t *testing.T) {
	r := NewRegistry()
	err := r.Enable("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent plugin")
	}
}

func TestRegistry_Disable(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "test1", Name: "Test", Enabled: true})
	if err := r.Disable("test1"); err != nil {
		t.Fatalf("Disable() error: %v", err)
	}
	if r.Get("test1").Enabled {
		t.Error("expected enabled=false")
	}
}

func TestRegistry_EnabledCount(t *testing.T) {
	r := NewRegistry()
	r.Register(&Entry{ID: "a", Enabled: true})
	r.Register(&Entry{ID: "b", Enabled: false})
	r.Register(&Entry{ID: "c", Enabled: true})
	if r.EnabledCount() != 2 {
		t.Errorf("expected 2 enabled, got %d", r.EnabledCount())
	}
}

func TestDefaultEnablementAppliesOnlyToPlatformPlugins(t *testing.T) {
	system := &System{}
	t.Setenv("TEST_SERVICE_BOT_TOKEN", "configured")

	if !system.isEnabledByDefault("test_service", CategoryPlatform) {
		t.Fatal("configured platform plugin was not enabled by default")
	}
	if system.isEnabledByDefault("test_service", CategoryTools) {
		t.Fatal("non-platform plugin was enabled from a platform token")
	}
	if !system.isEnabledByDefault("webhook", CategoryPlatform) {
		t.Fatal("tokenless built-in webhook platform was not enabled by default")
	}
}

// --- Global CLI Command tests ---

func TestGlobalCLICommands(t *testing.T) {
	// Save state
	orig := globalCLICommands
	defer func() {
		globalCLIMu.Lock()
		globalCLICommands = orig
		globalCLIMu.Unlock()
	}()

	globalCLIMu.Lock()
	globalCLICommands = nil
	globalCLIMu.Unlock()

	RegisterGlobalCLICommand(CLICommand{Name: "test1", Description: "Test 1"})
	RegisterGlobalCLICommand(CLICommand{Name: "test2", Description: "Test 2"})

	cmds := GlobalCLICommands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
}

// --- BaseLifecycleHook tests ---

func TestBaseLifecycleHook(t *testing.T) {
	h := BaseLifecycleHook{}
	ctx := context.Background()

	if h.Name() != "base" {
		t.Errorf("expected name 'base', got %q", h.Name())
	}
	if err := h.BeforeToolCall(ctx, "test", json.RawMessage("{}")); err != nil {
		t.Errorf("BeforeToolCall() error: %v", err)
	}
	h.AfterToolCall(ctx, "test", json.RawMessage("{}"), "result", nil)
	if err := h.BeforeModelCall(ctx, nil); err != nil {
		t.Errorf("BeforeModelCall() error: %v", err)
	}
	h.AfterModelCall(ctx, nil, "response", nil)

	out, err := h.TransformModelOutput(ctx, "test output")
	if err != nil || out != "test output" {
		t.Errorf("TransformModelOutput() = %q, %v", out, err)
	}

	h.OnTurnStart(ctx)
	h.OnTurnEnd(ctx)
	h.OnSessionStart(ctx, "session1")
	h.OnSessionEnd(ctx, "session1")
}

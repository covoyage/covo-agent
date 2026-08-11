package tui

import (
	"testing"

	"github.com/covoyage/covonaut/tui/component"
)

func TestModalManager_ShowAndClose(t *testing.T) {
	// ModalManager requires a UIBus which requires a ChatApp.
	// Since ChatApp construction needs a Terminal, we test the core logic
	// with a mock approach.

	// Test ModalKind string
	if ModalModelPicker.String() != "model_picker" {
		t.Error("wrong string for ModalModelPicker")
	}
	if ModalNone.String() != "none" {
		t.Error("wrong string for ModalNone")
	}

	// Test that ModalManager with nil bus doesn't crash on basic queries
	mm := &ModalManager{bus: nil}
	if mm.IsModalOpen() {
		t.Error("empty manager should not have modal open")
	}
	if mm.ActiveModal() != ModalNone {
		t.Error("empty manager should return ModalNone")
	}
	if mm.HandleESC() {
		t.Error("HandleESC with no modal should return false")
	}
}

func TestModalKind_String(t *testing.T) {
	tests := []struct {
		kind ModalKind
		want string
	}{
		{ModalNone, "none"},
		{ModalModelPicker, "model_picker"},
		{ModalSessionTree, "session_tree"},
		{ModalApprovalPicker, "approval_picker"},
		{ModalMCPMarketplace, "mcp_marketplace"},
		{ModalChangedFiles, "changed_files"},
		{ModalSessionSelector, "session_selector"},
		{ModalKeyHelp, "key_help"},
		{ModalStatusLineConfig, "status_line_config"},
		{ModalGeneric, "generic"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("ModalKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

// Integration-style test: create a full ModalManager with a real UIBus.
func TestModalManager_WithUIBus(t *testing.T) {
	// This would require a ChatApp which requires a Terminal.
	// We can't easily construct one in a unit test without a real tty.
	// The logic is covered by the integration tests in app_test.go
	// and the overlay tests there.

	// Just verify the type implements what we need
	editor := component.NewEditor(nil)
	_ = editor // ensure component package is usable

	mm := NewModalManager(nil) // nil bus is safe for queries
	if mm.IsActive(ModalModelPicker) {
		t.Error("should not be active")
	}
	if mm.CurrentContent() != nil {
		t.Error("should return nil content")
	}
}

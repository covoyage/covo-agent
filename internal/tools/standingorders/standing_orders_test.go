package standingorders

import (
	"os"
	"testing"
)

func TestStandingOrdersStore_AddAndList(t *testing.T) {
	dir, err := os.MkdirTemp("", "standing-orders-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	s := NewStandingOrdersStore(dir)

	// Initially empty
	orders := s.List()
	if len(orders) != 0 {
		t.Fatalf("expected empty store, got %d orders", len(orders))
	}

	// Add an order
	_, err = s.Add("Always use tabs for indentation")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	orders = s.List()
	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	if orders[0].Content != "Always use tabs for indentation" {
		t.Fatalf("unexpected content: %q", orders[0].Content)
	}

	// Add another
	_, err = s.Add("Prefer Python 3")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	orders = s.List()
	if len(orders) != 2 {
		t.Fatalf("expected 2 orders, got %d", len(orders))
	}

	// Newest first
	if orders[0].Content != "Prefer Python 3" {
		t.Fatalf("expected newest first, got %q", orders[0].Content)
	}
}

func TestStandingOrdersStore_AddEmpty(t *testing.T) {
	s := NewStandingOrdersStore(t.TempDir())
	_, err := s.Add("")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	_, err = s.Add("  ")
	if err == nil {
		t.Fatal("expected error for whitespace-only content")
	}
}

func TestStandingOrdersStore_Remove(t *testing.T) {
	s := NewStandingOrdersStore(t.TempDir())

	o, err := s.Add("Test order")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Remove(o.ID); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	orders := s.List()
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders after remove, got %d", len(orders))
	}

	// Remove non-existent returns error
	if err := s.Remove("nonexistent"); err == nil {
		t.Fatal("expected error for non-existent ID")
	}
}

func TestStandingOrdersStore_Clear(t *testing.T) {
	s := NewStandingOrdersStore(t.TempDir())

	s.Add("Order 1")
	s.Add("Order 2")
	s.Add("Order 3")

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	orders := s.List()
	if len(orders) != 0 {
		t.Fatalf("expected 0 orders after clear, got %d", len(orders))
	}
}

func TestStandingOrdersStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	s1 := NewStandingOrdersStore(dir)
	s1.Add("Persistent order 1")
	s1.Add("Persistent order 2")

	// Create a new store pointing to same directory
	s2 := NewStandingOrdersStore(dir)
	orders := s2.List()
	if len(orders) != 2 {
		t.Fatalf("expected 2 orders after reload, got %d", len(orders))
	}
}

func TestStandingOrdersStore_BuildPromptSuffix(t *testing.T) {
	s := NewStandingOrdersStore(t.TempDir())

	// Empty store returns empty string
	if suffix := s.BuildPromptSuffix(); suffix != "" {
		t.Fatalf("expected empty suffix for empty store, got %q", suffix)
	}

	s.Add("Always use tabs")
	s.Add("Prefer Python 3")

	suffix := s.BuildPromptSuffix()
	if suffix == "" {
		t.Fatal("expected non-empty suffix")
	}

	// Check it contains both orders
	if !contains(suffix, "Always use tabs") {
		t.Fatal("missing first order in suffix")
	}
	if !contains(suffix, "Prefer Python 3") {
		t.Fatal("missing second order in suffix")
	}
	if !contains(suffix, "<standing_orders>") {
		t.Fatal("missing opening tag")
	}
	if !contains(suffix, "</standing_orders>") {
		t.Fatal("missing closing tag")
	}
}

func TestStandingOrdersStore_ToToolItems(t *testing.T) {
	s := NewStandingOrdersStore(t.TempDir())
	s.Add("Order 1")
	s.Add("Order 2")

	items := s.ToToolItems()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	for _, item := range items {
		if item.ID == "" {
			t.Fatal("item ID should not be empty")
		}
		if item.Content == "" {
			t.Fatal("item Content should not be empty")
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

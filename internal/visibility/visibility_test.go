package visibility

import (
	"context"
	"testing"
)

func TestPolicyShouldAllow_NilPolicy(t *testing.T) {
	var p *Policy
	if !p.ShouldAllow("any:key") {
		t.Error("nil policy should allow everything")
	}
}

func TestPolicyShouldAllow_SameKey(t *testing.T) {
	p := &Policy{Mode: Isolated, CurrentKey: "tg:123"}
	if !p.ShouldAllow("tg:123") {
		t.Error("should always allow own session")
	}
}

func TestPolicyShouldAllow_Isolated(t *testing.T) {
	p := &Policy{Mode: Isolated, CurrentKey: "tg:123"}
	if p.ShouldAllow("tg:456") {
		t.Error("isolated mode should not allow other sessions")
	}
	if p.ShouldAllow("dc:789") {
		t.Error("isolated mode should not allow other platform sessions")
	}
}

func TestPolicyShouldAllow_Shared(t *testing.T) {
	p := &Policy{Mode: Shared, CurrentKey: "tg:123"}
	if !p.ShouldAllow("tg:456") {
		t.Error("shared mode should allow all sessions")
	}
	if !p.ShouldAllow("dc:789") {
		t.Error("shared mode should allow all platform sessions")
	}
}

func TestPolicyShouldAllow_Whitelist(t *testing.T) {
	p := &Policy{
		Mode:         Whitelist,
		CurrentKey:   "tg:123",
		AllowedPeers: []string{"tg:456", "dc:789"},
	}
	if !p.ShouldAllow("tg:456") {
		t.Error("whitelist should allow listed peer tg:456")
	}
	if !p.ShouldAllow("dc:789") {
		t.Error("whitelist should allow listed peer dc:789")
	}
	if p.ShouldAllow("tg:999") {
		t.Error("whitelist should not allow unlisted peer tg:999")
	}
	if p.ShouldAllow("slack:000") {
		t.Error("whitelist should not allow unlisted platform slack:000")
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := &Policy{Mode: Isolated, CurrentKey: "tg:123"}

	// No policy set initially
	if got := PolicyFromContext(ctx); got != nil {
		t.Errorf("expected nil policy, got %v", got)
	}

	// Set and retrieve
	ctx = WithPolicy(ctx, p)
	got := PolicyFromContext(ctx)
	if got == nil {
		t.Fatal("expected non-nil policy from context")
	}
	if got.Mode != Isolated {
		t.Errorf("expected mode Isolated, got %v", got.Mode)
	}
	if got.CurrentKey != "tg:123" {
		t.Errorf("expected key tg:123, got %s", got.CurrentKey)
	}
}

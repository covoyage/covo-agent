package agent

import (
	"testing"

	"github.com/covoyage/covo-agent/internal/agent/approval"
)

func TestNormalizePermissionModeInteractive(t *testing.T) {
	got, err := NormalizePermissionMode("ask", true)
	if err != nil || got != PermissionAsk {
		t.Fatalf("ask: %q %v", got, err)
	}
	got, err = NormalizePermissionMode("smart", true)
	if err != nil || got != PermissionAuto {
		t.Fatalf("smart: %q %v", got, err)
	}
	if _, err := NormalizePermissionMode("nope", true); err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizePermissionModeEmpty(t *testing.T) {
	got, err := NormalizePermissionMode("", true)
	if err != nil || got != PermissionAsk {
		t.Fatalf("interactive empty: %q %v", got, err)
	}
	got, err = NormalizePermissionMode("", false)
	if err != nil || got != PermissionAuto {
		t.Fatalf("exec empty: %q %v", got, err)
	}
}

func TestNormalizePermissionModeExecRejectsAsk(t *testing.T) {
	if _, err := NormalizePermissionMode("ask", false); err == nil {
		t.Fatal("exec ask should fail")
	}
	got, err := NormalizePermissionMode("smart", false)
	if err != nil || got != PermissionAuto {
		t.Fatalf("smart: %q %v", got, err)
	}
	got, err = NormalizePermissionMode("off", false)
	if err != nil || got != PermissionOff {
		t.Fatalf("off: %q %v", got, err)
	}
}

func TestApplyPermissionMode(t *testing.T) {
	sys := approval.NewSystem(approval.Config{Mode: "manual"})
	ApplyPermissionMode(sys, PermissionAuto)
	if sys.Mode() != "smart" || sys.IsYolo() {
		t.Fatalf("auto: mode=%s yolo=%v", sys.Mode(), sys.IsYolo())
	}
	ApplyPermissionMode(sys, PermissionFull)
	if !sys.IsYolo() {
		t.Fatal("full should enable yolo")
	}
	ApplyPermissionMode(sys, PermissionAsk)
	if sys.Mode() != "manual" || sys.IsYolo() {
		t.Fatalf("ask: mode=%s yolo=%v", sys.Mode(), sys.IsYolo())
	}
}

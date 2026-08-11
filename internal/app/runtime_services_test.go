package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/lifecycle"
)

func TestRuntimeServicesWritesRedactedLifecycleSidecar(t *testing.T) {
	t.Setenv("COVO_OTEL_ENABLED", "false")
	t.Setenv("COVO_SYSPOWER_ENABLED", "false")

	services := NewRuntimeServices(t.TempDir(), nil, nil)
	services.Start(context.Background())
	defer services.Stop()

	lifecycle.Emit(lifecycle.EventAfterToolCall, &lifecycle.HookContext{
		SessionID:  "session/with/slash",
		ToolName:   "http",
		ToolInput:  `{"api_key":"input-secret"}`,
		ToolResult: `https://user:result-secret@example.com/path`,
		Error:      errors.New("token=error-secret"),
	})

	lines, err := services.sidecar.Read("session/with/slash")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("sidecar lines = %d, want 1", len(lines))
	}
	for _, secret := range []string{"input-secret", "result-secret", "error-secret"} {
		if strings.Contains(lines[0], secret) {
			t.Fatalf("sidecar contains secret %q: %s", secret, lines[0])
		}
	}
	if !strings.Contains(lines[0], `"event":"after_tool_call"`) {
		t.Fatalf("sidecar event missing: %s", lines[0])
	}
}

func TestSystemPowerEnabledHonorsEnvironment(t *testing.T) {
	t.Setenv("COVO_SYSPOWER_ENABLED", "false")
	if systemPowerEnabled() {
		t.Fatal("systemPowerEnabled = true, want false")
	}
	t.Setenv("COVO_SYSPOWER_ENABLED", "true")
	if !systemPowerEnabled() {
		t.Fatal("systemPowerEnabled = false, want true")
	}
}

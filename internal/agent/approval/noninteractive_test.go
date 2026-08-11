package approval

import (
	"context"
	"log/slog"
	"testing"
)

func testSystem(t *testing.T, cfg Config) *System {
	t.Helper()
	cfg.Logger = slog.Default()
	return NewSystem(cfg)
}

func TestNonInteractive_AutoDeniesDangerousCommand(t *testing.T) {
	sys := testSystem(t, Config{Mode: "manual"})
	sys.SetNonInteractive(true)

	// "rm -rf /tmp/foo" is dangerous (recursive delete) but not hardline.
	// In interactive mode it would fall through to manual approval.
	// In non-interactive mode it should be auto-denied.
	d := sys.CheckCommand(context.Background(), "rm -rf /tmp/foo", "")
	if d.Approved {
		t.Errorf("expected dangerous command to be denied in non-interactive mode")
	}
	if d.PatternKey == "" {
		t.Errorf("expected pattern key to be set")
	}
}

func TestNonInteractive_AllowsSafeCommand(t *testing.T) {
	sys := testSystem(t, Config{Mode: "manual"})
	sys.SetNonInteractive(true)

	// Safe commands should still be approved even in non-interactive mode.
	d := sys.CheckCommand(context.Background(), "ls -la", "")
	if !d.Approved {
		t.Errorf("expected safe command to be approved in non-interactive mode, got: %s", d.Message)
	}
}

func TestNonInteractive_HardlineStillBlocks(t *testing.T) {
	sys := testSystem(t, Config{Mode: "manual"})
	sys.SetNonInteractive(true)

	// Hardline blocks should take precedence over everything.
	d := sys.CheckCommand(context.Background(), "rm -rf /", "")
	if d.Approved {
		t.Errorf("expected hardline command to be blocked")
	}
	if !d.Hardline {
		t.Errorf("expected hardline flag to be set")
	}
}

func TestNonInteractive_YoloOverrides(t *testing.T) {
	sys := testSystem(t, Config{Mode: "manual", YoloMode: true})
	sys.SetNonInteractive(true)

	// YOLO mode should bypass even non-interactive deny for dangerous commands.
	d := sys.CheckCommand(context.Background(), "rm -rf /tmp/foo", "")
	if !d.Approved {
		t.Errorf("expected YOLO to approve dangerous command even in non-interactive mode")
	}
}

func TestNonInteractive_AllowlistedStillWorks(t *testing.T) {
	sys := testSystem(t, Config{Mode: "manual"})
	sys.SetNonInteractive(true)

	// Approve "rm -rf /tmp/foo" pattern for the session.
	// First call to get the pattern key.
	d := sys.CheckCommand(context.Background(), "rm -rf /tmp/foo", "test-session")
	patternKey := d.PatternKey
	if patternKey == "" {
		t.Fatal("expected pattern key from dangerous command")
	}
	sys.ApproveSession("test-session", patternKey)

	// Second call should be approved via allowlist even in non-interactive mode.
	d = sys.CheckCommand(context.Background(), "rm -rf /tmp/foo", "test-session")
	if !d.Approved {
		t.Errorf("expected allowlisted command to be approved even in non-interactive mode")
	}
}

func TestCronModeDeny_SetsNonInteractive(t *testing.T) {
	sys := testSystem(t, Config{Mode: "manual", CronMode: "deny"})
	if !sys.IsNonInteractive() {
		t.Errorf("expected CronMode=deny to set NonInteractive=true")
	}
}

func TestCronModeApprove_DoesNotSetNonInteractive(t *testing.T) {
	sys := testSystem(t, Config{Mode: "manual", CronMode: "approve"})
	if sys.IsNonInteractive() {
		t.Errorf("expected CronMode=approve to leave NonInteractive=false")
	}
}

func TestSetNonInteractive(t *testing.T) {
	sys := testSystem(t, Config{Mode: "manual"})
	if sys.IsNonInteractive() {
		t.Errorf("expected NonInteractive=false by default")
	}
	sys.SetNonInteractive(true)
	if !sys.IsNonInteractive() {
		t.Errorf("expected NonInteractive=true after SetNonInteractive(true)")
	}
	sys.SetNonInteractive(false)
	if sys.IsNonInteractive() {
		t.Errorf("expected NonInteractive=false after SetNonInteractive(false)")
	}
}

package ossandbox

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"
)

func TestPrepareChildCommandRejectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := exec.Command("not-started")
	if err := PrepareChildCommand(ctx, cmd); err != context.Canceled {
		t.Fatalf("cancelled command preparation = %v", err)
	}
	if cmd.Process != nil {
		t.Fatal("preparation started a process")
	}
	if err := PrepareChildCommand(context.Background(), nil); err == nil {
		t.Fatal("nil command accepted")
	}
}

func TestPrepareChildCommandFailsClosedWithoutRequiredSandbox(t *testing.T) {
	globalState.mu.Lock()
	previousRequired, previousApplied := globalState.required, globalState.applied
	globalState.required, globalState.applied = true, false
	globalState.mu.Unlock()
	defer func() {
		globalState.mu.Lock()
		globalState.required, globalState.applied = previousRequired, previousApplied
		globalState.mu.Unlock()
	}()
	if err := PrepareChildCommand(context.Background(), exec.Command("never-started")); err == nil {
		t.Fatal("unavailable required sandbox allowed child execution")
	}
}

// testLogger creates a logger for tests that discards output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
}

// Ensure the test file compiles even on platforms where sandbox isn't supported.
var _ = os.Stdout

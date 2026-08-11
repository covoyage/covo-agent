package ossandbox

import (
	"io"
	"log/slog"
	"os"
)

// testLogger creates a logger for tests that discards output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
}

// Ensure the test file compiles even on platforms where sandbox isn't supported.
var _ = os.Stdout

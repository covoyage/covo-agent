package commands

import (
	"bytes"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func TestConfigureInteractiveLoggingRoutesGlobalLogsToWriter(t *testing.T) {
	previousSlog := slog.Default()
	previousLogWriter := log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(previousSlog)
		log.SetOutput(previousLogWriter)
	})

	var output bytes.Buffer
	logger := configureInteractiveLogging(&output)
	logger.Warn("explicit warning", "source", "test")
	slog.Warn("global warning", "source", "test")
	log.Print("standard warning")

	got := output.String()
	for _, message := range []string{"explicit warning", "global warning", "standard warning"} {
		if !strings.Contains(got, message) {
			t.Fatalf("log output missing %q: %s", message, got)
		}
	}
}

func TestConfigureInteractiveLoggingAcceptsNilWriter(t *testing.T) {
	previousSlog := slog.Default()
	previousLogWriter := log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(previousSlog)
		log.SetOutput(previousLogWriter)
	})

	configureInteractiveLogging(nil)
	slog.Warn("discarded")
	log.Print("discarded")
}

package logutil

import (
	"fmt"
	"log/slog"
	"strings"
)

// levelOverride is set once at startup from the --log-level flag (or env var).
// A nil value means "no override": each call site keeps its own default level.
var levelOverride *slog.Level

// SetLevel installs a global log-level override applied by ResolveLevel.
// It must be called before any loggers are constructed.
func SetLevel(l slog.Level) {
	levelOverride = &l
}

// ResolveLevel returns l unless a global override was installed via SetLevel,
// in which case the override wins. Call sites pass their natural default so
// behaviour is unchanged when no override is configured.
func ResolveLevel(l slog.Level) slog.Level {
	if levelOverride != nil {
		return *levelOverride
	}
	return l
}

// ParseLevel maps a user-facing level name to a slog.Level. It accepts
// DEBUG, INFO, WARN and ERROR (case-insensitive), matching the CLI choices.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug, nil
	case "INFO":
		return slog.LevelInfo, nil
	case "WARN":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: must be one of DEBUG, INFO, WARN, ERROR", s)
	}
}

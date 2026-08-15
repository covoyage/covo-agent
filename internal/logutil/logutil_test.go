package logutil

import (
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"DEBUG", slog.LevelDebug, true},
		{"INFO", slog.LevelInfo, true},
		{"WARN", slog.LevelWarn, true},
		{"ERROR", slog.LevelError, true},
		{"debug", slog.LevelDebug, true},
		{"  info ", slog.LevelInfo, true},
		{"verbose", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if c.ok && err != nil {
			t.Errorf("ParseLevel(%q) unexpected error: %v", c.in, err)
			continue
		}
		if !c.ok && err == nil {
			t.Errorf("ParseLevel(%q) expected error, got %v", c.in, got)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveLevel(t *testing.T) {
	SetLevel(slog.LevelDebug)
	defer SetLevel(slog.LevelInfo)
	if got := ResolveLevel(slog.LevelError); got != slog.LevelDebug {
		t.Errorf("ResolveLevel with override = %v, want DEBUG", got)
	}
}

func TestNoOverrideKeepsDefault(t *testing.T) {
	levelOverride = nil
	if got := ResolveLevel(slog.LevelError); got != slog.LevelError {
		t.Errorf("ResolveLevel without override = %v, want ERROR", got)
	}
}

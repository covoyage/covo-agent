package slashcmd

import (
	"strings"
	"testing"
)

func TestBuildSlashSuggestions_HasAllCommands(t *testing.T) {
	cmds := BuildSlashSuggestions()

	expectedMin := 25
	if len(cmds) < expectedMin {
		t.Fatalf("expected at least %d suggestions, got %d", expectedMin, len(cmds))
	}

	required := []string{"help", "clear", "mode general", "mode code",
		"provider openai", "model", "memory agent", "memory user",
		"skill", "session", "resume", "save",
		"compact", "curator",
		"retry",
		"background", "queue", "steer", "cancel", "logs", "respawn", "agents",
		"statusline", "rewind", "plan", "act", "stats",
		"usage", "vim", "recap", "focus", "quit"}
	for _, cmd := range required {
		found := false
		for _, s := range cmds {
			if strings.TrimRight(s.InsertText, " ") == cmd {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing required command: %q", cmd)
		}
	}

	seen := make(map[string]bool)
	for _, s := range cmds {
		key := strings.TrimRight(s.InsertText, " ")
		if seen[key] {
			t.Errorf("duplicate InsertText: %q", key)
		}
		seen[key] = true
	}
}

func TestBuildSlashSuggestions_HasHelpSubcommandResolution(t *testing.T) {
	cmds := BuildSlashSuggestions()

	for _, s := range cmds {
		if !strings.HasPrefix(s.Label, "/") {
			t.Errorf("Label %q does not start with /", s.Label)
		}
	}

	resolveTarget := func(target string) bool {
		for _, s := range cmds {
			if strings.TrimRight(s.InsertText, " ") == target {
				return true
			}
		}
		return false
	}

	for _, cmd := range []string{"help", "session", "skill", "quit", "retry", "background", "queue"} {
		if !resolveTarget(cmd) {
			t.Errorf("cannot resolve command: %q", cmd)
		}
	}
}

func TestBuildSlashSuggestions_InsertTextMatchesLabelPrefix(t *testing.T) {
	cmds := BuildSlashSuggestions()

	for _, s := range cmds {
		insert := strings.TrimRight(s.InsertText, " ")
		if strings.Contains(insert, " ") {
			continue
		}
		label := strings.TrimPrefix(s.Label, "/")
		labelParts := strings.Split(label, " ")[0]
		if insert != labelParts {
			t.Errorf("InsertText %q does not match label prefix %q for Label %q", insert, labelParts, s.Label)
		}
	}
}

func TestBuildAtSuggestions_DoesNotExposeUnimplementedURL(t *testing.T) {
	s := BuildAtSuggestions()
	for _, item := range s {
		if strings.TrimSpace(item.InsertText) == "url:" || strings.TrimSpace(item.Label) == "@url:" {
			t.Fatalf("unexpected @url suggestion exposed: %+v", item)
		}
	}
}

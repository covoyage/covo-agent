package tui

import (
	"strings"
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestBuildWelcomeMessageContainsSessionData(t *testing.T) {
	message := BuildWelcomeMessage(WelcomeInfo{
		Provider:   "openai",
		Model:      "gpt-test",
		Mode:       "code",
		WorkingDir: "/workspace",
		ToolCount:  12,
		SkillCount: 3,
	})
	for _, want := range []string{"openai", "gpt-test", "code", "/workspace", "12 tools · 3 skills"} {
		if !strings.Contains(message, want) {
			t.Errorf("welcome message missing %q", want)
		}
	}
	const bannerTail = `  \_____\____/|___/  \____/   /_/   \_\____|_____|_| \_| |_|`
	if !strings.Contains(message, bannerTail) {
		t.Fatal("welcome message banner changed")
	}
}

func TestBuildWelcomeMessageCyber(t *testing.T) {
	message := BuildWelcomeMessage(WelcomeInfo{
		Provider:   "openai",
		Model:      "gpt-test",
		Mode:       "cyber",
		WorkingDir: "/workspace",
		ToolCount:  8,
		SkillCount: 1,
	})
	if !strings.Contains(message, "cyber") {
		t.Errorf("cyber welcome missing mode %q", message)
	}

	empty := BuildWelcomeMessage(WelcomeInfo{Mode: "cyber"})
	if !strings.Contains(empty, "/mode") {
		t.Fatal("cyber welcome shortcuts should include /mode")
	}
}

func TestSessionCardLinesHaveStableWidth(t *testing.T) {
	for _, info := range []WelcomeInfo{
		{Provider: "p", Model: "m", Mode: "code", WorkingDir: "/tmp"},
		{Provider: "p", Model: "m", Mode: "cyber", WorkingDir: "/tmp"},
	} {
		lines := sessionCardLines(info)
		for index, line := range lines {
			if got := core.VisibleWidth(line); got != 74 {
				t.Errorf("mode=%s line %d width = %d, want 74: %q", info.Mode, index, got, line)
			}
		}
	}
}

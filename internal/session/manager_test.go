package session

import (
	"testing"
	"time"

	covosession "github.com/covoyage/covonaut/session"
)

func TestFormatSessionInfo(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)

	t.Run("with name and label", func(t *testing.T) {
		m := &Manager{currentID: "sess1234abcd"}
		info := covosession.Info{
			ID:           "sess1234abcd",
			Name:         "My Session",
			Label:        "important",
			UpdatedAt:    now,
			MessageCount: 15,
		}
		got := m.FormatSessionInfo(info)
		// Current format: "  %-10s%s %s  %3d msgs  %s%s"
		// ID[:8] = "sess1234", marker = " *", date = "05-31 10:04"
		wantContains := []string{"sess1234", "My Session", "[important]", "15 msgs", "*"}
		for _, s := range wantContains {
			if !containsStr(got, s) {
				t.Errorf("expected %q in output, got: %q", s, got)
			}
		}
	})

	t.Run("without name uses id", func(t *testing.T) {
		m := &Manager{currentID: "other"}
		info := covosession.Info{
			ID:           "abcdef123456",
			UpdatedAt:    now,
			MessageCount: 3,
		}
		got := m.FormatSessionInfo(info)
		if len(got) == 0 {
			t.Fatal("got empty string")
		}
		// Should contain a friendly placeholder with date
		if !containsStr(got, "会话") {
			t.Errorf("expected friendly placeholder, got: %q", got)
		}
	})

	t.Run("current session marker", func(t *testing.T) {
		id := "currsessid01"
		m := &Manager{currentID: id}
		info := covosession.Info{
			ID:           id,
			UpdatedAt:    now,
			MessageCount: 5,
		}
		got := m.FormatSessionInfo(info)
		// Current session should have '*' marker
		if !containsStr(got, "*") {
			t.Errorf("expected '*' marker for current session, got %q", got)
		}
	})

	t.Run("non-current session no marker", func(t *testing.T) {
		m := &Manager{currentID: "other"}
		info := covosession.Info{
			ID:           "abcdef123456",
			UpdatedAt:    now,
			MessageCount: 1,
		}
		got := m.FormatSessionInfo(info)
		for _, ch := range got {
			if ch == '*' {
				t.Errorf("unexpected '*' marker for non-current session: %q", got)
				break
			}
		}
	})
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

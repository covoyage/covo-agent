package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// Suggestion is one keyboard-selectable follow-up prompt.
type Suggestion struct {
	Key  string
	Text string
}

// SuggestionsManager renders and activates contextual follow-up prompts.
type SuggestionsManager struct {
	mu       sync.Mutex
	active   []Suggestion
	onSubmit func(string)
}

func NewSuggestionsManager(onSubmit func(string)) *SuggestionsManager {
	return &SuggestionsManager{onSubmit: onSubmit}
}

func (manager *SuggestionsManager) Show(app *chat.ChatApp) {
	manager.mu.Lock()
	manager.active = suggestionTexts()
	manager.mu.Unlock()
	manager.appendMessage(app)
}

func (manager *SuggestionsManager) appendMessage(app *chat.ChatApp) {
	palette := theme.CurrentPalette()
	var builder strings.Builder
	builder.WriteString(palette.Dim.Render(i18n.T("suggestions.title") + "\n"))

	manager.mu.Lock()
	active := append([]Suggestion(nil), manager.active...)
	manager.mu.Unlock()
	for _, suggestion := range active {
		builder.WriteString(fmt.Sprintf("  %s  %s\n",
			palette.Accent.Render(suggestion.Key),
			palette.Dim.Render(suggestion.Text)))
	}
	builder.WriteString(palette.Dim.Render("\n" + i18n.T("suggestions.follow_up_hint")))

	app.History().Append(chat.ChatMessage{
		ID:   manager.SuggestionMsgID(),
		Role: chat.RoleSystem,
		Text: builder.String(),
	})
}

func (manager *SuggestionsManager) Dismiss(*chat.ChatApp) {
	manager.mu.Lock()
	manager.active = nil
	manager.mu.Unlock()
}

func (manager *SuggestionsManager) HasActive() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return len(manager.active) > 0
}

func (manager *SuggestionsManager) HandleHotkey(data string) bool {
	switch {
	case terminal.MatchesKey(data, "ctrl+1"):
		return manager.activateSuggestion(0)
	case terminal.MatchesKey(data, "ctrl+2"):
		return manager.activateSuggestion(1)
	case terminal.MatchesKey(data, "ctrl+3"):
		return manager.activateSuggestion(2)
	default:
		return false
	}
}

func (manager *SuggestionsManager) activateSuggestion(index int) bool {
	manager.mu.Lock()
	if index < 0 || index >= len(manager.active) {
		manager.mu.Unlock()
		return false
	}
	text := manager.active[index].Text
	manager.active = nil
	onSubmit := manager.onSubmit
	manager.mu.Unlock()

	if onSubmit != nil {
		go onSubmit(text)
	}
	return true
}

func (*SuggestionsManager) SuggestionMsgID() string {
	return "suggestions"
}

func suggestionTexts() []Suggestion {
	return []Suggestion{
		{Key: "ctrl+1", Text: i18n.T("suggestions.detail")},
		{Key: "ctrl+2", Text: i18n.T("suggestions.changes")},
		{Key: "ctrl+3", Text: i18n.T("suggestions.risks")},
	}
}

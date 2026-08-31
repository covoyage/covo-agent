package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covonaut/tui/chat"
	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/i18n"
)

// HistoryMatch locates a query hit inside a ChatHistory message.
type HistoryMatch struct {
	MsgIndex  int
	LineIndex int
	Start     int
	End       int
}

// HistorySearchIndex indexes ChatHistory.Messages() for incremental find.
type HistorySearchIndex struct {
	mu      sync.RWMutex
	entries []indexedEntry
}

// NewHistorySearchIndex creates an empty transcript search index.
func NewHistorySearchIndex() *HistorySearchIndex {
	return &HistorySearchIndex{}
}

// Sync rebuilds the index from chat messages.
func (idx *HistorySearchIndex) Sync(messages []chat.ChatMessage) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.entries = make([]indexedEntry, 0, len(messages))
	for i, m := range messages {
		idx.entries = append(idx.entries, indexedEntry{
			id:   EntryID(i + 1),
			text: m.Text,
		})
	}
}

// Find returns case-insensitive substring matches.
func (idx *HistorySearchIndex) Find(query string) []HistoryMatch {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	lowerQ := strings.ToLower(query)
	var matches []HistoryMatch
	for i, ie := range idx.entries {
		lines := strings.Split(ie.text, "\n")
		for lineIdx, line := range lines {
			search := strings.ToLower(line)
			start := 0
			for {
				pos := strings.Index(search[start:], lowerQ)
				if pos < 0 {
					break
				}
				abs := start + pos
				matches = append(matches, HistoryMatch{
					MsgIndex:  i,
					LineIndex: lineIdx,
					Start:     abs,
					End:       abs + len(lowerQ),
				})
				start = abs + len(lowerQ)
				if start >= len(search) {
					break
				}
			}
		}
	}
	return matches
}

// HistorySearchOverlay is the incremental transcript search box.
type HistorySearchOverlay struct {
	mu      sync.Mutex
	query   string
	matches []HistoryMatch
	current int
	index   *HistorySearchIndex
	history *chat.ChatHistory
	onClose func()
	onJump  func(HistoryMatch)
}

// NewHistorySearchOverlay builds a search overlay bound to history.
func NewHistorySearchOverlay(history *chat.ChatHistory, onJump func(HistoryMatch), onClose func()) *HistorySearchOverlay {
	idx := NewHistorySearchIndex()
	if history != nil {
		idx.Sync(history.Messages())
	}
	return &HistorySearchOverlay{
		index:   idx,
		history: history,
		onJump:  onJump,
		onClose: onClose,
	}
}

// Query returns the current search string.
func (s *HistorySearchOverlay) Query() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.query
}

// MatchCount returns the number of hits.
func (s *HistorySearchOverlay) MatchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.matches)
}

// Current returns the highlighted match.
func (s *HistorySearchOverlay) Current() (HistoryMatch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return HistoryMatch{}, false
	}
	return s.matches[s.current], true
}

// Next moves to the next match.
func (s *HistorySearchOverlay) Next() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return false
	}
	s.current = (s.current + 1) % len(s.matches)
	return true
}

// Prev moves to the previous match.
func (s *HistorySearchOverlay) Prev() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return false
	}
	s.current = (s.current - 1 + len(s.matches)) % len(s.matches)
	return true
}

// Invalidate implements core.Component.
func (s *HistorySearchOverlay) Invalidate() {}

// Render implements core.Component.
func (s *HistorySearchOverlay) Render(width int64) []string {
	s.mu.Lock()
	query := s.query
	n := len(s.matches)
	cur := s.current
	s.mu.Unlock()
	pal := theme.CurrentPalette()
	status := i18n.T("search.no_matches")
	if n > 0 {
		status = i18n.T("search.match_status", "current", fmt.Sprintf("%d", cur+1), "count", fmt.Sprintf("%d", n))
	}
	line := pal.Accent.Render(i18n.T("search.prompt")+" ") + query + pal.Dim.Render("▎")
	hint := pal.Dim.Render(i18n.T("search.hint") + "  " + status)
	if width > 0 {
		line = core.TruncateToWidth(line, width, "")
		hint = core.TruncateToWidth(hint, width, "")
	}
	return []string{line, hint}
}

// Update implements core.Updatable.
func (s *HistorySearchOverlay) Update(msg core.Msg) core.Cmd {
	switch m := msg.(type) {
	case core.KeyMsg:
		s.handleKey(m.Data)
	case core.PasteMsg:
		s.appendQuery(m.Text)
	}
	return nil
}

func (s *HistorySearchOverlay) handleKey(data string) {
	switch {
	case terminal.MatchesKey(data, "escape"):
		if s.onClose != nil {
			s.onClose()
		}
	case terminal.MatchesKey(data, "enter"), terminal.MatchesKey(data, "ctrl+s"):
		if s.Next() {
			s.jump()
		}
	case terminal.MatchesKey(data, "ctrl+r"):
		if s.Prev() {
			s.jump()
		}
	case terminal.MatchesKey(data, "backspace"), terminal.MatchesKey(data, "ctrl+h"):
		s.mu.Lock()
		if s.query != "" {
			runes := []rune(s.query)
			s.query = string(runes[:len(runes)-1])
		}
		query := s.query
		s.mu.Unlock()
		s.refresh(query)
	default:
		for _, key := range terminal.ParseKeys(data) {
			if key.IsRelease() || !key.IsPrintable() {
				continue
			}
			if key.Rune == 0 {
				continue
			}
			s.mu.Lock()
			s.query += string(key.Rune)
			query := s.query
			s.mu.Unlock()
			s.refresh(query)
			return
		}
	}
}

func (s *HistorySearchOverlay) appendQuery(text string) {
	s.mu.Lock()
	s.query += text
	query := s.query
	s.mu.Unlock()
	s.refresh(query)
}

func (s *HistorySearchOverlay) refresh(query string) {
	matches := s.index.Find(query)
	s.mu.Lock()
	s.matches = matches
	s.current = 0
	s.mu.Unlock()
	s.jump()
}

func (s *HistorySearchOverlay) jump() {
	match, ok := s.Current()
	if !ok {
		return
	}
	if s.onJump != nil {
		s.onJump(match)
	}
}

var _ core.Component = (*HistorySearchOverlay)(nil)
var _ core.Updatable = (*HistorySearchOverlay)(nil)

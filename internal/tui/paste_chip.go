package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/covoyage/covonaut/tui/theme"
)

// LargePasteThreshold is the byte size above which a single-line paste is
// stored as a chip instead of exploding the composer.
const LargePasteThreshold = 10 * 1024

const pasteChipPrefix = "[Pasted ~"

var pasteChipRe = regexp.MustCompile(`\[Pasted ~[^\]]+\]`)

// PasteStore holds pasted blobs keyed by the chip token shown in the editor.
type PasteStore struct {
	mu     sync.Mutex
	next   atomic.Int64
	pastes map[int]string
	chips  map[string]int
}

// NewPasteStore creates an empty paste store.
func NewPasteStore() *PasteStore {
	return &PasteStore{
		pastes: make(map[int]string),
		chips:  make(map[string]int),
	}
}

// Store records text and returns the chip token inserted into the editor.
func (s *PasteStore) Store(text string) string {
	if s == nil {
		return text
	}
	id := int(s.next.Add(1))
	label := pasteChipLabel(text)
	chip := fmt.Sprintf("[Pasted ~%s]", label)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.chips[chip]; exists {
		chip = fmt.Sprintf("[Pasted ~%s #%d]", label, id)
	}
	s.pastes[id] = text
	s.chips[chip] = id
	return chip
}

// Expand replaces paste chips with the stored blobs.
func (s *PasteStore) Expand(input string) string {
	if s == nil || !strings.Contains(input, pasteChipPrefix) {
		return input
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type item struct {
		chip string
		blob string
	}
	items := make([]item, 0, len(s.chips))
	for chip, id := range s.chips {
		items = append(items, item{chip: chip, blob: s.pastes[id]})
	}
	sort.Slice(items, func(i, j int) bool {
		return len(items[i].chip) > len(items[j].chip)
	})
	out := input
	for _, it := range items {
		out = strings.ReplaceAll(out, it.chip, it.blob)
	}
	return out
}

// ShouldChip reports whether pasted text should be collapsed.
func ShouldChipPaste(text string) bool {
	return pasteLineCount(text) >= 2 || len(text) >= LargePasteThreshold
}

// StylePasteChips wraps chip tokens in the composer using the active theme.
func StylePasteChips(s string) string {
	if !strings.Contains(s, pasteChipPrefix) {
		return s
	}
	st := pasteChipStyle(theme.CurrentPalette())
	return pasteChipRe.ReplaceAllStringFunc(s, func(chip string) string {
		return st.Render(chip)
	})
}

func pasteChipStyle(pal *theme.Palette) theme.Style {
	s := theme.NewStyle().Bold()
	if pal == nil {
		return s.Bg(theme.Yellow).Fg(theme.Black)
	}
	bg := ""
	if pal.Semantic != nil {
		bg = pal.Semantic.Warning
		if bg == "" {
			bg = pal.Semantic.Accent
		}
	}
	if params := theme.BgParams(bg, pal.Mode); params != "" {
		s = s.WithBgParams(params)
	} else {
		s = s.Bg(theme.Yellow)
	}
	fg := contrastingChipFg(bg)
	if params := theme.FgParams(fg, pal.Mode); params != "" {
		s = s.WithFgParams(params)
	} else {
		s = s.Fg(theme.Black)
	}
	return s
}

func contrastingChipFg(bg string) string {
	r, g, b, ok := chipHexRGB(bg)
	if !ok {
		if n, err := strconv.Atoi(strings.TrimSpace(bg)); err == nil && n >= 0 && n <= 255 {
			if n >= 244 || n == 7 || n == 15 {
				return "#1a1a1a"
			}
			return "#f4f4f4"
		}
		return "#1a1a1a"
	}
	luma := 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
	if luma >= 140 {
		return "#1a1a1a"
	}
	return "#f4f4f4"
}

func chipHexRGB(hex string) (r, g, b int, ok bool) {
	h := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(hex)), "#")
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int((n >> 16) & 0xff), int((n >> 8) & 0xff), int(n & 0xff), true
}

func pasteLineCount(text string) int {
	t := strings.TrimRight(text, "\r\n")
	if t == "" {
		return 0
	}
	return strings.Count(t, "\n") + 1
}

func pasteChipLabel(text string) string {
	n := len([]rune(text))
	lines := pasteLineCount(text)
	if lines <= 1 {
		return fmt.Sprintf("%d chars", n)
	}
	return fmt.Sprintf("%d lines", lines)
}

package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Search Highlight + Navigation — 搜索高亮和 next/prev 导航。
//
// 基于 ScrollbackSearchIndex.Find() 的匹配位置，提供搜索状态管理、
// 高亮渲染和 next/prev 导航。
// ---------------------------------------------------------------------------

// SearchState 管理搜索 UI 状态。
type SearchState struct {
	mu         sync.Mutex
	active     bool
	query      string
	matches    []ScrollbackMatch
	currentIdx int // 当前高亮的匹配索引
	searchedAt time.Time
}

// NewSearchState 创建空状态。
func NewSearchState() *SearchState {
	return &SearchState{}
}

// Start 开启搜索模式。
func (s *SearchState) Start(query string, matches []ScrollbackMatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.query = query
	s.matches = matches
	s.currentIdx = 0
	s.searchedAt = time.Now()
}

// UpdateQuery 更新搜索查询和匹配结果。
func (s *SearchState) UpdateQuery(query string, matches []ScrollbackMatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.query = query
	s.matches = matches
	if s.currentIdx >= len(matches) {
		s.currentIdx = 0
	}
	s.searchedAt = time.Now()
}

// Next 移动到下一个匹配。返回是否成功。
func (s *SearchState) Next() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return false
	}
	s.currentIdx = (s.currentIdx + 1) % len(s.matches)
	return true
}

// Prev 移动到上一个匹配。返回是否成功。
func (s *SearchState) Prev() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return false
	}
	s.currentIdx = (s.currentIdx - 1 + len(s.matches)) % len(s.matches)
	return true
}

// Close 关闭搜索模式。
func (s *SearchState) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
	s.query = ""
	s.matches = nil
	s.currentIdx = 0
}

// IsActive 返回搜索是否活跃。
func (s *SearchState) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// CurrentMatch 返回当前高亮的匹配。
func (s *SearchState) CurrentMatch() (ScrollbackMatch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return ScrollbackMatch{}, false
	}
	return s.matches[s.currentIdx], true
}

// MatchCount 返回匹配数量。
func (s *SearchState) MatchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.matches)
}

// CurrentIndex 返回当前匹配索引（1-based）。
func (s *SearchState) CurrentIndex() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentIdx + 1
}

// HighlightLine 高亮渲染一行文本中的搜索匹配。
// line 是原始文本，entryID 是该行所属的 entry ID，
// lineIdx 是该行在 entry 中的行索引。
func (s *SearchState) HighlightLine(line string, entryID EntryID, lineIdx int, pal *theme.Palette) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active || len(s.matches) == 0 {
		return line
	}

	// 找到所有属于本行的匹配
	type matchRange struct {
		start, end int
		isCurrent  bool
	}
	var ranges []matchRange
	for i, m := range s.matches {
		if m.EntryID == entryID && m.LineIndex == lineIdx {
			ranges = append(ranges, matchRange{
				start:     m.ByteRange[0],
				end:       m.ByteRange[1],
				isCurrent: i == s.currentIdx,
			})
		}
	}

	if len(ranges) == 0 {
		return line
	}

	// 按位置排序并渲染
	var sb strings.Builder
	pos := 0
	for _, r := range ranges {
		if r.start < pos || r.end > len(line) {
			continue
		}
		sb.WriteString(line[pos:r.start])
		matchText := line[r.start:r.end]
		if r.isCurrent {
			sb.WriteString(pal.Accent.Render(matchText))
		} else {
			sb.WriteString(pal.Dim.Render(matchText))
		}
		pos = r.end
	}
	if pos < len(line) {
		sb.WriteString(line[pos:])
	}
	return sb.String()
}

// SearchStatusLine 返回搜索状态栏文本（如 "3/12 matches"）。
func (s *SearchState) SearchStatusLine(pal *theme.Palette) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return ""
	}
	if len(s.matches) == 0 {
		return pal.Dim.Render(fmt.Sprintf("search: \"%s\" — no matches", s.query))
	}
	return pal.Accent.Render(fmt.Sprintf("search: %d/%d matches", s.currentIdx+1, len(s.matches)))
}

package tui

import (
	"strconv"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Verb Group 智能折叠。
//
// 两种折叠家族：
//   - VerbRun：同类工具调用连续运行时折叠为 "Read 2 skills" 聚合标题
//   - Truncation：过长 dense run 截断为 "N more" 折叠头
//
// 用户可手动展开某个 group（按 EntryId 记忆）。
// ---------------------------------------------------------------------------

// GroupKind 标识折叠家族。
type GroupKind int

const (
	GroupKindNone GroupKind = iota
	// GroupKindVerbRun 同类工具调用连续运行
	GroupKindVerbRun
	// GroupKindTruncation 预算截断
	GroupKindTruncation
)

// GroupSpan 描述一个折叠区域。
type GroupSpan struct {
	StartIdx int       // 起始 entry 索引（含）
	EndIdx   int       // 结束 entry 索引（不含）
	Kind     GroupKind
	Members  int   // VerbRun: 成员数; Truncation: 参与者数
	Hidden   int   // Truncation: 隐藏数
	Expanded bool  // 用户是否手动展开
	Verb     string // VerbRun: 聚合动词（如 "Read"）
	EntryID  EntryID // 触发此 span 的 entry ID（用于查找展开状态）
}

// GroupModel 管理折叠状态。
type GroupModel struct {
	mu             sync.RWMutex
	spans          []GroupSpan
	expandedGroups map[EntryID]bool
}

// NewGroupModel 创建空模型。
func NewGroupModel() *GroupModel {
	return &GroupModel{
		expandedGroups: make(map[EntryID]bool),
	}
}

// Rebuild 从 pipeline entries 重建折叠 spans。
// maxVisible 是 Truncation 家族的可见上限。
func (gm *GroupModel) Rebuild(p *ScrollbackPipeline, maxVisible int) []GroupSpan {
	p.mu.RLock()
	entries := p.entries
	p.mu.RUnlock()

	gm.mu.Lock()
	defer gm.mu.Unlock()

	gm.spans = gm.spans[:0]

	// Phase 1: VerbRun — 扫描连续同类工具调用
	i := 0
	for i < len(entries) {
		entry := entries[i]
		if entry.Kind != BlockKindToolCall {
			i++
			continue
		}
		// 找到 verb（工具名的动词前缀）
		verb := toolVerb(entry)
		if verb == "" {
			i++
			continue
		}
		// 扫描连续同类
		start := i
		end := i // end 是连续同类 run 的最后一个索引（含）
		count := 1
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Kind == BlockKindToolCall && toolVerb(entries[j]) == verb {
				count++
				end = j
			} else {
				break
			}
		}
		if count >= 2 {
			entryID := entries[start].ID
			expanded, ok := gm.expandedGroups[entryID]
			if !ok {
				expanded = true // 默认展开
			}
			gm.spans = append(gm.spans, GroupSpan{
				StartIdx: start,
				EndIdx:   end + 1,
				Kind:     GroupKindVerbRun,
				Members:  count,
				Expanded: expanded,
				Verb:     verb,
				EntryID:  entryID,
			})
		}
		// 跳过已处理的 run（end+1 是下一个未检查的 entry）
		i = end + 1
	}

	// Phase 2: Truncation — 截断过长 dense run
	// (简化版：连续 thinking/system 块超过 maxVisible 时折叠)
	i = 0
	for i < len(entries) {
		entry := entries[i]
		if entry.Kind != BlockKindThinking && entry.Kind != BlockKindSystem {
			i++
			continue
		}
		start := i
		end := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Kind == entry.Kind {
				end = j
			} else {
				break
			}
		}
		participants := end - start + 1
		if participants > maxVisible {
			hidden := participants - maxVisible
			entryID := entries[start].ID
			expanded, ok := gm.expandedGroups[entryID]
			if !ok {
				expanded = true // 默认展开
			}
			gm.spans = append(gm.spans, GroupSpan{
				StartIdx: start,
				EndIdx:   end + 1,
				Kind:     GroupKindTruncation,
				Members:  participants,
				Hidden:   hidden,
				Expanded: expanded,
				EntryID:  entryID,
			})
		}
		i = end + 1
	}

	return gm.spans
}

// SpanContaining 返回包含给定 entry 索引的 span。
func (gm *GroupModel) SpanContaining(idx int) *GroupSpan {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	for i := range gm.spans {
		s := &gm.spans[i]
		if s.StartIdx <= idx && idx < s.EndIdx {
			return s
		}
	}
	return nil
}

// ToggleExpand 切换某个 group 的展开/折叠状态。
// 未显式设置的 group 默认展开，首次 toggle 折叠。
func (gm *GroupModel) ToggleExpand(entryID EntryID) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	expanded, ok := gm.expandedGroups[entryID]
	if !ok {
		// 默认展开 → 首次 toggle 折叠
		gm.expandedGroups[entryID] = false
		return
	}
	if expanded {
		delete(gm.expandedGroups, entryID)
	} else {
		gm.expandedGroups[entryID] = true
	}
}

// IsExpanded 检查某个 group 是否被手动展开。
// 未显式设置的 group 默认展开（让用户看到完整内容，需要时手动折叠）。
func (gm *GroupModel) IsExpanded(entryID EntryID) bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	expanded, ok := gm.expandedGroups[entryID]
	if !ok {
		return true // 默认展开
	}
	return expanded
}

// Spans 返回所有折叠 span。
func (gm *GroupModel) Spans() []GroupSpan {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	out := make([]GroupSpan, len(gm.spans))
	copy(out, gm.spans)
	return out
}

// IsEntryHidden 检查给定 entry 索引是否被折叠隐藏。
func (gm *GroupModel) IsEntryHidden(idx int) bool {
	span := gm.SpanContaining(idx)
	if span == nil {
		return false
	}
	// 检查实时展开状态（span.Expanded 是快照，可能已过时）
	gm.mu.RLock()
	expanded, ok := gm.expandedGroups[span.EntryID]
	if !ok {
		expanded = true // 默认展开
	}
	gm.mu.RUnlock()
	if expanded {
		return false
	}
	switch span.Kind {
	case GroupKindVerbRun:
		// 折叠时只显示第一行（聚合标题），其余隐藏
		return idx > span.StartIdx
	case GroupKindTruncation:
		// 折叠时只显示前 maxVisible 行
		visible := span.Members - span.Hidden
		return idx >= span.StartIdx+visible
	}
	return false
}

// GroupHeader 返回某个 span 的聚合标题行。
func GroupHeader(span GroupSpan) string {
	switch span.Kind {
	case GroupKindVerbRun:
		return span.Verb + " " + strconv.Itoa(span.Members) + " items"
	case GroupKindTruncation:
		return strconv.Itoa(span.Hidden) + " more…"
	}
	return ""
}

// toolVerb 从 ToolCallBlock 提取工具的动词前缀。
func toolVerb(entry *ScrollbackEntry) string {
	tc, ok := entry.Block.(*ToolCallBlock)
	if !ok {
		return ""
	}
	// 取工具名第一个下划线前的部分作为 verb
	parts := strings.SplitN(tc.ToolName, "_", 2)
	if len(parts) > 0 {
		return capitalize(parts[0])
	}
	return capitalize(tc.ToolName)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

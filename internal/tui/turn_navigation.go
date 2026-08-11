package tui

import (
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Turn 导航。
//
// 对话时间线：每个 turn（用户输入→助手回复）一个条目。
// 用于 /jump 选择器和 sidebar 时间线。
//
// 架构：
//   - TimelineEntry：每轮对话的快照（prompt entry ID + preview 首行截断）
//   - TurnModel：从 pipeline entries 构建时间线
//   - ResponseAnchor：在 turn 范围内反向扫描找到第一个非空 AgentMessage
// ---------------------------------------------------------------------------

// TimelineEntry 是对话时间线中的一轮。
type TimelineEntry struct {
	TurnIdx        int      // turn 序号
	PromptEntryID  EntryID // 对应 UserPrompt entry 的 ID
	Preview        string   // prompt 首行截断
}

// PreviewMaxChars 时间线预览最大字符数。
const previewMaxChars = 120

// TurnStatus 标识 turn 的状态。
type TurnStatus int

const (
	TurnCompleted TurnStatus = iota
	TurnRunning
)

// Turn 描述一个完整的对话轮次。
type Turn struct {
	PromptIndex int        // prompt entry 在 entries 列表中的索引
	EndIndex    int        // turn 结束位置（下一个 turn 的 prompt 索引）
	Status      TurnStatus
}

// TurnModel 管理对话时间线。
type TurnModel struct {
	mu           sync.RWMutex
	turns        []Turn
	stickyActive int  // 当前粘性标题对应的 turn 索引（-1=无）
}

// NewTurnModel 创建空模型。
func NewTurnModel() *TurnModel {
	return &TurnModel{stickyActive: -1}
}

// MarkStickyActive 标记当前粘性标题对应的 turn 索引。
func (tm *TurnModel) MarkStickyActive(entryIdx int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.stickyActive = entryIdx
}

// StickyActive 返回当前粘性标题对应的 turn 索引（-1=无）。
func (tm *TurnModel) StickyActive() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.stickyActive
}

// Rebuild 从 pipeline entries 重建 turn 索引。
func (tm *TurnModel) Rebuild(p *ScrollbackPipeline) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.turns = tm.turns[:0]
	currentStart := -1
	for i, entry := range p.entries {
		if entry.Kind == BlockKindUserPrompt {
			if currentStart >= 0 {
				tm.turns = append(tm.turns, Turn{
					PromptIndex: currentStart,
					EndIndex:    i,
					Status:      TurnCompleted,
				})
			}
			currentStart = i
		}
	}
	if currentStart >= 0 {
		status := TurnCompleted
		// 检查最后一个 turn 是否正在运行
		for _, e := range p.entries[currentStart:] {
			if e.Running {
				status = TurnRunning
				break
			}
		}
		tm.turns = append(tm.turns, Turn{
			PromptIndex: currentStart,
			EndIndex:    len(p.entries),
			Status:      status,
		})
	}
}

// Turns 返回所有 turn。
func (tm *TurnModel) Turns() []Turn {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]Turn, len(tm.turns))
	copy(out, tm.turns)
	return out
}

// TimelineEntries 从 pipeline 生成时间线条目（oldest first）。
func (tm *TurnModel) TimelineEntries(p *ScrollbackPipeline) []TimelineEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var result []TimelineEntry
	for turnIdx, turn := range tm.turns {
		if turn.PromptIndex >= len(p.entries) {
			continue
		}
		entry := p.entries[turn.PromptIndex]
		preview := promptPreview(entry.Block.Summary())
		result = append(result, TimelineEntry{
			TurnIdx:       turnIdx,
			PromptEntryID: entry.ID,
			Preview:       preview,
		})
	}
	return result
}

// FindResponseAnchor 在 turn 范围内反向扫描找到第一个非空 AgentMessage。
func FindResponseAnchor(p *ScrollbackPipeline, turn Turn) (int, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var anchor int = -1
	for i := turn.EndIndex - 1; i >= turn.PromptIndex; i-- {
		if i >= len(p.entries) {
			continue
		}
		entry := p.entries[i]
		switch entry.Kind {
		case BlockKindAgentMessage:
			if msg, ok := entry.Block.(*AgentMessageBlock); ok && strings.TrimSpace(msg.Text) != "" {
				anchor = i
			}
		case BlockKindToolCall, BlockKindThinking:
			return anchor, anchor >= 0
		}
	}
	return anchor, anchor >= 0
}

// promptPreview 取首行非空文本，截断到 previewMaxChars 字符。
func promptPreview(text string) string {
	var line string
	for _, l := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			line = trimmed
			break
		}
	}
	runes := []rune(line)
	if len(runes) <= previewMaxChars {
		return line
	}
	return string(runes[:previewMaxChars-1]) + "…"
}

// ---------------------------------------------------------------------------
// Tick Rail 渲染 — 在状态行中显示 turn 进度条。
//
// 渲染为紧凑的 tick 序列：●●●○○ 或 ▎▎▎░░
// 已完成的 turn 用实心标记，正在运行的用动画标记，未开始的用空心标记。
// ---------------------------------------------------------------------------

// TickRailStyle 控制 tick rail 的渲染样式。
type TickRailStyle int

const (
	// TickRailDots 使用 ●○ 样式（默认）
	TickRailDots TickRailStyle = iota
	// TickRailBars 使用 ▎░ 样式
	TickRailBars
	// TickRailChevrons 使用 ▸▹ 样式
	TickRailChevrons
)

// RenderTickRail 渲染 turn tick rail。
// maxTicks 是最大显示的 tick 数（超出时截断两端）。
// 返回渲染后的字符串。
func (tm *TurnModel) RenderTickRail(style TickRailStyle, maxTicks int) string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if len(tm.turns) == 0 {
		return ""
	}
	if maxTicks <= 0 {
		maxTicks = 20
	}

	var done, running, pending string
	switch style {
	case TickRailBars:
		done, running, pending = "▎", "▌", "░"
	case TickRailChevrons:
		done, running, pending = "▸", "▹", "·"
	default:
		done, running, pending = "●", "◐", "○"
	}

	// 截断：如果 turn 数超过 maxTicks，只显示最后 maxTicks 个
	startIdx := 0
	if len(tm.turns) > maxTicks {
		startIdx = len(tm.turns) - maxTicks
	}

	var sb strings.Builder
	if startIdx > 0 {
		sb.WriteString("…")
	}
	for i := startIdx; i < len(tm.turns); i++ {
		switch tm.turns[i].Status {
		case TurnRunning:
			sb.WriteString(running)
		case TurnCompleted:
			sb.WriteString(done)
		default:
			sb.WriteString(pending)
		}
	}
	return sb.String()
}

// RenderTickRailWithCurrent 渲染 tick rail 并标记当前 turn。
// currentTurn 是当前选中的 turn 索引（-1 表示无选中）。
func (tm *TurnModel) RenderTickRailWithCurrent(style TickRailStyle, maxTicks, currentTurn int) string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if len(tm.turns) == 0 {
		return ""
	}
	if maxTicks <= 0 {
		maxTicks = 20
	}

	var done, running, pending, current string
	switch style {
	case TickRailBars:
		done, running, pending, current = "▎", "▌", "░", "█"
	case TickRailChevrons:
		done, running, pending, current = "▸", "▹", "·", "▶"
	default:
		done, running, pending, current = "●", "◐", "○", "◉"
	}

	startIdx := 0
	if len(tm.turns) > maxTicks {
		startIdx = len(tm.turns) - maxTicks
	}

	var sb strings.Builder
	if startIdx > 0 {
		sb.WriteString("…")
	}
	for i := startIdx; i < len(tm.turns); i++ {
		if i == currentTurn {
			sb.WriteString(current)
			continue
		}
		switch tm.turns[i].Status {
		case TurnRunning:
			sb.WriteString(running)
		case TurnCompleted:
			sb.WriteString(done)
		default:
			sb.WriteString(pending)
		}
	}
	return sb.String()
}

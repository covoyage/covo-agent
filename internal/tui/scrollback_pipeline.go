package tui

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// Scrollback Block 渲染管线。
//
// 在 covo-agent 的 TUI 层引入类型化的 Block 渲染，使得工具调用、思考过程、
// 用户消息、系统消息等可以各自有独立的渲染逻辑，而不是全部走 covonaut/tui/chat 的
// 通用 chat.ChatMessage 渲染。
//
// 架构：
//   - BlockKind 枚举：标识每种消息类型
//   - Block 接口：每种类型实现自己的 Render() 方法
//   - ScrollbackEntry：包装一个 Block + 显示状态（fold/expand/running）
//   - ScrollbackPipeline：管理 entry 列表，支持增量添加和渲染
//
// 目前不做全量替换——而是提供一套可选的高层 API，
// 业务侧可以按需用它来增强某些消息类型的展示。
// ---------------------------------------------------------------------------

// BlockKind 标识 scrollback 中的一条消息的类型。
type BlockKind int

const (
	BlockKindUserPrompt BlockKind = iota
	BlockKindAgentMessage
	BlockKindThinking
	BlockKindToolCall
	BlockKindToolResult
	BlockKindSystem
	BlockKindError
	BlockKindSessionEvent
)

func (k BlockKind) String() string {
	switch k {
	case BlockKindUserPrompt:
		return "user"
	case BlockKindAgentMessage:
		return "assistant"
	case BlockKindThinking:
		return "thinking"
	case BlockKindToolCall:
		return "tool_call"
	case BlockKindToolResult:
		return "tool_result"
	case BlockKindSystem:
		return "system"
	case BlockKindError:
		return "error"
	case BlockKindSessionEvent:
		return "session_event"
	default:
		return "unknown"
	}
}

// DisplayMode 控制 block 的展示状态。
type DisplayMode int

const (
	// DisplayNormal 正常展开
	DisplayNormal DisplayMode = iota
	// DisplayCollapsed 折叠（只显示标题行）
	DisplayCollapsed
	// DisplayExpanded 全展开（包括工具调用的输入输出）
	DisplayExpanded
)

// EntryID 唯一标识 scrollback 中的一条 entry。
type EntryID uint64

var nextEntryID uint64

// ScrollbackEntry 包装一个 Block 并携带显示状态。
type ScrollbackEntry struct {
	ID        EntryID
	Kind      BlockKind
	Block     Block
	Running   bool // 是否正在执行（如正在运行的工具调用）
	Finished  bool // 是否已完成
	StartedAt time.Time
	FinishedAt time.Time
	Display   DisplayMode
}

// Block 是任何可渲染的 scrollback 块的接口。
type Block interface {
	// RenderLines 返回该 block 的渲染行（不含 accent line）。
	// width 是可用内容宽度。
	RenderLines(width int, pal *theme.Palette) []string
	// Summary 返回单行摘要（用于折叠状态和 sticky header）。
	Summary() string
	// Kind 返回块类型。
	Kind() BlockKind
}

// ---------------------------------------------------------------------------
// 具体 Block 实现
// ---------------------------------------------------------------------------

// UserPromptBlock 用户消息。
type UserPromptBlock struct {
	Text string
}

func (b *UserPromptBlock) Kind() BlockKind { return BlockKindUserPrompt }
func (b *UserPromptBlock) Summary() string {
	text := strings.TrimSpace(b.Text)
	if len(text) > 60 {
		return text[:57] + "..."
	}
	return text
}
func (b *UserPromptBlock) RenderLines(width int, pal *theme.Palette) []string {
	lines := strings.Split(b.Text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, pal.Accent.Render("> ")+line)
	}
	return out
}

// AgentMessageBlock 助手回复。
type AgentMessageBlock struct {
	Text string
}

func (b *AgentMessageBlock) Kind() BlockKind { return BlockKindAgentMessage }
func (b *AgentMessageBlock) Summary() string {
	text := strings.TrimSpace(b.Text)
	if len(text) > 60 {
		return text[:57] + "..."
	}
	return text
}
func (b *AgentMessageBlock) RenderLines(width int, pal *theme.Palette) []string {
	return strings.Split(b.Text, "\n")
}

// ThinkingBlock 思考过程。
type ThinkingBlock struct {
	Text string
}

func (b *ThinkingBlock) Kind() BlockKind { return BlockKindThinking }
func (b *ThinkingBlock) Summary() string {
	return "💭 " + strings.TrimSpace(b.Text)
}
func (b *ThinkingBlock) RenderLines(width int, pal *theme.Palette) []string {
	return []string{pal.Dim.Render("💭 " + strings.TrimSpace(b.Text))}
}

// ToolCallBlock 工具调用。
type ToolCallBlock struct {
	ToolName string
	Args     string
	Result   string
	Error    string
}

func (b *ToolCallBlock) Kind() BlockKind { return BlockKindToolCall }
func (b *ToolCallBlock) Summary() string {
	// 工具状态映射
	status := toolStatusMessages[b.ToolName]
	if status == "" {
		status = "running " + b.ToolName + "..."
	}
	if b.Error != "" {
		return "✗ " + b.ToolName + ": " + truncateText(b.Error, 40)
	}
	if b.Result != "" {
		return "✓ " + b.ToolName
	}
	return "⚙ " + status
}
func (b *ToolCallBlock) RenderLines(width int, pal *theme.Palette) []string {
	var lines []string
	// 工具名行
	prefix := "⚙"
	style := pal.Accent
	if b.Error != "" {
		prefix = "✗"
		style = pal.Error
	} else if b.Result != "" {
		prefix = "✓"
		style = pal.Success
	}
	lines = append(lines, style.Render(fmt.Sprintf("%s %s", prefix, b.ToolName)))

	// 参数行（折叠模式下不显示）
	if b.Args != "" {
		argPreview := truncateText(b.Args, int(width)-4)
		lines = append(lines, pal.Dim.Render("  "+argPreview))
	}

	// 结果行
	if b.Result != "" {
		resultPreview := truncateText(b.Result, int(width)-4)
		lines = append(lines, pal.Dim.Render("  → "+resultPreview))
	}

	// 错误行
	if b.Error != "" {
		errPreview := truncateText(b.Error, int(width)-4)
		lines = append(lines, pal.Error.Render("  ✗ "+errPreview))
	}

	return lines
}

// SystemBlock 系统消息。
type SystemBlock struct {
	Text string
}

func (b *SystemBlock) Kind() BlockKind { return BlockKindSystem }
func (b *SystemBlock) Summary() string { return b.Text }
func (b *SystemBlock) RenderLines(width int, pal *theme.Palette) []string {
	return []string{pal.Dim.Render("── " + b.Text + " ──")}
}

// ErrorBlock 错误消息。
type ErrorBlock struct {
	Text string
}

func (b *ErrorBlock) Kind() BlockKind { return BlockKindError }
func (b *ErrorBlock) Summary() string { return "✗ " + b.Text }
func (b *ErrorBlock) RenderLines(width int, pal *theme.Palette) []string {
	return []string{pal.Error.Render("✗ " + b.Text)}
}

// SessionEventBlock 会话事件（如模式切换、YOLO 开关）。
type SessionEventBlock struct {
	Text string
}

func (b *SessionEventBlock) Kind() BlockKind { return BlockKindSessionEvent }
func (b *SessionEventBlock) Summary() string { return b.Text }
func (b *SessionEventBlock) RenderLines(width int, pal *theme.Palette) []string {
	return []string{pal.Dim.Render("• " + b.Text)}
}

// ---------------------------------------------------------------------------
// ScrollbackPipeline — 管理 entry 列表的渲染管线
// ---------------------------------------------------------------------------

// ScrollbackPipeline 是 scrollback entry 的管理器。
type ScrollbackPipeline struct {
	mu               sync.RWMutex
	entries          []*ScrollbackEntry
	running          map[EntryID]bool
	contentGeneration uint64 // 每次 Append/Clear 递增——搜索索引用它检测变更
}

// NewScrollbackPipeline 创建空管线。
func NewScrollbackPipeline() *ScrollbackPipeline {
	return &ScrollbackPipeline{
		running: make(map[EntryID]bool),
	}
}

// Append 添加一个新 entry。
func (p *ScrollbackPipeline) Append(block Block) *ScrollbackEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.contentGeneration++
	id := EntryID(atomic.AddUint64(&nextEntryID, 1))
	entry := &ScrollbackEntry{
		ID:        id,
		Kind:      block.Kind(),
		Block:     block,
		StartedAt: time.Now(),
		Display:   DisplayNormal,
	}
	p.entries = append(p.entries, entry)
	return entry
}

// StartRunning 标记 entry 为正在执行。
func (p *ScrollbackPipeline) StartRunning(entry *ScrollbackEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry.Running = true
	p.running[entry.ID] = true
}

// FinishRunning 标记 entry 为完成。
func (p *ScrollbackPipeline) FinishRunning(entry *ScrollbackEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry.Running = false
	entry.Finished = true
	entry.FinishedAt = time.Now()
	delete(p.running, entry.ID)
}

// Entries 返回所有 entry 的快照。
func (p *ScrollbackPipeline) Entries() []*ScrollbackEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*ScrollbackEntry, len(p.entries))
	copy(out, p.entries)
	return out
}

// RunningEntries 返回正在运行的 entry。
func (p *ScrollbackPipeline) RunningEntries() []*ScrollbackEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*ScrollbackEntry, 0, len(p.running))
	for _, e := range p.entries {
		if e.Running {
			out = append(out, e)
		}
	}
	return out
}

// RenderAll 渲染全部 entry 到行列表。
func (p *ScrollbackPipeline) RenderAll(width int, pal *theme.Palette) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var lines []string
	for _, entry := range p.entries {
		// accent line
		lines = append(lines, renderAccentLine(entry, pal))
		// block lines
		lines = append(lines, entry.Block.RenderLines(width, pal)...)
		// 空行分隔
		lines = append(lines, "")
	}
	return lines
}

// RenderAllWithFlash 渲染全部 entry，使用 FinishFlashTracker 增强 accent line。
func (p *ScrollbackPipeline) RenderAllWithFlash(width int, pal *theme.Palette, tracker *FinishFlashTracker) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var lines []string
	for _, entry := range p.entries {
		// accent line（带闪烁效果）
		if tracker != nil {
			lines = append(lines, tracker.RenderFlashAccent(entry, pal))
		} else {
			lines = append(lines, renderAccentLine(entry, pal))
		}
		// block lines
		lines = append(lines, entry.Block.RenderLines(width, pal)...)
		// 空行分隔
		lines = append(lines, "")
	}
	return lines
}

// RenderAllWithFlashAndSearch 渲染全部 entry，同时应用 finish flash 和 search 高亮。
func (p *ScrollbackPipeline) RenderAllWithFlashAndSearch(width int, pal *theme.Palette, tracker *FinishFlashTracker, search *SearchState) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var lines []string
	for entryIdx, entry := range p.entries {
		// accent line（带闪烁效果）
		if tracker != nil {
			lines = append(lines, tracker.RenderFlashAccent(entry, pal))
		} else {
			lines = append(lines, renderAccentLine(entry, pal))
		}
		// block lines（带搜索高亮）
		blockLines := entry.Block.RenderLines(width, pal)
		if search != nil && search.IsActive() {
			for lineIdx, bl := range blockLines {
				blockLines[lineIdx] = search.HighlightLine(bl, entry.ID, lineIdx, pal)
			}
		}
		lines = append(lines, blockLines...)
		// 空行分隔
		lines = append(lines, "")
		_ = entryIdx
	}
	return lines
}

// RenderRunning 渲染正在运行的 entry（用于状态行）。
func (p *ScrollbackPipeline) RenderRunning(pal *theme.Palette) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var lines []string
	for _, entry := range p.entries {
		if entry.Running {
			lines = append(lines, entry.Block.Summary())
		}
	}
	return lines
}

// Clear 清空所有 entry。
func (p *ScrollbackPipeline) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = nil
	p.running = make(map[EntryID]bool)
	p.contentGeneration++
}

// FromAgentMessage 将 agentcore.Message 转换为 Block。
func FromAgentMessage(msg agentcore.Message) Block {
	switch msg.Role {
	case agentcore.RoleUser:
		return &UserPromptBlock{Text: msg.Content}
	case agentcore.RoleAssistant:
		if len(msg.ToolCalls) > 0 && msg.Content == "" {
			tc := msg.ToolCalls[0]
			return &ToolCallBlock{
				ToolName: tc.Name,
				Args:     tc.Arguments,
			}
		}
		return &AgentMessageBlock{Text: msg.Content}
	case agentcore.RoleTool:
		return &ToolResultBlockAdapter{ToolName: msg.ToolCallID, Result: msg.Content}
	case agentcore.RoleSystem:
		return &SystemBlock{Text: msg.Content}
	default:
		return &SystemBlock{Text: msg.Content}
	}
}

// ToolResultBlockAdapter 适配工具结果。
type ToolResultBlockAdapter struct {
	ToolName string
	Result   string
	Error    string
}

func (b *ToolResultBlockAdapter) Kind() BlockKind { return BlockKindToolResult }
func (b *ToolResultBlockAdapter) Summary() string {
	if b.Error != "" {
		return "✗ " + b.ToolName + ": " + truncateText(b.Error, 40)
	}
	return "→ " + b.ToolName
}
func (b *ToolResultBlockAdapter) RenderLines(width int, pal *theme.Palette) []string {
	style := pal.Dim
	if b.Error != "" {
		style = pal.Error
	}
	return []string{style.Render("  → " + truncateText(b.Result, int(width)-4))}
}

// --- helpers ---

// renderAccentLine 返回 entry 的 accent line（左侧竖线）。
func renderAccentLine(entry *ScrollbackEntry, pal *theme.Palette) string {
	switch entry.Kind {
	case BlockKindUserPrompt:
		return pal.Accent.Render("┃")
	case BlockKindAgentMessage:
		return pal.Assistant.Render("┃")
	case BlockKindThinking:
		return pal.Dim.Render("┊")
	case BlockKindToolCall:
		if entry.Running {
			return pal.Accent.Render("┃")
		}
		if entry.Block.(*ToolCallBlock).Error != "" {
			return pal.Error.Render("┃")
		}
		return pal.Success.Render("┃")
	case BlockKindSystem:
		return pal.Dim.Render("┄")
	case BlockKindError:
		return pal.Error.Render("┃")
	case BlockKindSessionEvent:
		return pal.Dim.Render("┄")
	default:
		return pal.Dim.Render("┃")
	}
}

func truncateText(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen-3]) + "..."
}

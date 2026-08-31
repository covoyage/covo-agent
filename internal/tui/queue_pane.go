package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/tui/core"
	"github.com/covoyage/covonaut/tui/terminal"
	"github.com/covoyage/covonaut/tui/theme"

	"github.com/covoyage/covo-agent/internal/promptqueue"
)

// ---------------------------------------------------------------------------
// QueuePane — promptqueue.Queue 的 TUI 面板。
//
// 渲染排队中的 prompt 列表，支持通过 ShowPanel 显示为 overlay。
// 每条 entry 显示序号、预览文本、排队时间。
// ---------------------------------------------------------------------------

// QueuePane 渲染 prompt queue 的面板组件。
type QueuePane struct {
	mu         sync.RWMutex
	queue      *promptqueue.Queue
	maxVisible int
	selected   int
	onSendNow  func(promptqueue.Entry)
	onRemove   func(promptqueue.Entry)
	onClose    func()
}

// NewQueuePane 创建队列面板。
func NewQueuePane(queue *promptqueue.Queue) *QueuePane {
	return &QueuePane{
		queue:      queue,
		maxVisible: 20,
	}
}

// SetOnSendNow is called when Enter sends the selected entry now.
func (qp *QueuePane) SetOnSendNow(fn func(promptqueue.Entry)) {
	qp.mu.Lock()
	qp.onSendNow = fn
	qp.mu.Unlock()
}

// SetOnRemove is called when Delete removes the selected entry.
func (qp *QueuePane) SetOnRemove(fn func(promptqueue.Entry)) {
	qp.mu.Lock()
	qp.onRemove = fn
	qp.mu.Unlock()
}

// SetOnClose is called when Esc closes the pane.
func (qp *QueuePane) SetOnClose(fn func()) {
	qp.mu.Lock()
	qp.onClose = fn
	qp.mu.Unlock()
}

// Render 实现 core.Component。渲染队列内容。
func (qp *QueuePane) Render(width int64) []string {
	qp.mu.RLock()
	defer qp.mu.RUnlock()

	pal := theme.CurrentPalette()

	if qp.queue == nil || qp.queue.IsEmpty() {
		return []string{
			pal.Dim.Render("  Queue is empty"),
			"",
			pal.Dim.Render("  Prompts queued while the agent is busy"),
			pal.Dim.Render("  will appear here."),
		}
	}

	entries := qp.queue.All()
	var lines []string

	// Header
	lines = append(lines, pal.Accent.Render(fmt.Sprintf("┃ Prompt Queue (%d)", len(entries))))
	lines = append(lines, pal.Dim.Render(strings.Repeat("─", int(width)-2)))

	for i, entry := range entries {
		if i >= qp.maxVisible {
			remaining := len(entries) - qp.maxVisible
			lines = append(lines, pal.Dim.Render(fmt.Sprintf("  ... and %d more", remaining)))
			break
		}

		// 序号标记
		marker := "▸"
		style := pal.Muted
		if i == qp.selected {
			marker = "▶"
			style = pal.Accent
		}

		// 预览文本（rune 安全截断到 width-6）
		preview := strings.ReplaceAll(entry.Text, "\n", " ")
		maxRunes := int(width) - 6
		if maxRunes < 10 {
			maxRunes = 10
		}
		if runeLen := len([]rune(preview)); runeLen > maxRunes {
			preview = string([]rune(preview)[:maxRunes-3]) + "..."
		}

		// 时间标记
		ageStr := formatQueueAge(entry.QueuedAt)
		combinedTag := ""
		if entry.Combined {
			combinedTag = pal.Dim.Render(" [merged]")
		}

		lines = append(lines, fmt.Sprintf("%s %s %s%s",
			style.Render(marker),
			style.Render(fmt.Sprintf("%2d", i+1)),
			preview,
			combinedTag,
		))
		lines = append(lines, pal.Dim.Render(fmt.Sprintf("    queued %s", ageStr)))
	}

	// Footer
	lines = append(lines, "")
	lines = append(lines, pal.Dim.Render("  ↑/↓ navigate · Enter send · Del remove · Esc close"))

	return lines
}

// Invalidate 实现 core.Component。
func (qp *QueuePane) Invalidate() {}

// Update implements core.Updatable.
func (qp *QueuePane) Update(msg core.Msg) core.Cmd {
	key, ok := msg.(core.KeyMsg)
	if !ok {
		return nil
	}
	qp.mu.Lock()
	queue := qp.queue
	onSendNow := qp.onSendNow
	onRemove := qp.onRemove
	onClose := qp.onClose
	selected := qp.selected
	qp.mu.Unlock()
	if queue == nil {
		return nil
	}
	entries := queue.All()
	n := len(entries)
	if n == 0 {
		if terminal.MatchesKey(key.Data, "escape") && onClose != nil {
			onClose()
		}
		return nil
	}
	if selected >= n {
		selected = n - 1
	}
	if selected < 0 {
		selected = 0
	}
	switch {
	case terminal.MatchesKey(key.Data, "up"), terminal.MatchesKey(key.Data, "ctrl+p"):
		if selected > 0 {
			selected--
		}
	case terminal.MatchesKey(key.Data, "down"), terminal.MatchesKey(key.Data, "ctrl+n"):
		if selected < n-1 {
			selected++
		}
	case terminal.MatchesKey(key.Data, "enter"):
		if onSendNow != nil {
			onSendNow(entries[selected])
		}
	case terminal.MatchesKey(key.Data, "delete"), terminal.MatchesKey(key.Data, "backspace"):
		if onRemove != nil {
			onRemove(entries[selected])
			if selected >= queue.Len() && selected > 0 {
				selected--
			}
		}
	case terminal.MatchesKey(key.Data, "escape"):
		if onClose != nil {
			onClose()
		}
	}
	qp.mu.Lock()
	qp.selected = selected
	qp.mu.Unlock()
	return nil
}

var _ core.Component = (*QueuePane)(nil)
var _ core.Updatable = (*QueuePane)(nil)

// SetMaxVisible 设置最大可见条目数。
func (qp *QueuePane) SetMaxVisible(n int) {
	qp.mu.Lock()
	defer qp.mu.Unlock()
	qp.maxVisible = n
}

// formatQueueAge 格式化排队时长。
func formatQueueAge(queuedAt time.Time) string {
	age := time.Since(queuedAt)
	switch {
	case age < 5*time.Second:
		return "just now"
	case age < time.Minute:
		return fmt.Sprintf("%ds ago", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	}
}

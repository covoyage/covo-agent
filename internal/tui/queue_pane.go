package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

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
}

// NewQueuePane 创建队列面板。
func NewQueuePane(queue *promptqueue.Queue) *QueuePane {
	return &QueuePane{
		queue:      queue,
		maxVisible: 20,
	}
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
		if i == 0 {
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

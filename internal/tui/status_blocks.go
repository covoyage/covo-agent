package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// 状态栏块系统。
//
// /queue、/tasks、/usage 命令的输出作为纯文本 Block 提交到 ScrollbackPipeline。
// 在最小模式（无交互面板）下，这是主要的检查面。
//
// 架构：
//   - QueueBlock：队列状态纯文本 block
//   - TasksBlock：任务状态纯文本 block
//   - UsageBlock：用量统计纯文本 block
// ---------------------------------------------------------------------------

// QueueBlock 队列状态 block。
type QueueBlock struct {
	Prompts []string // 排队的 prompt 文本（不含当前正在执行的）
}

func (b *QueueBlock) Kind() BlockKind { return BlockKindSystem }
func (b *QueueBlock) Summary() string {
	if len(b.Prompts) == 0 {
		return "Queue is empty."
	}
	return fmt.Sprintf("Queued prompts (%d):", len(b.Prompts))
}
func (b *QueueBlock) RenderLines(width int, pal *theme.Palette) []string {
	// pal 可能为 nil（测试环境），用安全访问
	dimRender := func(s string) string {
		if pal != nil {
			return pal.Dim.Render(s)
		}
		return s
	}
	accentRender := func(s string) string {
		if pal != nil {
			return pal.Accent.Render(s)
		}
		return s
	}
	if len(b.Prompts) == 0 {
		return []string{dimRender("Queue is empty.")}
	}
	lines := []string{accentRender(fmt.Sprintf("Queued prompts (%d):", len(b.Prompts)))}
	for i, prompt := range b.Prompts {
		preview := truncateText(prompt, width-6)
		lines = append(lines, dimRender(fmt.Sprintf("  %d. %s", i+1, preview)))
	}
	return lines
}

// TasksBlock 任务状态 block。
type TaskEntry struct {
	Name     string
	Status   string // running / completed / failed
	Duration time.Duration
}

type TasksBlock struct {
	Tasks []TaskEntry
}

func (b *TasksBlock) Kind() BlockKind { return BlockKindSystem }
func (b *TasksBlock) Summary() string {
	active := 0
	for _, t := range b.Tasks {
		if t.Status == "running" {
			active++
		}
	}
	if active > 0 {
		return fmt.Sprintf("Tasks: %d running", active)
	}
	return fmt.Sprintf("Tasks: %d total", len(b.Tasks))
}
func (b *TasksBlock) RenderLines(width int, pal *theme.Palette) []string {
	safeRender := func(s string) string {
		if pal != nil {
			return pal.Dim.Render(s)
		}
		return s
	}
	accentRender := func(s string) string {
		if pal != nil {
			return pal.Accent.Render(s)
		}
		return s
	}
	successRender := func(s string) string {
		if pal != nil {
			return pal.Success.Render(s)
		}
		return s
	}
	errorRender := func(s string) string {
		if pal != nil {
			return pal.Error.Render(s)
		}
		return s
	}
	if len(b.Tasks) == 0 {
		return []string{safeRender("No tasks.")}
	}
	lines := []string{accentRender("Tasks:")}
	for _, task := range b.Tasks {
		statusIcon := "•"
		render := safeRender
		switch task.Status {
		case "running":
			statusIcon = "⚙"
			render = accentRender
		case "completed":
			statusIcon = "✓"
			render = successRender
		case "failed":
			statusIcon = "✗"
			render = errorRender
		}
		name := truncateText(task.Name, width-20)
		line := fmt.Sprintf("  %s %s", statusIcon, name)
		if task.Duration > 0 {
			line += fmt.Sprintf(" (%s)", formatDuration(task.Duration))
		}
		lines = append(lines, render(line))
	}
	return lines
}

// UsageBlock 用量统计 block。
type UsageBlock struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Model        string
	Provider     string
	TurnCount    int
}

func (b *UsageBlock) Kind() BlockKind { return BlockKindSystem }
func (b *UsageBlock) Summary() string {
	return fmt.Sprintf("Usage: %s/%s — %d turns, %d tokens",
		b.Provider, b.Model, b.TurnCount, b.TotalTokens)
}
func (b *UsageBlock) RenderLines(width int, pal *theme.Palette) []string {
	dim := func(s string) string {
		if pal != nil {
			return pal.Dim.Render(s)
		}
		return s
	}
	accent := func(s string) string {
		if pal != nil {
			return pal.Accent.Render(s)
		}
		return s
	}
	return []string{
		accent("── Usage ──"),
		dim(fmt.Sprintf("  Model:       %s/%s", b.Provider, b.Model)),
		dim(fmt.Sprintf("  Turns:       %d", b.TurnCount)),
		dim(fmt.Sprintf("  Input:       %s tokens", groupThousands(b.InputTokens))),
		dim(fmt.Sprintf("  Output:      %s tokens", groupThousands(b.OutputTokens))),
		dim(fmt.Sprintf("  Total:       %s tokens", groupThousands(b.TotalTokens))),
	}
}

// formatDuration 格式化持续时间。
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

// groupThousands 每 3 位加逗号。
func groupThousands(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var sb strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		sb.WriteString(s[:rem])
		if len(s) > rem {
			sb.WriteByte(',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		sb.WriteString(s[i : i+3])
		if i+3 < len(s) {
			sb.WriteByte(',')
		}
	}
	return sb.String()
}

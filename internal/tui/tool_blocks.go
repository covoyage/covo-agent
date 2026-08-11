package tui

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// 工具特化 Block — 为常用工具提供定制化渲染。
//
// 为常用工具（Edit/Execute/Read/Search）提供独立 block 类型，支持语法高亮、
// diff 渲染和文件路径展示。NewToolBlock 根据工具名选择合适的 block。
// ---------------------------------------------------------------------------

// EditToolBlock 文件编辑工具，支持 diff 渲染。
type EditToolBlock struct {
	FilePath   string
	OldContent string
	NewContent string
	DiffText   string
	Result     string
	Error      string
}

func (b *EditToolBlock) Kind() BlockKind { return BlockKindToolCall }
func (b *EditToolBlock) Summary() string {
	if b.Error != "" {
		return "✗ edit " + b.FilePath + ": " + truncateText(b.Error, 30)
	}
	if b.Result != "" {
		return "✓ edit " + b.FilePath
	}
	return "✎ editing " + b.FilePath
}
func (b *EditToolBlock) RenderLines(width int, pal *theme.Palette) []string {
	var lines []string
	prefix := "✎"
	style := pal.Accent
	if b.Error != "" {
		prefix = "✗"
		style = pal.Error
	} else if b.Result != "" {
		prefix = "✓"
		style = pal.Success
	}
	lines = append(lines, style.Render(fmt.Sprintf("%s edit %s", prefix, b.FilePath)))

	// diff 渲染
	if b.DiffText != "" {
		diffLines := strings.Split(b.DiffText, "\n")
		maxLines := 15
		for i, dl := range diffLines {
			if i >= maxLines {
				remaining := len(diffLines) - maxLines
				lines = append(lines, pal.Dim.Render(fmt.Sprintf("  ... %d more diff lines", remaining)))
				break
			}
			lines = append(lines, renderDiffLine(dl, pal))
		}
	}

	if b.Error != "" {
		lines = append(lines, pal.Error.Render("  ✗ "+truncateText(b.Error, int(width)-4)))
	}
	return lines
}

// ExecuteToolBlock shell 命令执行。
type ExecuteToolBlock struct {
	Command  string
	Output   string
	ExitCode int
	Error    string
}

func (b *ExecuteToolBlock) Kind() BlockKind { return BlockKindToolCall }
func (b *ExecuteToolBlock) Summary() string {
	if b.Error != "" {
		return "✗ bash: " + truncateText(b.Command, 40)
	}
	if b.Output != "" {
		return "✓ bash: " + truncateText(b.Command, 40)
	}
	return "▶ bash: " + truncateText(b.Command, 40)
}
func (b *ExecuteToolBlock) RenderLines(width int, pal *theme.Palette) []string {
	var lines []string
	prefix := "▶"
	style := pal.Accent
	if b.Error != "" || b.ExitCode != 0 {
		prefix = "✗"
		style = pal.Error
	} else if b.Output != "" {
		prefix = "✓"
		style = pal.Success
	}
	lines = append(lines, style.Render(fmt.Sprintf("%s %s", prefix, b.Command)))

	// 输出预览（最多 8 行）
	if b.Output != "" {
		outLines := strings.Split(b.Output, "\n")
		maxLines := 8
		for i, ol := range outLines {
			if i >= maxLines {
				remaining := len(outLines) - maxLines
				lines = append(lines, pal.Dim.Render(fmt.Sprintf("  ... %d more lines", remaining)))
				break
			}
			lines = append(lines, pal.Dim.Render("  "+truncateText(ol, int(width)-4)))
		}
	}
	if b.Error != "" {
		lines = append(lines, pal.Error.Render("  ✗ "+truncateText(b.Error, int(width)-4)))
	}
	return lines
}

// ReadToolBlock 文件读取。
type ReadToolBlock struct {
	FilePath  string
	LineCount int
	Preview   string
	Error     string
}

func (b *ReadToolBlock) Kind() BlockKind { return BlockKindToolCall }
func (b *ReadToolBlock) Summary() string {
	if b.Error != "" {
		return "✗ read " + b.FilePath
	}
	return fmt.Sprintf("📖 read %s (%d lines)", b.FilePath, b.LineCount)
}
func (b *ReadToolBlock) RenderLines(width int, pal *theme.Palette) []string {
	var lines []string
	if b.Error != "" {
		lines = append(lines, pal.Error.Render(fmt.Sprintf("✗ read %s", b.FilePath)))
		lines = append(lines, pal.Error.Render("  "+truncateText(b.Error, int(width)-4)))
		return lines
	}
	lines = append(lines, pal.Success.Render(fmt.Sprintf("📖 read %s (%d lines)", b.FilePath, b.LineCount)))
	if b.Preview != "" {
		lines = append(lines, pal.Dim.Render("  "+truncateText(b.Preview, int(width)-4)))
	}
	return lines
}

// SearchToolBlock 代码搜索。
type SearchToolBlock struct {
	Pattern string
	Path    string
	Matches []SearchMatch
	Error   string
}

// SearchMatch 单个搜索匹配。
type SearchMatch struct {
	FilePath string
	LineNum  int
	LineText string
}

func (b *SearchToolBlock) Kind() BlockKind { return BlockKindToolCall }
func (b *SearchToolBlock) Summary() string {
	if b.Error != "" {
		return "✗ search: " + truncateText(b.Pattern, 30)
	}
	return fmt.Sprintf("🔍 search \"%s\" → %d matches", truncateText(b.Pattern, 30), len(b.Matches))
}
func (b *SearchToolBlock) RenderLines(width int, pal *theme.Palette) []string {
	var lines []string
	if b.Error != "" {
		lines = append(lines, pal.Error.Render(fmt.Sprintf("✗ search \"%s\"", b.Pattern)))
		lines = append(lines, pal.Error.Render("  "+truncateText(b.Error, int(width)-4)))
		return lines
	}
	lines = append(lines, pal.Accent.Render(fmt.Sprintf("🔍 search \"%s\"", b.Pattern)))
	if b.Path != "" {
		lines = append(lines, pal.Dim.Render("  in "+b.Path))
	}
	maxMatches := 5
	for i, m := range b.Matches {
		if i >= maxMatches {
			remaining := len(b.Matches) - maxMatches
			lines = append(lines, pal.Dim.Render(fmt.Sprintf("  ... %d more matches", remaining)))
			break
		}
		lines = append(lines, pal.Dim.Render(fmt.Sprintf("  %s:%d: %s", m.FilePath, m.LineNum, truncateText(m.LineText, int(width)-20))))
	}
	return lines
}

// NewToolBlock 根据工具名创建合适的特化 block。
// 未知工具回退到通用 ToolCallBlock。
func NewToolBlock(toolName, args string) Block {
	switch {
	case strings.Contains(toolName, "edit") || strings.Contains(toolName, "write") || strings.Contains(toolName, "patch"):
		return &EditToolBlock{FilePath: extractFilePath(args)}
	case strings.Contains(toolName, "bash") || strings.Contains(toolName, "exec") || strings.Contains(toolName, "shell"):
		return &ExecuteToolBlock{Command: args}
	case strings.Contains(toolName, "read") || strings.Contains(toolName, "cat"):
		return &ReadToolBlock{FilePath: extractFilePath(args)}
	case strings.Contains(toolName, "search") || strings.Contains(toolName, "grep") || strings.Contains(toolName, "find"):
		return &SearchToolBlock{Pattern: args}
	default:
		return &ToolCallBlock{ToolName: toolName, Args: args}
	}
}

// extractFilePath 从工具参数中提取文件路径（简化版）。
func extractFilePath(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	// 尝试 JSON 解析
	if strings.HasPrefix(args, "{") {
		// 简化：取第一个引号对中的内容
		idx := strings.Index(args, `"file"`)
		if idx < 0 {
			idx = strings.Index(args, `"path"`)
		}
		if idx >= 0 {
			rest := args[idx:]
			colon := strings.Index(rest, ":")
			if colon >= 0 {
				rest = rest[colon:]
				start := strings.Index(rest, `"`)
				if start >= 0 {
					rest = rest[start+1:]
					end := strings.Index(rest, `"`)
					if end >= 0 {
						return rest[:end]
					}
				}
			}
		}
	}
	// 非 JSON：取第一个 token
	parts := strings.Fields(args)
	if len(parts) > 0 {
		return parts[0]
	}
	return args
}

// renderDiffLine 渲染单行 diff。
func renderDiffLine(line string, pal *theme.Palette) string {
	if len(line) == 0 {
		return ""
	}
	switch line[0] {
	case '+':
		return pal.Success.Render("  " + line)
	case '-':
		return pal.Error.Render("  " + line)
	case '@':
		return pal.Accent.Render("  " + line)
	case ' ':
		return pal.Dim.Render("  " + line)
	default:
		return pal.Dim.Render("  " + line)
	}
}

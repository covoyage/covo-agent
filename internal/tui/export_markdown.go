package tui

import (
	"strings"
)

// ---------------------------------------------------------------------------
// 对话导出为 Markdown。
//
// 把 ScrollbackPipeline 的 Block 列表转为干净的 Markdown 文档：
//   - ## User 分节（用户原始输入）
//   - ## Assistant 分节（助手回复，连续消息合并到同一 header）
//   - ## Tools 分节（每个工具调用一行摘要）
//
// 跳过 thinking / system / session_event 等非对话内容。
// ---------------------------------------------------------------------------

// ExportToMarkdown 将 pipeline entries 导出为 Markdown 文本。
func ExportToMarkdown(p *ScrollbackPipeline) string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var sb strings.Builder
	lastWasAgent := false
	inToolsSection := false

	for _, entry := range p.entries {
		switch entry.Kind {
		case BlockKindUserPrompt:
			if inToolsSection {
				sb.WriteByte('\n')
				inToolsSection = false
			}
			sb.WriteString("## User\n\n")
			if up, ok := entry.Block.(*UserPromptBlock); ok {
				sb.WriteString(up.Text)
			}
			sb.WriteString("\n\n")
			lastWasAgent = false

		case BlockKindAgentMessage:
			if !lastWasAgent {
				if inToolsSection {
					sb.WriteByte('\n')
					inToolsSection = false
				}
				sb.WriteString("## Assistant\n\n")
			}
			if am, ok := entry.Block.(*AgentMessageBlock); ok {
				sb.WriteString(am.Text)
				sb.WriteString("\n\n")
			}
			lastWasAgent = true

		case BlockKindToolCall:
			if !inToolsSection {
				sb.WriteString("## Tools\n\n")
				inToolsSection = true
			}
			sb.WriteString("- ")
			sb.WriteString(entry.Block.Summary())
			sb.WriteByte('\n')
			lastWasAgent = false

		case BlockKindError:
			if inToolsSection {
				sb.WriteByte('\n')
				inToolsSection = false
			}
			sb.WriteString("## Error\n\n")
			if eb, ok := entry.Block.(*ErrorBlock); ok {
				sb.WriteString(eb.Text)
			} else {
				sb.WriteString(entry.Block.Summary())
			}
			sb.WriteString("\n\n")
			lastWasAgent = false

		// 跳过非对话内容
		case BlockKindThinking, BlockKindSystem, BlockKindSessionEvent, BlockKindToolResult:
			continue
		}
	}

	return sb.String()
}

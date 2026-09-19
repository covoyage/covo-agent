package agent

import (
	"context"
	"strings"
	"unicode"

	"github.com/covoyage/covonaut/agentcore"
)

const followUpSystemPrompt = `The user will click one line and send it as their next message.

Write exactly 3 lines. Each line is one self-contained request in the user's language.
Name the outcome and the target. Do not name the steps. Do not recap the answer.

Rules:
- First-person or imperative, like "对比权限模型" or "补上还缺的测试"
- 8-72 characters, one line each
- No markdown, headings, bullets, numbers, labels, quotes, or leftover **
- Do not copy titles, file counts, crate counts, or sentences from the assistant
- Skip the task if there is no sensible next step; then return nothing

Good:
对比 covo 和 Codex 的权限模型
补一份还缺的测试清单
把构建命令写进 AGENTS.md
Bad: 111 个 Cargo crate**, 4,464 个 Rust 文件
Bad: ## 项目分析: OpenAI Codex CLI
Bad: 总代码量约 **`

// GenerateFollowUps asks a lightweight model for the next user prompts.
// Returns nil when generation is skipped or fails; callers should fall back
// to static suggestions.
func GenerateFollowUps(ctx context.Context, ac *AuxiliaryClient, messages []agentcore.Message) []string {
	if ac == nil {
		return nil
	}
	user, assistant := lastExchange(messages)
	if strings.TrimSpace(user) == "" && strings.TrimSpace(assistant) == "" {
		return nil
	}
	raw, err := ac.Complete(ctx, TaskTitle, followUpSystemPrompt, formatFollowUpInput(user, assistant), 160, 0.2)
	if err != nil {
		return nil
	}
	out := filterCopiedFollowUps(ParseFollowUpLines(raw), user, assistant)
	if len(out) < 2 {
		return nil
	}
	return out
}

func formatFollowUpInput(user, assistant string) string {
	return "User request:\n" + clipFollowUpSnippet(user, 400) +
		"\n\nDo not recap this answer. Suggest the user's next requests:\n" +
		clipFollowUpSnippet(stripAssistantForFollowUp(assistant), 240)
}

func stripAssistantForFollowUp(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}
		if strings.Contains(line, "**") || looksLikeStatsLine(line) {
			continue
		}
		kept = append(kept, line)
		if len(kept) == 6 {
			break
		}
	}
	return strings.Join(kept, "\n")
}

func clipFollowUpSnippet(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return s
}

func lastExchange(messages []agentcore.Message) (user, assistant string) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		switch msg.Role {
		case agentcore.RoleAssistant:
			if assistant == "" {
				assistant = text
			}
		case agentcore.RoleUser:
			if user == "" {
				user = text
			}
		}
		if user != "" && assistant != "" {
			break
		}
	}
	return user, assistant
}

// ParseFollowUpLines extracts up to three follow-up prompts from model output.
func ParseFollowUpLines(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "**") {
			continue
		}
		line = cleanFollowUpLine(line)
		if !validFollowUpLine(line) {
			continue
		}
		key := strings.ToLower(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, line)
		if len(out) == 3 {
			break
		}
	}
	return out
}

func cleanFollowUpLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "`\"'")
	line = stripFollowUpPrefix(line)
	line = strings.TrimLeft(line, "#*_~ ")
	return strings.TrimSpace(line)
}

func validFollowUpLine(line string) bool {
	if line == "" {
		return false
	}
	n := utf8Len(line)
	if n < 8 || n > 72 {
		return false
	}
	if strings.ContainsAny(line, "\t*") {
		return false
	}
	if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		return false
	}
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "user:") || strings.HasPrefix(lower, "assistant:") {
		return false
	}
	if looksLikeStatsLine(line) {
		return false
	}
	return true
}

func looksLikeStatsLine(line string) bool {
	digits := 0
	for _, r := range line {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits >= 4 {
		return true
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "crate") || strings.Contains(lower, "loc") {
		return digits >= 2
	}
	if strings.Contains(line, "个") && digits >= 2 {
		return true
	}
	return strings.HasSuffix(strings.TrimSpace(line), "**")
}

func filterCopiedFollowUps(lines []string, user, assistant string) []string {
	source := strings.ToLower(user + "\n" + assistant)
	var out []string
	for _, line := range lines {
		if looksCopiedFrom(line, source) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func looksCopiedFrom(line, source string) bool {
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" || source == "" {
		return false
	}
	if strings.Contains(source, line) {
		return true
	}
	runes := []rune(line)
	if len(runes) >= 16 {
		head := string(runes[:16])
		if strings.Contains(source, head) {
			return true
		}
	}
	return false
}

func stripFollowUpPrefix(line string) string {
	line = strings.TrimLeftFunc(line, unicode.IsSpace)
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return strings.TrimSpace(line[2:])
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i < len(line) {
		rest := strings.TrimSpace(line[i:])
		if strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, ")") {
			return strings.TrimSpace(rest[1:])
		}
	}
	return line
}

func utf8Len(s string) int {
	return len([]rune(s))
}

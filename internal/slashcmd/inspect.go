package slashcmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/i18n"
)

func handleTools(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	info := covoAgent.InspectTools()
	sctx.UI.App.PrintSystem(formatToolInspect(info))
	return true
}

func handleContext(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	mode := "list"
	if len(parts) > 1 {
		mode = strings.ToLower(parts[1])
	}
	switch mode {
	case "list", "map", "detail":
	default:
		sctx.UI.App.PrintSystem(i18n.T("system.context_usage"))
		return true
	}
	sctx.UI.App.PrintSystem(formatContextInspect(covoAgent.InspectContext(), mode))
	return true
}

func formatToolInspect(info agent.ToolInspect) string {
	var b strings.Builder
	phase := "act"
	if info.PlanMode {
		phase = "plan"
	}
	lean := "off"
	if info.LeanMode {
		lean = "on"
	}
	side := "off"
	if info.SideFilter {
		side = "on"
	}
	fmt.Fprintf(&b, "── Tools (%d effective) ──\n", len(info.Kept))
	fmt.Fprintf(&b, "profile=%s  phase=%s  lean=%s  side=%s", info.Profile, phase, lean, side)
	if info.Platform != "" {
		fmt.Fprintf(&b, "  platform=%s", info.Platform)
	}
	b.WriteByte('\n')
	if len(info.Toolsets) > 0 {
		fmt.Fprintf(&b, "toolsets: %s\n", strings.Join(info.Toolsets, ", "))
	}
	if info.PlanMode {
		b.WriteString("plan mode: mutating tools hidden (use /act to restore)\n")
	}
	if info.LeanMode {
		b.WriteString("lean: long-tail schemas hidden; use tool_search / tool_describe / tool_call\n")
	}
	if info.SideFilter {
		b.WriteString("side filter: /btw read-only allowlist is active on this agent\n")
	}
	if len(info.Kept) == 0 {
		b.WriteString("(none)\n")
	} else {
		b.WriteString("active: ")
		b.WriteString(strings.Join(info.Kept, ", "))
		b.WriteByte('\n')
	}
	if len(info.Excluded) > 0 {
		reasons := make(map[string][]string)
		for name, reason := range info.Excluded {
			reasons[reason] = append(reasons[reason], name)
		}
		keys := make([]string, 0, len(reasons))
		for reason := range reasons {
			keys = append(keys, reason)
		}
		sort.Strings(keys)
		b.WriteString("hidden:\n")
		for _, reason := range keys {
			names := reasons[reason]
			sort.Strings(names)
			fmt.Fprintf(&b, "  %s (%d): %s\n", reason, len(names), strings.Join(names, ", "))
		}
	}
	return strings.TrimSpace(b.String())
}

func formatContextInspect(info agent.ContextInspect, mode string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "── Context (%s) ──\n", mode)
	fmt.Fprintf(&b, "mode=%s  engine=%s", info.Mode, info.Engine)
	if info.ContextLength > 0 {
		fmt.Fprintf(&b, "  window=%d", info.ContextLength)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "history: %d messages ≈ %d tokens\n", info.Messages, info.MessageTokens)
	fmt.Fprintf(&b, "system: ≈ %d tokens (stable %d + context %d + volatile %d)\n",
		info.SystemTokens, info.StableTokens, info.ContextTokens, info.VolatileTokens)
	if info.ContextLength > 0 {
		used := info.MessageTokens + int64(info.SystemTokens)
		pct := float64(used) / float64(info.ContextLength) * 100
		fmt.Fprintf(&b, "filled: %d / %d (%.1f%%)\n", used, info.ContextLength, pct)
	}

	if mode == "list" {
		for _, layer := range info.Layers {
			mark := "·"
			if !layer.Present {
				mark = "○"
			}
			fmt.Fprintf(&b, "%s %s", mark, layer.Name)
			if layer.Tokens > 0 {
				fmt.Fprintf(&b, "  ≈ %d tokens", layer.Tokens)
			}
			b.WriteByte('\n')
		}
		return strings.TrimSpace(b.String())
	}

	for _, layer := range info.Layers {
		mark := "●"
		if !layer.Present {
			mark = "○"
		}
		fmt.Fprintf(&b, "\n%s %s", mark, layer.Name)
		if layer.Tokens > 0 {
			fmt.Fprintf(&b, "  ≈ %d tokens", layer.Tokens)
		}
		b.WriteByte('\n')
		if layer.Preview != "" {
			preview := layer.Preview
			if mode == "map" {
				preview = firstLine(preview)
			}
			for _, line := range strings.Split(preview, "\n") {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		} else if !layer.Present {
			b.WriteString("  (empty)\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func formatSkillInspect(entries []agent.SkillCatalogEntry) string {
	var b strings.Builder
	kept := 0
	for _, e := range entries {
		if e.Kept {
			kept++
		}
	}
	fmt.Fprintf(&b, "── Skills (%d loaded, %d gated) ──\n", kept, len(entries)-kept)
	if len(entries) == 0 {
		b.WriteString("(none)\n")
		return strings.TrimSpace(b.String())
	}
	for _, e := range entries {
		mark := "●"
		if !e.Kept {
			mark = "○"
		}
		fmt.Fprintf(&b, "%s %s  source=%s", mark, e.Name, e.Source)
		if e.Reason != "" {
			fmt.Fprintf(&b, "  %s", e.Reason)
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

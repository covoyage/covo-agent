package tui

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/tui/theme"
)

// ---------------------------------------------------------------------------
// 额外 Block 类型 — Subagent / BgTask / Workflow。
//
// 结构化 block 用于展示子代理生命周期、后台任务状态和工作流阶段。
// ---------------------------------------------------------------------------

// SubagentBlockKindExt 标识子代理事件类型。
type SubagentBlockKindExt int

const (
	SubagentKindSpawn SubagentBlockKindExt = iota
	SubagentKindComplete
	SubagentKindError
	SubagentKindHandoff
)

// SubagentBlock 子代理生命周期 block。
type SubagentBlock struct {
	AgentName   string
	KindExt     SubagentBlockKindExt
	SummaryText string
	Result      string
}

func (b *SubagentBlock) Kind() BlockKind { return BlockKindSessionEvent }
func (b *SubagentBlock) SummaryStr() string {
	prefix := "↳"
	switch b.KindExt {
	case SubagentKindSpawn:
		prefix = "⚡"
	case SubagentKindComplete:
		prefix = "✓"
	case SubagentKindError:
		prefix = "✗"
	case SubagentKindHandoff:
		prefix = "⇄"
	}
	return fmt.Sprintf("%s subagent %s", prefix, b.AgentName)
}
func (b *SubagentBlock) Summary() string { return b.SummaryStr() }
func (b *SubagentBlock) RenderLines(width int, pal *theme.Palette) []string {
	var lines []string
	icon := "↳"
	style := pal.Dim
	switch b.KindExt {
	case SubagentKindSpawn:
		icon = "⚡"
		style = pal.Accent
	case SubagentKindComplete:
		icon = "✓"
		style = pal.Success
	case SubagentKindError:
		icon = "✗"
		style = pal.Error
	case SubagentKindHandoff:
		icon = "⇄"
		style = pal.Accent
	}
	lines = append(lines, style.Render(fmt.Sprintf("%s subagent %s", icon, b.AgentName)))
	if b.SummaryText != "" {
		lines = append(lines, pal.Dim.Render("  "+truncateText(b.SummaryText, int(width)-4)))
	}
	if b.Result != "" {
		lines = append(lines, pal.Dim.Render("  → "+truncateText(b.Result, int(width)-4)))
	}
	return lines
}

// BgTaskBlock 后台任务 block。
type BgTaskBlock struct {
	TaskName string
	Status   string // "running" | "completed" | "failed"
	Output   string
}

func (b *BgTaskBlock) Kind() BlockKind { return BlockKindSessionEvent }
func (b *BgTaskBlock) Summary() string {
	switch b.Status {
	case "running":
		return "⚙ bg: " + b.TaskName + " (running)"
	case "completed":
		return "✓ bg: " + b.TaskName
	case "failed":
		return "✗ bg: " + b.TaskName
	}
	return "bg: " + b.TaskName
}
func (b *BgTaskBlock) RenderLines(width int, pal *theme.Palette) []string {
	var lines []string
	icon := "⚙"
	style := pal.Accent
	switch b.Status {
	case "completed":
		icon = "✓"
		style = pal.Success
	case "failed":
		icon = "✗"
		style = pal.Error
	}
	lines = append(lines, style.Render(fmt.Sprintf("%s bg: %s (%s)", icon, b.TaskName, b.Status)))
	if b.Output != "" {
		outLines := strings.Split(b.Output, "\n")
		maxLines := 3
		for i, ol := range outLines {
			if i >= maxLines {
				lines = append(lines, pal.Dim.Render(fmt.Sprintf("  ... %d more", len(outLines)-maxLines)))
				break
			}
			lines = append(lines, pal.Dim.Render("  "+truncateText(ol, int(width)-4)))
		}
	}
	return lines
}

// WorkflowPhase 标识工作流阶段。
type WorkflowPhase int

const (
	WorkflowPhasePlanning WorkflowPhase = iota
	WorkflowPhaseExecuting
	WorkflowPhaseReviewing
	WorkflowPhaseComplete
	WorkflowPhaseFailed
)

// WorkflowBlock 工作流 block。
type WorkflowBlock struct {
	Name  string
	Phase WorkflowPhase
	Steps []WorkflowStep
}

// WorkflowStep 工作流单步。
type WorkflowStep struct {
	Name   string
	Status string // "pending" | "running" | "done" | "skip"
}

func (b *WorkflowBlock) Kind() BlockKind { return BlockKindSessionEvent }
func (b *WorkflowBlock) Summary() string {
	phaseStr := "planning"
	switch b.Phase {
	case WorkflowPhaseExecuting:
		phaseStr = "executing"
	case WorkflowPhaseReviewing:
		phaseStr = "reviewing"
	case WorkflowPhaseComplete:
		phaseStr = "complete"
	case WorkflowPhaseFailed:
		phaseStr = "failed"
	}
	return fmt.Sprintf("📋 workflow %s (%s)", b.Name, phaseStr)
}
func (b *WorkflowBlock) RenderLines(width int, pal *theme.Palette) []string {
	var lines []string
	lines = append(lines, pal.Accent.Render(fmt.Sprintf("📋 workflow: %s", b.Name)))

	phaseLabel := "planning"
	phaseStyle := pal.Dim
	switch b.Phase {
	case WorkflowPhaseExecuting:
		phaseLabel = "executing"
		phaseStyle = pal.Accent
	case WorkflowPhaseReviewing:
		phaseLabel = "reviewing"
		phaseStyle = pal.Accent
	case WorkflowPhaseComplete:
		phaseLabel = "complete"
		phaseStyle = pal.Success
	case WorkflowPhaseFailed:
		phaseLabel = "failed"
		phaseStyle = pal.Error
	}
	lines = append(lines, phaseStyle.Render("  phase: "+phaseLabel))

	for _, step := range b.Steps {
		icon := "○"
		stepStyle := pal.Dim
		switch step.Status {
		case "running":
			icon = "◐"
			stepStyle = pal.Accent
		case "done":
			icon = "●"
			stepStyle = pal.Success
		case "skip":
			icon = "◌"
			stepStyle = pal.Dim
		}
		lines = append(lines, stepStyle.Render(fmt.Sprintf("  %s %s", icon, step.Name)))
	}
	return lines
}

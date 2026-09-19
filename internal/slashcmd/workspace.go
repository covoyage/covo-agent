package slashcmd

import (
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/agent"
	"github.com/covoyage/covo-agent/internal/workspace"
)

func handlePermission(sctx *SlashContext, parts []string) bool {
	ca := sctx.Runtime.Agents.Current()
	if ca == nil {
		sctx.UI.App.PrintSystem("No active agent.")
		return true
	}
	if len(parts) < 2 || parts[1] == "status" {
		mode := agent.PermissionModeFromApproval(ca.ApprovalSystem())
		if sctx.Runtime.State != nil && sctx.Runtime.State.PermissionMode() != "" {
			mode = agent.PermissionMode(sctx.Runtime.State.PermissionMode())
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("Permission mode: %s (ask, auto, full)", mode))
		return true
	}
	mode, err := agent.NormalizePermissionMode(parts[1], true)
	if err != nil {
		sctx.UI.App.PrintSystem(err.Error())
		return true
	}
	agent.ApplyPermissionMode(ca.ApprovalSystem(), mode)
	if sctx.Runtime.State != nil {
		sctx.Runtime.State.SetPermissionMode(string(mode))
		sctx.Runtime.State.SetSessionYolo(mode == agent.PermissionFull)
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("Permission mode set to %s", mode))
	return true
}

func handleAddDir(sctx *SlashContext, parts []string) bool {
	ca := sctx.Runtime.Agents.Current()
	if ca == nil {
		sctx.UI.App.PrintSystem("No active agent.")
		return true
	}
	if len(parts) < 2 {
		roots := ca.ExtraWorkspaceRoots()
		if len(roots) == 0 {
			sctx.UI.App.PrintSystem("No extra workspace directories. Usage: /add-dir <path>")
			return true
		}
		sctx.UI.App.PrintSystem("Extra workspace directories:\n  " + strings.Join(roots, "\n  "))
		return true
	}
	abs, err := ca.AddWorkspaceRoot(strings.Join(parts[1:], " "))
	if err != nil {
		sctx.UI.App.PrintSystem(err.Error())
		return true
	}
	if sctx.Runtime.State != nil {
		sctx.Runtime.State.SetExtraWorkspaceRoots(ca.ExtraWorkspaceRoots())
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("Added workspace directory %s", abs))
	return true
}

func handleInit(sctx *SlashContext, parts []string) bool {
	dir := sctx.Runtime.WorkingDir
	force := false
	if len(parts) > 1 {
		switch strings.ToLower(parts[1]) {
		case "force", "--force", "-f":
			force = true
		default:
			dir = strings.Join(parts[1:], " ")
		}
	}
	path, created, err := workspace.WriteAgentsFile(dir, force)
	if err != nil {
		sctx.UI.App.PrintSystem(err.Error())
		return true
	}
	if created {
		sctx.UI.App.PrintSystem(fmt.Sprintf("Wrote %s. Fill in goals, layout, commands, and constraints.", path))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("%s already exists. Use /init force to overwrite.", path))
	return true
}

func submitSlashPrompt(sctx *SlashContext, text string) bool {
	if sctx.UI.SubmitPrompt == nil {
		sctx.UI.App.PrintSystem(text)
		return true
	}
	sctx.UI.SubmitPrompt(text)
	return true
}

func handleReviewTask(sctx *SlashContext, parts []string) bool {
	scope, extra := parseReviewArgs(parts)
	return submitSlashPrompt(sctx, buildReviewPrompt(scope, extra))
}

func handleInterview(sctx *SlashContext, parts []string) bool {
	topic := strings.TrimSpace(strings.Join(parts[1:], " "))
	return submitSlashPrompt(sctx, buildInterviewPrompt(topic))
}

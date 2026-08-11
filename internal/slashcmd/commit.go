package slashcmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
)

// handleCommit handles /commit — auto-generates a commit message from staged
// or unstaged changes and commits (or previews with /commit dry).
//
// Usage:
//
//	/commit         — commit staged changes with auto-generated message
//	/commit all     — stage all changes, then commit
//	/commit dry     — preview the generated message without committing
//	/commit dry all — stage all, then preview the message
func handleCommit(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}

	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}

	// Parse subcommand
	dryRun := false
	stageAll := false
	for _, p := range parts[1:] {
		switch p {
		case "dry", "preview", "message":
			dryRun = true
		case "all", "stage":
			stageAll = true
		}
	}

	sctx.Runtime.Busy.Store(true)
	safego.SafeGo(func() {
		defer sctx.Runtime.Busy.Store(false)

		wd := sctx.Runtime.WorkingDir
		if wd == "" {
			wd = "."
		}

		// Optionally stage all changes
		if stageAll {
			if out, err := runGitCmdOut(wd, "add", "-A"); err != nil {
				sctx.UI.App.PrintSystem(fmt.Sprintf("⚠️ git add -A failed: %v\n%s", err, out))
				return
			}
			sctx.UI.App.PrintSystem("📝 Staged all changes.")
		}

		// Check staged diff
		stagedStat, _ := runGitCmdOut(wd, "diff", "--staged", "--stat")
		if strings.TrimSpace(stagedStat) == "" {
			// Nothing staged
			if dryRun && !stageAll {
				// For dry-run without staging, show unstaged as preview
				unstagedStat, _ := runGitCmdOut(wd, "diff", "--stat")
				if strings.TrimSpace(unstagedStat) == "" {
					sctx.UI.App.PrintSystem("(no changes — nothing staged or unstaged)")
					return
				}
				sctx.UI.App.PrintSystem("ℹ️ Nothing staged. Showing unstaged diff for preview.\n" + unstagedStat)
				fullDiff, _ := runGitCmdOut(wd, "diff")
				generateAndShowCommitMsg(sctx.Runtime.Context, wd, fullDiff, unstagedStat, sctx, true)
				return
			}
			sctx.UI.App.PrintSystem("ℹ️ Nothing staged. Use /commit all to stage and commit, or /commit dry to preview.")
			return
		}

		sctx.UI.App.PrintSystem("📊 Staged changes:\n" + stagedStat)
		fullDiff, _ := runGitCmdOut(wd, "diff", "--staged")

		generateAndShowCommitMsg(sctx.Runtime.Context, wd, fullDiff, stagedStat, sctx, dryRun)
	}, nil)

	return true
}

// generateAndShowCommitMsg generates a commit message via LLM and either
// commits it (dryRun=false) or shows it as a preview (dryRun=true).
func generateAndShowCommitMsg(ctx context.Context, wd, fullDiff, diffStat string,
	sctx *SlashContext, dryRun bool) {

	// Truncate diff to avoid token explosion
	maxDiff := 12000
	if len(fullDiff) > maxDiff {
		fullDiff = fullDiff[:maxDiff] + "\n... (truncated)"
	}

	// Get recent commit messages for style reference
	recentLogs, _ := runGitCmdOut(wd, "log", "--oneline", "-5")

	sctx.UI.App.PrintSystem("🤖 Generating commit message...")

	ca := sctx.Runtime.Agents.Current()
	if ca == nil {
		sctx.UI.App.PrintSystem("⚠️ No active agent.")
		return
	}
	provider := ca.Core().Config().Provider
	model := ca.Model()
	if provider == nil {
		sctx.UI.App.PrintSystem("⚠️ No LLM provider available to generate commit message.")
		return
	}

	systemPrompt := `You are a commit message generator. Analyze the git diff and generate a concise, conventional commit message.

Rules:
- Use conventional commit format: type(scope): description
- Types: feat, fix, refactor, docs, test, chore, perf, ci, build, style
- Keep the subject line under 72 characters
- Do NOT include body or footer unless the change is complex
- Return ONLY the commit message, nothing else
- No quotes, no backticks, no code formatting
- Write in imperative mood (e.g. "add" not "added")`

	userPrompt := fmt.Sprintf("Recent commit messages for style reference:\n%s\n\nGit diff:\n%s", recentLogs, fullDiff)

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := &agentcore.ProviderRequest{
		Model: model,
		Messages: []agentcore.Message{
			{Role: agentcore.RoleSystem, Content: systemPrompt},
			{Role: agentcore.RoleUser, Content: userPrompt},
		},
		Temperature: 0.3,
		MaxTokens:   500,
	}

	resp, err := provider.Complete(reqCtx, req)
	var commitMsg string
	if err != nil {
		sctx.UI.App.PrintSystem(fmt.Sprintf("⚠️ LLM commit message generation failed: %v", err))
		commitMsg = generateFallbackCommitMessage(diffStat)
	} else {
		commitMsg = strings.TrimSpace(resp.Content)
		// Strip markdown formatting wrappers
		commitMsg = strings.Trim(commitMsg, "`\"'")
	}

	if dryRun {
		sctx.UI.App.PrintSystem(fmt.Sprintf("📝 Suggested commit message:\n%s", commitMsg))
		sctx.UI.App.PrintSystem("💡 Run /commit to commit staged changes, or /commit all to stage and commit.")
		if ed := sctx.UI.App.Editor(); ed != nil {
			ed.SetValue(commitMsg)
		}
		return
	}

	commitAndReport(wd, commitMsg, sctx)
}

func commitAndReport(wd, msg string, sctx *SlashContext) {
	// exec.Command passes args directly to the OS — no shell, so no escaping needed.
	out, err := runGitCmdOut(wd, "commit", "-m", msg)
	if err != nil {
		sctx.UI.App.PrintSystem(fmt.Sprintf("❌ git commit failed: %v\n%s", err, out))
		return
	}

	// Show the commit result
	result, _ := runGitCmdOut(wd, "log", "-1", "--stat", "--oneline")
	sctx.UI.App.PrintSystem("✅ Committed:\n" + result)
}

func generateFallbackCommitMessage(diffStat string) string {
	lines := strings.Split(strings.TrimSpace(diffStat), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "diff") || strings.HasPrefix(line, "-") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) > 0 {
			files = append(files, parts[0])
		}
	}
	if len(files) == 0 {
		return "chore: update files"
	}
	if len(files) == 1 {
		return fmt.Sprintf("chore: update %s", files[0])
	}
	return fmt.Sprintf("chore: update %d files", len(files))
}

func runGitCmdOut(wd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = wd
	out, err := cmd.CombinedOutput()
	return string(out), err
}

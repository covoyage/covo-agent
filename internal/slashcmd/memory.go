package slashcmd

import (
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
)

// handleMemory handles /memory
func handleMemory(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	store := evolution.MemoryAgent
	if len(parts) > 1 && strings.ToLower(parts[1]) == "user" {
		store = evolution.MemoryUser
	}
	entries, err := covoAgent.Memory().Read(store)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("read memory: %w", err))
		return true
	}
	if len(entries) == 0 {
		sctx.UI.App.PrintSystem(i18n.T("system.memory_empty", "kind", string(store)))
		return true
	}
	sctx.UI.App.PrintSystem(i18n.T("system.memory_header", "kind", string(store), "count", fmt.Sprintf("%d", len(entries))))
	for _, e := range entries {
		sctx.UI.App.PrintSystem(fmt.Sprintf("  § %s", e.Content))
	}
	return true
}

// handleCurator handles /curator
func handleCurator(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	cur := covoAgent.Curator()
	if cur == nil {
		sctx.UI.App.PrintSystem("(curator is not enabled)")
		return true
	}
	cur.RunNow()
	sctx.UI.App.PrintSystem("curator maintenance cycle completed")
	return true
}

// handleDistill handles /distill
func handleDistill(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	sctx.UI.App.PrintSystem("distilling skills from this session…")
	ctx := sctx.Runtime.Context
	safego.SafeGo(func() {
		candidate, created, err := covoAgent.DistillNow(ctx)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("distill: %w", err))
			return
		}
		if candidate == nil {
			sctx.UI.App.PrintSystem("distill: nothing worth extracting from this session yet")
			return
		}
		if created {
			sctx.UI.App.PrintSystem(fmt.Sprintf("✓ distilled new skill %q (confidence %.0f%%): %s",
				candidate.Name, candidate.Confidence*100, candidate.Description))
		} else {
			sctx.UI.App.PrintSystem(fmt.Sprintf("distill: candidate %q identified but not saved", candidate.Name))
		}
	}, nil)
	return true
}

// handleDream handles /dream
func handleDream(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	result, err := covoAgent.DreamNow()
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("dream: %w", err))
		return true
	}
	if result == nil || (len(result.Actions) == 0 && len(result.StaleSuggestions) == 0 && len(result.Conflicts) == 0) {
		sctx.UI.App.PrintSystem(fmt.Sprintf("dream: memory looks healthy (%d entries, nothing to consolidate)", result.TotalEntries))
		return true
	}
	sctx.UI.App.PrintSystem(fmt.Sprintf("── Memory audit (%d entries) ──", result.TotalEntries))
	for _, a := range result.Actions {
		sctx.UI.App.PrintSystem("  • " + a)
	}
	for _, s := range result.StaleSuggestions {
		sctx.UI.App.PrintSystem("  ◐ stale: " + s)
	}
	for _, c := range result.Conflicts {
		sctx.UI.App.PrintSystem("  ⚠ conflict: " + c)
	}
	return true
}

// handleConsolidate handles /consolidate
func handleConsolidate(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	sctx.UI.App.PrintSystem("reviewing skill library for overlapping skills…")
	ctx := sctx.Runtime.Context
	safego.SafeGo(func() {
		report, err := covoAgent.ConsolidateSkillsNow(ctx)
		if err != nil {
			sctx.UI.App.PrintError(fmt.Errorf("consolidate: %w", err))
			return
		}
		if report == nil || len(report.Clusters) == 0 {
			sctx.UI.App.PrintSystem(fmt.Sprintf("consolidate: no overlapping skills found (%d skills reviewed)", report.TotalSkills))
			return
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("── Skill consolidation suggestions (%d skills reviewed) ──", report.TotalSkills))
		for _, c := range report.Clusters {
			sctx.UI.App.PrintSystem(fmt.Sprintf("  • %s: %s (suggestion: %s)",
				strings.Join(c.Skills, " + "), c.Reason, c.Suggestion))
		}
	}, nil)
	return true
}

// handleCompact handles /compact
func handleCompact(sctx *SlashContext, parts []string) bool {
	ag := sctx.Runtime.Agents.Core()
	if ag == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	if sctx.Runtime.Busy.Load() {
		sctx.UI.App.PrintSystem(i18n.T("system.busy"))
		return true
	}
	ctx := sctx.Runtime.Context
	focusTopic := ""
	if len(parts) > 1 {
		focusTopic = strings.Join(parts[1:], " ")
	}
	msgsBefore := ag.State().Messages()
	tokensBefore := agentcore.EstimateMessagesTokens(msgsBefore)
	var err error
	if focusTopic != "" {
		err = ag.ForceCompactWithTopic(ctx, focusTopic)
	} else {
		err = ag.ForceCompact(ctx)
	}
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("compaction failed: %w", err))
		return true
	}
	msgsAfter := ag.State().Messages()
	tokensAfter := agentcore.EstimateMessagesTokens(msgsAfter)
	saved := tokensBefore - tokensAfter
	sctx.UI.App.PrintSystem(fmt.Sprintf("compacted: %d → %d tokens (saved %d)", tokensBefore, tokensAfter, saved))
	return true
}

// handleSkill handles /skill
func handleSkill(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	// /skill with no args: list all skills
	if len(parts) < 2 {
		records := covoAgent.SkillUsage().AllRecords()
		if len(records) == 0 {
			sctx.UI.App.PrintSystem(i18n.T("system.no_skills"))
			return true
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("── Skills (%d total) ──", len(records)))
		for _, rec := range records {
			stateIcon := "●"
			switch rec.State {
			case evolution.StateStale:
				stateIcon = "◐"
			case evolution.StateArchived:
				stateIcon = "○"
			}
			label := rec.Name
			if rec.ID != "" {
				label = rec.ID
			}
			sctx.UI.App.PrintSystem(fmt.Sprintf("  %s %s [%s] uses=%d provenance=%s",
				stateIcon, label, rec.State, rec.UseCount, rec.Provenance))
		}
		return true
	}
	// /skill <name>: view specific skill
	name := parts[1]
	skill, found := covoAgent.SkillManager().Find(name)
	if found {
		name = skill.ID
	}
	rec, ok := covoAgent.SkillUsage().GetRecord(name)
	if !ok {
		sctx.UI.App.PrintSystem(fmt.Sprintf("skill %q not found in usage tracker", name))
		return true
	}
	covoAgent.SkillUsage().RecordView(name)
	sctx.UI.App.PrintSystem(fmt.Sprintf("── Skill: %s ──", name))
	sctx.UI.App.PrintSystem(fmt.Sprintf("  State: %s | Uses: %d | Provenance: %s",
		rec.State, rec.UseCount, rec.Provenance))
	return true
}

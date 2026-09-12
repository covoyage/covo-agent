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
			return
		}
		pending := []evolution.SkillProposal{}
		if store := covoAgent.WorkshopStore(); store != nil {
			pending = store.ListPending()
		}
		for _, p := range pending {
			if p.SkillName == candidate.Name {
				sctx.UI.App.PrintSystem(i18n.T("system.distill_proposed",
					"name", candidate.Name, "id", p.ID))
				return
			}
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("distill: candidate %q identified but not saved", candidate.Name))
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
	if len(parts) >= 2 {
		switch strings.ToLower(parts[1]) {
		case "propose", "proposals", "pending":
			return handleSkillProposals(sctx)
		case "apply":
			if len(parts) < 3 {
				sctx.UI.App.PrintSystem(i18n.T("system.skill_apply_usage"))
				return true
			}
			return handleSkillApply(sctx, parts[2])
		case "reject":
			if len(parts) < 3 {
				sctx.UI.App.PrintSystem(i18n.T("system.skill_reject_usage"))
				return true
			}
			return handleSkillReject(sctx, parts[2])
		}
	}
	// /skill or /skills with no args: catalog with source and gate reasons
	if len(parts) < 2 {
		sctx.UI.App.PrintSystem(formatSkillInspect(covoAgent.InspectSkills()))
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

func handleSkillProposals(sctx *SlashContext) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	store := covoAgent.WorkshopStore()
	if store == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.skill_workshop_disabled"))
		return true
	}
	pending := store.ListPending()
	if len(pending) == 0 {
		sctx.UI.App.PrintSystem(i18n.T("system.skill_no_proposals"))
		return true
	}
	sctx.UI.App.PrintSystem(i18n.T("system.skill_proposals_header", "count", fmt.Sprintf("%d", len(pending))))
	for _, p := range pending {
		rationale := p.Rationale
		if rationale == "" {
			rationale = "-"
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("  • %s  %s [%s]  %s", p.ID, p.SkillName, p.Kind, rationale))
	}
	sctx.UI.App.PrintSystem(i18n.T("system.skill_propose_hint"))
	return true
}

func handleSkillApply(sctx *SlashContext, id string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	prop, err := covoAgent.ApplySkillProposal(id)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("apply proposal: %w", err))
		return true
	}
	sctx.UI.App.PrintSystem(i18n.T("system.skill_applied", "id", prop.ID, "name", prop.SkillName))
	return true
}

func handleSkillReject(sctx *SlashContext, id string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}
	store := covoAgent.WorkshopStore()
	if store == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.skill_workshop_disabled"))
		return true
	}
	prop, err := store.Reject(id)
	if err != nil {
		sctx.UI.App.PrintError(fmt.Errorf("reject proposal: %w", err))
		return true
	}
	sctx.UI.App.PrintSystem(i18n.T("system.skill_rejected", "id", prop.ID, "name", prop.SkillName))
	return true
}

package agent

import (
	"context"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

// ToolInspect is a read-only view of tools the current session can call.
type ToolInspect struct {
	Kept       []string
	Excluded   map[string]string
	Toolsets   []string
	Platform   string
	Profile    string
	PlanMode   bool
	LeanMode   bool
	SideFilter bool
}

// ContextInspect is a map of what is currently assembled for the model.
type ContextInspect struct {
	Mode           string
	Engine         string
	ContextLength  int64
	Messages       int
	MessageTokens  int64
	SystemTokens   int
	StableTokens   int
	ContextTokens  int
	VolatileTokens int
	Layers         []ContextLayer
}

// ContextLayer is one named slice of the assembled prompt/history.
type ContextLayer struct {
	Name    string
	Tokens  int
	Preview string
	Present bool
}

const inspectPreviewRunes = 240

// InspectTools returns the tools the model would see on the next call.
func (ca *CovoAgent) InspectTools() ToolInspect {
	out := ToolInspect{
		Excluded:   make(map[string]string),
		Profile:    ca.baseCfg.ToolProfile,
		PlanMode:   ca.IsPlanMode(),
		LeanMode:   ca.baseCfg.LeanTools,
		SideFilter: ca.baseCfg.ToolNameFilter != nil,
	}
	if out.Profile == "" {
		out.Profile = "full"
	}

	var names []string
	if ca.core != nil {
		names = ca.core.ToolNames()
	}
	if ca.toolsetFilter != nil {
		preview := ca.toolsetFilter.Preview(names)
		out.Kept = preview.Kept
		out.Excluded = preview.Excluded
		out.Toolsets = preview.Toolsets
		out.Platform = preview.Platform
		out.PlanMode = preview.PlanMode
		out.LeanMode = preview.LeanMode
		return out
	}

	out.Kept = append([]string(nil), names...)
	sort.Strings(out.Kept)
	return out
}

// InspectSkills returns loaded skills plus gating/collision reasons.
func (ca *CovoAgent) InspectSkills() []SkillCatalogEntry {
	entries := inspectSkillCatalog(ca.loadedSkills, ca.baseCfg.SkillDisabled, runtime.GOOS, ca.workDir)
	collided := map[string]string{}
	for _, d := range ca.skillDiags {
		if !strings.Contains(d.Message, "collides") {
			continue
		}
		name := filepath.Base(filepath.Dir(d.Path))
		collided[name] = d.Message
	}
	for i, e := range entries {
		if msg, ok := collided[e.Name]; ok && e.Kept {
			entries[i].Reason = "won collision"
		} else if ok && !e.Kept {
			entries[i].Reason = msg
		}
	}
	return entries
}

// InspectContext maps how the next model prompt is assembled.
func (ca *CovoAgent) InspectContext() ContextInspect {
	out := ContextInspect{
		Mode:   string(ca.Mode()),
		Engine: ca.baseCfg.ContextEngine,
	}
	if out.Engine == "" {
		out.Engine = "enhanced"
	}
	if ca.core != nil {
		if engine := ca.core.ContextEngine(); engine != nil {
			out.ContextLength = engine.ContextLength()
		}
		msgs := ca.core.State().Messages()
		out.Messages = len(msgs)
		out.MessageTokens = agentcore.EstimateMessagesTokens(msgs)
	}

	if pb := ca.promptBuilder; pb != nil {
		cp := pb.BuildCached(ca.Mode())
		out.StableTokens = estimateTokens(cp.Stable)
		out.ContextTokens = estimateTokens(cp.Context)
		out.VolatileTokens = estimateTokens(cp.Volatile)
		out.SystemTokens = out.StableTokens + out.ContextTokens + out.VolatileTokens
		out.Layers = append(out.Layers,
			layer("stable: identity + guidance", cp.Stable),
			layer("context: SOUL / project files / memory / standing orders", cp.Context),
			layer("volatile: plan / persona / nudges / kanban", cp.Volatile),
		)
	}

	if ca.lastRepoMapInjected != "" {
		out.Layers = append(out.Layers, layer("repo map", ca.lastRepoMapInjected))
	} else if ca.repoMap != nil {
		out.Layers = append(out.Layers, ContextLayer{Name: "repo map", Present: false, Preview: "(not injected yet; built on first Run)"})
	}

	if ca.goalStore != nil && ca.sessionMgr != nil && ca.goalSteering != nil {
		if g, _ := ca.goalStore.Get(context.Background(), ca.sessionMgr.CurrentID()); g != nil {
			out.Layers = append(out.Layers, layer("goal steering", ca.goalSteering.ContinuationPrompt(g)))
		}
	}

	if ca.core != nil {
		skills := ca.core.Config().SkillConfig.AvailableSkills
		if n := len(skills); n > 0 {
			names := make([]string, 0, n)
			for _, s := range skills {
				names = append(names, s.Name)
			}
			sort.Strings(names)
			out.Layers = append(out.Layers, ContextLayer{
				Name:    "skills catalog",
				Present: true,
				Preview: truncateRunes(strings.Join(names, ", "), inspectPreviewRunes),
			})
		}
	}

	return out
}

func layer(name, content string) ContextLayer {
	content = strings.TrimSpace(content)
	return ContextLayer{
		Name:    name,
		Tokens:  estimateTokens(content),
		Preview: truncateRunes(content, inspectPreviewRunes),
		Present: content != "",
	}
}

func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

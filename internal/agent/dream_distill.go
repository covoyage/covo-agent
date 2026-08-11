package agent

import (
	"context"
	"strings"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/evolution"
)

// auxLLMCall returns a simple system+user LLM call closure backed by the agent's
// provider, used by on-demand maintenance commands (distill). Returns nil when
// no provider is configured.
func (ca *CovoAgent) auxLLMCall() func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	provider := ca.cfg.Provider
	model := ca.model
	if provider == nil {
		return nil
	}
	return func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		resp, err := provider.Complete(ctx, &agentcore.ProviderRequest{
			Model: model,
			Messages: []agentcore.Message{
				{Role: agentcore.RoleSystem, Content: systemPrompt},
				{Role: agentcore.RoleUser, Content: userPrompt},
			},
			MaxTokens:   2000,
			Temperature: 0.3,
		})
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}
}

// trajectoryToMaps converts recorded trajectory entries into the generic map
// form expected by the skill extractor. Tool-call invocations are encoded into
// the content so the extractor's heuristic (which counts the literal
// "tool_calls" marker) recognises non-trivial sessions.
func trajectoryToMaps(entries []TrajectoryEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		content := e.Content
		if len(e.ToolCalls) > 0 {
			names := make([]string, len(e.ToolCalls))
			for i, tc := range e.ToolCalls {
				names[i] = tc.Name
			}
			marker := "[tool_calls: " + strings.Join(names, ", ") + "]"
			if content == "" {
				content = marker
			} else {
				content += "\n" + marker
			}
		}
		if content == "" && e.Name != "" {
			content = "[tool: " + e.Name + "]"
		}
		role := e.Role
		if role == "" {
			role = "assistant"
		}
		out = append(out, map[string]any{"role": role, "content": content})
	}
	return out
}

// DistillNow runs on-demand skill extraction over the current session's
// trajectory (the same pipeline normally run at session end) and persists a
// skill if a worthwhile candidate is found.
func (ca *CovoAgent) DistillNow(ctx context.Context) (*evolution.ExtractionCandidate, bool, error) {
	cur := ca.Curator()
	if cur == nil {
		return nil, false, errCuratorDisabled
	}
	traj := trajectoryToMaps(ca.Trajectory().Snapshot())
	return cur.Distill(ctx, traj, ca.auxLLMCall())
}

// DreamNow audits the memory system for stale/contradictory/bloated entries and
// returns the findings. Advisory only.
func (ca *CovoAgent) DreamNow() (*evolution.AuditResult, error) {
	cur := ca.Curator()
	if cur == nil {
		return nil, errCuratorDisabled
	}
	return cur.Dream()
}

// ConsolidateSkillsNow scans the agent-created skill library for overlapping
// coverage and asks an LLM to suggest which skills could be merged into a
// single umbrella skill. Advisory only -- like DreamNow, it never rewrites
// or deletes any skill itself.
func (ca *CovoAgent) ConsolidateSkillsNow(ctx context.Context) (*evolution.ConsolidationReport, error) {
	cur := ca.Curator()
	if cur == nil {
		return nil, errCuratorDisabled
	}
	return cur.ConsolidateSkills(ctx, ca.auxLLMCall())
}

var errCuratorDisabled = errCurator("curator is not enabled")

type errCurator string

func (e errCurator) Error() string { return string(e) }

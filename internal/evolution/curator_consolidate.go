package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// consolidatePrompt is the system prompt for the skill-consolidation LLM
// pass. Deliberately conservative: false positives (merging skills that
// aren't really redundant) are worse than false negatives here, since the
// caller decides whether to act on the suggestions at all.
const consolidatePrompt = "You are a precise, conservative auditor of an AI agent's " +
	"self-created skill library. Your only job is to spot skills whose " +
	"descriptions overlap enough that they cover the same procedure and " +
	"should be merged into one \"umbrella\" skill. Prefer reporting fewer, " +
	"high-confidence clusters over speculative ones. Never suggest merging " +
	"skills that merely share a topic but solve different problems."

// ConsolidationCluster groups two or more skill names the LLM believes are
// redundant enough to merge into a single umbrella skill.
type ConsolidationCluster struct {
	Skills     []string `json:"skills"`
	Reason     string   `json:"reason"`
	Suggestion string   `json:"suggestion"` // suggested umbrella name/description
}

// ConsolidationReport is the advisory result of ConsolidateSkills. Mirrors
// Curator.Dream()'s philosophy for memory: it reports what could be merged
// but never rewrites, patches, or deletes any skill itself. Callers (or the
// agent, via skill_manage) decide whether and how to act on a suggestion.
type ConsolidationReport struct {
	TotalSkills int                    `json:"total_skills"`
	Clusters    []ConsolidationCluster `json:"clusters"`
}

type consolidationResponse struct {
	Clusters []ConsolidationCluster `json:"clusters"`
}

// ConsolidateSkills scans agent-created, non-archived skills for
// overlapping coverage and asks an LLM to propose which ones could be
// merged into a single umbrella skill. Advisory only -- like Dream(), it
// never mutates the skill library itself; applying a suggested merge is a
// separate, explicit step (e.g. skill_manage improve/create + archiving the
// superseded skills).
func (c *Curator) ConsolidateSkills(
	ctx context.Context,
	llmCall func(ctx context.Context, systemPrompt, userPrompt string) (string, error),
) (*ConsolidationReport, error) {
	if c.skillMgr == nil {
		return nil, fmt.Errorf("skill manager is not linked")
	}
	if c.usage == nil {
		return nil, fmt.Errorf("skill usage tracker is not linked")
	}
	if llmCall == nil {
		return nil, fmt.Errorf("no LLM available for consolidation")
	}

	records := c.usage.AgentCreatedReport()
	var candidateNames []string
	for _, r := range records {
		if r.State != StateArchived {
			candidateNames = append(candidateNames, r.Name)
		}
	}
	if len(candidateNames) < 2 {
		return &ConsolidationReport{TotalSkills: len(candidateNames)}, nil
	}

	descriptions, err := c.skillDescriptions(candidateNames)
	if err != nil {
		return nil, fmt.Errorf("read skill descriptions: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("Here are the agent-created skills currently in the library:\n\n")
	for _, name := range candidateNames {
		fmt.Fprintf(&sb, "- %s: %s\n", name, descriptions[name])
	}
	sb.WriteString(
		"\nRespond with ONLY a JSON object of the form " +
			`{"clusters":[{"skills":["name1","name2"],"reason":"...","suggestion":"..."}]}` +
			". If nothing overlaps enough to merge, respond with {\"clusters\":[]}.",
	)

	raw, err := llmCall(ctx, consolidatePrompt, sb.String())
	if err != nil {
		return nil, fmt.Errorf("consolidation LLM call failed: %w", err)
	}

	parsed, err := parseConsolidationResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse consolidation response: %w", err)
	}

	return &ConsolidationReport{
		TotalSkills: len(candidateNames),
		Clusters:    parsed.Clusters,
	}, nil
}

// skillDescriptions builds a name->description map for the given skill
// names by scanning the skill manager's listing once.
func (c *Curator) skillDescriptions(names []string) (map[string]string, error) {
	infos, err := c.skillMgr.List()
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(names))
	for _, n := range names {
		wanted[n] = true
	}
	out := make(map[string]string, len(names))
	for _, info := range infos {
		if wanted[info.Name] {
			out[info.Name] = info.Description
		}
	}
	return out, nil
}

// parseConsolidationResponse extracts the JSON object from an LLM response,
// tolerating a markdown code fence around it.
func parseConsolidationResponse(response string) (*consolidationResponse, error) {
	jsonStr := strings.TrimSpace(response)
	if strings.HasPrefix(jsonStr, "```") {
		lines := strings.Split(jsonStr, "\n")
		if len(lines) > 1 {
			lines = lines[1:]
		}
		endIdx := -1
		for i, l := range lines {
			if strings.TrimSpace(l) == "```" {
				endIdx = i
				break
			}
		}
		if endIdx >= 0 {
			lines = lines[:endIdx]
		}
		jsonStr = strings.Join(lines, "\n")
	}

	var resp consolidationResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return &resp, nil
}

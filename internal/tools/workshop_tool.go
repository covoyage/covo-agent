package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covonaut/agentcore"
)

type WorkshopToolItem struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	SkillName string `json:"skill_name"`
	Rationale string `json:"rationale,omitempty"`
}

type WorkshopParams struct {
	Action     string `json:"action"`
	ProposalID string `json:"proposal_id,omitempty"`
	SkillName  string `json:"skill_name,omitempty"`
	Body       string `json:"body,omitempty"`
	Rationale  string `json:"rationale,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

func BuildSkillWorkshopTool(store *evolution.WorkshopStore, skillMgr *evolution.SkillManager) *agentcore.Tool {
	if store == nil {
		return nil
	}
	return &agentcore.Tool{
		Name:        "skill_workshop",
		Description: "Create and inspect Skill Workshop proposals. Proposals stay pending until the user runs /skill apply <id>. Never write proposal or skill files, and never apply/reject/quarantine from this tool.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action to perform: create, list, inspect, revise. apply/reject/quarantine are user-only via /skill.",
					"enum":        []string{"create", "list", "inspect", "revise"},
				},
				"proposal_id": map[string]any{
					"type":        "string",
					"description": "Proposal ID (required for inspect and revise)",
				},
				"skill_name": map[string]any{
					"type":        "string",
					"description": "Skill name (required for create)",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Full skill content including YAML frontmatter (required for create, revise)",
				},
				"rationale": map[string]any{
					"type":        "string",
					"description": "Why this skill should be created or updated (optional)",
				},
				"kind": map[string]any{
					"type":        "string",
					"enum":        []string{"create", "update", "delete"},
					"description": "Proposal kind (required for create): 'create' for new skill, 'update' for existing, 'delete' to archive",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = skillMgr // apply/reject stay user-only via /skill
			var p WorkshopParams
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch p.Action {
			case "create":
				kind := evolution.ProposalCreate
				switch p.Kind {
				case "update":
					kind = evolution.ProposalUpdate
				case "delete":
					kind = evolution.ProposalDelete
				}
				if p.SkillName == "" {
					return nil, fmt.Errorf("skill_name is required for 'create'")
				}
				if kind != evolution.ProposalDelete && p.Body == "" {
					return nil, fmt.Errorf("skill_name and body are required for 'create'")
				}
				prop, err := store.Create(kind, p.SkillName, p.Body, p.Rationale)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"status":   "created",
					"proposal": proposalToItem(*prop),
					"message":  fmt.Sprintf("Proposal %q created for skill %q. The user must apply it before it takes effect.", prop.ID, p.SkillName),
				}, nil

			case "list":
				props := store.List()
				items := make([]WorkshopToolItem, 0, len(props))
				for _, pr := range props {
					items = append(items, proposalToItem(pr))
				}
				return map[string]any{
					"status":    "ok",
					"proposals": items,
					"count":     len(items),
				}, nil

			case "inspect":
				if p.ProposalID == "" {
					return nil, fmt.Errorf("proposal_id is required for 'inspect'")
				}
				prop, err := store.Get(p.ProposalID)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"status":   "ok",
					"proposal": proposalToItem(*prop),
					"body":     prop.Body,
				}, nil

			case "revise":
				if p.ProposalID == "" || p.Body == "" {
					return nil, fmt.Errorf("proposal_id and body are required for 'revise'")
				}
				prop, err := store.Revise(p.ProposalID, p.Body)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"status":   "revised",
					"proposal": proposalToItem(*prop),
					"message":  fmt.Sprintf("Proposal %q revised.", p.ProposalID),
				}, nil

			case "apply", "reject", "quarantine":
				return nil, fmt.Errorf("%s is user-only; run /skill apply <id> or /skill reject <id> after review", p.Action)

			default:
				return nil, fmt.Errorf("unknown action %q (valid: create, list, inspect, revise)", p.Action)
			}
		},
	}
}

func proposalToItem(p evolution.SkillProposal) WorkshopToolItem {
	return WorkshopToolItem{
		ID:        p.ID,
		Kind:      string(p.Kind),
		Status:    string(p.Status),
		SkillName: p.SkillName,
		Rationale: p.Rationale,
	}
}

// BuildSkillWorkshopPromptSection builds system prompt content for the skill workshop.
func BuildSkillWorkshopPromptSection() string {
	return `## Skill Workshop

Route durable skill work (creating or updating skills) through the skill_workshop or skill_manage tools; never write proposal or skill files directly.
Generated skills are pending proposals. Tell the user to run /skill apply <id> after review. Do not apply, reject, or quarantine from this tool.
To create a skill: use action "create" with skill_name, body (including YAML frontmatter), and optional rationale.
To inspect a proposal: use action "inspect" with proposal_id.
To update an existing skill: use action "create" with kind "update".`
}

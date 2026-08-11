package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/covoyage/covonaut/agentcore"
	"github.com/covoyage/covo-agent/internal/evolution"
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
		Description: "Manage Skill Workshop proposals: create, update, revise, list, inspect, apply, reject, quarantine. Proposals are pending until applied by the user. Never write proposal or skill files directly.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action to perform: create, list, inspect, revise, apply, reject, quarantine",
					"enum":        []string{"create", "list", "inspect", "revise", "apply", "reject", "quarantine"},
				},
				"proposal_id": map[string]any{
					"type":        "string",
					"description": "Proposal ID (required for inspect, revise, apply, reject, quarantine)",
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
					"enum":         []string{"create", "update"},
					"description": "Proposal kind (required for create): 'create' for new skill, 'update' for existing",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p WorkshopParams
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch p.Action {
			case "create":
				kind := evolution.ProposalCreate
				if p.Kind == "update" {
					kind = evolution.ProposalUpdate
				}
				if p.SkillName == "" || p.Body == "" {
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

			case "apply":
				if p.ProposalID == "" {
					return nil, fmt.Errorf("proposal_id is required for 'apply'")
				}
				if skillMgr == nil {
					return nil, fmt.Errorf("skill manager is not available")
				}
				prop, err := store.Apply(p.ProposalID, func(pr evolution.SkillProposal) error {
					fm, skillBody, err := evolution.ParseSkillFrontmatter(pr.Body)
					if err != nil {
						return fmt.Errorf("parse skill body frontmatter: %w", err)
					}
					description := pr.Rationale
					if fm != nil && fm.Description != "" {
						description = fm.Description
					}
					if description == "" {
						description = fmt.Sprintf("Skill created from workshop proposal %s", pr.ID)
					}
					switch pr.Kind {
					case evolution.ProposalCreate:
						body := skillBody
						if body == "" {
							body = pr.Body
						}
						_, err := skillMgr.CreateWithMeta(pr.SkillName, description, "", nil, body)
						return err
					case evolution.ProposalUpdate:
						body := skillBody
						if body == "" {
							body = pr.Body
						}
						return skillMgr.Edit(pr.SkillName, body)
					default:
						return fmt.Errorf("unknown proposal kind: %s", pr.Kind)
					}
				})
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"status":   "applied",
					"proposal": proposalToItem(*prop),
					"message":  fmt.Sprintf("Proposal %q applied. Skill %q has been %sd.", p.ProposalID, prop.SkillName, string(prop.Kind)),
				}, nil

			case "reject":
				if p.ProposalID == "" {
					return nil, fmt.Errorf("proposal_id is required for 'reject'")
				}
				prop, err := store.Reject(p.ProposalID)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"status":   "rejected",
					"proposal": proposalToItem(*prop),
					"message":  fmt.Sprintf("Proposal %q rejected.", p.ProposalID),
				}, nil

			case "quarantine":
				if p.ProposalID == "" {
					return nil, fmt.Errorf("proposal_id is required for 'quarantine'")
				}
				prop, err := store.Quarantine(p.ProposalID)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"status":   "quarantined",
					"proposal": proposalToItem(*prop),
					"message":  fmt.Sprintf("Proposal %q quarantined.", p.ProposalID),
				}, nil

			default:
				return nil, fmt.Errorf("unknown action %q (valid: create, list, inspect, revise, apply, reject, quarantine)", p.Action)
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

Route durable skill work (creating or updating skills) through the skill_workshop tool; never write proposal or skill files directly.
Generated skills are pending proposals. Apply, reject, or quarantine only when the user explicitly asks.
To create a skill: use action "create" with skill_name, body (including YAML frontmatter), and optional rationale.
To inspect a proposal: use action "inspect" with proposal_id.
To update an existing skill: use action "create" with kind "update".`
}

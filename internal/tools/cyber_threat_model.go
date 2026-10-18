package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covonaut/agentcore"
)

func BuildThreatModelTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "threat_model",
		Description: strings.Join([]string{
			"Read, save, amend, or list the engagement's shared threat model.",
			"The model defines attacker identity, trust boundaries, attack surface, and severity calibration for all subagents.",
			"action is optional: model fields imply save, amendment implies amend, target implies get, and empty arguments imply list.",
			"Saving requires target, overview, attacker, non-empty trust_boundaries and attack_surface, and severity_calibration. A validation_required result with saved=false lists missing_fields; fill them from observed context, never fabricate missing evidence or repeat unchanged arguments.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":               map[string]any{"type": "string", "enum": []string{"get", "save", "amend", "list"}},
				"target":               map[string]any{"type": "string"},
				"overview":             map[string]any{"type": "string"},
				"attacker":             map[string]any{"type": "string"},
				"trust_boundaries":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"attack_surface":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"severity_calibration": map[string]any{"type": "string"},
				"author":               map[string]any{"type": "string"},
				"amendment":            map[string]any{"type": "string"},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if catalog == nil {
				return nil, fmt.Errorf("threat model catalog unavailable")
			}
			var params struct {
				Action              string   `json:"action"`
				Target              string   `json:"target"`
				Overview            string   `json:"overview"`
				Attacker            string   `json:"attacker"`
				TrustBoundaries     []string `json:"trust_boundaries"`
				AttackSurface       []string `json:"attack_surface"`
				SeverityCalibration string   `json:"severity_calibration"`
				Author              string   `json:"author"`
				Amendment           string   `json:"amendment"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			action := strings.ToLower(strings.TrimSpace(params.Action))
			if action == "" {
				switch {
				case strings.TrimSpace(params.Amendment) != "":
					action = "amend"
				case strings.TrimSpace(params.Overview) != "" || strings.TrimSpace(params.Attacker) != "" || len(params.TrustBoundaries) > 0 || len(params.AttackSurface) > 0 || strings.TrimSpace(params.SeverityCalibration) != "":
					action = "save"
				case strings.TrimSpace(params.Target) != "":
					action = "get"
				default:
					action = "list"
				}
			}
			switch action {
			case "list":
				return catalog.ListThreatModels()
			case "get":
				model, ok, err := catalog.GetThreatModel(params.Target)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("threat model for %q not found", params.Target)
				}
				return model, nil
			case "save":
				model, err := catalog.SaveThreatModel(cyber.ThreatModel{
					Target: params.Target, Overview: params.Overview, Attacker: params.Attacker,
					TrustBoundaries: params.TrustBoundaries, AttackSurface: params.AttackSurface,
					SeverityCalibration: params.SeverityCalibration,
				})
				var validation *cyber.ThreatModelValidationError
				if errors.As(err, &validation) {
					return map[string]any{
						"status":         "validation_required",
						"saved":          false,
						"missing_fields": validation.MissingFields,
						"error":          validation.Error(),
						"next_action":    "Supply the listed non-empty fields using observed or explicitly unknown context, then explicitly retry save. Nothing was saved; do not invent attacker capabilities, trust boundaries, attack surfaces, or severity conclusions to satisfy validation.",
					}, nil
				}
				return model, err
			case "amend":
				return catalog.AmendThreatModel(params.Target, params.Author, params.Amendment)
			default:
				return nil, fmt.Errorf("action must be get, save, amend, or list")
			}
		},
	}
}

func BuildDiffScopeTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "diff_scope",
		Description: "Configure or read a git diff-scoped security review. Findings must cite a changed file and line.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"base_ref": map[string]any{"type": "string", "description": "Base branch, tag, or commit."},
				"head_ref": map[string]any{"type": "string", "description": "Head ref; default HEAD."},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			_ = ctx
			if catalog == nil {
				return nil, fmt.Errorf("engagement catalog unavailable")
			}
			var params struct {
				BaseRef string `json:"base_ref"`
				HeadRef string `json:"head_ref"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.BaseRef) == "" {
				return catalog.GetEngagement()
			}
			return catalog.SetDiffScope(params.BaseRef, params.HeadRef)
		},
	}
}

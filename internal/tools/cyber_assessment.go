package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covo-agent/internal/cyber"
	"github.com/covoyage/covonaut/agentcore"
)

func BuildRequestIntakeTool(catalog *cyber.Catalog) *agentcore.Tool {
	return BuildRequestIntakeToolForRole(catalog, "parent")
}

func BuildRequestIntakeToolForRole(catalog *cyber.Catalog, instanceRole string) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "request_intake",
		Description: "Opt in to scoped HTTP operation review and context-triggered follow-ups, or inspect the durable queue. Each job has at most three automatic attempts. Grouping and identity observations do not expand replay permissions. No target is contacted by this tool.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"status", "enable", "disable"}},
			"hosts":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exact allowed hosts; required to enable."},
		}},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			if catalog == nil {
				return nil, fmt.Errorf("cyber catalog unavailable")
			}
			var params struct {
				Action string   `json:"action"`
				Hosts  []string `json:"hosts"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			switch params.Action {
			case "", "status":
				config, err := catalog.IntakeConfig()
				if err != nil {
					return nil, err
				}
				jobs, err := catalog.IntakeJobs()
				return map[string]any{"config": config, "jobs": jobs}, err
			case "enable":
				if instanceRole != "parent" {
					return nil, fmt.Errorf("only the parent may change request intake")
				}
				return catalog.SetIntake(cyber.IntakeConfig{Enabled: true, Hosts: params.Hosts})
			case "disable":
				if instanceRole != "parent" {
					return nil, fmt.Errorf("only the parent may change request intake")
				}
				return catalog.SetIntake(cyber.IntakeConfig{})
			default:
				return nil, fmt.Errorf("unknown intake action %q", params.Action)
			}
		},
	}
}

func BuildAssessmentTool(catalog *cyber.Catalog) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "assessment",
		Description: "Read the asset-by-risk coverage matrix and unconfirmed vulnerability chain candidates. Candidates are not findings.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Func: func(_ context.Context, _ json.RawMessage) (any, error) {
			if catalog == nil {
				return nil, fmt.Errorf("cyber catalog unavailable")
			}
			cells, err := catalog.CoverageMatrix()
			if err != nil {
				return nil, err
			}
			candidates, err := catalog.ChainCandidates()
			return map[string]any{"coverage": cells, "chain_candidates": candidates}, err
		},
	}
}

func BuildRemoteNodesTool(catalog *cyber.Catalog) *agentcore.Tool {
	return BuildRemoteNodesToolForRole(catalog, "parent")
}

func BuildRemoteNodesToolForRole(catalog *cyber.Catalog, instanceRole string) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "remote_nodes",
		Description: "Register, remove, or list named SSH execution nodes. Host keys must already be trusted; only key paths are saved.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"list", "upsert", "remove"}},
			"id":     map[string]any{"type": "string"}, "host": map[string]any{"type": "string"},
			"user": map[string]any{"type": "string"}, "key_path": map[string]any{"type": "string"},
			"zone": map[string]any{"type": "string"},
		}},
		Func: func(_ context.Context, args json.RawMessage) (any, error) {
			if catalog == nil {
				return nil, fmt.Errorf("cyber catalog unavailable")
			}
			var params struct {
				Action string `json:"action"`
				cyber.RemoteNode
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			switch params.Action {
			case "", "list":
				return catalog.ListNodes()
			case "upsert":
				if instanceRole != "parent" {
					return nil, fmt.Errorf("only the parent may change remote nodes")
				}
				return catalog.UpsertNode(params.RemoteNode)
			case "remove":
				if instanceRole != "parent" {
					return nil, fmt.Errorf("only the parent may change remote nodes")
				}
				return map[string]any{"removed": params.ID}, catalog.RemoveNode(params.ID)
			default:
				return nil, fmt.Errorf("unknown remote_nodes action %q", params.Action)
			}
		},
	}
}

func BuildRemoteNodeExecTool(catalog *cyber.Catalog, instanceRole string) *agentcore.Tool {
	return &agentcore.Tool{
		Name:        "remote_node_exec",
		Description: "Run one command on an already registered SSH node (hands-on cyber roles only). Requires trusted host keys.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"id": map[string]any{"type": "string"}, "command": map[string]any{"type": "string"},
		}, "required": []string{"id", "command"}},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			if catalog == nil {
				return nil, fmt.Errorf("cyber catalog unavailable")
			}
			if !cyber.AllowAgentTool(instanceRole, subagentRole(ctx), "remote_node_exec") {
				return nil, fmt.Errorf("remote_node_exec is reserved for spawned hands-on roles")
			}
			var params struct {
				ID      string `json:"id"`
				Command string `json:"command"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			return catalog.ExecuteNode(ctx, strings.TrimSpace(params.ID), params.Command)
		},
	}
}

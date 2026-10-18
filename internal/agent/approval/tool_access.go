package approval

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/covoyage/covo-agent/internal/tools/capability"
)

type requiredApprovalKey struct{}

func WithRequiredApproval(ctx context.Context) context.Context {
	return context.WithValue(ctx, requiredApprovalKey{}, true)
}

func ApprovalRequired(ctx context.Context) bool {
	required, _ := ctx.Value(requiredApprovalKey{}).(bool)
	return required
}

func DangerousTools() map[string]bool {
	names := []string{"bash", "process", "sandbox", "mcp", "remote_exec", "external_agent", "skill_script", "preview", "write_file", "edit_block", "write", "edit", "apply_patch", "append_file", "patch", "move", "git_commit", "git_push", "delete", "delete_file", "execute_code", "computer_use", "exit_plan_mode", "scan", "scan_profile", "request_intake", "web_param_probe", "remote_node_exec", "exchange_replay", "exchange_compare", "csrf_retest", "web_workflow", "web_test_execute", "deploy", "cronjob", "finding_retest", "cyber_storage", "oast"}
	tools := make(map[string]bool, len(names))
	tools["browser"] = true
	for _, name := range names {
		tools[name] = true
	}
	return tools
}

var dangerousToolNames = DangerousTools()

func RequiresToolApproval(name string, args []byte) bool {
	if strings.Contains(name, "__") {
		return true
	}
	if !dangerousToolNames[name] {
		return false
	}
	var params struct {
		Action string `json:"action"`
		Recipe string `json:"recipe"`
	}
	if json.Unmarshal(args, &params) != nil {
		return true
	}
	switch name {
	case "browser":
		return params.Action == "upload" || params.Action == "downloads_enable" || params.Action == "download" || params.Action == "resume"
	case "mcp":
		return params.Action != "list"
	case "scan":
		return !strings.EqualFold(strings.TrimSpace(params.Recipe), "list")
	case "scan_profile":
		return strings.TrimSpace(string(args)) != "{}"
	case "request_intake":
		return params.Action != "" && params.Action != "status"
	case "cronjob":
		return params.Action != "list" && params.Action != "get"
	}
	return true
}

func RequiresApproval(ctx context.Context, name string, args []byte) bool {
	if metadata, ok := capability.FromContext(ctx); ok {
		switch metadata.Kind {
		case capability.Write, capability.Execute:
			return true
		case capability.Read:
			return false
		case capability.Conditional:
			if !dangerousToolNames[name] {
				return true
			}
			return RequiresToolApproval(name, args)
		default:
			return true
		}
	}
	return RequiresToolApproval(name, args)
}

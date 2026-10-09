package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	toolssubagent "github.com/covoyage/covo-agent/internal/tools/subagent"
	"github.com/covoyage/covonaut/agentcore"
)

// cyberSpawnRequested reports whether a subagent spawn implies cyber mode:
// either an explicit cyber role or a cyber toolset request.
func cyberSpawnRequested(ctx context.Context, toolsetNames []string) bool {
	role := strings.ToLower(strings.TrimSpace(toolssubagent.SubagentRoleFromContext(ctx)))
	switch role {
	case "recon", "verifier", "exploiter", "privesc", "lateral",
		"persistence", "impact", "opsec", "cleanup", "reporter":
		return true
	}
	for _, name := range toolsetNames {
		if strings.EqualFold(strings.TrimSpace(name), "cyber") {
			return true
		}
	}
	return false
}

func resolveCyberChildRole(mode AgentMode, role string) (string, error) {
	if mode != ModeCyber {
		return role, nil
	}
	if strings.EqualFold(strings.TrimSpace(role), "parent") {
		return "", fmt.Errorf("spawn: parent is reserved for the top-level cyber agent")
	}
	if strings.TrimSpace(role) == "" {
		return "leaf", nil
	}
	return role, nil
}

func intakeToolAllowed(scope toolssubagent.IntakeScope, call *agentcore.HookContext) bool {
	switch call.ToolName {
	case "assessment":
		return true
	case "exchange_get", "web_context":
		var args struct {
			ID string `json:"id"`
		}
		return json.Unmarshal(call.Arguments, &args) == nil && args.ID == scope.ExchangeID
	case "coverage_upsert":
		var args struct {
			Target string `json:"target"`
		}
		if json.Unmarshal(call.Arguments, &args) != nil {
			return false
		}
		_, endpoint, ok := strings.Cut(scope.Endpoint, " ")
		if !ok {
			return false
		}
		parsed, err := url.Parse(endpoint)
		return err == nil && (strings.EqualFold(args.Target, parsed.Hostname()) || args.Target == parsed.EscapedPath())
	case "exchange_replay", "exchange_compare", "web_param_probe":
		method, endpoint, ok := strings.Cut(scope.Endpoint, " ")
		if !ok || (method != "GET" && method != "HEAD") {
			return false
		}
		var args struct {
			ID     string `json:"id"`
			Method string `json:"method"`
			URL    string `json:"url"`
		}
		if json.Unmarshal(call.Arguments, &args) != nil || args.ID != scope.ExchangeID ||
			(args.Method != "" && !strings.EqualFold(args.Method, method)) {
			return false
		}
		if args.URL == "" {
			return true
		}
		expected, err := url.Parse(endpoint)
		if err != nil {
			return false
		}
		actual, err := url.Parse(args.URL)
		return err == nil && actual.User == nil && strings.EqualFold(actual.Scheme, expected.Scheme) &&
			strings.EqualFold(actual.Hostname(), expected.Hostname()) && actual.Port() == expected.Port() &&
			actual.EscapedPath() == expected.EscapedPath()
	}
	return false
}

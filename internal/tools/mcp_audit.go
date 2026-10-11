package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/evolution"
	"github.com/covoyage/covonaut/agentcore"
)

type mcpAuditReport struct {
	Server         string              `json:"server"`
	Scope          string              `json:"scope"`
	Complete       bool                `json:"complete"`
	RequiresReview bool                `json:"requires_review"`
	Changed        bool                `json:"changed"`
	Fingerprint    string              `json:"fingerprint"`
	Tools          []mcpToolDef        `json:"tools"`
	Candidates     []evolution.Finding `json:"candidates"`
	Errors         []string            `json:"errors,omitempty"`
}

func mcpDefinitionFingerprint(tools []mcpToolDef) (string, error) {
	ordered := append([]mcpToolDef(nil), tools...)
	sort.Slice(ordered, func(first, second int) bool { return ordered[first].Name < ordered[second].Name })
	data, err := json.Marshal(ordered)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func buildMCPAuditTool(store *MCPStore) *agentcore.Tool {
	safe := true
	return &agentcore.Tool{
		Name:            "mcp_audit",
		Description:     "Audit connected MCP tool metadata without invoking tools or spawning processes. Returns raw definitions, candidate risks, definition drift, and audit completeness; this is not a server behavior certification.",
		ConcurrencySafe: &safe,
		Parameters:      map[string]any{"type": "object", "properties": map[string]any{"server": map[string]any{"type": "string"}}, "required": []string{"server"}, "additionalProperties": false},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(args, &fields); err != nil {
				return nil, err
			}
			if len(fields) != 1 || fields["server"] == nil {
				return nil, fmt.Errorf("only a server argument is supported")
			}
			var params struct {
				Server string `json:"server"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, err
			}
			if strings.TrimSpace(params.Server) == "" {
				return nil, fmt.Errorf("server is required")
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			store.mu.Lock()
			server := store.servers[params.Server]
			store.mu.Unlock()
			if server == nil {
				return nil, fmt.Errorf("MCP server %q is not connected", params.Server)
			}
			server.toolsMu.Lock()
			if server.auditFingerprint == "" {
				fingerprint, err := mcpDefinitionFingerprint(server.tools)
				if err != nil {
					server.toolsMu.Unlock()
					return nil, err
				}
				server.auditFingerprint = fingerprint
			}
			baseline := server.auditFingerprint
			server.toolsMu.Unlock()
			report := mcpAuditReport{Server: params.Server, Scope: "tool_metadata", Tools: []mcpToolDef{}, Candidates: []evolution.Finding{}}
			probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			definitions, err := server.client.ListTools(probeCtx)
			if err != nil {
				report.Errors = []string{err.Error()}
				report.RequiresReview = true
				return report, nil
			}
			if len(definitions) > 1000 {
				report.Errors = []string{"tool count exceeds audit budget"}
				report.RequiresReview = true
				return report, nil
			}
			data, err := json.Marshal(definitions)
			if err != nil {
				return nil, err
			}
			if len(data) > 256*1024 || len(definitions) > 1000 {
				report.Errors = []string{"tool metadata exceeds audit budget"}
				report.RequiresReview = true
				return report, nil
			}
			if err := json.Unmarshal(data, &report.Tools); err != nil {
				return nil, err
			}
			report.Fingerprint, err = mcpDefinitionFingerprint(report.Tools)
			if err != nil {
				return nil, err
			}
			report.Changed = report.Fingerprint != baseline
			report.Complete = len(report.Tools) > 0
			seen := make(map[string]bool)
			for _, definition := range report.Tools {
				if strings.TrimSpace(definition.Name) == "" || seen[definition.Name] || definition.InputSchema["type"] != "object" {
					report.Complete = false
					report.Errors = append(report.Errors, "empty/duplicate tool name or invalid object input schema")
				}
				seen[definition.Name] = true
				report.Candidates = append(report.Candidates, evolution.ScanContent(definition.Name+"\n"+definition.Description+"\n"+mcpSchemaText(definition.InputSchema), definition.Name)...)
			}
			if len(report.Tools) == 0 {
				report.Errors = append(report.Errors, "no tool definitions returned; audit has no coverage")
			}
			report.RequiresReview = report.Changed || !report.Complete || len(report.Candidates) > 0
			return report, nil
		},
	}
}

func mcpSchemaText(value any) string {
	var pieces []string
	var collect func(any)
	collect = func(value any) {
		switch typed := value.(type) {
		case string:
			pieces = append(pieces, typed)
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				pieces = append(pieces, key)
				collect(typed[key])
			}
		case []any:
			for _, item := range typed {
				collect(item)
			}
		}
	}
	collect(value)
	return strings.Join(pieces, "\n")
}

package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/covoyage/covo-agent/internal/toolset"
	"github.com/covoyage/covonaut/agentcore"
)

func hideCyberOnlyTools(defs []agentcore.ToolDefinition, cyberMode bool) []agentcore.ToolDefinition {
	if cyberMode {
		return defs
	}
	out := make([]agentcore.ToolDefinition, 0, len(defs))
	for _, td := range defs {
		if toolset.CyberOnlyTools[td.Name] {
			continue
		}
		out = append(out, td)
	}
	return out
}

type toolVisibilityFilter func(context.Context, string) bool

type toolDiscoveryPolicyKey struct{}

const (
	defaultToolDiscoveryBytes = 32_000
	maxToolDiscoveryBytes     = 128_000
)

func toolDiscoveryBudget(requested int) (int, error) {
	if requested == 0 {
		return defaultToolDiscoveryBytes, nil
	}
	if requested < 1024 || requested > maxToolDiscoveryBytes {
		return 0, fmt.Errorf("max_output_bytes must be between 1024 and %d", maxToolDiscoveryBytes)
	}
	return requested, nil
}

func toolDiscoveryBudgetSchema() map[string]any {
	return map[string]any{
		"type": "integer", "minimum": 1024, "maximum": maxToolDiscoveryBytes,
		"description": "Maximum JSON output bytes (default 32000, maximum 128000). Schemas are never partially truncated.",
	}
}

func (e *Extension) SetToolDiscoveryPolicy(policy func(context.Context, []string) []string) {
	e.toolDiscoveryPolicy = policy
}

func toolVisible(ctx context.Context, name string, filters []toolVisibilityFilter) bool {
	for _, filter := range filters {
		if filter != nil && !filter(ctx, name) {
			return false
		}
	}
	return true
}

func buildToolSearchTool(getToolDefs func() []agentcore.ToolDefinition, filters ...toolVisibilityFilter) *agentcore.Tool {
	return buildToolSearchToolWithCatalog(func() ([]toolSearchDefinition, error) {
		return toolSearchDefinitions(getToolDefs()), nil
	}, filters...)
}

func buildToolSearchToolWithCatalog(getCatalog func() ([]toolSearchDefinition, error), filters ...toolVisibilityFilter) *agentcore.Tool {
	cache := &toolSearchCache{}
	return &agentcore.Tool{
		Name: "tool_search",
		Description: strings.Join([]string{
			"Discover tools with BM25 capability search or exact-name selection.",
			"Returns matching tools' names, descriptions, and complete parameter schemas.",
			"Search includes local retrieval tags, parameter names, descriptions, and action/enum values.",
			"Use select:Name1,Name2 for exact selection, or +word to require a substring in the tool name.",
			"",
			"Use the returned schema with 'tool_call' to invoke a tool by name.",
			"Use 'tool_describe' only to refresh a missing or changed schema.",
			"Budget-limited results list deferred tools; describe one with a larger max_output_bytes. Do not retry schemas marked schema_too_large.",
			"Translate non-English requests into English capability keywords before searching; preserve exact tool names.",
			"Tools already visible in your tool list do not need searching.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "English capability keywords (e.g. 'cancel download'), +name requirements, or select:Name1,Name2.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default: 10).",
					"minimum":     1,
					"maximum":     50,
				},
				"max_output_bytes": toolDiscoveryBudgetSchema(),
			},
			"required": []string{"query"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Query          string `json:"query"`
				Limit          int    `json:"limit"`
				MaxOutputBytes int    `json:"max_output_bytes"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Query) == "" {
				return nil, fmt.Errorf("query is required")
			}
			budget, err := toolDiscoveryBudget(params.MaxOutputBytes)
			if err != nil {
				return nil, err
			}
			if params.Limit <= 0 {
				params.Limit = 10
			}
			if params.Limit > 50 {
				params.Limit = 50
			}

			definitions, err := getCatalog()
			if err != nil {
				return nil, err
			}
			var visible []toolSearchDefinition
			for _, def := range definitions {
				if !isDiscoveryTool(def.Name) && toolVisible(ctx, def.Name, filters) {
					visible = append(visible, def)
				}
			}
			index, err := cache.snapshotCatalog(visible)
			if err != nil {
				return nil, err
			}
			return limitToolSearchOutput(index.search(params.Query, params.Limit), budget)
		},
	}
}

func toolSearchSchemaText(schema map[string]any) []string {
	var text []string
	for _, key := range []string{"title", "description", "const"} {
		if value, ok := schema[key].(string); ok {
			text = append(text, value)
		}
	}
	if values, ok := schema["enum"].([]any); ok {
		for _, value := range values {
			if value, ok := value.(string); ok {
				text = append(text, value)
			}
		}
	}
	for _, key := range []string{"properties", "$defs", "definitions", "patternProperties"} {
		if properties, ok := schema[key].(map[string]any); ok {
			for name, value := range properties {
				if key == "properties" {
					text = append(text, name)
				}
				if child, ok := value.(map[string]any); ok {
					text = append(text, toolSearchSchemaText(child)...)
				}
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties", "anyOf", "oneOf", "allOf", "prefixItems"} {
		switch value := schema[key].(type) {
		case map[string]any:
			text = append(text, toolSearchSchemaText(value)...)
		case []any:
			for _, value := range value {
				if child, ok := value.(map[string]any); ok {
					text = append(text, toolSearchSchemaText(child)...)
				}
			}
		}
	}
	return text
}

func buildToolDescribeTool(getToolDefs func() []agentcore.ToolDefinition, filters ...toolVisibilityFilter) *agentcore.Tool {
	return buildToolDescribeToolWithCatalog(func() ([]toolSearchDefinition, error) {
		return toolSearchDefinitions(getToolDefs()), nil
	}, filters...)
}

func buildToolDescribeToolWithCatalog(getCatalog func() ([]toolSearchDefinition, error), filters ...toolVisibilityFilter) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "tool_describe",
		Description: strings.Join([]string{
			"Refresh the full JSON schema for a tool when its search result is missing or stale.",
			"Returns the tool's name, description, and complete parameter schema.",
			"For output_budget_exceeded, raise max_output_bytes to required_bytes. schema_too_large is terminal: choose another tool or a directly visible interface.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Exact tool name (as returned by tool_search).",
				},
				"max_output_bytes": toolDiscoveryBudgetSchema(),
			},
			"required": []string{"name"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Name           string `json:"name"`
				MaxOutputBytes int    `json:"max_output_bytes"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Name) == "" {
				return nil, fmt.Errorf("name is required")
			}
			budget, err := toolDiscoveryBudget(params.MaxOutputBytes)
			if err != nil {
				return nil, err
			}

			toolDefs, err := getCatalog()
			if err != nil {
				return nil, err
			}
			var names []string
			for _, td := range toolDefs {
				if !isDiscoveryTool(td.Name) && toolVisible(ctx, td.Name, filters) {
					names = append(names, td.Name)
				}
			}
			name, candidates := resolveToolSearchName(names, params.Name)
			if len(candidates) > 1 {
				return nil, fmt.Errorf("tool name %q is ambiguous; use an exact name: %s", params.Name, strings.Join(candidates, ", "))
			}
			for _, td := range toolDefs {
				if name != "" && td.Name == name {
					declaration := map[string]any{
						"name":        td.Name,
						"description": td.Description,
						"parameters":  td.Parameters,
					}
					if td.Source != nil {
						declaration["source"] = *td.Source
					}
					return limitToolDescription(declaration, budget)
				}
			}

			return nil, fmt.Errorf("tool '%s' not found or not available in this role; use tool_search to discover available tools", params.Name)
		},
	}
}

func (e *Extension) toolDiscoveryCatalog(agent *agentcore.Agent) ([]toolSearchDefinition, error) {
	var definitions []agentcore.ToolDefinition
	registered := make(map[string]bool)
	for _, name := range agent.ToolNames() {
		registered[name] = true
		if tool, ok := agent.GetTool(name); ok {
			definitions = append(definitions, agentcore.ToolDefinition{
				Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters, SearchTerms: tool.Tags,
			})
		}
	}
	entries := toolSearchDefinitions(hideCyberOnlyTools(definitions, e.cyberMode))
	if !registered["mcp"] {
		return entries, nil
	}
	remote, err := e.mcpStore.discoverySnapshot()
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, entry := range remote {
		counts[entry.Name]++
	}
	for _, entry := range remote {
		if !registered[entry.Name] && counts[entry.Name] == 1 {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func buildToolCallTool(agent *agentcore.Agent, filters ...toolVisibilityFilter) *agentcore.Tool {
	return buildToolCallToolWithReview(agent, func(name string, parameters map[string]any) string {
		review := &toolSchemaReview{}
		review.replace(agent.State().Messages())
		return review.check(name, parameters)
	}, filters...)
}

func buildToolCallToolWithReview(agent *agentcore.Agent, checkSchema func(string, map[string]any) string, filters ...toolVisibilityFilter) *agentcore.Tool {
	return buildToolCallToolWithCatalog(agent, checkSchema, nil, nil, filters...)
}

func buildToolCallToolWithCatalog(agent *agentcore.Agent, checkSchema func(string, map[string]any) string, getCatalog func() ([]toolSearchDefinition, error), review *toolSchemaReview, filters ...toolVisibilityFilter) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "tool_call",
		Description: strings.Join([]string{
			"Invoke a tool discovered via 'tool_search' by its name with the given arguments.",
			"Use the complete parameter schema returned by 'tool_search'; no extra describe call is needed.",
			"The matching schema must still be present in the current model context; refresh it with 'tool_describe' if missing or changed.",
			"Policy, hooks, and approval flows run exactly as for any directly-listed tool.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Exact tool name to invoke.",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "Arguments for the tool, matching its parameter schema.",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Name) == "" {
				return map[string]any{
					"status": "invalid_request",
					"error":  "tool name is required; call tool_search, then pass {name, arguments}",
				}, nil
			}

			if params.Name == "tool_call" {
				return map[string]any{
					"status": "invalid_request",
					"error":  "tool_call cannot invoke itself; pass the discovered target tool name",
				}, nil
			}
			if isDiscoveryTool(strings.ToLower(strings.TrimSpace(params.Name))) {
				return map[string]any{
					"status": "invalid_request",
					"error":  "discovery tools must be called directly, not through tool_call",
				}, nil
			}
			var names []string
			for _, name := range agent.ToolNames() {
				if !isDiscoveryTool(name) && toolVisible(ctx, name, filters) {
					names = append(names, name)
				}
			}
			var remote map[string]toolSearchDefinition
			if getCatalog != nil {
				definitions, err := getCatalog()
				if err != nil {
					return nil, err
				}
				remote = make(map[string]toolSearchDefinition)
				for _, definition := range definitions {
					if definition.Source != nil && toolVisible(ctx, definition.Name, filters) {
						names = append(names, definition.Name)
						remote[definition.Name] = definition
					}
				}
			}
			name, candidates := resolveToolSearchName(names, params.Name)
			if len(candidates) > 1 {
				return map[string]any{
					"status":     "invalid_request",
					"error":      "tool name is ambiguous; use an exact name returned by tool_search",
					"candidates": candidates,
				}, nil
			}
			if name == "" {
				return map[string]any{
					"status": "unavailable",
					"error":  fmt.Sprintf("tool '%s' was not found or is not available in this role", params.Name),
				}, nil
			}
			params.Name = name
			if len(params.Arguments) == 0 || string(params.Arguments) == "null" {
				params.Arguments = json.RawMessage(`{}`)
			}
			if name == "mcp" && getCatalog != nil {
				var envelope struct {
					Action string `json:"action"`
					Name   string `json:"name"`
				}
				if json.Unmarshal(params.Arguments, &envelope) == nil && (envelope.Action == "call" || (envelope.Action == "" && strings.Contains(envelope.Name, "__"))) {
					return map[string]any{"status": "schema_required", "tool": name, "error": "Search or describe the concrete mcp__<server>__<tool> name, then pass that name and its schema-shaped arguments to tool_call; the generic MCP envelope does not review the remote contract."}, nil
				}
			}
			if definition, ok := remote[name]; ok {
				if status := review.checkSource(name, definition.Parameters, definition.Source); status != "" {
					return map[string]any{"status": status, "tool": name, "error": "Review this MCP tool's complete schema and source with tool_search or tool_describe, then retry."}, nil
				}
				if err := agentcore.ValidateToolArguments(&agentcore.Tool{Parameters: definition.Parameters}, string(params.Arguments)); err != nil {
					return map[string]any{"status": "invalid_request", "tool": name, "error": err.Error()}, nil
				}
				var arguments map[string]any
				decoder := json.NewDecoder(bytes.NewReader(params.Arguments))
				decoder.UseNumber()
				if err := decoder.Decode(&arguments); err != nil || arguments == nil {
					return map[string]any{"status": "invalid_request", "tool": name, "error": "MCP tool arguments must be a JSON object"}, nil
				}
				parameters, err := json.Marshal(definition.Parameters)
				if err != nil {
					return nil, err
				}
				fingerprint, err := toolSchemaFingerprint(name, parameters, definition.Source)
				if err != nil {
					return nil, err
				}
				ctx = context.WithValue(ctx, mcpDiscoveryGuardKey{}, mcpDiscoveryGuard{source: *definition.Source, fingerprint: fingerprint})
				envelope, err := json.Marshal(map[string]any{"action": "call", "server": definition.Source.Server, "tool": definition.Source.Tool, "arguments": arguments})
				if err != nil {
					return nil, err
				}
				text, err := agent.InvokeTool(ctx, "mcp", envelope)
				if err != nil {
					return map[string]any{"status": "error", "tool": name, "error": err.Error()}, nil
				}
				return map[string]any{"status": "ok", "tool": name, "result": text}, nil
			}
			target, ok := agent.GetTool(params.Name)
			if !ok {
				return map[string]any{"status": "unavailable", "error": "tool was removed; search again"}, nil
			}
			if status := checkSchema(params.Name, target.Parameters); status != "" {
				return map[string]any{
					"status": status,
					"error":  "A complete, current parameter schema is required in the model context; call tool_search or tool_describe, then retry using that schema",
					"tool":   params.Name,
				}, nil
			}

			// InvokeTool runs the same before/after hooks, plan gate, and
			// approval path as a model-issued call. Do not call Func directly.
			text, err := agent.InvokeTool(ctx, params.Name, params.Arguments)
			if err != nil {
				return map[string]any{
					"status": "error",
					"error":  err.Error(),
					"tool":   params.Name,
				}, nil
			}

			return map[string]any{
				"status": "ok",
				"result": text,
				"tool":   params.Name,
			}, nil
		},
	}
}

type toolSchemaReview struct {
	mu        sync.RWMutex
	contracts map[string][32]byte
}

func (e *Extension) LifecycleHook() agentcore.LifecycleHook { return e }

func (e *Extension) AfterModelCall(_ context.Context, _ *agentcore.AgentRunContext, model *agentcore.ModelCallContext) {
	if e.toolReview == nil {
		return
	}
	if model.Request == nil || model.Err != nil {
		e.toolReview.replace(nil)
		return
	}
	e.toolReview.replace(model.Request.Messages)
}

func toolSchemaFingerprint(name string, parameters json.RawMessage, sources ...*toolSearchSource) ([32]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(parameters))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return [32]byte{}, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, err
	}
	if len(sources) > 0 && sources[0] != nil {
		source, err := json.Marshal(sources[0])
		if err != nil {
			return [32]byte{}, err
		}
		name += "\x00" + string(source)
	}
	return sha256.Sum256(append([]byte(name+"\x00"), canonical...)), nil
}

func (review *toolSchemaReview) replace(messages []agentcore.Message) {
	contracts := make(map[string][32]byte)
	calls := make(map[string]string)
	for _, message := range messages {
		if message.Role == agentcore.RoleAssistant {
			for _, call := range message.ToolCalls {
				if call.ID != "" {
					calls[call.ID] = call.Name
				}
			}
		}
		name := calls[message.ToolCallID]
		if message.Role != agentcore.RoleTool || (name != "tool_search" && name != "tool_describe") ||
			(message.Name != "" && message.Name != name) {
			continue
		}
		type declaration struct {
			Name       string            `json:"name"`
			Parameters json.RawMessage   `json:"parameters"`
			Source     *toolSearchSource `json:"source,omitempty"`
		}
		var result struct {
			declaration
			Matches []declaration `json:"matches"`
		}
		if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
			continue
		}
		declarations := result.Matches
		if name == "tool_describe" {
			declarations = []declaration{result.declaration}
		}
		for _, declaration := range declarations {
			if declaration.Name == "" || len(declaration.Parameters) == 0 || isDiscoveryTool(declaration.Name) {
				continue
			}
			if fingerprint, err := toolSchemaFingerprint(declaration.Name, declaration.Parameters, declaration.Source); err == nil {
				contracts[declaration.Name] = fingerprint
			}
		}
	}
	review.mu.Lock()
	review.contracts = contracts
	review.mu.Unlock()
}

func (review *toolSchemaReview) check(name string, parameters map[string]any) string {
	return review.checkSource(name, parameters, nil)
}

func (review *toolSchemaReview) checkSource(name string, parameters map[string]any, source *toolSearchSource) string {
	review.mu.RLock()
	expected, ok := review.contracts[name]
	review.mu.RUnlock()
	if !ok {
		return "schema_required"
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return "schema_changed"
	}
	fingerprint, err := toolSchemaFingerprint(name, encoded, source)
	if err != nil || fingerprint != expected {
		return "schema_changed"
	}
	return ""
}

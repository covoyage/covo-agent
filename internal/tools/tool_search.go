package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func buildToolSearchTool(getToolDefs func() []agentcore.ToolDefinition) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "tool_search",
		Description: strings.Join([]string{
			"Search for available tools by keyword or capability description.",
			"Returns matching tools' names and descriptions so you can discover",
			"what tools are available for a given task.",
			"",
			"Use 'tool_describe' to view a tool's full parameter schema,",
			"then 'tool_call' to invoke it by name.",
			"Tools already visible in your tool list do not need searching.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Keywords describing the capability you need (e.g. 'edit files', 'search code', 'git').",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default: 10).",
				},
			},
			"required": []string{"query"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Query) == "" {
				return nil, fmt.Errorf("query is required")
			}
			if params.Limit <= 0 {
				params.Limit = 10
			}
			if params.Limit > 50 {
				params.Limit = 50
			}

			toolDefs := getToolDefs()
			query := strings.ToLower(params.Query)
			queryTerms := strings.Fields(query)

			type match struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				score       float64
			}

			var matches []match
			for _, td := range toolDefs {
				name := td.Name
				desc := td.Description
				lowerName := strings.ToLower(name)
				lowerDesc := strings.ToLower(desc)
				score := 0.0
				for _, term := range queryTerms {
					if strings.Contains(lowerName, term) {
						score += 10.0
					}
					if strings.Contains(lowerDesc, term) {
						score += 2.0
					}
					if lowerName == term {
						score += 50.0
					}
				}

				if score > 0 {
					descPreview := desc
					if len(descPreview) > 300 {
						descPreview = descPreview[:300] + "..."
					}
					matches = append(matches, match{
						Name:        name,
						Description: descPreview,
						score:       score,
					})
				}
			}

			// Sort by score descending
			for i := 0; i < len(matches); i++ {
				for j := i + 1; j < len(matches); j++ {
					if matches[j].score > matches[i].score {
						matches[i], matches[j] = matches[j], matches[i]
					}
				}
			}

			if len(matches) > params.Limit {
				matches = matches[:params.Limit]
			}

			results := make([]map[string]string, len(matches))
			for i, m := range matches {
				results[i] = map[string]string{
					"name":        m.Name,
					"description": m.Description,
				}
			}

			return map[string]any{
				"query":           params.Query,
				"total_available": len(toolDefs),
				"matches":         results,
			}, nil
		},
	}
}

func buildToolDescribeTool(getToolDefs func() []agentcore.ToolDefinition) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "tool_describe",
		Description: strings.Join([]string{
			"Load the full JSON schema for a tool returned by 'tool_search'.",
			"Returns the tool's name, description, and complete parameter schema.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Exact tool name (as returned by tool_search).",
				},
			},
			"required": []string{"name"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if strings.TrimSpace(params.Name) == "" {
				return nil, fmt.Errorf("name is required")
			}

			toolDefs := getToolDefs()
			for _, td := range toolDefs {
				if td.Name == params.Name {
					return map[string]any{
						"name":        td.Name,
						"description": td.Description,
						"parameters":  td.Parameters,
					}, nil
				}
			}

			return nil, fmt.Errorf("tool '%s' not found. Check spelling or use tool_search to discover available tools", params.Name)
		},
	}
}

func buildToolCallTool(agent *agentcore.Agent) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "tool_call",
		Description: strings.Join([]string{
			"Invoke a tool discovered via 'tool_search' by its name with the given arguments.",
			"The argument shape must match the tool's parameter schema (see 'tool_describe').",
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
			"required": []string{"name", "arguments"},
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
				return nil, fmt.Errorf("name is required")
			}

			tool, ok := agent.GetTool(params.Name)
			if !ok {
				return nil, fmt.Errorf("tool '%s' not found in registry", params.Name)
			}

			result, err := tool.Func(ctx, params.Arguments)
			if err != nil {
				return map[string]any{
					"status": "error",
					"error":  err.Error(),
					"tool":   params.Name,
				}, nil
			}

			return map[string]any{
				"status": "ok",
				"result": result,
				"tool":   params.Name,
			}, nil
		},
	}
}

package codegraph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/covoyage/covo-agent/internal/codegraph"
	"github.com/covoyage/covonaut/agentcore"
)

// BuildCodeGraphTool returns a tool that builds a Go package dependency graph.
func BuildCodeGraphTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "code_graph",
		Description: `Build a dependency graph of Go packages in the workspace.

Analyzes import statements across all .go files and produces a directed graph
showing which packages depend on which. Can detect import cycles.

Output formats: text (default), mermaid, dot.`,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"format": map[string]any{
					"type":        "string",
					"enum":        []string{"text", "mermaid", "dot"},
					"description": "Output format (default: text)",
				},
				"detect_cycles": map[string]any{
					"type":        "boolean",
					"description": "If true, only report import cycles (default: false)",
				},
			},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Format       string `json:"format"`
				DetectCycles bool   `json:"detect_cycles"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("parse arguments: %w", err)
			}
			if params.Format == "" {
				params.Format = "text"
			}

			workDir, _ := ctx.Value("work_dir").(string)
			if workDir == "" {
				workDir = "."
			}

			graph, err := codegraph.Build(workDir)
			if err != nil {
				return nil, fmt.Errorf("build code graph: %w", err)
			}

			if params.DetectCycles {
				cycles := graph.DetectCycles()
				if len(cycles) == 0 {
					return "No import cycles detected.", nil
				}
				result := fmt.Sprintf("Found %d import cycle(s):\n", len(cycles))
				for i, cycle := range cycles {
					result += fmt.Sprintf("%d. %s\n", i+1, cycle)
				}
				return result, nil
			}

			switch params.Format {
			case "mermaid":
				return graph.ToMermaid(), nil
			case "dot":
				return graph.ToDOT(), nil
			default:
				return graph.ToText(), nil
			}
		},
	}
}

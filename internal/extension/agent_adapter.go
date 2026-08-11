package extension

import (
	"context"
	"encoding/json"

	"github.com/covoyage/covonaut/agentcore"
)

// AgentExtension wraps the extension Manager as an agentcore.Extension.
// It implements ToolProvider to contribute extension tools to the agent.
type AgentExtension struct {
	mgr *Manager
}

func NewAgentExtension(mgr *Manager) *AgentExtension {
	return &AgentExtension{mgr: mgr}
}

func (e *AgentExtension) Name() string {
	return "extensions"
}

func (e *AgentExtension) Init(ctx context.Context, agent *agentcore.Agent) error {
	return e.mgr.Discover(ctx)
}

func (e *AgentExtension) Dispose() error {
	return nil
}

func (e *AgentExtension) Tools() []*agentcore.Tool {
	exts := e.mgr.List()
	if len(exts) == 0 {
		return nil
	}
	var tools []*agentcore.Tool
	for _, ext := range exts {
		if !ext.Enabled {
			continue
		}
		extName := ext.Name
		for _, td := range ext.Tools {
			toolName := td.Name
			tools = append(tools, &agentcore.Tool{
				Name:        extName + "_" + toolName,
				Description: td.Description,
				Parameters:  parseParams(td.Parameters),
				Func: func(ctx context.Context, args json.RawMessage) (any, error) {
					result, err := e.mgr.ExecuteTool(ctx, extName, toolName, args)
					if err != nil {
						return nil, err
					}
					var resultMap map[string]any
					if err := json.Unmarshal(result, &resultMap); err != nil {
						return string(result), nil
					}
					return resultMap, nil
				},
			})
		}
	}
	return tools
}

func parseParams(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return params
}

var _ agentcore.Extension = (*AgentExtension)(nil)
var _ agentcore.ToolProvider = (*AgentExtension)(nil)

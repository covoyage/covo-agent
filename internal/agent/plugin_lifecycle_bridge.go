package agent

import (
	"context"
	"encoding/json"

	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/covoyage/covonaut/agentcore"
)

// PluginLifecycleAdapter wraps a plugin.LifecycleHook as an agentcore.LifecycleHook.
type PluginLifecycleAdapter struct {
	inner plugin.LifecycleHook
}

func NewPluginLifecycleAdapter(hook plugin.LifecycleHook) *PluginLifecycleAdapter {
	return &PluginLifecycleAdapter{inner: hook}
}

func (a *PluginLifecycleAdapter) BeforeAgentRun(ctx context.Context, arc *agentcore.AgentRunContext) error {
	return nil
}

func (a *PluginLifecycleAdapter) AfterAgentRun(ctx context.Context, arc *agentcore.AgentRunContext, output string, err error) {
}

func (a *PluginLifecycleAdapter) BeforeModelCall(ctx context.Context, arc *agentcore.AgentRunContext, mcc *agentcore.ModelCallContext) error {
	if mcc == nil {
		return nil
	}
	msgs := a.extractMessages(mcc)
	return a.inner.BeforeModelCall(ctx, msgs)
}

func (a *PluginLifecycleAdapter) AfterModelCall(ctx context.Context, arc *agentcore.AgentRunContext, mcc *agentcore.ModelCallContext) {
	if mcc == nil {
		return
	}
	msgs := a.extractMessages(mcc)
	response := ""
	if mcc.Response != nil {
		response = mcc.Response.Content
	}
	a.inner.AfterModelCall(ctx, msgs, response, mcc.Err)

	// Apply TransformModelOutput to modify the response content.
	if mcc.Response != nil && mcc.Response.Content != "" && mcc.Err == nil {
		transformed, err := a.inner.TransformModelOutput(ctx, mcc.Response.Content)
		if err == nil && transformed != mcc.Response.Content {
			mcc.Response.Content = transformed
		}
	}
}

func (a *PluginLifecycleAdapter) extractMessages(mcc *agentcore.ModelCallContext) []any {
	if mcc.Request != nil {
		msgs := make([]any, len(mcc.Request.Messages))
		for i, m := range mcc.Request.Messages {
			msgs[i] = m
		}
		return msgs
	}
	if mcc.Response != nil {
		return nil
	}
	return nil
}

func (a *PluginLifecycleAdapter) BeforeToolExecution(ctx context.Context, arc *agentcore.AgentRunContext, tec *agentcore.ToolExecutionContext) error {
	if tec == nil {
		return nil
	}
	for _, tc := range tec.ToolCalls {
		var args json.RawMessage
		if tc.Arguments != "" {
			args = json.RawMessage(tc.Arguments)
		}
		if err := a.inner.BeforeToolCall(ctx, tc.Name, args); err != nil {
			return err
		}
	}
	return nil
}

func (a *PluginLifecycleAdapter) AfterToolExecution(ctx context.Context, arc *agentcore.AgentRunContext, tec *agentcore.ToolExecutionContext) {
	if tec == nil {
		return
	}
	for i, tc := range tec.ToolCalls {
		var args json.RawMessage
		if tc.Arguments != "" {
			args = json.RawMessage(tc.Arguments)
		}
		result := ""
		var err error
		if i < len(tec.Results) {
			result = tec.Results[i].Result
			err = tec.Results[i].Err
		}
		a.inner.AfterToolCall(ctx, tc.Name, args, result, err)
	}
}

func (a *PluginLifecycleAdapter) BeforeTurn(ctx context.Context, arc *agentcore.AgentRunContext) error {
	a.inner.OnTurnStart(ctx)
	return nil
}

func (a *PluginLifecycleAdapter) AfterTurn(ctx context.Context, arc *agentcore.AgentRunContext, info agentcore.TurnInfo) {
	a.inner.OnTurnEnd(ctx)
}

func (a *PluginLifecycleAdapter) BeforeMessagePersist(ctx context.Context, arc *agentcore.AgentRunContext, msg *agentcore.Message) error {
	return nil
}

func (a *PluginLifecycleAdapter) AfterMessagePersist(ctx context.Context, arc *agentcore.AgentRunContext, msg agentcore.Message) {
}

// ConvertPluginHooks converts plugin.LifecycleHook slices to agentcore.LifecycleHook.
func ConvertPluginHooks(hooks []plugin.LifecycleHook) []agentcore.LifecycleHook {
	if len(hooks) == 0 {
		return nil
	}
	result := make([]agentcore.LifecycleHook, 0, len(hooks))
	for _, h := range hooks {
		result = append(result, NewPluginLifecycleAdapter(h))
	}
	return result
}

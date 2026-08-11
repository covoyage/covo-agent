package agent

import (
	"context"

	"github.com/covoyage/covonaut/agentcore"

	"github.com/covoyage/covo-agent/internal/lifecycle"
)

type lifecycleContributorBridge struct {
	agentcore.BaseLifecycleHook
	registry *lifecycle.Registry
	agent    *CovoAgent
}

func newLifecycleContributorBridge(agent *CovoAgent, registry *lifecycle.Registry) agentcore.LifecycleHook {
	if registry == nil {
		registry = lifecycle.Global()
	}
	return &lifecycleContributorBridge{registry: registry, agent: agent}
}

func (bridge *lifecycleContributorBridge) BeforeAgentRun(ctx context.Context, arc *agentcore.AgentRunContext) error {
	bridge.emit(lifecycle.EventOnSessionStart, ctx, arc, "", "", nil)
	return nil
}

func (bridge *lifecycleContributorBridge) AfterAgentRun(ctx context.Context, arc *agentcore.AgentRunContext, output string, err error) {
	if err != nil {
		bridge.emit(lifecycle.EventOnError, ctx, arc, "", output, err)
	}
	bridge.emit(lifecycle.EventOnSessionEnd, ctx, arc, "", output, err)
}

func (bridge *lifecycleContributorBridge) BeforeTurn(ctx context.Context, arc *agentcore.AgentRunContext) error {
	bridge.emit(lifecycle.EventBeforeTurn, ctx, arc, "", "", nil)
	return nil
}

func (bridge *lifecycleContributorBridge) AfterTurn(ctx context.Context, arc *agentcore.AgentRunContext, _ agentcore.TurnInfo) {
	bridge.emit(lifecycle.EventAfterTurn, ctx, arc, "", "", nil)
}

func (bridge *lifecycleContributorBridge) BeforeToolExecution(ctx context.Context, arc *agentcore.AgentRunContext, execution *agentcore.ToolExecutionContext) error {
	if execution == nil {
		return nil
	}
	for _, call := range execution.ToolCalls {
		bridge.emit(lifecycle.EventBeforeToolCall, ctx, arc, call.Name, call.Arguments, nil)
	}
	return nil
}

func (bridge *lifecycleContributorBridge) AfterToolExecution(ctx context.Context, arc *agentcore.AgentRunContext, execution *agentcore.ToolExecutionContext) {
	if execution == nil {
		return
	}
	for index, call := range execution.ToolCalls {
		result := ""
		var err error
		if index < len(execution.Results) {
			result = execution.Results[index].Result
			err = execution.Results[index].Err
		}
		bridge.emit(lifecycle.EventAfterToolCall, ctx, arc, call.Name, result, err)
		if err != nil {
			bridge.emit(lifecycle.EventOnError, ctx, arc, call.Name, result, err)
		}
	}
}

func (bridge *lifecycleContributorBridge) emit(event lifecycle.Event, ctx context.Context, arc *agentcore.AgentRunContext, toolName, value string, err error) {
	hookContext := &lifecycle.HookContext{
		Ctx:      ctx,
		ToolName: toolName,
		Error:    err,
	}
	if bridge.agent != nil && bridge.agent.sessionMgr != nil {
		hookContext.SessionID = bridge.agent.sessionMgr.CurrentID()
	}
	if arc != nil {
		hookContext.Turn = int(arc.Turn)
		hookContext.Extra = map[string]any{"input": arc.Input}
	}
	if event == lifecycle.EventBeforeToolCall {
		hookContext.ToolInput = value
	} else {
		hookContext.ToolResult = value
	}
	bridge.registry.Emit(event, hookContext)
}

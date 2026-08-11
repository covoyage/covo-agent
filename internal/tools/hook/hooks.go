package hook

import (
	"encoding/json"
	"sync"
)

// Hook types for the tool/LLM-call plugin lifecycle.

type HookContext map[string]any

// PreLLMCallHook runs before each LLM API call. Can inject context into system/user prompt.
type PreLLMCallHook func(ctx HookContext) HookContext

// PostLLMCallHook runs after each LLM API response.
type PostLLMCallHook func(ctx HookContext, response string, toolCalls []any) HookContext

// PreToolCallHook runs before tool execution. Return non-nil error to block.
type PreToolCallHook func(ctx HookContext, toolName string, args json.RawMessage) error

// PostToolCallHook runs after tool execution.
type PostToolCallHook func(ctx HookContext, toolName string, result string) HookContext

// TurnHook runs at turn boundaries.
type TurnHook func(ctx HookContext, message string) HookContext

// SessionHook runs at session boundaries.
type SessionHook func(ctx HookContext) HookContext

// ApprovalHook runs before/after approval requests.
type ApprovalHook func(ctx HookContext, command string) HookContext

// HookRegistry manages all registered lifecycle hooks.
type HookRegistry struct {
	mu sync.RWMutex

	preLLMCall     []PreLLMCallHook
	postLLMCall    []PostLLMCallHook
	preToolCall    []PreToolCallHook
	postToolCall   []PostToolCallHook
	onTurnStart    []TurnHook
	onTurnEnd      []TurnHook
	onSessionStart []SessionHook
	onSessionEnd   []SessionHook
	preApproval    []ApprovalHook
	postApproval   []ApprovalHook
}

func NewHookRegistry() *HookRegistry {
	return &HookRegistry{}
}

// --- Registration ---

func (r *HookRegistry) RegisterPreLLMCall(hook PreLLMCallHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preLLMCall = append(r.preLLMCall, hook)
}

func (r *HookRegistry) RegisterPostLLMCall(hook PostLLMCallHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.postLLMCall = append(r.postLLMCall, hook)
}

func (r *HookRegistry) RegisterPreToolCall(hook PreToolCallHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preToolCall = append(r.preToolCall, hook)
}

func (r *HookRegistry) RegisterPostToolCall(hook PostToolCallHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.postToolCall = append(r.postToolCall, hook)
}

func (r *HookRegistry) RegisterOnTurnStart(hook TurnHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onTurnStart = append(r.onTurnStart, hook)
}

func (r *HookRegistry) RegisterOnTurnEnd(hook TurnHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onTurnEnd = append(r.onTurnEnd, hook)
}

func (r *HookRegistry) RegisterOnSessionStart(hook SessionHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onSessionStart = append(r.onSessionStart, hook)
}

func (r *HookRegistry) RegisterOnSessionEnd(hook SessionHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onSessionEnd = append(r.onSessionEnd, hook)
}

func (r *HookRegistry) RegisterPreApproval(hook ApprovalHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preApproval = append(r.preApproval, hook)
}

func (r *HookRegistry) RegisterPostApproval(hook ApprovalHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.postApproval = append(r.postApproval, hook)
}

// --- Execution ---

func (r *HookRegistry) FirePreLLMCall(ctx HookContext) HookContext {
	r.mu.RLock()
	hooks := make([]PreLLMCallHook, len(r.preLLMCall))
	copy(hooks, r.preLLMCall)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx)
	}
	return ctx
}

func (r *HookRegistry) FirePostLLMCall(ctx HookContext, response string, toolCalls []any) HookContext {
	r.mu.RLock()
	hooks := make([]PostLLMCallHook, len(r.postLLMCall))
	copy(hooks, r.postLLMCall)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx, response, toolCalls)
	}
	return ctx
}

// FirePreToolCall returns error from the first hook that blocks.
func (r *HookRegistry) FirePreToolCall(ctx HookContext, toolName string, args json.RawMessage) error {
	r.mu.RLock()
	hooks := make([]PreToolCallHook, len(r.preToolCall))
	copy(hooks, r.preToolCall)
	r.mu.RUnlock()
	for _, h := range hooks {
		if err := h(ctx, toolName, args); err != nil {
			return err
		}
	}
	return nil
}

func (r *HookRegistry) FirePostToolCall(ctx HookContext, toolName, result string) HookContext {
	r.mu.RLock()
	hooks := make([]PostToolCallHook, len(r.postToolCall))
	copy(hooks, r.postToolCall)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx, toolName, result)
	}
	return ctx
}

func (r *HookRegistry) FireOnTurnStart(ctx HookContext, message string) HookContext {
	r.mu.RLock()
	hooks := make([]TurnHook, len(r.onTurnStart))
	copy(hooks, r.onTurnStart)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx, message)
	}
	return ctx
}

func (r *HookRegistry) FireOnTurnEnd(ctx HookContext, message string) HookContext {
	r.mu.RLock()
	hooks := make([]TurnHook, len(r.onTurnEnd))
	copy(hooks, r.onTurnEnd)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx, message)
	}
	return ctx
}

func (r *HookRegistry) FireOnSessionStart(ctx HookContext) HookContext {
	r.mu.RLock()
	hooks := make([]SessionHook, len(r.onSessionStart))
	copy(hooks, r.onSessionStart)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx)
	}
	return ctx
}

func (r *HookRegistry) FireOnSessionEnd(ctx HookContext) HookContext {
	r.mu.RLock()
	hooks := make([]SessionHook, len(r.onSessionEnd))
	copy(hooks, r.onSessionEnd)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx)
	}
	return ctx
}

func (r *HookRegistry) FirePreApproval(ctx HookContext, command string) HookContext {
	r.mu.RLock()
	hooks := make([]ApprovalHook, len(r.preApproval))
	copy(hooks, r.preApproval)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx, command)
	}
	return ctx
}

func (r *HookRegistry) FirePostApproval(ctx HookContext, command string) HookContext {
	r.mu.RLock()
	hooks := make([]ApprovalHook, len(r.postApproval))
	copy(hooks, r.postApproval)
	r.mu.RUnlock()
	for _, h := range hooks {
		ctx = h(ctx, command)
	}
	return ctx
}

// --- Management ---

func (r *HookRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.preLLMCall = nil
	r.postLLMCall = nil
	r.preToolCall = nil
	r.postToolCall = nil
	r.onTurnStart = nil
	r.onTurnEnd = nil
	r.onSessionStart = nil
	r.onSessionEnd = nil
	r.preApproval = nil
	r.postApproval = nil
}

func (r *HookRegistry) Counts() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return map[string]int{
		"pre_llm_call":     len(r.preLLMCall),
		"post_llm_call":    len(r.postLLMCall),
		"pre_tool_call":    len(r.preToolCall),
		"post_tool_call":   len(r.postToolCall),
		"on_turn_start":    len(r.onTurnStart),
		"on_turn_end":      len(r.onTurnEnd),
		"on_session_start": len(r.onSessionStart),
		"on_session_end":   len(r.onSessionEnd),
		"pre_approval":     len(r.preApproval),
		"post_approval":    len(r.postApproval),
	}
}

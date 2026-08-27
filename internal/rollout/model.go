// Package rollout provides complete conversation tracing and deterministic
// replay for debugging, regression testing, and analysis.
//
// A Rollout captures the full lifecycle of an agent run: every LLM request,
// every response (including tool calls), and every tool result. This enables:
//   - Deterministic replay against the same or different models
//   - Regression testing (verify prompt changes don't break behavior)
//   - Debugging (inspect exactly what the model saw and decided)
//   - Export/import for sharing reproducible traces
package rollout

import (
	"encoding/json"
	"time"
)

// Rollout is a complete record of an agent run, capturing every LLM
// interaction and tool execution in sequence.
type Rollout struct {
	ID        string            `json:"id"`
	SessionID string            `json:"session_id"`
	// ParentID references the rollout of the agent that spawned this one (a
	// subagent's parent), linking a subagent run to its caller's trace.
	ParentID  string            `json:"parent_id,omitempty"`
	Provider  string            `json:"provider"`
	Model     string            `json:"model"`
	CWD       string            `json:"cwd,omitempty"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at,omitempty"`
	Turns     []Turn            `json:"turns"`
	// Edges records subagent lifecycle interactions (send_message, result,
	// close) anchored to the turn that emitted them, linking this parent
	// rollout to spawned child rollouts at per-turn granularity.
	Edges    []InteractionEdge  `json:"edges,omitempty"`
	Config   RolloutConfig      `json:"config,omitempty"`
	Metadata map[string]string  `json:"metadata,omitempty"`
	Exported bool               `json:"exported,omitempty"`
}

// SubagentEdgeKind enumerates the lifecycle stages of a spawned subagent.
const (
	SubagentEdgeSpawn   = "spawn"
	SubagentEdgeSend    = "send_message"
	SubagentEdgeResult  = "result"
	SubagentEdgeClose   = "close"
	SubagentEdgeTimeout = "timeout"
)

// InteractionEdge records a subagent lifecycle event in the parent's rollout.
// It links the parent trace to a child rollout/session at a specific turn.
type InteractionEdge struct {
	Kind      string    `json:"kind"`
	ChildID   string    `json:"child_id,omitempty"`   // child session or rollout id
	ChildSession string  `json:"child_session,omitempty"`
	ParentTurn int       `json:"parent_turn,omitempty"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// Turn is a logical agent turn: one loop of "prompt the model -> run tools".
// It groups the main model interaction together with any auxiliary interactions
// (context compression, title generation, review, guardrails) that happened
// during that turn, plus the tool calls made by the main interaction.
type Turn struct {
	// Number is the 1-based logical turn sequence within the run.
	Number int `json:"number"`
	// Interactions are every LLM call made during this turn, in call order.
	// The first is usually the main agent call; the rest are auxiliary.
	Interactions []Interaction `json:"interactions"`
	// ToolCalls are the tool calls issued by the main model interaction,
	// with results filled in by ID.
	ToolCalls []ToolCallSnapshot `json:"tool_calls,omitempty"`
	StartedAt time.Time          `json:"started_at"`
	EndedAt   time.Time          `json:"ended_at,omitempty"`
	Error     string             `json:"error,omitempty"`
}

// Interaction is a single LLM request -> response cycle recorded within a turn.
// It distinguishes the main agent call from auxiliary calls (compression,
// titles, review, guardrails) so replays/diffs can focus on the primary path.
type Interaction struct {
	// Kind classifies the call: "main" for the agent's model call, or a
	// descriptive kind for auxiliary calls (e.g. "compression", "title",
	// "review", "guardrail", "aux").
	Kind     string           `json:"kind,omitempty"`
	Request  RequestSnapshot  `json:"request"`
	Response ResponseSnapshot `json:"response"`
	// ToolCalls are the tool calls produced by this interaction.
	ToolCalls []ToolCallSnapshot `json:"tool_calls,omitempty"`
	// Error is set if the LLM call itself failed.
	Error    string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// MainInteraction returns the primary model interaction of the turn, or nil.
func (t *Turn) MainInteraction() *Interaction {
	for i := range t.Interactions {
		if t.Interactions[i].Kind == "" || t.Interactions[i].Kind == "main" {
			return &t.Interactions[i]
		}
	}
	return nil
}

// RequestSnapshot captures the full LLM request for a single turn.
type RequestSnapshot struct {
	Model            string              `json:"model"`
	Messages         []MessageSnapshot   `json:"messages"`
	Tools            []ToolDefSnapshot   `json:"tools,omitempty"`
	Temperature      float64             `json:"temperature,omitempty"`
	MaxTokens        int64               `json:"max_tokens,omitempty"`
	FrequencyPenalty float64             `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64             `json:"presence_penalty,omitempty"`
	FastMode         bool                `json:"fast_mode,omitempty"`
	Thinking         *ThinkingSnapshot   `json:"thinking,omitempty"`
	ResponseFormat   *ResponseFmtSnapshot `json:"response_format,omitempty"`
}

// ResponseSnapshot captures the full LLM response for a single turn.
type ResponseSnapshot struct {
	Content       string              `json:"content,omitempty"`
	ToolCalls     []ToolCallSnapshot  `json:"tool_calls,omitempty"`
	FinishReason  string              `json:"finish_reason,omitempty"`
	PromptTokens  int64               `json:"prompt_tokens"`
	CompletionTokens int64            `json:"completion_tokens"`
	TotalTokens   int64               `json:"total_tokens"`
	SuppressPersist bool              `json:"suppress_persist,omitempty"`
}

// MessageSnapshot is a simplified message representation for storage.
type MessageSnapshot struct {
	Role       string              `json:"role"`
	Content    string              `json:"content,omitempty"`
	ToolCalls  []ToolCallSnapshot  `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	Name       string              `json:"name,omitempty"`
}

// ToolCallSnapshot captures a single tool call and its result.
type ToolCallSnapshot struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	Duration  int64  `json:"duration_ms,omitempty"`
}

// ToolDefSnapshot is the tool schema sent to the model.
type ToolDefSnapshot struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ThinkingSnapshot captures thinking/reasoning configuration.
type ThinkingSnapshot struct {
	IncludeThoughts bool   `json:"include_thoughts,omitempty"`
	Display         string `json:"display,omitempty"`
	Effort          string `json:"effort,omitempty"`
	Budget          int64  `json:"budget,omitempty"`
}

// ResponseFmtSnapshot captures the response format configuration.
type ResponseFmtSnapshot struct {
	Type       string         `json:"type"`
	JSONSchema map[string]any `json:"json_schema,omitempty"`
}

// RolloutConfig captures agent-level configuration that affects behavior.
type RolloutConfig struct {
	Mode          string `json:"mode,omitempty"`
	SystemPrompt  string `json:"system_prompt,omitempty"`
	Sandbox       string `json:"sandbox,omitempty"`
	YOLO          bool   `json:"yolo,omitempty"`
	ContextEngine string `json:"context_engine,omitempty"`
}

// Bundle is an exportable unit containing one or more rollouts with metadata.
type Bundle struct {
	Version   int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	Rollouts  []Rollout `json:"rollouts"`
}

// NewBundle creates a new export bundle from a list of rollouts.
func NewBundle(rollouts []Rollout) *Bundle {
	return &Bundle{
		Version:    1,
		ExportedAt: time.Now(),
		Rollouts:   rollouts,
	}
}

// Marshal serializes the bundle to indented JSON.
func (b *Bundle) Marshal() ([]byte, error) {
	return json.MarshalIndent(b, "", "  ")
}

// UnmarshalBundle deserializes a bundle from JSON.
func UnmarshalBundle(data []byte) (*Bundle, error) {
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

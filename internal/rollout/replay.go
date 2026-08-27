package rollout

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// ReplayMode controls how the replay engine handles tool execution.
type ReplayMode int

const (
	// ReplayModeDeterministic replays with recorded tool results (no real execution).
	ReplayModeDeterministic ReplayMode = iota
	// ReplayModeLive executes tools for real but replays the conversation history.
	ReplayModeLive
)

// ToolStrategy controls how tool calls are handled during replay.
type ToolStrategy int

const (
	// StrategyMock uses recorded tool results (default for deterministic mode).
	StrategyMock ToolStrategy = iota
	// StrategyReal executes tools for real against the working directory.
	StrategyReal
	// StrategyAbort stops replay on the first tool call.
	StrategyAbort
)

// ToolExecutor executes a tool call and returns the result.
type ToolExecutor interface {
	Execute(ctx context.Context, name string, args json.RawMessage) (string, error)
}

// ReplayConfig configures a replay session.
type ReplayConfig struct {
	Model       string
	Temperature *float64
	MaxTurns    int
	Mode        ReplayMode
	Strategy    ToolStrategy
	Provider    agentcore.Provider
	ToolExec    ToolExecutor
	Logger      *slog.Logger
}

// ReplayResult contains the outcome of a replay session.
type ReplayResult struct {
	Rollout       *Rollout              `json:"rollout"`
	OriginalID    string                `json:"original_id"`
	Mode          string                `json:"mode"`
	Strategy      string                `json:"strategy"`
	TurnsReplayed int                   `json:"turns_replayed"`
	TotalTokens   agentcore.TokenUsage  `json:"total_tokens"`
	Duration      time.Duration         `json:"duration"`
	Errors        []string              `json:"errors,omitempty"`
}

// ReplayEngine drives a replay of a recorded rollout.
type ReplayEngine struct {
	config ReplayConfig
	logger *slog.Logger
}

// NewReplayEngine creates a new replay engine.
func NewReplayEngine(cfg ReplayConfig) *ReplayEngine {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ReplayEngine{config: cfg, logger: logger}
}

// Replay re-drives the recorded rollout against the configured provider.
func (e *ReplayEngine) Replay(ctx context.Context, r *Rollout) (*ReplayResult, error) {
	if r == nil || len(r.Turns) == 0 {
		return nil, fmt.Errorf("rollout has no turns to replay")
	}
	if e.config.Provider == nil {
		return nil, fmt.Errorf("no provider configured for replay")
	}

	model := r.Model
	if e.config.Model != "" {
		model = e.config.Model
	}

	strategyName := "mock"
	switch e.config.Strategy {
	case StrategyReal:
		strategyName = "real"
	case StrategyAbort:
		strategyName = "abort"
	}

	result := &ReplayResult{
		OriginalID: r.ID,
		Mode:       "deterministic",
		Strategy:   strategyName,
		Rollout: &Rollout{
			ID:        generateID(),
			SessionID: r.SessionID,
			Provider:  r.Provider,
			Model:     model,
			CWD:       r.CWD,
			StartedAt: time.Now(),
			Metadata: map[string]string{
				"replay_of":      r.ID,
				"original_model": r.Model,
			},
		},
	}
	if e.config.Mode == ReplayModeLive {
		result.Mode = "live"
	}

	startTime := time.Now()
	maxTurns := e.config.MaxTurns
	if maxTurns <= 0 || maxTurns > len(r.Turns) {
		maxTurns = len(r.Turns)
	}

	var history []agentcore.Message

	for i, recordedTurn := range r.Turns {
		if i >= maxTurns {
			break
		}

		mi := recordedTurn.MainInteraction()
		if mi == nil {
			result.Errors = append(result.Errors, fmt.Sprintf("turn %d: no main interaction", recordedTurn.Number))
			break
		}

		turn, err := e.replayTurn(ctx, recordedTurn, mi, history, model)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("turn %d: %v", recordedTurn.Number, err))
			break
		}
		result.Rollout.Turns = append(result.Rollout.Turns, *turn)
		result.TurnsReplayed++
		result.TotalTokens.PromptTokens += mi.Response.PromptTokens
		result.TotalTokens.CompletionTokens += mi.Response.CompletionTokens

		// Grow history for the next turn.
		history = buildHistoryFromSnapshot(mi.Request.Messages)
		// Append the assistant response and tool results to history.
		history = append(history, agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: mi.Response.Content,
			ToolCalls: mapToolCallSnapshotsToAgentCore(turn.ToolCalls),
		})
		for _, tc := range turn.ToolCalls {
			history = append(history, agentcore.Message{
				Role:       agentcore.RoleTool,
				Content:    tc.Result,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			})
		}
	}

	result.Rollout.EndedAt = time.Now()
	result.Duration = time.Since(startTime)
	return result, nil
}

func (e *ReplayEngine) replayTurn(ctx context.Context, recorded Turn, mi *Interaction, history []agentcore.Message, model string) (*Turn, error) {
	e.logger.Info("replaying turn",
		"number", recorded.Number,
		"model", model,
		"strategy", e.config.Strategy,
		"tool_calls", len(mi.ToolCalls))

	req := buildRequestFromSnapshot(mi.Request, history, model, e.config.Temperature)

	resp, err := e.config.Provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	turn := &Turn{
		Number:    recorded.Number,
		StartedAt: time.Now(),
		Interactions: []Interaction{
			{
				Kind:       "main",
				Request:    snapshotRequest(req),
				Response:   snapshotResponse(resp),
				ToolCalls:  snapshotToolCalls(resp.ToolCalls),
				StartedAt:  time.Now(),
				EndedAt:    time.Now(),
			},
		},
	}

	if len(resp.ToolCalls) > 0 {
		turn.ToolCalls = make([]ToolCallSnapshot, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			ts := ToolCallSnapshot{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}

			switch e.config.Strategy {
			case StrategyMock:
				// Use recorded tool results, matched by name with consumption tracking.
				e.applyMockResult(&ts, recorded.ToolCalls, &usedTracker{})

			case StrategyReal:
				// Execute the tool for real.
				result, execErr := e.executeTool(ctx, tc.Name, json.RawMessage(tc.Arguments))
				ts.Result = result
				if execErr != nil {
					ts.Error = execErr.Error()
				}

			case StrategyAbort:
				// Record the tool call but don't execute; mark as aborted.
				ts.Error = "aborted by strategy"
			}

			turn.ToolCalls[i] = ts
		}
	}

	turn.EndedAt = time.Now()
	return turn, nil
}

func snapshotToolCalls(calls []agentcore.ToolCall) []ToolCallSnapshot {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCallSnapshot, len(calls))
	for i, tc := range calls {
		out[i] = ToolCallSnapshot{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
	}
	return out
}

func (e *ReplayEngine) applyMockResult(ts *ToolCallSnapshot, recorded []ToolCallSnapshot, used *usedTracker) {
	for j, rec := range recorded {
		if !used.isUsed(j) && rec.Name == ts.Name {
			ts.Result = rec.Result
			ts.Error = rec.Error
			ts.Duration = rec.Duration
			used.mark(j)
			return
		}
	}
}

func (e *ReplayEngine) executeTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if e.config.ToolExec == nil {
		return "", fmt.Errorf("no tool executor configured for live replay")
	}
	return e.config.ToolExec.Execute(ctx, name, args)
}

// usedTracker tracks which indices have been consumed.
type usedTracker struct {
	used map[int]bool
}

func (t *usedTracker) isUsed(i int) bool {
	if t.used == nil {
		return false
	}
	return t.used[i]
}

func (t *usedTracker) mark(i int) {
	if t.used == nil {
		t.used = make(map[int]bool)
	}
	t.used[i] = true
}

// ---------------------------------------------------------------------------
// Request/response snapshot builders
// ---------------------------------------------------------------------------

func buildRequestFromSnapshot(snap RequestSnapshot, history []agentcore.Message, model string, tempOverride *float64) *agentcore.ProviderRequest {
	req := &agentcore.ProviderRequest{
		Model:            model,
		Temperature:      snap.Temperature,
		MaxTokens:        snap.MaxTokens,
		FrequencyPenalty: snap.FrequencyPenalty,
		PresencePenalty:  snap.PresencePenalty,
		FastMode:         snap.FastMode,
	}

	if tempOverride != nil {
		req.Temperature = *tempOverride
	}

	if len(history) > 0 {
		req.Messages = history
	} else {
		req.Messages = make([]agentcore.Message, len(snap.Messages))
		for i, m := range snap.Messages {
			req.Messages[i] = agentcore.Message{
				Role:       agentcore.Role(m.Role),
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
				Name:       m.Name,
			}
			if len(m.ToolCalls) > 0 {
				req.Messages[i].ToolCalls = make([]agentcore.ToolCall, len(m.ToolCalls))
				for j, tc := range m.ToolCalls {
					req.Messages[i].ToolCalls[j] = agentcore.ToolCall{
						ID:        tc.ID,
						Name:      tc.Name,
						Arguments: tc.Arguments,
					}
				}
			}
		}
	}

	if len(snap.Tools) > 0 {
		req.Tools = make([]agentcore.ToolDefinition, len(snap.Tools))
		for i, t := range snap.Tools {
			req.Tools[i] = agentcore.ToolDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			}
		}
	}

	if snap.Thinking != nil {
		req.Thinking = &agentcore.ThinkingConfig{
			IncludeThoughts: snap.Thinking.IncludeThoughts,
			Display:         agentcore.ThinkingDisplay(snap.Thinking.Display),
			Effort:          agentcore.ThinkingEffort(snap.Thinking.Effort),
			Budget:          snap.Thinking.Budget,
		}
	}

	if snap.ResponseFormat != nil {
		rf := &agentcore.ResponseFormat{
			Type: agentcore.ResponseFormatType(snap.ResponseFormat.Type),
		}
		if snap.ResponseFormat.JSONSchema != nil {
			name, _ := snap.ResponseFormat.JSONSchema["name"].(string)
			schema, _ := snap.ResponseFormat.JSONSchema["schema"].(map[string]any)
			rf.JSONSchema = &agentcore.ResponseFormatJSONSchemaConfig{
				Name:   name,
				Schema: schema,
			}
		}
		req.ResponseFormat = rf
	}

	return req
}

func buildHistoryFromSnapshot(msgs []MessageSnapshot) []agentcore.Message {
	history := make([]agentcore.Message, len(msgs))
	for i, m := range msgs {
		history[i] = agentcore.Message{
			Role:       agentcore.Role(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		if len(m.ToolCalls) > 0 {
			history[i].ToolCalls = make([]agentcore.ToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				history[i].ToolCalls[j] = agentcore.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				}
			}
		}
	}
	return history
}

func mapToolCallSnapshotsToAgentCore(snapshots []ToolCallSnapshot) []agentcore.ToolCall {
	if len(snapshots) == 0 {
		return nil
	}
	result := make([]agentcore.ToolCall, len(snapshots))
	for i, s := range snapshots {
		result[i] = agentcore.ToolCall{
			ID:        s.ID,
			Name:      s.Name,
			Arguments: s.Arguments,
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

// FormatTurnSummary returns a one-line summary of a turn.
func FormatTurnSummary(t *Turn) string {
	parts := []string{fmt.Sprintf("turn:%d", t.Number)}
	mi := t.MainInteraction()
	if mi != nil {
		if mi.Response.Content != "" {
			parts = append(parts, fmt.Sprintf("text:%dch", len(mi.Response.Content)))
		}
		if len(mi.ToolCalls) > 0 {
			names := make([]string, len(mi.ToolCalls))
			for i, tc := range mi.ToolCalls {
				names[i] = tc.Name
			}
			parts = append(parts, fmt.Sprintf("tools:[%s]", strings.Join(names, ",")))
		}
	}
	if n := len(t.Interactions); n > 1 {
		parts = append(parts, fmt.Sprintf("aux:%d", n-1))
	}
	if t.Error != "" {
		parts = append(parts, fmt.Sprintf("err:%s", t.Error))
	}
	if mi != nil {
		tokens := mi.Response.PromptTokens + mi.Response.CompletionTokens
		if tokens > 0 {
			parts = append(parts, fmt.Sprintf("tokens:%d", tokens))
		}
	}
	return strings.Join(parts, " ")
}

// FormatRolloutSummary returns a multi-line summary of a rollout.
func FormatRolloutSummary(r *Rollout) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Rollout: %s\n", r.ID)
	fmt.Fprintf(&b, "Session: %s\n", r.SessionID)
	fmt.Fprintf(&b, "Model:   %s\n", r.Model)
	fmt.Fprintf(&b, "Turns:   %d\n", len(r.Turns))
	if !r.StartedAt.IsZero() {
		fmt.Fprintf(&b, "Started: %s\n", r.StartedAt.Format(time.RFC3339))
	}
	if !r.EndedAt.IsZero() {
		fmt.Fprintf(&b, "Ended:   %s\n", r.EndedAt.Format(time.RFC3339))
	}
	var totalTokens agentcore.TokenUsage
	for _, t := range r.Turns {
		if mi := t.MainInteraction(); mi != nil {
			totalTokens.PromptTokens += mi.Response.PromptTokens
			totalTokens.CompletionTokens += mi.Response.CompletionTokens
		}
	}
	fmt.Fprintf(&b, "Tokens:  %d prompt + %d completion = %d total\n",
		totalTokens.PromptTokens, totalTokens.CompletionTokens, totalTokens.TotalTokens)

	if len(r.Turns) > 0 {
		b.WriteString("\nTurns:\n")
		for _, t := range r.Turns {
			fmt.Fprintf(&b, "  %s\n", FormatTurnSummary(&t))
		}
	}
	return b.String()
}

// ParseBundleOrRollout parses JSON as either a Bundle or a single Rollout.
func ParseBundleOrRollout(data []byte) (*Bundle, *Rollout, error) {
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err == nil && bundle.Version > 0 {
		return &bundle, nil, nil
	}
	var rollout Rollout
	if err := json.Unmarshal(data, &rollout); err == nil && rollout.ID != "" {
		return nil, &rollout, nil
	}
	return nil, nil, fmt.Errorf("invalid rollout or bundle format")
}

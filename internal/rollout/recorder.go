package rollout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

// Recorder is a provider wrapper that captures every LLM request/response
// pair into a Rollout, enabling deterministic replay and debugging.
// It implements agentcore.Provider and wraps an inner provider.
//
// A logical agent Turn may contain multiple LLM Interactions: the main model
// call plus auxiliary calls (context compression, title generation, review,
// guardrails). The recorder distinguishes them via the model-run info in the
// call context and groups them under the active Turn so tool results attach
// to the main interaction precisely.
type Recorder struct {
	inner    agentcore.Provider
	rollout  *Rollout
	mu       sync.Mutex
	logger   *slog.Logger
	provider string
	model    string

	// turnMu serializes turn boundaries so concurrent tool calls don't
	// interleave with turn-level metadata updates.
	turnMu      sync.Mutex
	currentTurn int
	activeTurn  *Turn // current logical turn being built
}

// RecorderConfig holds configuration for creating a new Recorder.
type RecorderConfig struct {
	Provider  string
	Model     string
	SessionID string
	CWD       string
	Logger    *slog.Logger
}

// NewRecorder creates a new Recorder that wraps the given provider.
func NewRecorder(cfg RecorderConfig) *Recorder {
	return &Recorder{
		rollout: &Rollout{
			ID:        generateID(),
			SessionID: cfg.SessionID,
			Provider:  cfg.Provider,
			Model:     cfg.Model,
			CWD:       cfg.CWD,
			StartedAt: time.Now(),
			Metadata:  make(map[string]string),
		},
		logger:   cfg.Logger,
		provider: cfg.Provider,
		model:    cfg.Model,
	}
}

// SetInner sets the inner provider to wrap. Must be called before any
// Complete/Stream calls.
func (r *Recorder) SetInner(p agentcore.Provider) {
	r.inner = p
}

// Rollout returns a deep copy of the recorded rollout. Safe to call concurrently.
func (r *Recorder) Rollout() *Rollout {
	r.mu.Lock()
	defer r.mu.Unlock()
	return deepCopyRollout(r.rollout)
}

func deepCopyRollout(src *Rollout) *Rollout {
	cp := *src
	if src.Turns != nil {
		cp.Turns = make([]Turn, len(src.Turns))
		for i, t := range src.Turns {
			ct := t
			if t.Interactions != nil {
				ct.Interactions = make([]Interaction, len(t.Interactions))
				for j, in := range t.Interactions {
					ci := in
					if in.Request.Messages != nil {
						ci.Request.Messages = make([]MessageSnapshot, len(in.Request.Messages))
						copy(ci.Request.Messages, in.Request.Messages)
					}
					if in.Request.Tools != nil {
						ci.Request.Tools = make([]ToolDefSnapshot, len(in.Request.Tools))
						copy(ci.Request.Tools, in.Request.Tools)
					}
					if in.ToolCalls != nil {
						ci.ToolCalls = make([]ToolCallSnapshot, len(in.ToolCalls))
						copy(ci.ToolCalls, in.ToolCalls)
					}
					ct.Interactions[j] = ci
				}
			}
			if t.ToolCalls != nil {
				ct.ToolCalls = make([]ToolCallSnapshot, len(t.ToolCalls))
				copy(ct.ToolCalls, t.ToolCalls)
			}
			cp.Turns[i] = ct
		}
	}
	if src.Metadata != nil {
		cp.Metadata = make(map[string]string, len(src.Metadata))
		for k, v := range src.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp
}

// MarkComplete finalizes the rollout's end time.
func (r *Recorder) MarkComplete() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rollout.EndedAt = time.Now()
}

// SetMetadata sets a key-value pair in the rollout metadata.
func (r *Recorder) SetMetadata(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rollout.Metadata == nil {
		r.rollout.Metadata = make(map[string]string)
	}
	r.rollout.Metadata[key] = value
}

// BeginTurn signals the start of a new logical turn and returns its number.
func (r *Recorder) BeginTurn() int {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	r.currentTurn++
	turn := &Turn{
		Number:    r.currentTurn,
		StartedAt: time.Now(),
	}
	r.rollout.Turns = append(r.rollout.Turns, *turn)
	r.activeTurn = &r.rollout.Turns[len(r.rollout.Turns)-1]
	return r.currentTurn
}

// CompleteTurn records the end time of the current logical turn.
func (r *Recorder) CompleteTurn() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeTurn != nil {
		r.activeTurn.EndedAt = time.Now()
		r.activeTurn = nil
	}
}

// RecordToolResult fills in the result for a tool call by matching tool call ID
// across the active turn's interactions and its aggregated tool calls.
func (r *Recorder) RecordToolResult(toolCallID, result string, err error, duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.activeTurn; t != nil {
		setToolResult(&t.ToolCalls, toolCallID, result, err, duration)
		for i := range t.Interactions {
			setToolResult(&t.Interactions[i].ToolCalls, toolCallID, result, err, duration)
		}
	}
}

// RecordToolResultsBatch fills in results for multiple tool calls at once.
func (r *Recorder) RecordToolResultsBatch(results []agentcore.ToolResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeTurn == nil {
		return
	}
	for _, tr := range results {
		setToolResult(&r.activeTurn.ToolCalls, tr.ToolCallID, tr.Result, tr.Err, tr.Duration)
		for i := range r.activeTurn.Interactions {
			setToolResult(&r.activeTurn.Interactions[i].ToolCalls, tr.ToolCallID, tr.Result, tr.Err, tr.Duration)
		}
	}
}

func setToolResult(calls *[]ToolCallSnapshot, id, result string, err error, duration time.Duration) {
	for i := range *calls {
		if (*calls)[i].ID == id {
			(*calls)[i].Result = result
			(*calls)[i].Duration = duration.Milliseconds()
			if err != nil {
				(*calls)[i].Error = err.Error()
			}
			return
		}
	}
}

// SetError records an error on the current turn.
func (r *Recorder) SetError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		return
	}
	if r.activeTurn != nil {
		r.activeTurn.Error = err.Error()
	}
	if len(r.rollout.Turns) > 0 {
		r.rollout.Turns[len(r.rollout.Turns)-1].Error = err.Error()
	}
}

func (r *Recorder) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	started := time.Now()
	resp, err := r.inner.Complete(ctx, req)
	r.recordInteraction(ctx, req, resp, err, started)
	return resp, err
}

func (r *Recorder) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	stream, err := r.inner.Stream(ctx, req)
	if err != nil {
		r.recordInteraction(ctx, req, nil, err, time.Now())
		return stream, err
	}

	started := time.Now()
	out := make(chan agentcore.StreamDelta)
	safego.SafeGo(func() {
		defer close(out)
		acc := &streamAccumulator{}
		for delta := range stream {
			acc.add(delta)
			out <- delta
		}
		r.recordInteraction(ctx, req, acc.buildResponse(), nil, started)
	}, r.logger)
	return out, nil
}

// streamAccumulator collects streaming deltas into a complete response.
type streamAccumulator struct {
	content   string
	toolCalls []agentcore.ToolCall
	usage     agentcore.TokenUsage
	finish    string
}

func (a *streamAccumulator) add(d agentcore.StreamDelta) {
	if d.Content != "" {
		a.content += d.Content
	}
	if d.Usage != nil {
		a.usage = *d.Usage
	}
	if d.FinishReason != "" {
		a.finish = d.FinishReason
	}
	for _, tc := range d.ToolCalls {
		a.toolCalls = append(a.toolCalls, agentcore.ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		})
	}
}

func (a *streamAccumulator) buildResponse() *agentcore.ProviderResponse {
	return &agentcore.ProviderResponse{
		Content:      a.content,
		ToolCalls:    a.toolCalls,
		Usage:        a.usage,
		FinishReason: a.finish,
	}
}

// classifyKind determines whether a call is the main agent model call or an
// auxiliary call by inspecting the model-run info in the context. When a
// "model" component RunInfo is present, the call is the main path (or a
// nested main call); otherwise it is auxiliary.
func classifyKind(ctx context.Context) string {
	info, ok := agentcore.RunInfoFromContext(ctx)
	if ok && info.Component == "model" {
		return "main"
	}
	return "aux"
}

func (r *Recorder) recordInteraction(ctx context.Context, req *agentcore.ProviderRequest, resp *agentcore.ProviderResponse, callErr error, started time.Time) {
	inter := buildInteraction(req, resp, callErr, started)
	inter.Kind = classifyKind(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()

	// Ensure there is a turn to hold the interaction (e.g. an aux call that
	// fires outside a begin/end lifecycle window).
	if r.activeTurn == nil {
		r.turnMu.Lock()
		r.currentTurn++
		turn := &Turn{Number: r.currentTurn, StartedAt: started}
		r.rollout.Turns = append(r.rollout.Turns, *turn)
		r.activeTurn = &r.rollout.Turns[len(r.rollout.Turns)-1]
		r.turnMu.Unlock()
	}

	r.activeTurn.Interactions = append(r.activeTurn.Interactions, inter)

	// Aggregate the tool calls from the main interaction onto the turn so
	// replay/diff operate on the primary tool path.
	if inter.Kind == "main" {
		r.activeTurn.ToolCalls = append(append([]ToolCallSnapshot{}, inter.ToolCalls...), r.activeTurn.ToolCalls...)
	}
}

func buildInteraction(req *agentcore.ProviderRequest, resp *agentcore.ProviderResponse, callErr error, started time.Time) Interaction {
	inter := Interaction{
		StartedAt: started,
		Request:   snapshotRequest(req),
	}
	if resp != nil {
		inter.Response = snapshotResponse(resp)
		inter.ToolCalls = make([]ToolCallSnapshot, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			inter.ToolCalls[i] = ToolCallSnapshot{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}
		}
	}
	if callErr != nil {
		inter.Error = callErr.Error()
	}
	inter.EndedAt = time.Now()
	return inter
}

func snapshotRequest(req *agentcore.ProviderRequest) RequestSnapshot {
	snap := RequestSnapshot{
		Model:            req.Model,
		Temperature:      req.Temperature,
		MaxTokens:        req.MaxTokens,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		FastMode:         req.FastMode,
	}

	// Snapshot messages.
	snap.Messages = make([]MessageSnapshot, len(req.Messages))
	for i, m := range req.Messages {
		ms := MessageSnapshot{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		if len(m.ToolCalls) > 0 {
			ms.ToolCalls = make([]ToolCallSnapshot, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				ms.ToolCalls[j] = ToolCallSnapshot{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				}
			}
		}
		snap.Messages[i] = ms
	}

	// Snapshot tools.
	if len(req.Tools) > 0 {
		snap.Tools = make([]ToolDefSnapshot, len(req.Tools))
		for i, t := range req.Tools {
			snap.Tools[i] = ToolDefSnapshot{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			}
		}
	}

	// Snapshot thinking config.
	if req.Thinking != nil {
		snap.Thinking = &ThinkingSnapshot{
			IncludeThoughts: req.Thinking.IncludeThoughts,
			Display:         string(req.Thinking.Display),
			Effort:          string(req.Thinking.Effort),
			Budget:          req.Thinking.Budget,
		}
	}

	// Snapshot response format.
	if req.ResponseFormat != nil {
		rfs := &ResponseFmtSnapshot{
			Type: string(req.ResponseFormat.Type),
		}
		if req.ResponseFormat.JSONSchema != nil {
			rfs.JSONSchema = map[string]any{
				"name":   req.ResponseFormat.JSONSchema.Name,
				"schema": req.ResponseFormat.JSONSchema.Schema,
			}
		}
		snap.ResponseFormat = rfs
	}

	return snap
}

func snapshotResponse(resp *agentcore.ProviderResponse) ResponseSnapshot {
	return ResponseSnapshot{
		Content:          resp.Content,
		FinishReason:     resp.FinishReason,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
		SuppressPersist:  resp.SuppressPersist,
	}
}

func generateID() string {
	return time.Now().Format("20060102-150405") + "-" + randomHex(8)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based if crypto/rand fails (shouldn't happen).
		return time.Now().Format("01020405") + "00000000"
	}
	return hex.EncodeToString(b)
}

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

// fakeMetricsRecorder implements metricsSink without requiring a live OTel
// pipeline. The library's model metrics middleware records into it via the
// embedded agentcore.Metrics methods.
type fakeMetricsRecorder struct {
	calls    int
	lastCost float64
}

func (f *fakeMetricsRecorder) RecordModelCall(_ context.Context, _ *agentcore.ProviderRequest, _ *agentcore.TokenUsage, _ time.Time) {
	f.calls++
}

func (f *fakeMetricsRecorder) RecordCost(_ context.Context, _ string, amountUSD float64, _ string, _ string) {
	f.lastCost = amountUSD
}

func (f *fakeMetricsRecorder) RecordError(_ context.Context, _ string, _ error) {}

func TestCostTrackingMiddlewareRecordsCostMetric(t *testing.T) {
	tracker := NewCostTracker("openai", "gpt-4o")
	mock := &fakeMetricsRecorder{}
	mw := NewCostTrackingMiddleware(tracker, mock, nil)

	resp, err := mw(&streamMockProvider{}).Complete(context.Background(), &agentcore.ProviderRequest{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "direct-response" {
		t.Fatalf("content = %q", resp.Content)
	}
	if mock.calls != 0 {
		t.Fatalf("expected no model call records in cost test, got %d", mock.calls)
	}
	if mock.lastCost <= 0 {
		t.Fatalf("expected positive cost for gpt-4o, got %v", mock.lastCost)
	}
	if tracker.CurrentCost() <= 0 {
		t.Fatalf("expected tracker cost > 0, got %v", tracker.CurrentCost())
	}
}

func TestCostTrackingMiddlewareNoRecordWithoutMetrics(t *testing.T) {
	tracker := NewCostTracker("openai", "gpt-4o")
	mw := NewCostTrackingMiddleware(tracker, nil, nil)

	if _, err := mw(&streamMockProvider{}).Complete(context.Background(), &agentcore.ProviderRequest{Model: "gpt-4o"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if tracker.CurrentCost() <= 0 {
		t.Fatalf("expected tracker to still accumulate cost, got %v", tracker.CurrentCost())
	}
}

func TestAuxiliaryDedicatedProviderRecordsCost(t *testing.T) {
	ac := NewAuxiliaryClient(nil, "main-model", nil, func(providerType, baseURL, apiKey string) (agentcore.Provider, error) {
		return &streamMockProvider{}, nil
	}, nil)

	mock := &fakeMetricsRecorder{}
	ac.SetMetricsRecorder(mock)
	ac.resolveTask(TaskReview, &AuxiliaryModelConfig{
		Provider: "openai",
		Model:    "gpt-4o",
		BaseURL:  "http://test.local",
		APIKey:   "test-key",
	})

	provider := ac.Provider(TaskReview)
	if provider == nil {
		t.Fatal("expected a resolved dedicated provider")
	}
	if _, err := provider.Complete(context.Background(), &agentcore.ProviderRequest{Model: "gpt-4o"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if mock.calls != 1 {
		t.Fatalf("expected 1 model call recorded, got %d", mock.calls)
	}
	if mock.lastCost <= 0 {
		t.Fatalf("expected positive cost, got %v", mock.lastCost)
	}
}

func TestAuxiliaryModelOnlyOverrideSharesMainChain(t *testing.T) {
	mainProvider := &streamMockProvider{}
	ac := NewAuxiliaryClient(mainProvider, "main-model", &AuxiliaryConfig{
		Title: &AuxiliaryModelConfig{Model: "gpt-4o-mini"},
	}, nil, nil)

	if got := ac.Provider(TaskTitle); got != agentcore.Provider(mainProvider) {
		t.Fatalf("model-only override should reuse the main provider, got %T", got)
	}
}

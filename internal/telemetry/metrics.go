package telemetry

import (
	"context"
	"runtime"
	gometrics "runtime/metrics"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ModelMetricsRecorder records per-call model metrics using OTel GenAI semantic
// conventions, plus cost, error, and process-level gauges. Instruments are
// created once against the process-wide meter, so the recorder is safe for
// concurrent use.
type ModelMetricsRecorder struct {
	tokenUsage metric.Int64Counter
	opDuration metric.Float64Histogram
	cost       metric.Float64Counter
	errors     metric.Int64Counter
}

func newModelMetricsRecorder(meter metric.Meter) *ModelMetricsRecorder {
	tokenUsage, err := meter.Int64Counter(
		"gen_ai.client.token.usage",
		metric.WithDescription("Number of input and output tokens used by LLM calls"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		// Instruments are created once per process; failure would only happen
		// on name conflicts or an invalid meter, so fall back to a no-op.
		tokenUsage, _ = meter.Int64Counter("gen_ai.client.token.usage")
	}
	opDuration, err := meter.Float64Histogram(
		"gen_ai.client.operation.duration",
		metric.WithDescription("Duration of LLM operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		opDuration, _ = meter.Float64Histogram("gen_ai.client.operation.duration")
	}
	cost, err := meter.Float64Counter(
		"covo_agent.model.cost",
		metric.WithDescription("Accumulated USD cost of LLM calls, per pricing table"),
		metric.WithUnit("USD"),
	)
	if err != nil {
		cost, _ = meter.Float64Counter("covo_agent.model.cost")
	}
	errors, err := meter.Int64Counter(
		"covo_agent.errors",
		metric.WithDescription("Number of failed LLM operations, by operation"),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		errors, _ = meter.Int64Counter("covo_agent.errors")
	}
	return &ModelMetricsRecorder{tokenUsage: tokenUsage, opDuration: opDuration, cost: cost, errors: errors}
}

// RecordModelCall records token usage and duration for a completed LLM call.
// usage may be nil for calls that did not report usage.
func (r *ModelMetricsRecorder) RecordModelCall(ctx context.Context, req *agentcore.ProviderRequest, usage *agentcore.TokenUsage, start time.Time) {
	if r == nil || req == nil {
		return
	}
	base := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.request.model", req.Model),
	}

	if usage != nil {
		if usage.PromptTokens > 0 {
			r.tokenUsage.Add(ctx, usage.PromptTokens,
				metric.WithAttributes(append(base, attribute.String("gen_ai.token.type", "input"))...))
		}
		if usage.CompletionTokens > 0 {
			r.tokenUsage.Add(ctx, usage.CompletionTokens,
				metric.WithAttributes(append(base, attribute.String("gen_ai.token.type", "output"))...))
		}
	}

	r.opDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(base...))
}

// RecordCost records the USD cost of a completed LLM call. status and source
// describe how the cost was derived (e.g. "actual"/"official_docs/0.1").
func (r *ModelMetricsRecorder) RecordCost(ctx context.Context, model string, amountUSD float64, status, source string) {
	if r == nil || r.cost == nil || model == "" || amountUSD <= 0 {
		return
	}
	r.cost.Add(ctx, amountUSD, metric.WithAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.request.model", model),
		attribute.String("covo_agent.cost.status", status),
		attribute.String("covo_agent.cost.source", source),
	))
}

// RecordError counts a failed operation (model call, tool execution, agent run)
// by component, mirroring the trace span component attribute. It satisfies
// agentcore.Metrics so the recorder can back the library's metrics middleware.
// err is recorded for interface compatibility; only the component dimension is
// counted.
func (r *ModelMetricsRecorder) RecordError(ctx context.Context, component string, _ error) {
	if r == nil || r.errors == nil {
		return
	}
	r.errors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("component", component),
	))
}

// processSamples reads low-overhead process telemetry via runtime/metrics.
var processSamples = []gometrics.Sample{
	{Name: "/memory/classes/heap/objects:bytes"},
	{Name: "/cpu/classes/total:seconds"},
}

// registerProcessMetrics publishes process-level gauges (goroutines, heap
// bytes) and the cumulative CPU-time counter. Reads come from runtime/metrics,
// so no external process-monitoring dependency is needed. The CPU counter is
// only registered where runtime/metrics provides it (e.g. Linux); on other
// platforms the remaining gauges still publish.
func registerProcessMetrics(meter metric.Meter) {
	goroutines, err := meter.Int64ObservableGauge("process.runtime.go.goroutines",
		metric.WithDescription("Number of goroutines that currently exist"))
	if err != nil {
		return
	}
	heapBytes, err := meter.Int64ObservableGauge("process.runtime.go.memory.heap_objects",
		metric.WithDescription("Number of bytes allocated to heap objects"))
	if err != nil {
		return
	}

	probe := []gometrics.Sample{{Name: "/cpu/classes/total:seconds"}}
	gometrics.Read(probe)
	var cpuTime metric.Float64ObservableCounter
	if probe[0].Value.Kind() == gometrics.KindFloat64 {
		cpuTime, err = meter.Float64ObservableCounter("process.cpu.time",
			metric.WithDescription("Cumulative CPU time consumed by the process"),
			metric.WithUnit("s"))
		if err != nil {
			cpuTime = nil
		}
	}

	instruments := []metric.Observable{goroutines, heapBytes}
	if cpuTime != nil {
		instruments = append(instruments, cpuTime)
	}

	_, _ = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		gometrics.Read(processSamples)
		o.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
		for _, s := range processSamples {
			switch s.Name {
			case "/memory/classes/heap/objects:bytes":
				if s.Value.Kind() == gometrics.KindUint64 {
					o.ObserveInt64(heapBytes, int64(s.Value.Uint64()))
				}
			case "/cpu/classes/total:seconds":
				if cpuTime != nil && s.Value.Kind() == gometrics.KindFloat64 {
					o.ObserveFloat64(cpuTime, s.Value.Float64())
				}
			}
		}
		return nil
	}, instruments...)
}

// StartEvent opens a named component span for a business event (guardrail
// decisions, compaction retries, approvals). The span is a child of any span
// already carried in ctx and becomes a root span otherwise. Returns a no-op
// span when tracing is disabled, so callers can safely End() it.
func StartEvent(ctx context.Context, component, name string, attrs ...agentcore.SpanAttribute) (context.Context, agentcore.Span) {
	ctx, span, _ := agentcore.StartComponentRun(ctx, AgentTracer(), component, name, attrs...)
	return ctx, span
}

package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	gometrics "runtime/metrics"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// resetOtel clears the process-wide singleton so each test can initialize its
// own pipeline. Tests live in the same package so they may touch the internals.
func resetOtel() {
	otelInitOnce = sync.Once{}
	setOtelHandle(nil)
}

func TestParseHeaders(t *testing.T) {
	headers := parseHeaders("Authorization: Bearer pk-lf-abc, X-Tenant:acme; extra=1")
	if len(headers) != 3 {
		t.Fatalf("headers = %v", headers)
	}
	if headers["Authorization"] != "Bearer pk-lf-abc" {
		t.Errorf("Authorization = %q", headers["Authorization"])
	}
	if headers["X-Tenant"] != "acme" {
		t.Errorf("X-Tenant = %q", headers["X-Tenant"])
	}
	if headers["extra"] != "1" {
		t.Errorf("extra = %q", headers["extra"])
	}
}

func TestParseHeadersEmpty(t *testing.T) {
	if parseHeaders("") != nil {
		t.Fatal("expected nil headers for empty input")
	}
	if parseHeaders("  ,  ; ") != nil {
		t.Fatal("expected nil headers for separators only")
	}
}

func TestParseHeadersSkipsInvalidPairs(t *testing.T) {
	headers := parseHeaders("no-separator, :empty-key, key:")
	if len(headers) != 0 {
		t.Fatalf("expected no headers, got %v", headers)
	}
}

func TestConfigFromEnvHeaders(t *testing.T) {
	t.Setenv("COVO_OTEL_HEADERS", "Authorization: Bearer pk-lf-x")
	cfg := ConfigFromEnv()
	if cfg.Headers["Authorization"] != "Bearer pk-lf-x" {
		t.Fatalf("headers = %v", cfg.Headers)
	}
}

func TestConfigFromEnvMetrics(t *testing.T) {
	t.Setenv("COVO_OTEL_METRICS_ENABLED", "true")
	t.Setenv("COVO_OTEL_METRICS_ENDPOINT", "http://metrics.example:4318")
	cfg := ConfigFromEnv()
	if !cfg.MetricsEnabled {
		t.Fatal("expected metrics enabled")
	}
	if cfg.MetricsEndpoint != "http://metrics.example:4318" {
		t.Fatalf("metrics endpoint = %q", cfg.MetricsEndpoint)
	}
}

func TestModelMetricsRecorderRecords(t *testing.T) {
	manual := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(manual))
	defer mp.Shutdown(context.Background())

	rec := newModelMetricsRecorder(mp.Meter("test"))
	req := &agentcore.ProviderRequest{Model: "gpt-4o"}
	rec.RecordModelCall(context.Background(), req,
		&agentcore.TokenUsage{PromptTokens: 100, CompletionTokens: 25}, time.Now().Add(-2*time.Second))
	rec.RecordModelCall(context.Background(), req,
		&agentcore.TokenUsage{PromptTokens: 50, CompletionTokens: 0}, time.Now().Add(-1*time.Second))

	var rm metricdata.ResourceMetrics
	if err := manual.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var tokenSum int
	var totalDurationCount uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "gen_ai.client.token.usage":
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					t.Fatalf("token usage data type = %T", m.Data)
				}
				for _, dp := range sum.DataPoints {
					tokenSum++
					for _, kv := range dp.Attributes.ToSlice() {
						if kv.Key == "gen_ai.token.type" {
							switch kv.Value.AsString() {
							case "input":
								if dp.Value != 150 {
									t.Errorf("input tokens = %d, want 150", dp.Value)
								}
							case "output":
								if dp.Value != 25 {
									t.Errorf("output tokens = %d, want 25", dp.Value)
								}
							}
						}
					}
				}
			case "gen_ai.client.operation.duration":
				hist, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("duration data type = %T", m.Data)
				}
				// Both calls share the same attributes, so they aggregate into
				// one data point whose count reflects both recordings.
				for _, dp := range hist.DataPoints {
					totalDurationCount += dp.Count
				}
			}
		}
	}
	if tokenSum != 2 {
		t.Errorf("token usage data points = %d, want 2 (input+output)", tokenSum)
	}
	if totalDurationCount != 2 {
		t.Errorf("duration recordings = %d, want 2", totalDurationCount)
	}
}

func TestModelMetricsRecorderRecordsCostAndError(t *testing.T) {
	manual := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(manual))
	defer mp.Shutdown(context.Background())

	rec := newModelMetricsRecorder(mp.Meter("test"))
	rec.RecordCost(context.Background(), "gpt-4o", 0.0123, "actual", "official_docs/0.1")
	rec.RecordError(context.Background(), "model", errors.New("boom"))
	rec.RecordError(context.Background(), "tool", errors.New("boom"))

	var rm metricdata.ResourceMetrics
	if err := manual.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	var costSum float64
	var errCount int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "covo_agent.model.cost":
				sum, ok := m.Data.(metricdata.Sum[float64])
				if !ok {
					t.Fatalf("cost data type = %T", m.Data)
				}
				for _, dp := range sum.DataPoints {
					costSum += dp.Value
				}
			case "covo_agent.errors":
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					t.Fatalf("errors data type = %T", m.Data)
				}
				for _, dp := range sum.DataPoints {
					errCount += dp.Value
				}
			}
		}
	}
	if costSum != 0.0123 {
		t.Errorf("cost sum = %v, want 0.0123", costSum)
	}
	if errCount != 2 {
		t.Errorf("error count = %d, want 2", errCount)
	}
}

func TestProcessMetricsRegistered(t *testing.T) {
	manual := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(manual))
	defer mp.Shutdown(context.Background())

	registerProcessMetrics(mp.Meter("test"))

	var rm metricdata.ResourceMetrics
	if err := manual.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	// process.cpu.time is platform-dependent (runtime/metrics only publishes
	// /cpu/classes on Linux); the runtime gauges must always be present.
	want := map[string]bool{
		"process.runtime.go.goroutines":          false,
		"process.runtime.go.memory.heap_objects": false,
	}
	var cpuProbe []gometrics.Sample
	cpuProbe = append(cpuProbe, gometrics.Sample{Name: "/cpu/classes/total:seconds"})
	gometrics.Read(cpuProbe)
	if cpuProbe[0].Value.Kind() == gometrics.KindFloat64 {
		want["process.cpu.time"] = false
	}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if _, ok := want[m.Name]; ok {
				want[m.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("process metric %q not collected", name)
		}
	}
}

func TestMetricsEndToEndExport(t *testing.T) {
	var mu sync.Mutex
	var hits int
	var path string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		path = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	resetOtel()
	t.Cleanup(resetOtel)
	t.Setenv("COVO_OTEL_ENDPOINT", ts.URL)
	t.Setenv("COVO_OTEL_METRICS_ENABLED", "true")

	handle := InitOtel(context.Background(), testLogger())
	if handle == nil || handle.meterProvider == nil {
		t.Fatal("expected metrics pipeline to be initialized")
	}
	rec := MetricsRecorder()
	if rec == nil {
		t.Fatal("expected non-nil metrics recorder")
	}

	rec.RecordModelCall(context.Background(),
		&agentcore.ProviderRequest{Model: "gpt-4o"},
		&agentcore.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
		time.Now().Add(-time.Second))

	FlushOtel(context.Background())
	mu.Lock()
	h, p := hits, path
	mu.Unlock()
	if h == 0 {
		t.Fatal("expected metrics export")
	}
	if p != "/v1/metrics" {
		t.Errorf("expected /v1/metrics, got %q", p)
	}
	ShutdownOtel(context.Background())
}

func TestOtelDisabled(t *testing.T) {
	resetOtel()
	t.Cleanup(resetOtel)
	handle := InitOtel(context.Background(), testLogger())
	if handle == nil {
		t.Fatal("handle should never be nil")
	}
	if handle.provider != nil {
		t.Fatal("expected no provider when disabled")
	}
	if AgentTracer() != nil {
		t.Fatal("expected nil tracer when disabled")
	}
	FlushOtel(context.Background())
	ShutdownOtel(context.Background())
}

func TestFlushOtelExportsWithoutShutdown(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	resetOtel()
	t.Cleanup(resetOtel)
	t.Setenv("COVO_OTEL_ENDPOINT", ts.URL)

	handle := InitOtel(context.Background(), testLogger())
	if handle == nil || handle.provider == nil {
		t.Fatal("expected initialized provider")
	}
	tracer := AgentTracer()

	emit := func() {
		ctx, span, _ := agentcore.StartComponentRun(context.Background(), tracer, "model", "gpt-4o")
		span.End()
		_ = ctx
	}
	emit()

	// ForceFlush exports buffered spans without tearing down the pipeline.
	FlushOtel(context.Background())
	if hits == 0 {
		t.Fatal("expected FlushOtel to export buffered spans")
	}

	// The pipeline must still be usable after a flush.
	emit()
	ShutdownOtel(context.Background())
	if hits < 2 {
		t.Fatalf("expected later spans to export on shutdown, got %d exports", hits)
	}
}

func TestOtelEndToEndExport(t *testing.T) {
	var hits int
	var path string
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	resetOtel()
	t.Cleanup(resetOtel)
	t.Setenv("COVO_OTEL_ENDPOINT", ts.URL)
	t.Setenv("COVO_OTEL_HEADERS", "Authorization: Bearer pk-lf-test")

	handle := InitOtel(context.Background(), testLogger())
	if handle == nil || handle.provider == nil {
		t.Fatal("expected initialized provider")
	}
	tracer := AgentTracer()
	if tracer == nil {
		t.Fatal("expected non-nil tracer")
	}

	// Emit a model span with gen_ai attributes through the covonaut adapter.
	ctx, span, _ := agentcore.StartComponentRun(context.Background(), tracer, "model", "gpt-4o",
		agentcore.Attr("gen_ai.request.model", "gpt-4o"),
		agentcore.Attr("gen_ai.usage.input_tokens", int64(10)),
		agentcore.Attr("gen_ai.usage.output_tokens", int64(5)),
	)
	span.End()
	_ = ctx

	ShutdownOtel(context.Background())

	if hits == 0 {
		t.Fatal("expected at least one OTLP export")
	}
	if path != "/v1/traces" {
		t.Errorf("expected path /v1/traces, got %q", path)
	}
	if auth != "Bearer pk-lf-test" {
		t.Errorf("expected Authorization header, got %q", auth)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

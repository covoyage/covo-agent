package telemetry

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// otelHandle owns the process-wide OpenTelemetry tracing and metrics pipelines.
type otelHandle struct {
	provider        *sdktrace.TracerProvider
	meterProvider   *sdkmetric.MeterProvider
	metricsRecorder *ModelMetricsRecorder
}

var (
	otelInitOnce       sync.Once
	otelHandleMu       sync.RWMutex
	otelHandleInstance *otelHandle
)

func getOtelHandle() *otelHandle {
	otelHandleMu.RLock()
	defer otelHandleMu.RUnlock()
	return otelHandleInstance
}

func setOtelHandle(h *otelHandle) {
	otelHandleMu.Lock()
	defer otelHandleMu.Unlock()
	otelHandleInstance = h
}

// sdkPipelineActive reports whether the OpenTelemetry SDK tracing pipeline is
// initialized. When it is, the audit-based exporter must not also post traces
// to the same endpoint (see Exporter.send).
func sdkPipelineActive() bool {
	h := getOtelHandle()
	return h != nil && h.provider != nil
}

// InitOtel initializes the OpenTelemetry SDK from environment configuration
// (see ConfigFromEnv). Idempotent: subsequent calls return the existing
// handle. The returned handle is always non-nil; call Shutdown on it to flush
// buffered spans/metrics and release resources.
func InitOtel(ctx context.Context, logger *slog.Logger) *otelHandle {
	otelInitOnce.Do(func() {
		if logger == nil {
			logger = slog.Default()
		}
		cfg := ConfigFromEnv()
		handle := &otelHandle{}

		if cfg.Enabled && cfg.Endpoint != "" {
			tp, err := newTraceProvider(ctx, cfg, logger)
			if err != nil {
				logger.Warn("otel: trace init failed, tracing disabled", "err", err)
			} else {
				handle.provider = tp
			}
		}

		if cfg.MetricsEnabled && (cfg.MetricsEndpoint != "" || cfg.Endpoint != "") {
			mp, err := newMeterProvider(ctx, cfg, logger)
			if err != nil {
				logger.Warn("otel: metrics init failed, metrics disabled", "err", err)
			} else {
				handle.meterProvider = mp
				handle.metricsRecorder = newModelMetricsRecorder(mp.Meter("covo-agent"))
				registerProcessMetrics(mp.Meter("covo-agent"))
			}
		}

		setOtelHandle(handle)
	})
	return getOtelHandle()
}

// buildResource creates the service.name resource used by both signals.
func buildResource(ctx context.Context, cfg Config, logger *slog.Logger) *sdkresource.Resource {
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			attribute.String("service.name", cfg.ServiceName),
		),
	)
	if err != nil {
		logger.Warn("otel: resource creation failed, using defaults", "err", err)
		return sdkresource.Default()
	}
	return res
}

func newTraceProvider(ctx context.Context, cfg Config, logger *slog.Logger) (*sdktrace.TracerProvider, error) {
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if !strings.HasSuffix(endpoint, "/v1/traces") {
		endpoint += "/v1/traces"
	}
	exporterOpts := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint),
	}
	for key, value := range cfg.Headers {
		exporterOpts = append(exporterOpts, otlptracehttp.WithHeaders(map[string]string{key: value}))
	}
	exporter, err := otlptracehttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, err
	}

	res := buildResource(ctx, cfg, logger)
	interval := cfg.ExportInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(interval)),
		sdktrace.WithResource(res),
	), nil
}

func newMeterProvider(ctx context.Context, cfg Config, logger *slog.Logger) (*sdkmetric.MeterProvider, error) {
	endpoint := cfg.MetricsEndpoint
	if endpoint == "" {
		endpoint = cfg.Endpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/v1/metrics") {
		endpoint += "/v1/metrics"
	}
	exporterOpts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(endpoint),
	}
	for key, value := range cfg.Headers {
		exporterOpts = append(exporterOpts, otlpmetrichttp.WithHeaders(map[string]string{key: value}))
	}
	exporter, err := otlpmetrichttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, err
	}

	res := buildResource(ctx, cfg, logger)
	interval := cfg.ExportInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))),
		sdkmetric.WithResource(res),
	), nil
}

// AgentTracer returns the agentcore.Tracer adapter backed by the process-wide
// OpenTelemetry pipeline, or nil when tracing is disabled or not initialized.
// Callers pass the result to agentcore Config.Tracer; nil is safe (noop).
func AgentTracer() agentcore.Tracer {
	h := getOtelHandle()
	if h == nil {
		// Lazy init for entrypoints that never call InitOtel explicitly.
		InitOtel(context.Background(), slog.Default())
		h = getOtelHandle()
	}
	if h == nil || h.provider == nil {
		return nil
	}
	return agentcore.NewOtelTracer(h.provider.Tracer("covo-agent"))
}

// MetricsRecorder returns the process-wide model metrics recorder, or nil when
// metrics are disabled. The recorder is safe for concurrent use.
func MetricsRecorder() *ModelMetricsRecorder {
	h := getOtelHandle()
	if h == nil {
		// Lazy init for entrypoints that never call InitOtel explicitly.
		InitOtel(context.Background(), slog.Default())
		h = getOtelHandle()
	}
	if h == nil {
		return nil
	}
	return h.metricsRecorder
}

// FlushOtel force-flushes buffered spans and metrics without shutting the
// pipelines down. Safe to call multiple times; no-op when disabled. Use this
// in long-lived processes (background tasks, cron jobs) where ShutdownOtel
// would tear down the pipelines for the rest of the process.
func FlushOtel(ctx context.Context) {
	h := getOtelHandle()
	if h == nil {
		return
	}
	if h.provider != nil {
		_ = h.provider.ForceFlush(ctx)
	}
	if h.meterProvider != nil {
		_ = h.meterProvider.ForceFlush(ctx)
	}
}

// ShutdownOtel flushes and shuts down the process-wide OpenTelemetry pipelines.
// Safe to call multiple times; no-op when telemetry was never initialized.
func ShutdownOtel(ctx context.Context) {
	h := getOtelHandle()
	if h == nil {
		return
	}
	if h.provider != nil {
		_ = h.provider.Shutdown(ctx)
	}
	if h.meterProvider != nil {
		_ = h.meterProvider.Shutdown(ctx)
	}
}

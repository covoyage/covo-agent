// Package telemetry provides OpenTelemetry-compatible telemetry export for
// covo-agent. It bridges the existing audit log to OTLP-compatible traces and
// metrics, allowing users to monitor token usage, tool call latency, and agent
// performance in standard observability platforms (Jaeger, Grafana, Datadog, etc.).
//
// Configuration via environment variables:
//
//	COVO_OTEL_ENDPOINT=https://otel-collector.example.com:4318
//	COVO_OTEL_SERVICE_NAME=covo-agent
//	COVO_OTEL_ENABLED=true
//	COVO_OTEL_EXPORT_INTERVAL=30s
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/audit"
)

// Config holds the telemetry exporter configuration.
type Config struct {
	Enabled        bool
	Endpoint       string        // OTLP HTTP endpoint, e.g. "http://localhost:4318"
	ServiceName    string        // service.name attribute
	ExportInterval time.Duration // how often to flush
}

// ConfigFromEnv reads telemetry config from environment variables.
func ConfigFromEnv() Config {
	cfg := Config{
		Enabled:        os.Getenv("COVO_OTEL_ENABLED") == "true" || os.Getenv("COVO_OTEL_ENABLED") == "1",
		Endpoint:       os.Getenv("COVO_OTEL_ENDPOINT"),
		ServiceName:    os.Getenv("COVO_OTEL_SERVICE_NAME"),
		ExportInterval: 30 * time.Second,
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "covo-agent"
	}
	if v := os.Getenv("COVO_OTEL_EXPORT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ExportInterval = d
		}
	}
	if cfg.Endpoint != "" {
		cfg.Enabled = true
	}
	return cfg
}

// Exporter bridges audit log entries to OpenTelemetry-compatible export.
type Exporter struct {
	cfg        Config
	store      *audit.Store
	client     *http.Client
	logger     *slog.Logger
	redact     func(string) string
	mu         sync.Mutex
	lastExport time.Time
	stopCh     chan struct{}
}

// traceSpan is an OTLP-compatible trace span.
type traceSpan struct {
	TraceID    string         `json:"trace_id"`
	SpanID     string         `json:"span_id"`
	Name       string         `json:"name"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time"`
	Attributes map[string]any `json:"attributes,omitempty"`
	Status     string         `json:"status"`
}

// New creates a telemetry exporter. If the config is disabled, the exporter
// is a no-op.
func New(cfg Config, store *audit.Store) *Exporter {
	return &Exporter{
		cfg:    cfg,
		store:  store,
		client: &http.Client{Timeout: 10 * time.Second},
		logger: slog.Default(),
		redact: func(value string) string { return value },
		stopCh: make(chan struct{}),
	}
}

// SetLogger sets the logger for exporter diagnostics.
func (e *Exporter) SetLogger(l *slog.Logger) {
	e.logger = l
}

// SetRedactor configures mandatory sanitization before audit data is exported.
func (e *Exporter) SetRedactor(redact func(string) string) {
	if redact != nil {
		e.redact = redact
	}
}

// Start begins periodic export of telemetry data. Call Stop() to clean up.
func (e *Exporter) Start(ctx context.Context) {
	if !e.cfg.Enabled {
		return
	}

	e.logger.Info("telemetry exporter started",
		"endpoint", e.cfg.Endpoint,
		"service", e.cfg.ServiceName,
		"interval", e.cfg.ExportInterval,
	)

	go func() {
		ticker := time.NewTicker(e.cfg.ExportInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := e.export(ctx); err != nil {
					e.logger.Warn("telemetry export failed", "err", err)
				}
			case <-e.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop gracefully stops the exporter and flushes remaining data.
func (e *Exporter) Stop() {
	close(e.stopCh)
	if e.cfg.Enabled {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = e.export(ctx)
	}
}

// ExportNow triggers an immediate export of buffered telemetry data.
func (e *Exporter) ExportNow(ctx context.Context) error {
	if !e.cfg.Enabled {
		return nil
	}
	return e.export(ctx)
}

// export reads recent audit entries, converts them to OTLP spans/metrics,
// and sends them to the configured endpoint.
func (e *Exporter) export(ctx context.Context) error {
	e.mu.Lock()
	lastExport := e.lastExport
	e.lastExport = time.Now()
	e.mu.Unlock()

	if lastExport.IsZero() {
		lastExport = time.Now().Add(-e.cfg.ExportInterval)
	}

	// Read audit entries since last export
	entries, err := e.store.Query(audit.QueryFilter{
		Since: lastExport,
		Limit: 1000,
	})
	if err != nil {
		return fmt.Errorf("telemetry: query audit: %w", err)
	}

	if len(entries) == 0 {
		return nil
	}

	// Convert entries to spans
	spans := e.entriesToSpans(entries)
	if len(spans) == 0 {
		return nil
	}

	// Convert to OTLP JSON format
	otlpPayload := e.buildOTLP(spans)

	// Send to collector
	return e.send(ctx, otlpPayload)
}

// entriesToSpans converts audit entries to trace spans.
func (e *Exporter) entriesToSpans(entries []audit.Entry) []traceSpan {
	// Group tool:start and tool:end pairs by session+tool
	type spanKey struct {
		sessionID string
		toolName  string
	}
	startTimes := make(map[spanKey]time.Time)

	var spans []traceSpan
	for _, entry := range entries {
		sessionID := e.redact(entry.SessionID)
		toolName := e.redact(entry.ToolName)
		agentID := e.redact(entry.AgentID)
		key := spanKey{sessionID: sessionID, toolName: toolName}

		switch entry.EventType {
		case "tool:start":
			startTimes[key] = entry.CreatedAt
		case "tool:end":
			start, ok := startTimes[key]
			if !ok {
				start = entry.CreatedAt.Add(-1 * time.Second)
			}
			delete(startTimes, key)

			span := traceSpan{
				TraceID:   generateTraceID(sessionID),
				SpanID:    generateSpanID(entry.ID),
				Name:      toolName,
				StartTime: start,
				EndTime:   entry.CreatedAt,
				Status:    "OK",
				Attributes: map[string]any{
					"service.name": e.cfg.ServiceName,
					"session.id":   sessionID,
					"tool.name":    toolName,
					"agent.id":     agentID,
				},
			}
			// Parse data for status
			if entry.Data != "" {
				var data map[string]any
				if json.Unmarshal([]byte(e.redact(entry.Data)), &data) == nil {
					if status, ok := data["status"].(string); ok && status != "ok" {
						span.Status = status
					}
					for k, v := range data {
						span.Attributes[k] = v
					}
				}
			}
			spans = append(spans, span)
		case "session:start":
			spans = append(spans, traceSpan{
				TraceID:   generateTraceID(sessionID),
				SpanID:    generateSpanID(entry.ID),
				Name:      "session.start",
				StartTime: entry.CreatedAt,
				EndTime:   entry.CreatedAt,
				Status:    "OK",
				Attributes: map[string]any{
					"service.name": e.cfg.ServiceName,
					"session.id":   sessionID,
				},
			})
		}
	}

	return spans
}

// buildOTLP creates an OTLP-compatible JSON payload.
func (e *Exporter) buildOTLP(spans []traceSpan) map[string]any {
	otlpSpans := make([]map[string]any, len(spans))
	for i, s := range spans {
		otlpSpans[i] = map[string]any{
			"trace_id":   s.TraceID,
			"span_id":    s.SpanID,
			"name":       s.Name,
			"start_time": s.StartTime.UnixNano(),
			"end_time":   s.EndTime.UnixNano(),
			"status": map[string]any{
				"code": s.Status,
			},
			"attributes": s.Attributes,
		}
	}

	return map[string]any{
		"resource_spans": []map[string]any{
			{
				"resource": map[string]any{
					"attributes": []map[string]any{
						{"key": "service.name", "value": map[string]any{"string_value": e.cfg.ServiceName}},
					},
				},
				"scope_spans": []map[string]any{
					{
						"scope": map[string]any{
							"name": "covo-agent",
						},
						"spans": otlpSpans,
					},
				},
			},
		},
	}
}

// send sends the OTLP payload to the collector via HTTP.
func (e *Exporter) send(ctx context.Context, payload map[string]any) error {
	if e.cfg.Endpoint == "" {
		return nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telemetry: marshal: %w", err)
	}

	url := strings.TrimRight(e.cfg.Endpoint, "/") + "/v1/traces"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telemetry: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("telemetry: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telemetry: collector returned %s", resp.Status)
	}

	e.logger.Debug("telemetry exported", "spans", len(payload), "endpoint", url)
	return nil
}

// generateTraceID generates a deterministic trace ID from a session ID.
func generateTraceID(sessionID string) string {
	if len(sessionID) >= 32 {
		return sessionID[:32]
	}
	// Pad with zeros
	return fmt.Sprintf("%032s", sessionID)
}

// generateSpanID generates a deterministic span ID from an entry ID.
func generateSpanID(id int64) string {
	return fmt.Sprintf("%016x", id)
}

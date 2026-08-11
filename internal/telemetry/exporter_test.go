package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/audit"
)

func newTestStore(t *testing.T) *audit.Store {
	t.Helper()
	s, err := audit.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("COVO_OTEL_ENABLED", "true")
	t.Setenv("COVO_OTEL_ENDPOINT", "http://localhost:4318")
	t.Setenv("COVO_OTEL_SERVICE_NAME", "test-agent")

	cfg := ConfigFromEnv()
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.Endpoint != "http://localhost:4318" {
		t.Errorf("expected 'http://localhost:4318', got %q", cfg.Endpoint)
	}
	if cfg.ServiceName != "test-agent" {
		t.Errorf("expected 'test-agent', got %q", cfg.ServiceName)
	}
}

func TestConfigFromEnv_DisabledByDefault(t *testing.T) {
	cfg := ConfigFromEnv()
	if cfg.Enabled {
		t.Error("expected disabled by default")
	}
}

func TestConfigFromEnv_EnableByEndpoint(t *testing.T) {
	t.Setenv("COVO_OTEL_ENDPOINT", "http://localhost:4318")
	cfg := ConfigFromEnv()
	if !cfg.Enabled {
		t.Error("expected enabled when endpoint is set")
	}
}

func TestExporter_Disabled(t *testing.T) {
	store := newTestStore(t)
	exporter := New(Config{Enabled: false}, store)

	// Should be no-op
	exporter.Start(context.Background())
	exporter.Stop()
}

func TestEntriesToSpans(t *testing.T) {
	store := newTestStore(t)

	// Log a tool:start and tool:end
	startTime := time.Now()
	store.Log("tool:start", "sess-1", "edit_block", "agent-1", nil)
	time.Sleep(10 * time.Millisecond)
	store.Log("tool:end", "sess-1", "edit_block", "agent-1", map[string]any{"status": "ok"})

	entries, _ := store.Query(audit.QueryFilter{Limit: 10})
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}

	exporter := New(Config{Enabled: true, ServiceName: "test"}, store)
	spans := exporter.entriesToSpans(entries)
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}

	// Should have a span for edit_block
	found := false
	for _, s := range spans {
		if s.Name == "edit_block" {
			found = true
			if s.Attributes["tool.name"] != "edit_block" {
				t.Error("expected tool.name attribute")
			}
		}
	}
	if !found {
		t.Error("expected span for 'edit_block'")
	}

	_ = startTime
}

func TestEntriesToSpansRedactsAuditData(t *testing.T) {
	exporter := New(Config{Enabled: true, ServiceName: "test"}, nil)
	exporter.SetRedactor(func(value string) string {
		return strings.ReplaceAll(value, "audit-secret", "***")
	})
	now := time.Now()
	spans := exporter.entriesToSpans([]audit.Entry{
		{ID: 1, EventType: "tool:start", SessionID: "session", ToolName: "http", CreatedAt: now},
		{ID: 2, EventType: "tool:end", SessionID: "session", ToolName: "http", Data: `{"token":"audit-secret"}`, CreatedAt: now.Add(time.Second)},
	})
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	if got := spans[0].Attributes["token"]; got != "***" {
		t.Fatalf("token attribute = %v", got)
	}
}

func TestBuildOTLP(t *testing.T) {
	exporter := New(Config{Enabled: true, ServiceName: "test"}, nil)

	spans := []traceSpan{
		{
			TraceID:   "abc123",
			SpanID:    "def456",
			Name:      "test.tool",
			StartTime: time.Now(),
			EndTime:   time.Now().Add(time.Second),
			Status:    "OK",
			Attributes: map[string]any{
				"tool.name": "test.tool",
			},
		},
	}

	payload := exporter.buildOTLP(spans)
	if payload == nil {
		t.Fatal("expected non-nil payload")
	}

	// Verify structure
	resourceSpans, ok := payload["resource_spans"].([]map[string]any)
	if !ok || len(resourceSpans) != 1 {
		t.Fatal("expected 1 resource span")
	}

	scopeSpans, ok := resourceSpans[0]["scope_spans"].([]map[string]any)
	if !ok || len(scopeSpans) != 1 {
		t.Fatal("expected 1 scope span")
	}

	otlpSpans, ok := scopeSpans[0]["spans"].([]map[string]any)
	if !ok || len(otlpSpans) != 1 {
		t.Fatal("expected 1 span in OTLP")
	}

	if otlpSpans[0]["name"] != "test.tool" {
		t.Error("expected span name 'test.tool'")
	}
}

func TestSend(t *testing.T) {
	var receivedPayload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Errorf("expected path /v1/traces, got %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	store := newTestStore(t)
	exporter := New(Config{
		Enabled:     true,
		Endpoint:    ts.URL,
		ServiceName: "test",
	}, store)

	store.Log("tool:start", "s1", "test_tool", "a1", nil)
	time.Sleep(5 * time.Millisecond)
	store.Log("tool:end", "s1", "test_tool", "a1", nil)

	// Wait a bit for entries to be queryable
	time.Sleep(10 * time.Millisecond)

	err := exporter.ExportNow(context.Background())
	if err != nil {
		t.Fatalf("ExportNow: %v", err)
	}

	if receivedPayload == nil {
		t.Fatal("expected non-nil received payload")
	}
}

func TestGenerateTraceID(t *testing.T) {
	id := generateTraceID("session-123")
	if len(id) != 32 {
		t.Errorf("expected 32 char trace ID, got %d", len(id))
	}
}

func TestGenerateSpanID(t *testing.T) {
	id := generateSpanID(42)
	if len(id) != 16 {
		t.Errorf("expected 16 char span ID, got %d", len(id))
	}
}

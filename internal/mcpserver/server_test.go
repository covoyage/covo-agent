package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRPCID_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		id   rpcID
		want string
	}{
		{"number", rpcID{Num: 42}, "42"},
		{"string", rpcID{Str: "abc"}, `"abc"`},
		{"null", rpcID{Null: true}, "null"},
		{"zero", rpcID{Num: 0}, "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.id.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON() error: %v", err)
			}
			if string(data) != tt.want {
				t.Errorf("MarshalJSON() = %s, want %s", data, tt.want)
			}
		})
	}
}

func TestRPCID_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  rpcID
	}{
		{"number", "42", rpcID{Num: 42}},
		{"string", `"abc"`, rpcID{Str: "abc"}},
		{"null", "null", rpcID{Null: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id rpcID
			if err := id.UnmarshalJSON([]byte(tt.input)); err != nil {
				t.Fatalf("UnmarshalJSON() error: %v", err)
			}
			if id != tt.want {
				t.Errorf("UnmarshalJSON() = %+v, want %+v", id, tt.want)
			}
		})
	}
}

func TestRPCError_Error(t *testing.T) {
	e := &rpcError{Code: -32601, Message: "method not found"}
	got := e.Error()
	if got == "" {
		t.Error("expected non-empty error string")
	}
}

func TestNewServer(t *testing.T) {
	s := NewServer(nil)
	if s == nil {
		t.Fatal("expected non-nil Server")
	}
	if s.initialized {
		t.Error("expected not initialized")
	}
}

func TestIsRequest(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"valid request", `{"jsonrpc":"2.0","id":1,"method":"test"}`, true},
		{"notification no id", `{"jsonrpc":"2.0","method":"test"}`, false},
		{"invalid json", `{not json}`, false},
		{"empty", ``, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRequest([]byte(tt.input)); got != tt.want {
				t.Errorf("isRequest(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestInitializeResult_JSON(t *testing.T) {
	r := initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    map[string]any{"tools": map[string]any{}},
		ServerInfo:      map[string]string{"name": "test", "version": "1.0"},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	var decoded initializeResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if decoded.ProtocolVersion != protocolVersion {
		t.Errorf("expected protocol version %q, got %q", protocolVersion, decoded.ProtocolVersion)
	}
}

func TestCallToolParams_JSON(t *testing.T) {
	jsonStr := `{"name":"test_tool","arguments":{"key":"value"}}`
	var params callToolParams
	if err := json.Unmarshal([]byte(jsonStr), &params); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if params.Name != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", params.Name)
	}
	if params.Arguments["key"] != "value" {
		t.Errorf("expected arguments[key]='value', got %v", params.Arguments["key"])
	}
}

func TestCallToolResult_JSON(t *testing.T) {
	r := callToolResult{
		Content: []callToolContent{{Type: "text", Text: "result text"}},
		IsError: false,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	if !contains(string(data), "result text") {
		t.Errorf("expected 'result text' in JSON, got %s", data)
	}
}

func TestMCPToolSchema_JSON(t *testing.T) {
	schema := mcpToolSchema{
		Name:        "test",
		Description: "A test tool",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"arg1": map[string]any{"type": "string"},
			},
		},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	var decoded mcpToolSchema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if decoded.Name != "test" {
		t.Errorf("expected name 'test', got %q", decoded.Name)
	}
}

func TestServer_RegisterTool(t *testing.T) {
	s := NewServer(nil)
	s.RegisterTool(mcpToolSchema{Name: "custom_tool"}, func(ctx context.Context, args json.RawMessage) (any, error) {
		return "result", nil
	})
	// Tool should be registered in extraTools
	s.mu.Lock()
	_, exists := s.extraTools["custom_tool"]
	s.mu.Unlock()
	if !exists {
		t.Error("expected custom_tool to be registered")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

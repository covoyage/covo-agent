package canvas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gobwas/ws"
)

func TestLiveCanvasHub_Broadcast(t *testing.T) {
	hub := &liveCanvasHub{
		clients: make(map[chan []byte]struct{}),
	}

	ch := make(chan []byte, 16)
	hub.clients[ch] = struct{}{}

	hub.broadcast("<h1>Hello</h1>", "Test", "<h1>Hello</h1>")

	select {
	case msg := <-ch:
		var m map[string]any
		if err := json.Unmarshal(msg, &m); err != nil {
			t.Fatalf("failed to unmarshal broadcast message: %v", err)
		}
		if m["type"] != "update" {
			t.Errorf("expected type 'update', got %v", m["type"])
		}
		if m["content"] != "<h1>Hello</h1>" {
			t.Errorf("expected content '<h1>Hello</h1>', got %v", m["content"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for broadcast")
	}
}

func TestLiveCanvasHub_Stop(t *testing.T) {
	hub := &liveCanvasHub{
		clients: make(map[chan []byte]struct{}),
		started: true,
	}

	ch := make(chan []byte, 16)
	hub.clients[ch] = struct{}{}

	hub.stop()

	hub.mu.RLock()
	clientCount := len(hub.clients)
	hub.mu.RUnlock()

	if clientCount != 0 {
		t.Errorf("expected 0 clients after stop, got %d", clientCount)
	}
}

func TestWrapLiveCanvasContent_SVG(t *testing.T) {
	result := wrapLiveCanvasContent("<circle/>", "svg")
	if !strings.Contains(result, "<circle/>") {
		t.Error("SVG content not found in result")
	}
	if !strings.Contains(result, "background:#1a1a2e") {
		t.Error("dark theme not applied")
	}
}

func TestWrapLiveCanvasContent_Markdown(t *testing.T) {
	result := wrapLiveCanvasContent("# Hello\n\nWorld", "markdown")
	if !strings.Contains(result, "<h1>") {
		t.Error("markdown heading not converted")
	}
	if !strings.Contains(result, "<p>World</p>") {
		t.Error("markdown paragraph not converted")
	}
}

func TestMdToHTML(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"# Title", "<h1>Title</h1>"},
		{"## Subtitle", "<h2>Subtitle</h2>"},
		{"### Section", "<h3>Section</h3>"},
		{"- item1\n- item2", "<li>item1</li>"},
		{"```go\ncode\n```", "<code>"},
		{"regular text", "<p>regular text</p>"},
	}

	for _, tt := range tests {
		result := mdToHTML(tt.input)
		if !strings.Contains(result, tt.contains) {
			t.Errorf("mdToHTML(%q) = %q, want to contain %q", tt.input, result, tt.contains)
		}
	}
}

func TestLiveCanvasHub_HandleWS(t *testing.T) {
	hub := &liveCanvasHub{
		clients: make(map[chan []byte]struct{}),
	}

	// Set up test HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.handleWS)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Connect WebSocket client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, _, err := ws.DefaultDialer.Dial(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer conn.Close()

	// Wait for connection to be registered
	time.Sleep(50 * time.Millisecond)

	hub.mu.RLock()
	clientCount := len(hub.clients)
	hub.mu.RUnlock()

	if clientCount != 1 {
		t.Errorf("expected 1 client, got %d", clientCount)
	}
}

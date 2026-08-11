package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func TestSendMessageSplitsLongUTF8Text(t *testing.T) {
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		mu.Lock()
		received = append(received, body.Content.Text)
		mu.Unlock()
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter := &Adapter{webhookURL: server.URL, httpClient: server.Client()}
	text := strings.Repeat("\u98de", 2000)
	if err := adapter.SendMessage(context.Background(), "", plugin.OutgoingMessage{Text: text}); err != nil {
		t.Fatal(err)
	}
	assertChunks(t, received, text)
}

func assertChunks(t *testing.T, chunks []string, original string) {
	t.Helper()
	if len(chunks) != 2 {
		t.Fatalf("requests = %d, want 2", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 4000 || !utf8.ValidString(chunk) {
			t.Fatalf("invalid chunk: bytes=%d utf8=%v", len(chunk), utf8.ValidString(chunk))
		}
	}
	if strings.Join(chunks, "") != original {
		t.Fatal("chunks do not preserve text")
	}
}

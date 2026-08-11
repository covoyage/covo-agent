package whatsapp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/plugin"
)

type recordingTransport struct {
	mu       sync.Mutex
	received []string
}

func (transport *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body struct {
		Text struct {
			Body string `json:"body"`
		} `json:"text"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return nil, err
	}
	transport.mu.Lock()
	transport.received = append(transport.received, body.Text.Body)
	transport.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     make(http.Header),
		Request:    request,
	}, nil
}

func TestSendMessageSplitsLongUTF8Text(t *testing.T) {
	transport := &recordingTransport{}
	adapter := &Adapter{
		phoneNumberID: "phone",
		accessToken:   "token",
		httpClient:    &http.Client{Transport: transport},
	}
	text := strings.Repeat("\u804a", 2000)
	if err := adapter.SendMessage(context.Background(), "recipient", plugin.OutgoingMessage{Text: text}); err != nil {
		t.Fatal(err)
	}
	if len(transport.received) != 2 {
		t.Fatalf("requests = %d, want 2", len(transport.received))
	}
	for _, chunk := range transport.received {
		if len(chunk) > 4000 || !utf8.ValidString(chunk) {
			t.Fatalf("invalid chunk: bytes=%d utf8=%v", len(chunk), utf8.ValidString(chunk))
		}
	}
	if strings.Join(transport.received, "") != text {
		t.Fatal("chunks do not preserve text")
	}
}

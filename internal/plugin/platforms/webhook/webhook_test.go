package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func TestAdapterSendToResponseURL(t *testing.T) {
	var gotBody []byte
	var gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSecret = r.Header.Get("X-Webhook-Secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := New()
	a.responseURL = srv.URL
	a.secret = "s3cr3t"

	if err := a.Send(context.Background(), "chan-1", "hello world"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("unmarshal delivered body: %v", err)
	}
	if payload["text"] != "hello world" || payload["channel_id"] != "chan-1" {
		t.Errorf("unexpected payload: %v", payload)
	}
	if gotSecret != "s3cr3t" {
		t.Errorf("expected secret header, got %q", gotSecret)
	}
}

func TestAdapterSendToURLChannelID(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := New() // no WEBHOOK_RESPONSE_URL configured
	// channel_id is itself a callback URL.
	if err := a.SendMessage(context.Background(), srv.URL, plugin.OutgoingMessage{Text: "hi"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !hit {
		t.Error("expected callback URL channel_id to be POSTed to")
	}
}

func TestAdapterSendNoTargetErrors(t *testing.T) {
	a := New()
	a.responseURL = "" // no configured target and channel_id is not a URL
	if err := a.Send(context.Background(), "chan-1", "hi"); err == nil {
		t.Error("expected error when no callback URL is available")
	}
}

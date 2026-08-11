package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func TestGatewaySnapshotsConfiguredPlatforms(t *testing.T) {
	first := &mockPlatform{name: "first"}
	second := &mockPlatform{name: "second"}
	platforms := []plugin.PlatformProvider{first}
	gateway := New(Config{Platforms: platforms})
	platforms[0] = second

	status := gateway.Status()
	if len(status.Platforms) != 1 || status.Platforms[0].Name != "first" {
		t.Fatalf("platform snapshot = %+v", status.Platforms)
	}
}

func TestGatewayStopCancelsOwnedContext(t *testing.T) {
	platform := &mockPlatform{name: "test"}
	gateway := New(Config{
		Platforms:    []plugin.PlatformProvider{platform},
		TickInterval: time.Hour,
	})
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- gateway.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Gateway.Stop did not cancel dispatch workers")
	}
}

func TestApplyAutoTranscription_NoAttachment(t *testing.T) {
	g := &Gateway{}
	msg := plugin.IncomingMessage{Text: "hello"}

	got := g.applyAutoTranscription(context.Background(), msg)
	if got != "hello" {
		t.Errorf("expected text unchanged, got %q", got)
	}
}

func TestApplyAutoTranscription_NonAudioAttachmentIgnored(t *testing.T) {
	g := &Gateway{}
	msg := plugin.IncomingMessage{
		Text: "hi",
		Attachments: []plugin.Attachment{
			{Type: plugin.AttachmentTypeImage, URL: "https://example.com/pic.png"},
		},
	}

	got := g.applyAutoTranscription(context.Background(), msg)
	if got != "hi" {
		t.Errorf("expected text unchanged for non-audio attachment, got %q", got)
	}
}

func TestApplyAutoTranscription_BackendUnavailableFallsBack(t *testing.T) {
	// No whisper-cli/whisper/OPENAI_API_KEY available in the test
	// environment, so transcription must fail gracefully and the original
	// text (possibly empty) must be returned rather than panicking.
	f, err := os.CreateTemp(t.TempDir(), "voice-*.ogg")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.WriteString("not-real-audio")
	f.Close()

	g := &Gateway{}
	msg := plugin.IncomingMessage{
		Text: "caption",
		Attachments: []plugin.Attachment{
			{Type: plugin.AttachmentTypeAudio, LocalPath: f.Name()},
		},
	}

	got := g.applyAutoTranscription(context.Background(), msg)
	if got != "caption" {
		t.Errorf("expected fallback to original text, got %q", got)
	}
}

func TestDownloadToTemp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("audio-bytes"))
	}))
	defer srv.Close()

	path, err := downloadToTemp(context.Background(), srv.URL+"/voice.ogg", "voice.ogg")
	if err != nil {
		t.Fatalf("downloadToTemp: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != "audio-bytes" {
		t.Errorf("expected downloaded content %q, got %q", "audio-bytes", string(data))
	}
}

func TestDownloadToTemp_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := downloadToTemp(context.Background(), srv.URL, "voice.ogg"); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

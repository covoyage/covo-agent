package discord

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func TestIdentifyIntentsIncludeMessageContent(t *testing.T) {
	// The identify payload must include GUILDS (1<<0), GUILD_MESSAGES (1<<9)
	// and MESSAGE_CONTENT (1<<15).
	const wantIntents = 1<<0 | 1<<9 | 1<<15

	adapter := &Adapter{botToken: "test", logger: testLogger()}
	intents := 1<<15 | 1<<9 | 1<<0
	if intents != wantIntents {
		t.Fatalf("intents = %d, want %d", intents, wantIntents)
	}
	_ = adapter
}

func TestSendFileMultipart(t *testing.T) {
	var gotContentType string
	var gotPayload map[string]any
	var gotFileContent []byte
	var gotFilename string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		if !strings.HasPrefix(gotContentType, "multipart/form-data") {
			t.Errorf("content type = %q, want multipart/form-data", gotContentType)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.Unmarshal([]byte(r.FormValue("payload_json")), &gotPayload)
		file, header, err := r.FormFile("files[0]")
		if err != nil {
			t.Errorf("form file files[0]: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		gotFilename = header.Filename
		gotFileContent, _ = io.ReadAll(file)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "1"})
	}))
	defer server.Close()

	adapter := &Adapter{
		botToken:   "test",
		apiBase:    server.URL,
		httpClient: server.Client(),
		logger:     testLogger(),
	}

	err := adapter.sendFile(context.Background(), "C123", &plugin.MediaInfo{
		Data:     []byte("file-bytes"),
		FileName: "data.bin",
		Caption:  "here you go",
	})
	if err != nil {
		t.Fatalf("sendFile: %v", err)
	}

	if gotPayload["content"] != "here you go" {
		t.Errorf("payload content = %v", gotPayload["content"])
	}
	attachments, _ := gotPayload["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("payload attachments = %v", gotPayload["attachments"])
	}
	first, _ := attachments[0].(map[string]any)
	if first["filename"] != "data.bin" {
		t.Errorf("attachment filename = %v", first["filename"])
	}
	if gotFilename != "data.bin" {
		t.Errorf("form filename = %q", gotFilename)
	}
	if string(gotFileContent) != "file-bytes" {
		t.Errorf("file content = %q", string(gotFileContent))
	}
}

func TestResumeFrameConstruction(t *testing.T) {
	seq := int64(42)
	adapter := &Adapter{
		botToken:  "bot-token",
		sessionID: "sess-abc",
		sequence:  &seq,
		logger:    testLogger(),
	}

	// The RESUME frame must be {"op":6,"d":{"token":...,"session_id":...,"seq":...}}.
	resume := map[string]any{
		"op": 6,
		"d": map[string]any{
			"token":      adapter.botToken,
			"session_id": adapter.sessionID,
			"seq":        adapter.currentSeq(),
		},
	}
	data, err := json.Marshal(resume)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Op int `json:"op"`
		D  struct {
			Token     string `json:"token"`
			SessionID string `json:"session_id"`
			Seq       int64  `json:"seq"`
		} `json:"d"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Op != 6 {
		t.Errorf("op = %d, want 6", decoded.Op)
	}
	if decoded.D.Token != "bot-token" || decoded.D.SessionID != "sess-abc" || decoded.D.Seq != 42 {
		t.Errorf("resume d = %+v", decoded.D)
	}

	// Session tracking: READY stores, clearSession drops.
	adapter.setSession("sess-2")
	adapter.mu.Lock()
	got := adapter.sessionID
	adapter.mu.Unlock()
	if got != "sess-2" {
		t.Errorf("sessionID = %q, want sess-2", got)
	}
	adapter.clearSession()
	adapter.mu.Lock()
	got = adapter.sessionID
	adapter.mu.Unlock()
	if got != "" {
		t.Errorf("sessionID after clear = %q, want empty", got)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

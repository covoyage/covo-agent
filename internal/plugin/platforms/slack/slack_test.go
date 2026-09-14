package slack

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestUploadFileThreeStepFlow(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var uploadBody []byte
	var completeParams map[string]any
	var gotUploadToken bool

	mux := http.NewServeMux()
	mux.HandleFunc("/files.getUploadURLExternal", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, "getUploadURLExternal")
		mu.Unlock()
		if got := r.Header.Get("Authorization"); !strings.Contains(got, "xoxb-") {
			t.Errorf("missing bot token auth, got %q", got)
		}
		var params struct {
			Filename string `json:"filename"`
			Length   int    `json:"length"`
		}
		_ = json.NewDecoder(r.Body).Decode(&params)
		if params.Filename != "notes.txt" || params.Length != 11 {
			t.Errorf("getUploadURLExternal params = %+v", params)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "upload_url": "http://" + r.Host + "/upload?token=abc", "file_id": "F123",
		})
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, "upload")
		gotUploadToken = r.URL.Query().Get("token") == "abc"
		uploadBody, _ = readAll(r)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/files.completeUploadExternal", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, "completeUploadExternal")
		_ = json.NewDecoder(r.Body).Decode(&completeParams)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	adapter := &Adapter{
		botToken:   "xoxb-test",
		apiBase:    server.URL,
		httpClient: server.Client(),
		logger:     testLogger(t),
	}

	err := adapter.uploadFile(context.Background(), "C0123", &plugin.MediaInfo{
		Data:     []byte("hello world"),
		FileName: "notes.txt",
		Caption:  "see attached",
	})
	if err != nil {
		t.Fatalf("uploadFile: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	wantOrder := []string{"getUploadURLExternal", "upload", "completeUploadExternal"}
	if len(calls) != 3 {
		t.Fatalf("calls = %v, want %v", calls, wantOrder)
	}
	for i, want := range wantOrder {
		if calls[i] != want {
			t.Fatalf("calls = %v, want order %v", calls, wantOrder)
		}
	}
	if !gotUploadToken {
		t.Error("upload request did not hit the issued upload_url")
	}
	if string(uploadBody) != "hello world" {
		t.Errorf("uploaded body = %q, want %q", string(uploadBody), "hello world")
	}
	if completeParams["channel_id"] != "C0123" {
		t.Errorf("complete channel_id = %v", completeParams["channel_id"])
	}
	files, _ := completeParams["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("complete files = %v", completeParams["files"])
	}
	first, _ := files[0].(map[string]any)
	if first["id"] != "F123" {
		t.Errorf("complete file id = %v, want F123", first["id"])
	}
}

func TestSocketModeLoopReconnectsAfterDrop(t *testing.T) {
	var mu sync.Mutex
	connCount := 0
	connReady := make(chan struct{}, 8)
	handleWSConn := func(t *testing.T, conn net.Conn) {
		t.Helper()
		mu.Lock()
		connCount++
		n := connCount
		mu.Unlock()

		if err := wsutil.WriteServerText(conn, []byte(`{"type":"hello","num_connections":1}`)); err != nil {
			conn.Close()
			return
		}
		connReady <- struct{}{}

		if n == 1 {
			// First session: drop the connection right after hello to simulate a
			// network failure.
			conn.Close()
			return
		}
		// Subsequent sessions stay open until the test tears down.
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				conn.Close()
				return
			}
		}
	}

	upgrader := ws.HTTPUpgrader{}

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _, err := upgrader.Upgrade(r, w)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		handleWSConn(t, conn)
	}))
	defer wsServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/apps.connections.open") {
			t.Errorf("unexpected API call: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "url": "ws" + strings.TrimPrefix(wsServer.URL, "http"),
		})
	}))
	defer apiServer.Close()

	adapter := &Adapter{
		botToken:   "xoxb-test",
		appToken:   "xapp-test",
		apiBase:    apiServer.URL,
		httpClient: apiServer.Client(),
		logger:     testLogger(t),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go adapter.socketModeLoop(ctx)

	// Wait until the second connection is established (i.e. reconnect after
	// the forced drop happened).
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := connCount
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket mode did not reconnect after drop, connections = %d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	mu.Lock()
	final := connCount
	mu.Unlock()
	if final < 2 {
		t.Fatalf("expected at least 2 connections, got %d", final)
	}
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

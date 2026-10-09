package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type captureMCPWriter struct{ messages chan []byte }

type blockingMCPWriter struct {
	*io.PipeWriter
	started chan struct{}
	once    sync.Once
}

func (w *blockingMCPWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	return w.PipeWriter.Write(data)
}

func (w *captureMCPWriter) Write(data []byte) (int, error) {
	w.messages <- append([]byte(nil), data...)
	return len(data), nil
}

func (w *captureMCPWriter) Close() error { return nil }

func TestMCPCancelNotificationAndLateResponse(t *testing.T) {
	w := &captureMCPWriter{messages: make(chan []byte, 4)}
	client := &stdioClient{stdin: w, pending: make(map[int64]chan<- rpcResponse)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := client.CallTool(ctx, "slow", nil); done <- err }()
	var request rpcRequest
	select {
	case data := <-w.messages:
		if err := json.Unmarshal(data, &request); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("request not sent")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancel result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	var notification struct {
		Method string `json:"method"`
		Params struct {
			RequestID int64 `json:"requestId"`
		} `json:"params"`
	}
	if err := json.Unmarshal(<-w.messages, &notification); err != nil {
		t.Fatal(err)
	}
	if notification.Method != "notifications/cancelled" || notification.Params.RequestID != request.ID {
		t.Fatalf("notification: %+v", notification)
	}
	client.mu.Lock()
	if len(client.pending) != 0 {
		t.Error("pending call retained")
	}
	client.mu.Unlock()
	client.scanner = bufio.NewScanner(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
	client.readLoop()
	client.Close()
}

func TestMCPPreCancelledCallDoesNotWrite(t *testing.T) {
	w := &captureMCPWriter{messages: make(chan []byte, 1)}
	client := &stdioClient{stdin: w, pending: make(map[int64]chan<- rpcResponse)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.CallTool(ctx, "never", nil); err != context.Canceled {
		t.Fatal(err)
	}
	if len(w.messages) != 0 {
		t.Fatal("pre-cancelled request sent")
	}
}

func TestMCPBlockedWriteCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	blocking := &blockingMCPWriter{PipeWriter: writer, started: make(chan struct{})}
	client := &stdioClient{stdin: blocking, pending: make(map[int64]chan<- rpcResponse)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := client.CallTool(ctx, "blocked", nil); done <- err }()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked write: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked write did not cancel")
	}
}

func TestMCPHTTPCancelNotification(t *testing.T) {
	started := make(chan struct{})
	notifications := make(chan rpcRequest, 1)
	client := &httpClient{endpoint: "https://example.test/mcp", sessionID: "session", httpCli: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var request rpcRequest
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			return nil, err
		}
		if request.Method == "notifications/cancelled" {
			if req.Header.Get("Mcp-Session-Id") != "session" {
				t.Error("cancel lost session")
			}
			notifications <- request
			return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := client.CallTool(ctx, "slow", nil); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP cancellation blocked")
	}
	select {
	case notice := <-notifications:
		if notice.Method != "notifications/cancelled" {
			t.Fatal(notice)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP cancellation not notified")
	}
}

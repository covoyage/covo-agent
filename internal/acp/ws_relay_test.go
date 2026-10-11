package acp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestRelayDefaultsAndAuthentication(t *testing.T) {
	relay, err := NewWSRelay(WSRelayConfig{})
	if err != nil || relay.cfg.Addr != "127.0.0.1:17891" {
		t.Fatalf("defaults: %+v %v", relay, err)
	}
	if _, err := NewWSRelay(WSRelayConfig{Addr: ":17891"}); err == nil {
		t.Fatal("public relay accepted without authentication")
	}
	relay, err = NewWSRelay(WSRelayConfig{Addr: ":17891", AuthToken: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "Bearer wrong"} {
		request := httptest.NewRequest("GET", "http://localhost/ws", nil)
		request.Header.Set("Authorization", token)
		response := httptest.NewRecorder()
		relay.handleWebSocket(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status=%d", response.Code)
		}
	}
	request := httptest.NewRequest("GET", "http://localhost/ws", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Origin", "https://other.test")
	response := httptest.NewRecorder()
	relay.handleWebSocket(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status=%d", response.Code)
	}
}

func TestRelayBoundsMessages(t *testing.T) {
	for _, size := range []int{4, 65} {
		var frame bytes.Buffer
		if err := wsutil.WriteClientMessage(&frame, ws.OpText, bytes.Repeat([]byte("x"), size)); err != nil {
			t.Fatal(err)
		}
		data, err := readRelayMessage(&frame, 64)
		if size <= 64 && (err != nil || len(data) != size) {
			t.Fatalf("valid message: %d %v", len(data), err)
		}
		if size > 64 && err == nil {
			t.Fatal("oversized message accepted")
		}
	}
}

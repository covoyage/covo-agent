package mcpserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestSessionToolProvidersReportOfflineWhenGatewayDown 验证 gateway 未运行
// 时，5 个工具返回可执行的 gateway_offline 状态（探测真实 socket），
// 而不是无条件 stub。
func TestSessionToolProvidersReportOfflineWhenGatewayDown(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "gateway.sock") // 不存在 → 探测失败
	p := &SessionToolProvider{gateway: newGatewayClient(socket)}
	ctx := context.Background()

	tests := []struct {
		name string
		args string
		fn   func(context.Context, json.RawMessage) (any, error)
	}{
		{name: "messages_send", args: `{"target":"telegram:123","message":"hi"}`, fn: p.messagesSend},
		{name: "events_poll", fn: p.eventsPoll},
		{name: "events_wait", fn: p.eventsWait},
		{name: "permissions_list_open", fn: p.permissionsListOpen},
		{name: "permissions_respond", args: `{"id":"perm-1","decision":"allow-once"}`, fn: p.permissionsRespond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.fn(ctx, json.RawMessage(tt.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			m, ok := res.(map[string]any)
			if !ok {
				t.Fatalf("expected map[string]any response, got %T", res)
			}
			if got, _ := m["status"].(string); got != "unavailable" {
				t.Fatalf("status = %q, want unavailable", got)
			}
			if got, _ := m["code"].(string); got != "gateway_offline" {
				t.Fatalf("code = %q, want gateway_offline", got)
			}
			msg, _ := m["message"].(string)
			if msg == "" {
				t.Fatalf("offline status must carry an actionable message")
			}
		})
	}
}

// TestSessionToolProvidersLiveGateway 用一个内存内 unix-socket HTTP 服务
// 模拟 gateway IPC，验证 messages_send / events_poll / permissions 往返。
func TestSessionToolProvidersLiveGateway(t *testing.T) {
	// macOS 的 t.TempDir() 路径超过 unix socket 104 字符限制，用 /tmp 短路径。
	socket := filepath.Join("/tmp", "covo-gw-test-"+strconv.Itoa(os.Getpid())+".sock")
	_ = os.Remove(socket)
	t.Cleanup(func() { _ = os.Remove(socket) })

	mux := http.NewServeMux()
	var gotSend map[string]any
	mux.HandleFunc("POST /api/send", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotSend)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "sent", "platform": "telegram", "channel_id": gotSend["channel_id"]})
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"events": []map[string]any{{"seq": 1, "type": "message_received"}},
			"cursor": 1, "latest": 1,
		})
	})
	mux.HandleFunc("GET /api/permissions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pending": []map[string]any{{"id": "perm-1", "command": "rm -rf /"}},
		})
	})
	mux.HandleFunc("POST /api/permissions/respond", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["id"] != "perm-1" || req["choice"] != "deny" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "id": req["id"], "choice": req["choice"]})
	})

	// unix listener
	unixLn, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer unixLn.Close()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = srv.Serve(unixLn) }()
	defer srv.Close()

	p := &SessionToolProvider{gateway: newGatewayClient(socket)}
	ctx := context.Background()

	// messages_send 往返。
	res, err := p.messagesSend(ctx, json.RawMessage(`{"target":"telegram:123","message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := res.(map[string]any); m["status"] != "sent" {
		t.Fatalf("messages_send status = %v, want sent", m["status"])
	}
	if gotSend == nil || gotSend["channel_id"] != "123" {
		t.Fatalf("gateway did not receive correct payload: %v", gotSend)
	}

	// events_poll。
	res, err = p.eventsPoll(ctx, json.RawMessage(`{"after_cursor":0}`))
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if evs, ok := m["events"].([]any); !ok || len(evs) != 1 {
		t.Fatalf("events_poll events = %#v, want 1 event", m["events"])
	}

	// permissions 往返。
	res, err = p.permissionsListOpen(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pend, ok := res.(map[string]any)["pending"].([]any); !ok || len(pend) != 1 {
		t.Fatalf("permissions_list_open pending = %#v", res)
	}
	res, err = p.permissionsRespond(ctx, json.RawMessage(`{"id":"perm-1","decision":"deny"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := res.(map[string]any); m["status"] != "ok" {
		t.Fatalf("permissions_respond = %v, want ok", m)
	}
}

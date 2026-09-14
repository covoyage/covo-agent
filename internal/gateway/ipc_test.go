package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/agent/approval"
	"github.com/covoyage/covo-agent/internal/plugin"
)

func TestEventLogCursorAndNotify(t *testing.T) {
	log := NewEventLog()
	if got := log.LastSeq(); got != 0 {
		t.Fatalf("fresh log seq = %d, want 0", got)
	}
	log.Append(Event{Type: EventMessageReceived, Text: "a"})
	e2 := log.Append(Event{Type: EventReplySent, Text: "b"})
	if log.LastSeq() != 2 {
		t.Fatalf("seq = %d, want 2", log.LastSeq())
	}

	// cursor=0 → 全部；cursor=1 → 只有第二条。
	if evs := log.Since(0); len(evs) != 2 {
		t.Fatalf("Since(0) = %d events, want 2", len(evs))
	}
	evs := log.Since(1)
	if len(evs) != 1 || evs[0].Seq != 2 {
		t.Fatalf("Since(1) = %#v, want seq 2 only", evs)
	}

	// Wait：已有事件立即返回。
	if evs := log.Wait(context.Background(), 0, time.Second); len(evs) != 2 {
		t.Fatalf("Wait(0) = %d events, want 2", len(evs))
	}
	// Wait：无事件时被 Append 唤醒。
	done := make(chan []Event, 1)
	go func() { done <- log.Wait(context.Background(), e2.Seq, 2*time.Second) }()
	time.Sleep(20 * time.Millisecond)
	log.Append(Event{Type: EventMessageReceived, Text: "c"})
	if evs := <-done; len(evs) != 1 || evs[0].Seq != 3 {
		t.Fatalf("Wait wake = %#v, want seq 3", evs)
	}
}

func TestPermissionStoreRoundTrip(t *testing.T) {
	ps := NewPermissionStore()
	req := ps.Register(PermissionRequest{Command: "ls -la", PatternKey: "p1"})
	if req.ID == "" {
		t.Fatal("Register must assign an ID")
	}
	if open := ps.ListOpen(); len(open) != 1 || open[0].ID != req.ID {
		t.Fatalf("ListOpen = %#v, want 1 pending %s", open, req.ID)
	}

	decided := make(chan error, 1)
	go func() {
		choice, err := ps.Wait(context.Background(), req.ID)
		if err != nil {
			decided <- err
			return
		}
		if choice != "always" {
			decided <- errBadChoice(choice)
			return
		}
		decided <- nil
	}()
	// Wait 挂起中：Respond 投递决策。
	if !ps.Respond(req.ID, "always") {
		t.Fatal("Respond returned false for pending id")
	}
	if err := <-decided; err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// 决策后不再在 open 列表，重复 Respond 失败。
	if open := ps.ListOpen(); len(open) != 0 {
		t.Fatalf("ListOpen after decide = %d, want 0", len(open))
	}
	if ps.Respond(req.ID, "deny") {
		t.Fatal("double respond should fail")
	}
}

type errBadChoice string

func (e errBadChoice) Error() string { return "bad choice: " + string(e) }

// fakePlatform 是最小可用的 PlatformProvider，用于 IPC 往返测试。
type fakePlatform struct {
	name    string
	sent    []string
	onMsg   func(plugin.IncomingMessage)
	started chan struct{}
}

func (f *fakePlatform) Name() string             { return f.name }
func (f *fakePlatform) Category() plugin.Category { return plugin.CategoryPlatform }
func (f *fakePlatform) Validate() error          { return nil }
func (f *fakePlatform) OnMessage(cb func(plugin.IncomingMessage)) { f.onMsg = cb }
func (f *fakePlatform) Send(ctx context.Context, channelID, text string) error {
	return f.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}
func (f *fakePlatform) Start(ctx context.Context) error {
	close(f.started)
	return nil
}
func (f *fakePlatform) Stop() error { return nil }
func (f *fakePlatform) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	f.sent = append(f.sent, channelID+"|"+msg.Text)
	return nil
}

func TestIPCServerSendEventsPermissions(t *testing.T) {
	socket := filepath.Join("/tmp", "covo-ipc-test-"+strconv.Itoa(os.Getpid())+".sock")
	_ = os.Remove(socket)
	t.Cleanup(func() { _ = os.Remove(socket) })

	plat := &fakePlatform{name: "testplat", started: make(chan struct{})}
	gw := New(Config{
		Platforms:     []plugin.PlatformProvider{plat},
		AgentFactory:  func(ctx context.Context) (Agent, error) { return nil, context.Canceled },
		IPCSocketPath: socket,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := gw.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer gw.Stop()
	<-plat.started

	// 通过 socket 发消息。
	body, _ := json.Marshal(map[string]string{"platform": "testplat", "channel_id": "c1", "text": "hello"})
	resp, err := ipcPost(socket, "/api/send", body)
	if err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "sent" {
		t.Fatalf("send = %#v, want sent", resp)
	}
	if len(plat.sent) != 1 || plat.sent[0] != "c1|hello" {
		t.Fatalf("platform sent = %#v", plat.sent)
	}

	// 事件里能看到 reply_sent（message_received 只来自入站消息流）。
	status, evs := ipcGet(socket, "/api/events?cursor=0")
	if status != http.StatusOK {
		t.Fatalf("events status %d", status)
	}
	list := evs["events"].([]any)
	if len(list) != 1 {
		t.Fatalf("events = %d, want 1", len(list))
	}
	if list[0].(map[string]any)["type"] != EventReplySent {
		t.Fatalf("event type = %#v, want reply_sent", list[0])
	}

	// 审批桥往返：注册（桥）→ respond（IPC）→ Wait 收到决策。
	bridge := gw.ApprovalBridge()
	bridgeCtx := WithMessageContext(context.Background(), plugin.IncomingMessage{
		Platform: "testplat", ChannelID: "c1", UserID: "u1", UserName: "U",
	})
	go func() {
		time.Sleep(50 * time.Millisecond)
		// 找到 pending id 再 respond。
		_, list := ipcGet(socket, "/api/permissions")
		pending := list["pending"].([]any)
		if len(pending) != 1 {
			t.Errorf("pending = %d, want 1", len(pending))
			return
		}
		id := pending[0].(map[string]any)["id"].(string)
		b, _ := json.Marshal(map[string]string{"id": id, "choice": "once"})
		ipcPost(socket, "/api/permissions/respond", b)
	}()
	choice, err := bridge(bridgeCtx, approval.ExternalApprovalRequest{
		Command: "rm x", PatternKey: "rm", Description: "delete",
	})
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	if choice != "once" {
		t.Fatalf("choice = %q, want once", choice)
	}
}

func ipcPost(socket, path string, body []byte) (map[string]any, error) {
	client := ipcClient(socket)
	resp, err := client.Post("http://gateway"+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func ipcGet(socket, path string) (int, map[string]any) {
	client := ipcClient(socket)
	resp, err := client.Get("http://gateway" + path)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func ipcClient(socket string) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
}

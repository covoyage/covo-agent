package yuanbao

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gobwas/ws"

	"github.com/covoyage/covo-agent/internal/plugin"
)

// 类型别名，简化测试书写。
type (
	pluginIncoming = plugin.IncomingMessage
	pluginOutgoing = plugin.OutgoingMessage
)

func mustCompile(pattern string) *regexp.Regexp { return regexp.MustCompile(pattern) }

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- sign-token 测试 ----

// newFakeSignServer 校验签名算法并返回固定 token。
func newFakeSignServer(t *testing.T, appKey, appSecret string, count *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("bad sign-token payload: %v", err)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if payload["app_key"] != appKey {
			t.Errorf("app_key = %q", payload["app_key"])
		}
		// 断言签名算法：HMAC-SHA256(nonce+timestamp+app_key+app_secret, key=app_secret)
		want := computeSignature(payload["nonce"], payload["timestamp"], appKey, appSecret)
		if payload["signature"] != want {
			t.Errorf("signature = %q, want %q", payload["signature"], want)
		}
		// timestamp 形如 2026-09-13T12:00:00+08:00
		if !signTsRe.MatchString(payload["timestamp"]) {
			t.Errorf("timestamp = %q", payload["timestamp"])
		}
		if got := r.Header.Get("X-Instance-Id"); got != fmt.Sprint(instanceID) {
			t.Errorf("X-Instance-Id = %q", got)
		}
		if got := r.Header.Get("X-OperationSystem"); got == "" {
			t.Error("missing X-OperationSystem")
		}
		if count != nil {
			*count++
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"data":{"token":"tok-1","bot_id":"bot-1","source":"bot","duration":3600}}`)
	}))
}

var signTsRe = mustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\+08:00$`)

func TestComputeSignature(t *testing.T) {
	// 独立实现一份 HMAC 以交叉验证拼接顺序。
	nonce, ts, key, secret := "abcd1234", "2026-09-13T08:30:00+08:00", "my-key", "my-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(nonce + ts + key + secret))
	want := hex.EncodeToString(mac.Sum(nil))
	if got := computeSignature(nonce, ts, key, secret); got != want {
		t.Fatalf("computeSignature = %q, want %q", got, want)
	}
	if got2 := computeSignature("n", "t", "k", "s"); len(got2) != 64 {
		t.Fatalf("signature length = %d, want 64 hex chars", len(got2))
	}
}

func TestBuildSignTimestamp(t *testing.T) {
	// 固定 UTC 时刻 → 北京时区 ISO-8601 无毫秒。
	utc := time.Date(2026, 9, 13, 4, 5, 6, 0, time.UTC)
	want := "2026-09-13T12:05:06+08:00"
	if got := buildSignTimestamp(utc); got != want {
		t.Fatalf("buildSignTimestamp = %q, want %q", got, want)
	}
}

func TestSignTokenFetchCacheAndHeaders(t *testing.T) {
	var count int
	appKey, appSecret := "app-key-1", "app-secret-1"
	srv := newFakeSignServer(t, appKey, appSecret, &count)
	defer srv.Close()

	m := newSignManager("0.0.0")
	m.sleep = func(context.Context, time.Duration) error { return nil }

	ctx := context.Background()
	td, err := m.getToken(ctx, appKey, appSecret, srv.URL, "", false)
	if err != nil {
		t.Fatalf("getToken: %v", err)
	}
	if td.Token != "tok-1" || td.BotID != "bot-1" || td.Source != "bot" {
		t.Fatalf("tokenData = %+v", td)
	}
	if td.ExpireTS.Before(m.now()) {
		t.Fatal("expire not in the future")
	}

	// 缓存命中：不再发请求。
	if _, err := m.getToken(ctx, appKey, appSecret, srv.URL, "", false); err != nil {
		t.Fatalf("getToken cached: %v", err)
	}
	if count != 1 {
		t.Fatalf("request count = %d, want 1 (cached)", count)
	}

	// force 刷新。
	if _, err := m.getToken(ctx, appKey, appSecret, srv.URL, "", true); err != nil {
		t.Fatalf("getToken force: %v", err)
	}
	if count != 2 {
		t.Fatalf("request count = %d, want 2 (force refresh)", count)
	}
}

func TestSignTokenRetryable(t *testing.T) {
	appKey, appSecret := "app-key-2", "app-secret-2"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"code":10099,"msg":"retry me"}`)
			return
		}
		fmt.Fprint(w, `{"code":0,"data":{"token":"tok-2","bot_id":"bot-2","source":"bot","duration":600}}`)
	}))
	defer srv.Close()

	m := newSignManager("0.0.0")
	retried := 0
	m.sleep = func(context.Context, time.Duration) error { retried++; return nil }

	td, err := m.getToken(context.Background(), appKey, appSecret, srv.URL, "", false)
	if err != nil {
		t.Fatalf("getToken with retry: %v", err)
	}
	if td.Token != "tok-2" || calls != 2 || retried != 1 {
		t.Fatalf("td=%+v calls=%d retried=%d", td, calls, retried)
	}
}

func TestSignTokenPermanentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":10001,"msg":"bad app key"}`)
	}))
	defer srv.Close()

	m := newSignManager("0.0.0")
	m.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := m.getToken(context.Background(), "k", "s", srv.URL, "", false); err == nil {
		t.Fatal("expected error for permanent sign-token failure")
	}
}

// ---- env 映射测试 ----

func TestEnvMapping(t *testing.T) {
	// 旧名 YUANBAO_API_KEY 兜底为 app_key。
	t.Setenv("YUANBAO_API_KEY", "legacy-key")
	t.Setenv("YUANBAO_APP_SECRET", "secret-1")
	a := New()
	if a.appKey != "legacy-key" || a.appSecret != "secret-1" {
		t.Fatalf("appKey=%q appSecret=%q", a.appKey, a.appSecret)
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// 新名优先。
	t.Setenv("YUANBAO_APP_ID", "new-key")
	a2 := New()
	if a2.appKey != "new-key" {
		t.Fatalf("appKey = %q, want new-key", a2.appKey)
	}
	// bot_id / ws / domain 默认值。
	if a.wsURL != defaultWSURL || a.apiDomain != defaultAPIDomain {
		t.Fatalf("wsURL=%q apiDomain=%q", a.wsURL, a.apiDomain)
	}
}

// ---- fake WS server（gobwas 服务端 upgrade） ----

type fakeWSServer struct {
	t      *testing.T
	ln     net.Listener
	URL    string
	mu     sync.Mutex
	frames [][]byte
	conns  []net.Conn
	auths  int
	handle func(f *fakeWSServer, raw []byte, respond func([]byte))
}

func newFakeWSServer(t *testing.T) *fakeWSServer {
	t.Helper()
	f := &fakeWSServer{t: t}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f.ln = ln
	f.URL = "ws://" + ln.Addr().String()
	go f.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeWSServer) acceptLoop() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handleConn(conn)
	}
}

func (f *fakeWSServer) handleConn(conn net.Conn) {
	f.mu.Lock()
	f.conns = append(f.conns, conn)
	f.mu.Unlock()
	defer conn.Close()
	if _, err := ws.Upgrade(conn); err != nil {
		return
	}
	for {
		fr, err := ws.ReadFrame(conn)
		if err != nil {
			return
		}
		if fr.Header.Masked {
			fr = ws.UnmaskFrame(fr)
		}
		switch fr.Header.OpCode {
		case ws.OpBinary:
			f.mu.Lock()
			f.frames = append(f.frames, fr.Payload)
			f.mu.Unlock()
			respond := func(data []byte) {
				_ = ws.WriteFrame(conn, ws.NewBinaryFrame(data))
			}
			f.mu.Lock()
			h := f.handle
			f.mu.Unlock()
			if h != nil {
				h(f, fr.Payload, respond)
			}
		case ws.OpPing:
			_ = ws.WriteFrame(conn, ws.NewPongFrame(fr.Payload))
		case ws.OpClose:
			_ = ws.WriteFrame(conn, ws.NewCloseFrame(nil))
			return
		}
	}
}

func (f *fakeWSServer) frameCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

func (f *fakeWSServer) decodedFrames() []*connMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*connMsg
	for _, raw := range f.frames {
		if m, err := decodeConnMsg(raw); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeWSServer) closeActiveConn() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.conns = nil
}

// currentConn 返回最近一条服务端连接（用于测试主动下发帧）。
func (f *fakeWSServer) currentConn() *fakeWSConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.conns) == 0 {
		return nil
	}
	return &fakeWSConn{conn: f.conns[len(f.conns)-1]}
}

// fakeWSConn 是测试向客户端主动发送帧的包装。
type fakeWSConn struct{ conn net.Conn }

func (c *fakeWSConn) Send(data []byte) {
	_ = ws.WriteFrame(c.conn, ws.NewBinaryFrame(data))
}

// ---- 帧构造辅助 ----

func buildBindAckFrame(reqMsgID string) []byte {
	var p pbBuf
	p.varint(1, 0) // code = 0
	p.str(3, "conn-1")
	return encodeConnMsg(cmdTypeResponse, cmdAuthBind, nextSeqNo(), reqMsgID, moduleConnAccess, p.b)
}

func buildBindAckErrFrame(reqMsgID string, code uint64, msg string) []byte {
	var p pbBuf
	p.varint(1, code)
	p.str(2, msg)
	return encodeConnMsg(cmdTypeResponse, cmdAuthBind, nextSeqNo(), reqMsgID, moduleConnAccess, p.b)
}

func buildPushFrame(from, group, text string, needAck bool) []byte {
	var p pbBuf
	p.strIf(2, from)
	p.strIf(6, group)
	p.varint(8, 100) // msg_seq
	p.nested(13, encodeMsgBodyElem(textElem(text)))
	return encodeConnMsgFull(cmdTypePush, "cb", nextSeqNo(), "push-msg-1", "trpc.yuanbao.yuanbao_conn", p.b, needAck)
}

func buildJSONPushFrame(from, group, text string) []byte {
	payload := fmt.Sprintf(
		`{"callback_command":"C2CMessageSync","from_account":%q,"group_code":%q,"group_name":"g1",`+
			`"msg_seq":7,"msg_id":"mid-1","msg_body":[{"msg_type":"TIMTextElem","msg_content":{"text":%q}}]}`,
		from, group, text)
	return encodeConnMsgFull(cmdTypePush, "cb", nextSeqNo(), "push-json-1", "trpc.yuanbao.yuanbao_conn", []byte(payload), true)
}

func defaultAdapter(t *testing.T, signURL, wsURL string) *Adapter {
	t.Helper()
	a := &Adapter{
		appKey:      "app-key-1",
		appSecret:   "app-secret-1",
		wsURL:       wsURL,
		apiDomain:   signURL,
		logger:      newTestLogger(),
		sign:        newSignManager(appVersion),
		hbInterval:  80 * time.Millisecond,
		sendTimeout: 3 * time.Second,
		pending:     map[string]chan *connMsg{},
	}
	a.sign.sleep = func(context.Context, time.Duration) error { return nil }
	return a
}

// defaultWSHandler 自动应答 AUTH_BIND / ping / send 请求。
func defaultWSHandler(f *fakeWSServer, raw []byte, respond func([]byte)) {
	msg, err := decodeConnMsg(raw)
	if err != nil {
		return
	}
	switch {
	case msg.cmdType == cmdTypeRequest && msg.cmd == cmdAuthBind:
		f.mu.Lock()
		f.auths++
		f.mu.Unlock()
		respond(buildBindAckFrame(msg.msgID))
	case msg.cmdType == cmdTypeRequest && msg.cmd == cmdPing:
		respond(encodeConnMsg(cmdTypeResponse, cmdPing, msg.seqNo, msg.msgID, msg.module, nil))
	case msg.cmdType == cmdTypeRequest:
		respond(encodeConnMsg(cmdTypeResponse, msg.cmd, msg.seqNo, msg.msgID, msg.module, nil))
	}
}

func waitAuthed(t *testing.T, a *Adapter, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if a.authed.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("adapter did not complete AUTH_BIND in time")
}

// ---- WS 流程测试 ----

func TestAuthBindAndProtobufPush(t *testing.T) {
	srv := newFakeSignServer(t, "app-key-1", "app-secret-1", nil)
	defer srv.Close()
	wsSrv := newFakeWSServer(t)
	wsSrv.handle = defaultWSHandler

	a := defaultAdapter(t, srv.URL, wsSrv.URL)
	got := make(chan pluginIncoming, 8)
	a.OnMessage(func(m pluginIncoming) { got <- m })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()
	waitAuthed(t, a, 5*time.Second)

	// 断言 AUTH_BIND 帧结构。
	authFrame := func() *connMsg {
		for _, m := range wsSrv.decodedFrames() {
			if m.cmd == cmdAuthBind && m.cmdType == cmdTypeRequest {
				return m
			}
		}
		return nil
	}()
	if authFrame == nil {
		t.Fatal("no AUTH_BIND frame received")
	}
	if authFrame.module != moduleConnAccess || authFrame.msgID == "" {
		t.Fatalf("AUTH_BIND head: %+v", authFrame)
	}
	d, err := parseDict(authFrame.data)
	if err != nil {
		t.Fatalf("parse AUTH_BIND data: %v", err)
	}
	if got := d.getString(1); got != bizID {
		t.Errorf("biz_id = %q, want %q", got, bizID)
	}
	authInfo := parseDictMust(t, d.getBytes(2))
	if authInfo.getString(1) != "bot-1" { // uid = sign-token 返回的 bot_id
		t.Errorf("uid = %q, want bot-1", authInfo.getString(1))
	}
	if authInfo.getString(2) != "bot" {
		t.Errorf("source = %q", authInfo.getString(2))
	}
	if authInfo.getString(3) != "tok-1" {
		t.Errorf("token = %q", authInfo.getString(3))
	}
	dev := parseDictMust(t, d.getBytes(3))
	if dev.getString(10) != fmt.Sprint(instanceID) {
		t.Errorf("device_info.instance_id = %q, want %q", dev.getString(10), fmt.Sprint(instanceID))
	}

	// protobuf 入站推送 → 回调（服务端主动下发）。
	conn := wsSrv.currentConn()
	if conn == nil {
		t.Fatal("no ws connection")
	}
	conn.Send(buildPushFrame("user-42", "", "hello protobuf", false))

	select {
	case m := <-got:
		if m.Platform != PlatformName || m.UserID != "user-42" || m.ChannelID != "user-42" || m.Text != "hello protobuf" {
			t.Fatalf("message: %+v", m)
		}
		if m.UserName != "user-42" {
			t.Errorf("UserName = %q", m.UserName)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("callback not invoked for protobuf push")
	}
}

func TestAuthBindError(t *testing.T) {
	srv := newFakeSignServer(t, "app-key-1", "app-secret-1", nil)
	defer srv.Close()
	wsSrv := newFakeWSServer(t)
	wsSrv.handle = func(f *fakeWSServer, raw []byte, respond func([]byte)) {
		if msg, err := decodeConnMsg(raw); err == nil && msg.cmd == cmdAuthBind {
			respond(buildBindAckErrFrame(msg.msgID, 401, "bad token"))
			return
		}
	}
	a := defaultAdapter(t, srv.URL, wsSrv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()
	// AUTH 失败 → 不应进入 connected 状态。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.authed.Load() {
			t.Fatal("authed despite BIND_ACK error")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestJSONPushAndPushAck(t *testing.T) {
	srv := newFakeSignServer(t, "app-key-1", "app-secret-1", nil)
	defer srv.Close()
	wsSrv := newFakeWSServer(t)
	wsSrv.handle = defaultWSHandler

	a := defaultAdapter(t, srv.URL, wsSrv.URL)
	got := make(chan pluginIncoming, 8)
	a.OnMessage(func(m pluginIncoming) { got <- m })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()
	waitAuthed(t, a, 5*time.Second)

	// JSON 入站推送（need_ack=true → 期待 PushAck）。
	conn := wsSrv.currentConn()
	conn.Send(buildJSONPushFrame("user-7", "", "hello json"))
	// 群聊 JSON 推送：channelID = group:<code>。
	conn.Send(buildJSONPushFrame("user-9", "777", "group msg"))

	// 两条回调并发投递，收集后按 UserID 断言。
	seen := map[string]pluginIncoming{}
	collectTimeout := time.After(3 * time.Second)
	for len(seen) < 2 {
		select {
		case m := <-got:
			seen[m.UserID] = m
		case <-collectTimeout:
			t.Fatalf("callbacks not invoked, got %d", len(seen))
		}
	}
	dm, gm := seen["user-7"], seen["user-9"]
	if dm.UserID != "user-7" || dm.ChannelID != "user-7" || dm.Text != "hello json" {
		t.Fatalf("dm message: %+v", dm)
	}
	if gm.ChannelID != "group:777" || gm.UserID != "user-9" || gm.Text != "group msg" {
		t.Fatalf("group message: %+v", gm)
	}

	// 断言 PushAck：cmd_type=3，回显 push head 的 cmd/msg_id/module。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range wsSrv.decodedFrames() {
			if m.cmdType == cmdTypePushAck {
				if m.msgID != "push-json-1" || m.cmd != "cb" || m.module != "trpc.yuanbao.yuanbao_conn" {
					t.Fatalf("PushAck head: %+v", m)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no PushAck frame received")
}

func TestSendC2CAndGroupFrames(t *testing.T) {
	srv := newFakeSignServer(t, "app-key-1", "app-secret-1", nil)
	defer srv.Close()
	wsSrv := newFakeWSServer(t)
	wsSrv.handle = defaultWSHandler

	a := defaultAdapter(t, srv.URL, wsSrv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()
	waitAuthed(t, a, 5*time.Second)

	// C2C 发送。
	if err := a.SendMessage(ctx, "user-1", pluginOutgoing{Text: "hi c2c"}); err != nil {
		t.Fatalf("SendMessage c2c: %v", err)
	}
	// 群聊发送（含引用）。
	if err := a.SendMessage(ctx, "group:888", pluginOutgoing{Text: "hi group", ReplyTo: "ref-1"}); err != nil {
		t.Fatalf("SendMessage group: %v", err)
	}
	// 空文本 no-op。
	if err := a.SendMessage(ctx, "user-1", pluginOutgoing{Text: ""}); err != nil {
		t.Fatalf("empty send: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var c2c, grp *connMsg
	for time.Now().Before(deadline) && (c2c == nil || grp == nil) {
		for _, m := range wsSrv.decodedFrames() {
			if m.cmdType != cmdTypeRequest {
				continue
			}
			if m.cmd == "send_c2c_message" && c2c == nil {
				c2c = m
			}
			if m.cmd == "send_group_message" && grp == nil {
				grp = m
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c2c == nil || grp == nil {
		t.Fatal("send frames not received")
	}
	// C2C 帧：module = bizPkg 常量值，data: 2 to_account, 3 from_account, 5 msg_body。
	if c2c.module != bizPkg || c2c.msgID == "" || !strings.HasPrefix(c2c.msgID, "c2c_") {
		t.Fatalf("c2c head: %+v", c2c)
	}
	d := parseDictMust(t, c2c.data)
	if d.getString(2) != "user-1" {
		t.Errorf("c2c to_account = %q", d.getString(2))
	}
	if d.getString(3) != "bot-1" {
		t.Errorf("c2c from_account = %q", d.getString(3))
	}
	body := d.repeatedBytes(5)
	if len(body) != 1 {
		t.Fatalf("c2c msg_body len = %d", len(body))
	}
	el := decodeMsgBodyElem(body[0])
	if el.MsgType != "TIMTextElem" || el.MsgContent.Text != "hi c2c" {
		t.Fatalf("c2c elem: %+v", el)
	}
	// 群聊帧：data: 2 group_code, 7 ref_msg_id, 6 msg_body。
	if grp.module != bizPkg || !strings.HasPrefix(grp.msgID, "grp_") {
		t.Fatalf("group head: %+v", grp)
	}
	gd := parseDictMust(t, grp.data)
	if gd.getString(2) != "888" {
		t.Errorf("group group_code = %q", gd.getString(2))
	}
	if gd.getString(7) != "ref-1" {
		t.Errorf("group ref_msg_id = %q", gd.getString(7))
	}
	gbody := gd.repeatedBytes(6)
	if len(gbody) != 1 {
		t.Fatalf("group msg_body len = %d", len(gbody))
	}
	gel := decodeMsgBodyElem(gbody[0])
	if gel.MsgType != "TIMTextElem" || gel.MsgContent.Text != "hi group" {
		t.Fatalf("group elem: %+v", gel)
	}
}

func TestSendNotConnected(t *testing.T) {
	a := defaultAdapter(t, "http://unused", "ws://unused")
	if err := a.SendMessage(context.Background(), "u", pluginOutgoing{Text: "x"}); err == nil {
		t.Fatal("expected not-connected error")
	}
}

func TestHeartbeatPing(t *testing.T) {
	srv := newFakeSignServer(t, "app-key-1", "app-secret-1", nil)
	defer srv.Close()
	wsSrv := newFakeWSServer(t)
	wsSrv.handle = defaultWSHandler

	a := defaultAdapter(t, srv.URL, wsSrv.URL)
	a.hbInterval = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()
	waitAuthed(t, a, 5*time.Second)

	// 等待至少 2 个心跳帧并断言其结构。
	deadline := time.Now().Add(3 * time.Second)
	pings := 0
	for time.Now().Before(deadline) {
		pings = 0
		for _, m := range wsSrv.decodedFrames() {
			if m.cmd == cmdPing && m.cmdType == cmdTypeRequest {
				pings++
				if m.module != moduleConnAccess || m.msgID == "" || len(m.data) != 0 {
					t.Fatalf("ping head: %+v", m)
				}
			}
		}
		if pings >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pings < 2 {
		t.Fatalf("ping count = %d, want >= 2", pings)
	}
	// 心跳正常：不应发生重连（仅 1 次 auth）。
	time.Sleep(120 * time.Millisecond)
	if n := func() int { wsSrv.mu.Lock(); defer wsSrv.mu.Unlock(); return wsSrv.auths }(); n != 1 {
		t.Fatalf("auth count = %d, want 1 (no reconnect)", n)
	}
}

func TestReconnectWithBackoff(t *testing.T) {
	// 退避序列：1s, 2s, 4s, 8s, 16s, 32s, 60s（上限）。
	want := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second,
	}
	for i, w := range want {
		if got := reconnectWait(i + 1); got != w {
			t.Fatalf("reconnectWait(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestReconnectFlow(t *testing.T) {
	srv := newFakeSignServer(t, "app-key-1", "app-secret-1", nil)
	defer srv.Close()
	wsSrv := newFakeWSServer(t)
	wsSrv.handle = defaultWSHandler

	a := defaultAdapter(t, srv.URL, wsSrv.URL)
	a.hbInterval = 1 * time.Hour // 排除心跳干扰
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()
	waitAuthed(t, a, 5*time.Second)

	// 服务端断开 → 适配器应在 ~1s 后重连（第二次 sign-token + AUTH_BIND）。
	wsSrv.closeActiveConn()

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		wsSrv.mu.Lock()
		auths := wsSrv.auths
		wsSrv.mu.Unlock()
		if auths >= 2 && a.authed.Load() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("adapter did not reconnect after connection loss")
}

// ---- 入站解码单元测试（不经 WS） ----

func TestDecodeInboundFrameDualFormat(t *testing.T) {
	// protobuf。
	protoPush, err := decodeInboundPushProto(mustPushPayload("u1", "g1", "pb text"))
	if err != nil {
		t.Fatalf("decode proto push: %v", err)
	}
	if protoPush.FromAccount != "u1" || protoPush.GroupCode != "g1" || len(protoPush.MsgBody) != 1 ||
		protoPush.MsgBody[0].MsgContent.Text != "pb text" {
		t.Fatalf("proto push: %+v", protoPush)
	}
	// JSON。
	jsonPush := decodeInboundFrame([]byte(`{"from_account":"u2","group_code":"g2","msg_body":[{"msg_type":"TIMTextElem","msg_content":{"text":"json text"}}]}`))
	if jsonPush == nil || jsonPush.FromAccount != "u2" || jsonPush.MsgBody[0].MsgContent.Text != "json text" {
		t.Fatalf("json push: %+v", jsonPush)
	}
	// JSON PascalCase 兼容。
	legacy := decodeInboundFrame([]byte(`{"From_Account":"u3","GroupId":"g3","MsgBody":[{"MsgType":"TIMTextElem","MsgContent":{"Text":"legacy"}}]}`))
	if legacy == nil || legacy.FromAccount != "u3" || legacy.GroupCode != "g3" || legacy.MsgBody[0].MsgContent.Text != "legacy" {
		t.Fatalf("legacy json push: %+v", legacy)
	}
	// 无效输入。
	if decodeInboundFrame([]byte("not a frame")) != nil {
		t.Fatal("expected nil for garbage input")
	}
}

func TestExtractText(t *testing.T) {
	body := []msgBodyElem{
		textElem("hello"),
		{MsgType: "TIMImageElem"},
		{MsgType: "TIMFileElem", MsgContent: msgContent{FileName: "report.pdf"}},
		{MsgType: "TIMCustomElem", MsgContent: msgContent{Data: `{"elem_type":1002,"text":"@nick"}`}},
		{MsgType: "TIMUnknownElem"},
	}
	want := "hello [image] [file: report.pdf] @nick [TIMUnknownElem]"
	if got := extractText(body); got != want {
		t.Fatalf("extractText = %q, want %q", got, want)
	}
}

// ---- 辅助 ----

func parseDictMust(t *testing.T, data []byte) pbDict {
	t.Helper()
	d, err := parseDict(data)
	if err != nil {
		t.Fatalf("parseDict: %v", err)
	}
	return d
}

// mustPushPayload 构造 protobuf InboundMessagePush biz payload（不含 ConnMsg 包装）。
func mustPushPayload(from, group, text string) []byte {
	var p pbBuf
	p.str(2, from)
	p.str(6, group)
	p.varint(8, 100)
	p.nested(13, encodeMsgBodyElem(textElem(text)))
	return p.b
}

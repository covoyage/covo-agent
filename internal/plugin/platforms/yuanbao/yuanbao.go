// Package yuanbao implements a Tencent Yuanbao (腾讯元宝) platform adapter.
//
// 协议对齐 参考实现-agent gateway/platforms/yuanbao*.py（Python 参考实现）：
//
//   - 鉴权：POST {domain}/api/v5/robotLogic/sign-token，payload
//     {app_key, nonce, timestamp, signature}，其中
//     signature = HMAC-SHA256(nonce+timestamp+app_key+app_secret,
//     key=app_secret)；响应 {token, bot_id, source, duration} 缓存至过期。
//     （对应 REST 侧请求头 X-ID/X-Token/X-Source 的三元组经 WS AUTH_BIND
//     帧下发。）
//   - 传输：WebSocket 长连接 wss://bot-wss.yuanbao.tencent.com/wss/connection，
//     每帧为一条手写 protobuf 的 ConnMsg（见 proto.go）。
//   - 连接握手：AUTH_BIND（biz_id=ybBot, uid=bot_id, source, token,
//     device_info.instance_id）→ BIND_ACK；之后 30s ping/pong 心跳
//     （连续 2 次超时触发重连）、Push 帧 need_ack 时回 PushAck。
//   - 入站 InboundMessagePush（protobuf / JSON 双格式解码）→ OnMessage 回调；
//     出站 SendC2CMessageReq / SendGroupMessageReq（TIM 格式消息体，
//     文本用 TIMTextElem）。
//   - 断线重连指数退避（1s, 2s, 4s, … 上限 60s）；ctx 取消干净退出。
//
// 环境变量（与 参考实现 配置的对应关系）：
//
//	YUANBAO_APP_ID      app_key（参考实现: app_id）——主配置
//	YUANBAO_APP_SECRET  app_secret（参考实现: app_secret）——主配置
//	                    签名需要 app_key + app_secret 成对使用。
//	YUANBAO_API_KEY     旧配置名，保留兼容：未设置 YUANBAO_APP_ID 时视为
//	                    app_key。注意旧实现将其作为 REST bearer token
//	                    （POST {base}/bot/{botID}/messages + Bearer），该
//	                    web 端点约定已被证实与真实协议完全不符；sign-token
//	                    签名实际需要的是 app_key（参考实现 app_id），而非
//	                    bearer token。
//	YUANBAO_BOT_ID      可选；sign-token 返回的 bot_id 会覆盖/补齐
//	                    （参考实现: bot_id 可选，由 sign-token 返回）。
//	YUANBAO_WS_URL      WS 网关地址，默认
//	                    wss://bot-wss.yuanbao.tencent.com/wss/connection
//	YUANBAO_API_DOMAIN  sign-token API 域名，默认 https://bot.yuanbao.tencent.com
//	YUANBAO_ROUTE_ENV   可选路由环境（X-Route-Env 头 / AUTH_BIND env_name）
//
// channelID 约定：群聊为 "group:<group_code>"，单聊为对端用户 id（发送时也
// 接受 "direct:<account>" 前缀，对齐 参考实现 的 chat_id 路由）。入站回调中
// UserID 与单聊 channelID 均为对端账号（from_account）。
package yuanbao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName         = "yuanbao"
	defaultWSURL         = "wss://bot-wss.yuanbao.tencent.com/wss/connection"
	heartbeatInterval    = 30 * time.Second // 对齐 HEARTBEAT_INTERVAL_SECONDS
	pongTimeout          = 10 * time.Second // 对齐心跳单次等待
	heartbeatMissLimit   = 2                // 连续丢 pong 阈值，超过触发重连
	authTimeout          = 10 * time.Second // AUTH_BIND 等待 BIND_ACK
	defaultSendTimeout   = 30 * time.Second // WS 业务请求超时
	maxReconnectAttempts = 100              // 对齐 MAX_RECONNECT_ATTEMPTS
	maxBackoff           = 60 * time.Second // 退避上限
	bizID                = "ybBot"          // AUTH_BIND biz_id
	defaultSource        = "bot"            // sign-token source 缺省值
	appVersion           = "0.0.0"          // X-AppVersion / X-Bot-Version
)

// Adapter 是元宝平台适配器（WS 长连接模式）。
type Adapter struct {
	appKey    string
	appSecret string
	wsURL     string
	apiDomain string
	routeEnv  string
	logger    *slog.Logger

	// 以下字段可注入以便测试
	sign        *signManager
	botID       string
	hbInterval  time.Duration
	sendTimeout time.Duration

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	wg        sync.WaitGroup

	connMu  sync.Mutex
	conn    *wsTransport
	hbDone  chan struct{} // 每条连接的退出信号（心跳 goroutine 监听）
	pending map[string]chan *connMsg
	authed  atomic.Bool // AUTH_BIND 完成（BIND_ACK code=0）
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))
	appKey := firstNonEmpty(envTrim("YUANBAO_APP_ID"), envTrim("YUANBAO_API_KEY"))
	return &Adapter{
		appKey:      appKey,
		appSecret:   envTrim("YUANBAO_APP_SECRET"),
		botID:       envTrim("YUANBAO_BOT_ID"),
		wsURL:       firstNonEmpty(envTrim("YUANBAO_WS_URL"), defaultWSURL),
		apiDomain:   firstNonEmpty(envTrim("YUANBAO_API_DOMAIN"), defaultAPIDomain),
		routeEnv:    envTrim("YUANBAO_ROUTE_ENV"),
		logger:      logger,
		sign:        newSignManager(appVersion),
		hbInterval:  heartbeatInterval,
		sendTimeout: defaultSendTimeout,
		pending:     map[string]chan *connMsg{},
	}
}

func envTrim(name string) string { return strings.TrimSpace(os.Getenv(name)) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(vals ...uint64) uint64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	if a.appKey == "" || a.appSecret == "" {
		return errors.New("YUANBAO_APP_ID (or legacy YUANBAO_API_KEY) and YUANBAO_APP_SECRET must be set in environment")
	}
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

// Start 校验配置并启动后台连接循环（sign-token → WS → AUTH_BIND → 心跳 →
// 收发），立即返回。断线后按指数退避重连，ctx 取消时干净退出。
func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("yuanbao: validation failed: %w", err)
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("yuanbao: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.runLoop(ctx)
	}()
	return nil
}

// Stop 取消连接循环并等待退出。
func (a *Adapter) Stop() error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	cancel := a.cancel
	a.running = false
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.wg.Wait()
	a.closeConn()
	a.logger.Info("yuanbao: stopped")
	return nil
}

// runLoop 连接主循环：连接失败/断开后按指数退避重连（1s, 2s, 4s, … 上限
// 60s），重连时强制刷新 token；成功后计数归零。
func (a *Adapter) runLoop(ctx context.Context) {
	attempt := 0
	for {
		err := a.connectOnce(ctx, attempt > 0)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.logger.Warn("yuanbao: connection lost", "error", err)
		}
		attempt++
		if attempt > maxReconnectAttempts {
			a.logger.Error("yuanbao: giving up after reconnect attempts", "attempts", maxReconnectAttempts)
			return
		}
		wait := reconnectWait(attempt)
		a.logger.Info("yuanbao: reconnect scheduled", "attempt", attempt, "wait", wait.String())
		if err := sleepCtx(ctx, wait); err != nil {
			return
		}
	}
}

// reconnectWait 指数退避：1s, 2s, 4s, … 上限 60s（attempt 从 1 开始）。
func reconnectWait(attempt int) time.Duration {
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	wait := time.Duration(1<<uint(shift)) * time.Second
	if wait > maxBackoff {
		wait = maxBackoff
	}
	return wait
}

func (a *Adapter) getBotID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.botID
}

func (a *Adapter) setBotID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.botID = id
}

func (a *Adapter) currentConn() *wsTransport {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.conn
}

func (a *Adapter) closeConn() {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if a.conn != nil {
		_ = a.conn.close()
		a.conn = nil
	}
	a.authed.Store(false)
}

// registerPending 注册一个按 msg_id 等待响应的通道。
func (a *Adapter) registerPending(msgID string) chan *connMsg {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	ch := make(chan *connMsg, 1)
	a.pending[msgID] = ch
	return ch
}

func (a *Adapter) popPending(msgID string) chan *connMsg {
	if msgID == "" {
		return nil
	}
	a.connMu.Lock()
	defer a.connMu.Unlock()
	ch, ok := a.pending[msgID]
	if ok {
		delete(a.pending, msgID)
	}
	return ch
}

// failAllPending 在连接断开后唤醒所有等待者（投递 nil 表示失败）。
func (a *Adapter) failAllPending() {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	for id, ch := range a.pending {
		select {
		case ch <- nil:
		default:
		}
		delete(a.pending, id)
	}
}

// connectOnce 执行 sign-token → WS 连接 → AUTH_BIND → 心跳循环，阻塞至
// 连接断开。forceNewToken 为 true 时强制重新签名（重连场景）。
func (a *Adapter) connectOnce(ctx context.Context, forceNewToken bool) error {
	td, err := a.sign.getToken(ctx, a.appKey, a.appSecret, a.apiDomain, a.routeEnv, forceNewToken)
	if err != nil {
		return fmt.Errorf("yuanbao: sign-token: %w", err)
	}
	if td.BotID != "" {
		a.setBotID(td.BotID)
	}

	t, err := dialWS(ctx, a.wsURL)
	if err != nil {
		return err
	}
	hbDone := make(chan struct{})
	a.connMu.Lock()
	a.conn = t
	a.connMu.Unlock()

	readErr := make(chan error, 1)
	go a.readLoop(ctx, t, hbDone, readErr)

	// AUTH_BIND
	uid := a.getBotID()
	source := td.Source
	if source == "" {
		source = defaultSource
	}
	msgID := uuid.NewString()
	auth := encodeAuthBind(bizID, uid, source, td.Token, msgID, appVersion, runtime.GOOS, appVersion, a.routeEnv)
	ch := a.registerPending(msgID)
	defer a.popPending(msgID)
	if err := t.writeBinary(auth); err != nil {
		_ = t.close()
		<-hbDone
		return fmt.Errorf("yuanbao: send AUTH_BIND: %w", err)
	}
	var rsp *connMsg
	select {
	case rsp = <-ch:
	case <-time.After(authTimeout):
	case <-ctx.Done():
	}
	if rsp == nil {
		_ = t.close()
		<-hbDone
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("yuanbao: AUTH_BIND timeout waiting for BIND_ACK")
	}
	if rsp.cmdType != cmdTypeResponse || rsp.cmd != cmdAuthBind {
		_ = t.close()
		<-hbDone
		return fmt.Errorf("yuanbao: unexpected AUTH_BIND response: cmd=%s cmd_type=%d", rsp.cmd, rsp.cmdType)
	}
	code, message, connectID, err := authBindRsp(rsp.data)
	if err != nil {
		_ = t.close()
		<-hbDone
		return fmt.Errorf("yuanbao: parse BIND_ACK: %w", err)
	}
	if code != 0 {
		_ = t.close()
		<-hbDone
		return fmt.Errorf("yuanbao: AuthBindRsp error: code=%d message=%q", code, message)
	}
	a.logger.Info("yuanbao: connected", "connect_id", connectID, "bot_id", a.getBotID())
	a.authed.Store(true)

	// 启动心跳
	go a.heartbeatLoop(ctx, t, hbDone)

	// 阻塞直到连接断开
	select {
	case err := <-readErr:
		return err
	case <-ctx.Done():
		_ = t.close()
		<-hbDone
		return ctx.Err()
	}
}

// readLoop 读取 WS 帧并按 cmd_type 分发；退出时关闭 hbDone 并投递错误。
func (a *Adapter) readLoop(ctx context.Context, t *wsTransport, hbDone chan struct{}, readErr chan<- error) {
	var err error
	defer func() {
		_ = t.close()
		a.authed.Store(false)
		close(hbDone)
		a.failAllPending()
		readErr <- err
	}()
	for {
		raw, rerr := t.readMessage()
		if rerr != nil {
			err = rerr
			return
		}
		if ctx.Err() != nil {
			err = ctx.Err()
			return
		}
		msg, derr := decodeConnMsg(raw)
		if derr != nil {
			a.logger.Debug("yuanbao: failed to decode frame", "error", derr)
			continue
		}
		switch msg.cmdType {
		case cmdTypeResponse:
			// 请求回包（含 ping 的 PONG）：按 msg_id 唤醒等待者。
			if ch := a.popPending(msg.msgID); ch != nil {
				select {
				case ch <- msg:
				default:
				}
			}
		case cmdTypePush:
			// 下行推送：need_ack 时回 PushAck。
			if msg.needAck {
				if aerr := t.writeBinary(encodePushAck(msg)); aerr != nil {
					a.logger.Debug("yuanbao: failed to send PushAck", "error", aerr)
				}
			}
			if len(msg.data) > 0 {
				a.dispatchInbound(msg.data)
			}
		default:
			a.logger.Debug("yuanbao: ignoring frame", "cmd_type", msg.cmdType, "cmd", msg.cmd)
		}
	}
}

// heartbeatLoop 每 hbInterval 发送 PING；连续 heartbeatMissLimit 次超时关闭
// 连接（触发重连）。
func (a *Adapter) heartbeatLoop(ctx context.Context, t *wsTransport, hbDone chan struct{}) {
	misses := 0
	ticker := time.NewTicker(a.hbInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hbDone:
			return
		case <-ticker.C:
		}
		msgID := uuid.NewString()
		ch := a.registerPending(msgID)
		if err := t.writeBinary(encodePing(msgID)); err != nil {
			a.popPending(msgID)
			a.logger.Debug("yuanbao: heartbeat send failed", "error", err)
			return
		}
		select {
		case <-ch:
			misses = 0
		case <-time.After(pongTimeout):
			a.popPending(msgID)
			misses++
			a.logger.Warn("yuanbao: PONG timeout", "misses", misses, "limit", heartbeatMissLimit)
			if misses >= heartbeatMissLimit {
				a.logger.Warn("yuanbao: heartbeat threshold exceeded, triggering reconnect")
				_ = t.close()
				return
			}
		case <-ctx.Done():
			a.popPending(msgID)
			return
		case <-hbDone:
			a.popPending(msgID)
			return
		}
	}
}

// dispatchInbound 解码入站 push（protobuf/JSON 双格式）并投递 OnMessage。
func (a *Adapter) dispatchInbound(data []byte) {
	push := decodeInboundFrame(data)
	if push == nil || !push.validPush() {
		a.logger.Debug("yuanbao: push decoded but no valid message")
		return
	}
	// 撤回回调等无消息体的帧不分发。
	if len(push.MsgBody) == 0 {
		return
	}
	msg := plugin.IncomingMessage{
		Platform:  PlatformName,
		Text:      extractText(push.MsgBody),
		Timestamp: time.Now(),
		Raw:       push,
	}
	if push.GroupCode != "" {
		msg.ChannelID = "group:" + push.GroupCode
	} else {
		msg.ChannelID = push.FromAccount
	}
	msg.UserID = push.FromAccount
	msg.UserName = firstNonEmpty(push.SenderNickname, push.FromAccount)

	a.mu.Lock()
	cb := a.onMessage
	a.mu.Unlock()
	if cb == nil {
		return
	}
	go cb(msg)
}

// extractText 从 MsgBody 提取纯文本（对齐 参考实现 ExtractContentMiddleware
// 的简化版：文本元素原样保留，媒体/未知元素输出占位符，空格连接）。
func extractText(body []msgBodyElem) string {
	var parts []string
	for _, el := range body {
		switch el.MsgType {
		case "TIMTextElem":
			if el.MsgContent.Text != "" {
				parts = append(parts, el.MsgContent.Text)
			}
		case "TIMImageElem":
			parts = append(parts, "[image]")
		case "TIMSoundElem":
			parts = append(parts, "[voice]")
		case "TIMVideoFileElem":
			parts = append(parts, "[video]")
		case "TIMFileElem":
			name := firstNonEmpty(el.MsgContent.FileName, "file")
			parts = append(parts, "[file: "+name+"]")
		case "TIMFaceElem":
			parts = append(parts, "[emoji]")
		case "TIMCustomElem":
			parts = append(parts, customElemText(el.MsgContent.Data))
		default:
			if el.MsgType != "" {
				parts = append(parts, "["+el.MsgType+"]")
			}
		}
	}
	return strings.Join(parts, " ")
}

// customElemText 提取自定义元素文本：elem_type 1002 (@提及) / 1009 (聊天记录)
// 携带 text 字段；其余 JSON 原样透传，解析失败返回占位符。
func customElemText(data string) string {
	if data == "" {
		return "[unsupported]"
	}
	var custom struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(data), &custom); err != nil || custom.Text == "" {
		return data
	}
	return custom.Text
}

// Send 发送纯文本。
func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

// SendMessage 通过 WS 发送消息：channelID 为 "group:<code>"（群聊，
// SendGroupMessageReq）或用户 id / "direct:<account>"（C2C，
// SendC2CMessageReq）。文本以 TIMTextElem 封装。
func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}
	if err := a.Validate(); err != nil {
		return fmt.Errorf("yuanbao: %w", err)
	}
	t := a.currentConn()
	if t == nil {
		return errors.New("yuanbao: not connected")
	}
	body := []msgBodyElem{textElem(msg.Text)}
	botID := a.getBotID()
	var reqID string
	var frame []byte
	if strings.HasPrefix(channelID, "group:") {
		groupCode := strings.TrimPrefix(channelID, "group:")
		reqID = fmt.Sprintf("grp_%d", nextSeqNo())
		frame = encodeSendGroupMessage(groupCode, body, botID, reqID, msg.ReplyTo)
	} else {
		to := strings.TrimPrefix(channelID, "direct:")
		reqID = fmt.Sprintf("c2c_%d", nextSeqNo())
		frame = encodeSendC2CMessage(to, body, botID, reqID, "")
	}
	return a.sendBizRequest(ctx, t, reqID, frame)
}

// sendBizRequest 发送业务请求并等待响应（30s 超时；head.status != 0 视为失败）。
func (a *Adapter) sendBizRequest(ctx context.Context, t *wsTransport, reqID string, frame []byte) error {
	ch := a.registerPending(reqID)
	defer a.popPending(reqID)
	if err := t.writeBinary(frame); err != nil {
		return fmt.Errorf("yuanbao: send: %w", err)
	}
	timeout := a.sendTimeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case rsp := <-ch:
		if rsp == nil {
			return errors.New("yuanbao: connection lost while awaiting response")
		}
		if rsp.status != 0 {
			return fmt.Errorf("yuanbao: send failed: status=%d", rsp.status)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("yuanbao: request timeout after %s", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

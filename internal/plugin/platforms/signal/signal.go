package signal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName       = "signal"
	defaultPollSeconds = 3
	responseTimeout    = 30 * time.Second
	maxBackoff         = 30 * time.Second
)

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("code=%d message=%s", e.Code, e.Message)
}

// rpcFrame is one line of the signal-cli JSON-RPC socket protocol: either a
// request response (id + result/error) or a server-pushed event (method +
// params).
type rpcFrame struct {
	ID      *int64          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	JSONRPC string          `json:"jsonrpc,omitempty"`
}

type Adapter struct {
	socketAddr  string
	account     string
	pollSeconds int
	logger      *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)

	connMu  sync.Mutex
	conn    net.Conn
	nextID  int64
	writeMu sync.Mutex
	pending map[int64]chan rpcFrame
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	pollSeconds := defaultPollSeconds
	if v := strings.TrimSpace(os.Getenv("SIGNAL_POLL_SECONDS")); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &pollSeconds); err != nil || n != 1 || pollSeconds < 1 {
			pollSeconds = defaultPollSeconds
		}
	}

	return &Adapter{
		socketAddr:  strings.TrimSpace(os.Getenv("SIGNAL_CLI_SOCKET")),
		account:     strings.TrimSpace(os.Getenv("SIGNAL_ACCOUNT")),
		pollSeconds: pollSeconds,
		logger:      logger,
		pending:     make(map[int64]chan rpcFrame),
	}
}

func (a *Adapter) Name() string                      { return PlatformName }
func (a *Adapter) GetName() string                   { return PlatformName }
func (a *Adapter) GetID() string                     { return PlatformName }
func (a *Adapter) Category() plugin.Category         { return plugin.CategoryPlatform }
func (a *Adapter) GetCategory() plugin.Category      { return plugin.CategoryPlatform }
func (a *Adapter) ID() string                        { return PlatformName }
func (a *Adapter) Platform() plugin.PlatformProvider { return a }

func (a *Adapter) Validate() error {
	if a.account == "" {
		return fmt.Errorf("SIGNAL_ACCOUNT not set in environment")
	}
	if a.socketAddr == "" {
		return fmt.Errorf("SIGNAL_CLI_SOCKET not set in environment")
	}
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("signal: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("signal: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	// Fail fast with actionable guidance when signal-cli is not running.
	if err := a.handshake(ctx); err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("signal: cannot connect to signal-cli at %s: %w (start it with: signal-cli -u %s --socket <path>, or unix:/path/to/socket for unix sockets)", a.socketAddr, err, a.account)
	}

	go a.readLoop(ctx)
	a.logger.Info("signal: connected to signal-cli", "socket", a.socketAddr, "account", a.account)
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.running = false
	a.closeConn()
	a.logger.Info("signal: stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

// groupChannelPrefix marks inbound group channel IDs so outbound sends can
// route them via the JSON-RPC "groupId" parameter instead of "recipient".
const groupChannelPrefix = "group."

// groupIDFromChannel strips the group marker ("group." or "group:") from a
// channel ID. ok is false for plain recipient channel IDs (phone numbers,
// usernames).
func groupIDFromChannel(channelID string) (groupID string, ok bool) {
	if g, found := strings.CutPrefix(channelID, groupChannelPrefix); found {
		return g, true
	}
	if g, found := strings.CutPrefix(channelID, "group:"); found {
		return g, true
	}
	return channelID, false
}

// SendMessage sends a text message to channelID (a phone number, a username,
// or a "group."-prefixed group ID) via the signal-cli JSON-RPC "send" method.
// signal-cli handles long text natively, so no chunking is needed.
func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}
	params := map[string]any{
		"account": a.account,
		"message": msg.Text,
	}
	// Group sends must target the group, not a recipient: signal-cli rejects
	// requests carrying both (or a groupId in recipient).
	if groupID, isGroup := groupIDFromChannel(channelID); isGroup {
		params["groupId"] = groupID
	} else {
		params["recipient"] = []string{channelID}
	}
	result, err := a.call(ctx, "send", params)
	if err != nil {
		return fmt.Errorf("signal: send to %s: %w", channelID, err)
	}
	a.logger.Debug("signal: sent", "channel", channelID, "result", string(result))
	return nil
}

// dial parses SIGNAL_CLI_SOCKET ("unix:/path/to/socket" or "host:port") and
// opens the connection.
func (a *Adapter) dial() (net.Conn, error) {
	addr := a.socketAddr
	if strings.HasPrefix(addr, "unix:") {
		return net.DialTimeout("unix", strings.TrimPrefix(addr, "unix:"), 5*time.Second)
	}
	return net.DialTimeout("tcp", addr, 5*time.Second)
}

// handshake dials signal-cli, sends the "subscribe" request synchronously
// (before any read loop exists to feed pending channels), and installs the
// connection as the persistent one.
func (a *Adapter) handshake(ctx context.Context) error {
	conn, err := a.dial()
	if err != nil {
		return err
	}

	a.connMu.Lock()
	a.nextID++
	id := a.nextID
	a.connMu.Unlock()

	if err := a.writeRequest(conn, id, "subscribe", map[string]string{"account": a.account}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("subscribe: %w", err)
	}
	if _, err := a.readResponse(ctx, conn, id); err != nil {
		_ = conn.Close()
		return fmt.Errorf("subscribe %s: %w", a.account, err)
	}

	a.setConn(conn)
	return nil
}

func (a *Adapter) currentConn() net.Conn {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.conn
}

func (a *Adapter) setConn(conn net.Conn) {
	a.connMu.Lock()
	a.conn = conn
	a.connMu.Unlock()
}

func (a *Adapter) closeConn() {
	a.connMu.Lock()
	conn := a.conn
	a.conn = nil
	pending := a.pending
	a.pending = make(map[int64]chan rpcFrame)
	a.connMu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
	// Unblock any waiters.
	for _, ch := range pending {
		close(ch)
	}
}

func (a *Adapter) dropConn(conn net.Conn) {
	a.connMu.Lock()
	if a.conn == conn {
		a.conn = nil
	}
	pending := a.pending
	a.pending = make(map[int64]chan rpcFrame)
	a.connMu.Unlock()

	_ = conn.Close()
	for _, ch := range pending {
		close(ch)
	}
}

// call sends a JSON-RPC request and waits for the response with the matching
// id. It reuses the persistent connection while running, otherwise it opens
// a fresh one-shot connection (so SendMessage works before Start/after Stop).
func (a *Adapter) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	a.connMu.Lock()
	a.nextID++
	id := a.nextID
	conn := a.conn
	ch := make(chan rpcFrame, 1)
	if conn != nil {
		a.pending[id] = ch
	}
	a.connMu.Unlock()

	oneshot := conn == nil
	if oneshot {
		var err error
		conn, err = a.dial()
		if err != nil {
			return nil, fmt.Errorf("cannot connect to signal-cli at %s: %w (start it with: signal-cli -u %s --socket <path>)", a.socketAddr, err, a.account)
		}
		defer conn.Close()
	}

	if err := a.writeRequest(conn, id, method, params); err != nil {
		if !oneshot {
			a.dropConn(conn)
		}
		return nil, fmt.Errorf("write request: %w", err)
	}

	if oneshot {
		return a.readResponse(ctx, conn, id)
	}

	select {
	case frame, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("connection closed while waiting for response")
		}
		if frame.Error != nil {
			return nil, fmt.Errorf("json-rpc error: %w", frame.Error)
		}
		return frame.Result, nil
	case <-time.After(responseTimeout):
		a.connMu.Lock()
		delete(a.pending, id)
		a.connMu.Unlock()
		return nil, fmt.Errorf("timed out waiting for %s response", method)
	case <-ctx.Done():
		a.connMu.Lock()
		delete(a.pending, id)
		a.connMu.Unlock()
		return nil, ctx.Err()
	}
}

func (a *Adapter) writeRequest(conn net.Conn, id int64, method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// readResponse reads newline-delimited frames from a one-shot connection
// until the response with the matching id arrives; server events encountered
// along the way are still dispatched.
func (a *Adapter) readResponse(ctx context.Context, conn net.Conn, id int64) (json.RawMessage, error) {
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		var frame rpcFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		if frame.ID != nil && *frame.ID == id {
			if frame.Error != nil {
				return nil, fmt.Errorf("json-rpc error: %w", frame.Error)
			}
			return frame.Result, nil
		}
		// Not our response (e.g. a pushed event): handle it.
		a.handleFrame(frame)
	}
}

// readLoop maintains the persistent receive connection: it consumes frames,
// dispatches events to the callback, delivers responses to pending callers,
// and reconnects with exponential backoff when the connection drops.
func (a *Adapter) readLoop(ctx context.Context) {
	backoff := time.Duration(a.pollSeconds) * time.Second
	if backoff < time.Second {
		backoff = time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn := a.currentConn()
		if conn == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if err := a.handshake(ctx); err != nil {
				a.logger.Warn("signal: reconnect failed", "socket", a.socketAddr, "error", err)
				backoff = min(backoff*2, maxBackoff)
				continue
			}
			backoff = time.Duration(a.pollSeconds) * time.Second
			a.logger.Info("signal: reconnected to signal-cli")
			conn = a.currentConn()
		}

		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
				a.logger.Warn("signal: connection lost, will reconnect", "error", err)
				a.dropConn(conn)
				break
			}
			a.handleLine(line)
		}
	}
}

func (a *Adapter) handleLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var frame rpcFrame
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		a.logger.Warn("signal: invalid frame", "error", err)
		return
	}
	a.handleFrame(frame)
}

func (a *Adapter) handleFrame(frame rpcFrame) {
	switch frame.Method {
	case "receive", "subscription":
		a.handleEvent(frame.Params)
		return
	}

	if frame.ID != nil {
		a.connMu.Lock()
		ch, ok := a.pending[*frame.ID]
		delete(a.pending, *frame.ID)
		a.connMu.Unlock()
		if ok {
			ch <- frame
		}
	}
}

type signalEnvelopeParams struct {
	Envelope struct {
		Source       string `json:"source"`
		SourceNumber string `json:"sourceNumber"`
		SourceName   string `json:"sourceName"`
		Timestamp    int64  `json:"timestamp"`
		TimestampMs  int64  `json:"timestampMs"`
		DataMessage  *struct {
			Message   string `json:"message"`
			GroupInfo *struct {
				GroupID string `json:"groupId"`
			} `json:"groupInfo"`
		} `json:"dataMessage"`
	} `json:"envelope"`
}

func (a *Adapter) handleEvent(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var params signalEnvelopeParams
	if err := json.Unmarshal(raw, &params); err != nil {
		a.logger.Warn("signal: invalid event params", "error", err)
		return
	}

	dm := params.Envelope.DataMessage
	if dm == nil || dm.Message == "" {
		return
	}

	source := params.Envelope.Source
	if source == "" {
		source = params.Envelope.SourceNumber
	}

	channelID := source
	userName := params.Envelope.SourceName
	if dm.GroupInfo != nil && dm.GroupInfo.GroupID != "" {
		// Mark the channel as a group so replies route via groupId.
		channelID = groupChannelPrefix + dm.GroupInfo.GroupID
	}

	var ts time.Time
	if params.Envelope.TimestampMs > 0 {
		ts = time.UnixMilli(params.Envelope.TimestampMs)
	} else if params.Envelope.Timestamp > 0 {
		ts = time.Unix(params.Envelope.Timestamp, 0)
	} else {
		ts = time.Now()
	}

	incoming := plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: channelID,
		UserID:    source,
		UserName:  userName,
		Text:      dm.Message,
		Timestamp: ts,
		Raw:       params,
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(incoming)
	}
}

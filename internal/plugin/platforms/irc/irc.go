package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "irc"

	defaultHost      = "irc.libera.chat"
	defaultTLSPort   = "6697"
	defaultPlainPort = "6667"

	// RFC 2812 caps a single IRC line at 512 bytes including the trailing
	// CRLF; the PRIVMSG payload budget is derived per target from that.
	maxLineBytes  = 512
	maxChunkChars = 350 // conservative character cap on top of the byte budget

	maxNickAttempts = 3 // 433/436 retries with a "_"-suffixed nick
	writeTimeout    = 10 * time.Second
	dialTimeout     = 15 * time.Second
	initialBackoff  = 2 * time.Second
	maxBackoff      = 60 * time.Second
	connCloseGrace  = 500 * time.Millisecond
)

type Adapter struct {
	server   string // host:port
	nick     string
	password string
	channels []string
	useTLS   bool
	logger   *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	conn      net.Conn

	writeMu sync.Mutex
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	useTLS := true
	if v := strings.TrimSpace(os.Getenv("IRC_TLS")); v != "" {
		useTLS = !(strings.EqualFold(v, "false") || v == "0" || strings.EqualFold(v, "no"))
	}

	server := strings.TrimSpace(os.Getenv("IRC_SERVER"))
	port := strings.TrimSpace(os.Getenv("IRC_PORT"))
	if port == "" {
		if useTLS {
			port = defaultTLSPort
		} else {
			port = defaultPlainPort
		}
	}
	if server == "" {
		server = net.JoinHostPort(defaultHost, port)
	} else if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, port)
	}

	nick := strings.TrimSpace(os.Getenv("IRC_NICK"))
	if nick == "" {
		nick = "covo-bot"
	}
	channelsStr := strings.TrimSpace(os.Getenv("IRC_CHANNELS"))
	var channels []string
	if channelsStr != "" {
		for _, ch := range strings.Split(channelsStr, ",") {
			ch = strings.TrimSpace(ch)
			if ch != "" {
				channels = append(channels, ch)
			}
		}
	}

	return &Adapter{
		server:   server,
		nick:     nick,
		password: strings.TrimSpace(os.Getenv("IRC_PASSWORD")),
		channels: channels,
		useTLS:   useTLS,
		logger:   logger,
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
	if a.server == "" {
		return fmt.Errorf("IRC_SERVER not set in environment")
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
		return fmt.Errorf("irc: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("irc: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	go a.connectLoop(ctx)

	a.logger.Info("irc: adapter started", "server", a.server, "nick", a.nick)
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	conn := a.conn
	cancel := a.cancel
	a.running = false
	a.mu.Unlock()

	// Best-effort graceful QUIT before tearing the socket down.
	if conn != nil {
		_ = a.writeLine(conn, "QUIT :covo-agent shutting down")
	}
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	a.mu.Lock()
	a.conn = nil
	a.mu.Unlock()

	a.logger.Info("irc: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if channelID == "" {
		return fmt.Errorf("irc: empty channel ID")
	}
	if msg.Text == "" {
		return nil
	}
	conn := a.currentConn()
	if conn == nil {
		return fmt.Errorf("irc: not connected")
	}

	text := sanitizeIrcOutboundText(msg.Text)
	if text == "" {
		return nil
	}

	chunks := splitMessage(text, privmsgPayloadBudget(channelID), maxChunkChars)
	for _, chunk := range chunks {
		if err := a.writeLine(conn, "PRIVMSG %s :%s", channelID, chunk); err != nil {
			return fmt.Errorf("irc: send PRIVMSG to %s: %w", channelID, err)
		}
	}
	return nil
}

// sanitizeIrcOutboundText makes text safe for a PRIVMSG payload: CR/LF are
// replaced with spaces and C0 control characters, DEL and C1 (0x80-0x9F) are
// stripped, mirroring the reference implementation's sanitizeIrcOutboundText.
func sanitizeIrcOutboundText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r <= 0x1f || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// privmsgPayloadBudget returns the maximum PRIVMSG payload size in bytes for
// a target: the 512-byte IRC line budget minus "PRIVMSG <target> :" and the
// trailing CRLF.
func privmsgPayloadBudget(target string) int {
	overhead := len("PRIVMSG ") + len(target) + len(" :") + len("\r\n")
	budget := maxLineBytes - overhead
	if budget < 1 {
		budget = 1 // degenerate over-long target; the server will reject the line
	}
	return budget
}

// currentConn returns the active connection, or nil.
func (a *Adapter) currentConn() net.Conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn
}

// writeLine writes a single IRC line to conn under the write lock.
func (a *Adapter) writeLine(conn net.Conn, format string, args ...any) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := fmt.Fprintf(conn, format+"\r\n", args...); err != nil {
		return err
	}
	return nil
}

// connectLoop dials the server, runs the session and reconnects with
// exponential backoff until ctx is cancelled.
func (a *Adapter) connectLoop(ctx context.Context) {
	backoff := initialBackoff
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		err := a.runSession(ctx)
		if ctx.Err() != nil {
			return
		}
		a.logger.Warn("irc: connection lost, reconnecting", "server", a.server, "error", err)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// runSession dials, registers (NICK/USER), joins channels after 001 and
// serves the read loop until an error or ctx cancellation.
func (a *Adapter) runSession(ctx context.Context) error {
	conn, err := a.dial(ctx)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.conn == conn {
			a.conn = nil
		}
		a.mu.Unlock()
		_ = conn.Close()
	}()

	// Close the socket when ctx is cancelled so the blocking read unblocks.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if a.password != "" {
		if err := a.writeLine(conn, "PASS %s", a.password); err != nil {
			return fmt.Errorf("irc: send PASS: %w", err)
		}
	}
	currentNick := a.nick
	if err := a.writeLine(conn, "NICK %s", currentNick); err != nil {
		return fmt.Errorf("irc: send NICK: %w", err)
	}
	if err := a.writeLine(conn, "USER %s 0 * :covo-agent", a.nick); err != nil {
		return fmt.Errorf("irc: send USER: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	welcomed := false
	nickAttempts := 0
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" {
			continue
		}

		prefix, cmd, params := parseIRCLine(line)
		switch cmd {
		case "PING":
			// PONG with the same token/payload.
			if err := a.writeLine(conn, "PONG %s", params); err != nil {
				return fmt.Errorf("irc: send PONG: %w", err)
			}
		case "433", "436":
			// Nickname collision during registration: retry with a
			// "_"-suffixed nick and keep waiting for 001.
			if !welcomed && nickAttempts < maxNickAttempts {
				nickAttempts++
				currentNick += "_"
				a.logger.Warn("irc: nickname in use, retrying", "nick", currentNick)
				if err := a.writeLine(conn, "NICK %s", currentNick); err != nil {
					return fmt.Errorf("irc: send NICK retry: %w", err)
				}
				continue
			}
			return fmt.Errorf("irc: nickname registration failed (%s): %s", cmd, params)
		case "001":
			if !welcomed {
				welcomed = true
				for _, ch := range a.channels {
					if err := a.writeLine(conn, "JOIN %s", ch); err != nil {
						return fmt.Errorf("irc: JOIN %s: %w", ch, err)
					}
				}
			}
		case "PRIVMSG":
			a.handlePrivmsg(prefix, params)
		case "ERROR":
			return fmt.Errorf("irc: server error: %s", params)
		default:
			// Ignore other commands numerics/modes/...
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("irc: read loop: %w", err)
	}
	return fmt.Errorf("irc: connection closed by server")
}

// dial establishes a TCP (optionally TLS) connection with ctx support.
func (a *Adapter) dial(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", a.server)
	if err != nil {
		return nil, fmt.Errorf("irc: dial %s: %w", a.server, err)
	}
	if !a.useTLS {
		return raw, nil
	}
	host, _, _ := net.SplitHostPort(a.server)
	tlsConn := tls.Client(raw, &tls.Config{ServerName: host})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("irc: tls handshake with %s: %w", a.server, err)
	}
	return tlsConn, nil
}

// handlePrivmsg forwards a channel or private message to the callback.
func (a *Adapter) handlePrivmsg(prefix, params string) {
	if prefix == "" || params == "" {
		return
	}
	sender := prefix
	if i := strings.IndexByte(prefix, '!'); i >= 0 {
		sender = prefix[:i]
	}

	target := params
	text := ""
	if i := strings.Index(params, " :"); i >= 0 {
		target = params[:i]
		text = params[i+2:]
	} else if i := strings.IndexByte(params, ' '); i >= 0 {
		target = params[:i]
	}
	if text == "" {
		return
	}

	channelID := sender // private message: reply to the sender's nick
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		channelID = target // channel message
	}

	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback == nil {
		return
	}
	callback(plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: channelID,
		UserID:    sender,
		UserName:  sender,
		Text:      text,
		Timestamp: time.Now(),
	})
}

// parseIRCLine splits an IRC line into prefix, command and params
// (params includes the trailing text after " :").
func parseIRCLine(line string) (prefix, cmd, params string) {
	rest := line
	if strings.HasPrefix(rest, ":") {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return "", strings.TrimSpace(rest[1:]), ""
		}
		prefix = rest[1:i]
		rest = rest[i+1:]
	}
	i := strings.IndexByte(rest, ' ')
	if i < 0 {
		return prefix, rest, ""
	}
	cmd = rest[:i]
	params = rest[i+1:]
	return prefix, cmd, params
}

// splitMessage splits text into chunks of at most maxBytes AND maxChars
// (characters), never breaking a multi-byte UTF-8 rune across chunks.
func splitMessage(text string, maxBytes, maxChars int) []string {
	if maxBytes < 1 {
		maxBytes = 1
	}
	if maxChars < 1 {
		maxChars = 1
	}
	if len(text) <= maxBytes && utf8.RuneCountInString(text) <= maxChars {
		return []string{text}
	}
	var chunks []string
	var cur strings.Builder
	curLen := 0
	curChars := 0
	for _, r := range text {
		size := len(string(r))
		if (curLen+size > maxBytes || curChars+1 > maxChars) && curLen > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
			curLen = 0
			curChars = 0
		}
		cur.WriteRune(r)
		curLen += size
		curChars++
	}
	if curLen > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}

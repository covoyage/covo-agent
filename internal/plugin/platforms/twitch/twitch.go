package twitch

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

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "twitch"

	defaultServer   = "irc.chat.twitch.tv:6697"
	maxChunkBytes   = 450 // Twitch caps outgoing PRIVMSG at ~500 bytes including overhead
	dialTimeout     = 15 * time.Second
	writeTimeout    = 10 * time.Second
	initialBackoff  = 2 * time.Second
	maxBackoff      = 60 * time.Second
)

type Adapter struct {
	username   string // Twitch login name
	oauthToken string // token without the "oauth:" prefix
	channels   []string
	logger     *slog.Logger

	// Test hooks: serverAddr/skipTLS let unit tests point the adapter at a
	// local fake TCP server instead of the real Twitch IRC gateway.
	serverAddr string
	skipTLS    bool

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)
	conn      net.Conn
	selfNick  string // lowercase own login, set when NICK is registered

	writeMu sync.Mutex
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	token := strings.TrimSpace(os.Getenv("TWITCH_OAUTH_TOKEN"))
	token = strings.TrimPrefix(token, "oauth:")

	nick := strings.TrimSpace(os.Getenv("TWITCH_NICK"))
	if nick == "" {
		nick = strings.TrimSpace(os.Getenv("TWITCH_USERNAME"))
	}

	channelsStr := strings.TrimSpace(os.Getenv("TWITCH_CHANNELS"))
	if channelsStr == "" {
		channelsStr = strings.TrimSpace(os.Getenv("TWITCH_CHANNEL"))
	}
	if channelsStr == "" {
		channelsStr = nick
	}
	var channels []string
	for _, ch := range strings.Split(channelsStr, ",") {
		ch = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ch), "#")))
		if ch != "" {
			channels = append(channels, ch)
		}
	}

	return &Adapter{
		username:   nick,
		oauthToken: token,
		channels:   channels,
		serverAddr: defaultServer,
		logger:     logger,
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
	if a.username == "" {
		return fmt.Errorf("TWITCH_NICK not set in environment")
	}
	if a.oauthToken == "" {
		return fmt.Errorf("TWITCH_OAUTH_TOKEN not set in environment")
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
		return fmt.Errorf("twitch: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("twitch: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	go a.connectLoop(ctx)

	a.logger.Info("twitch: adapter started", "server", a.serverAddr, "channels", a.channels)
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

	if conn != nil {
		_ = a.writeLine(conn, "QUIT") // best-effort; Twitch does not require a QUIT reason
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

	a.logger.Info("twitch: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}
	if a.oauthToken == "" {
		return fmt.Errorf("twitch: OAuth token not configured")
	}
	if channelID == "" {
		return fmt.Errorf("twitch: empty channel ID")
	}
	conn := a.currentConn()
	if conn == nil {
		return fmt.Errorf("twitch: not connected")
	}

	channel := strings.ToLower(strings.TrimPrefix(channelID, "#"))
	text := strings.ReplaceAll(msg.Text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")

	for _, chunk := range splitMessage(text, maxChunkBytes) {
		if err := a.writeLine(conn, "PRIVMSG #%s :%s", channel, chunk); err != nil {
			return fmt.Errorf("twitch: send PRIVMSG to #%s: %w", channel, err)
		}
	}
	return nil
}

// currentConn returns the active connection, or nil.
func (a *Adapter) currentConn() net.Conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn
}

// writeLine writes a single IRC line under the write lock.
func (a *Adapter) writeLine(conn net.Conn, format string, args ...any) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := fmt.Fprintf(conn, format+"\r\n", args...); err != nil {
		return err
	}
	return nil
}

// connectLoop keeps a live connection, reconnecting with exponential backoff.
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
		a.logger.Warn("twitch: connection lost, reconnecting", "server", a.serverAddr, "error", err)

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

// runSession dials the Twitch IRC gateway, performs the handshake
// (CAP REQ / PASS / NICK), joins channels after 001 and serves the read loop.
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

	// Unblock the read loop when ctx is cancelled.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if err := a.writeLine(conn, "CAP REQ twitch.tv/tags twitch.tv/commands"); err != nil {
		return fmt.Errorf("twitch: CAP REQ: %w", err)
	}
	if err := a.writeLine(conn, "PASS oauth:%s", a.oauthToken); err != nil {
		return fmt.Errorf("twitch: PASS: %w", err)
	}
	if err := a.writeLine(conn, "NICK %s", a.username); err != nil {
		return fmt.Errorf("twitch: NICK: %w", err)
	}
	// Remember our own login so self-echoes (the bot seeing its own PRIVMSG)
	// can be filtered out and not re-delivered as incoming messages.
	a.mu.Lock()
	a.selfNick = strings.ToLower(a.username)
	a.mu.Unlock()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	welcomed := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" {
			continue
		}

		tags, prefix, cmd, params := parseTwitchLine(line)
		switch cmd {
		case "PING":
			// Twitch sends "PING :tmi.twitch.tv" — echo the payload back.
			if err := a.writeLine(conn, "PONG %s", params); err != nil {
				return fmt.Errorf("twitch: PONG: %w", err)
			}
		case "001":
			if !welcomed {
				welcomed = true
				for _, ch := range a.channels {
					if err := a.writeLine(conn, "JOIN #%s", ch); err != nil {
						return fmt.Errorf("twitch: JOIN #%s: %w", ch, err)
					}
				}
			}
		case "PRIVMSG":
			a.handlePrivmsg(tags, prefix, params)
		case "RECONNECT":
			// Twitch asks us to reconnect; drop the session so the
			// connect loop re-establishes it.
			return fmt.Errorf("twitch: server requested RECONNECT")
		default:
			// Ignore user notices, PINGs from services, etc.
		}
		_ = prefix
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("twitch: read loop: %w", err)
	}
	return fmt.Errorf("twitch: connection closed by server")
}

// dial establishes the TCP/TLS connection to the gateway.
func (a *Adapter) dial(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", a.serverAddr)
	if err != nil {
		return nil, fmt.Errorf("twitch: dial %s: %w", a.serverAddr, err)
	}
	if a.skipTLS {
		return raw, nil
	}
	host, _, _ := net.SplitHostPort(a.serverAddr)
	tlsConn := tls.Client(raw, &tls.Config{ServerName: host})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("twitch: tls handshake with %s: %w", a.serverAddr, err)
	}
	return tlsConn, nil
}

// handlePrivmsg parses a tagged PRIVMSG and forwards it to the callback.
func (a *Adapter) handlePrivmsg(tags map[string]string, prefix, params string) {
	channel := params
	text := ""
	if i := strings.Index(params, " :"); i >= 0 {
		channel = params[:i]
		text = params[i+2:]
	}
	if text == "" {
		return
	}

	// login: from the prefix (nick!login@host) or the tags.
	login := prefix
	if i := strings.IndexByte(prefix, '!'); i >= 0 {
		login = prefix[:i]
	}
	displayName := tags["display-name"]
	if displayName == "" {
		displayName = tags["login"]
	}
	if displayName == "" {
		displayName = login
	}
	userID := tags["user-id"]
	if userID == "" {
		userID = login
	}

	// Echo filter: skip messages the bot sent itself (matched against the
	// lowercase own nick via tags login / display-name or the prefix nick),
	// otherwise our own sends would loop back as incoming messages.
	a.mu.Lock()
	selfNick := a.selfNick
	callback := a.onMessage
	a.mu.Unlock()
	if selfNick != "" {
		if strings.ToLower(tags["login"]) == selfNick ||
			strings.ToLower(tags["display-name"]) == selfNick ||
			strings.ToLower(login) == selfNick {
			a.logger.Debug("twitch: skipping self echo", "nick", selfNick)
			return
		}
	}
	if callback == nil {
		return
	}
	callback(plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: channel, // already "#name"
		UserID:    userID,
		UserName:  displayName,
		Text:      text,
		Timestamp: time.Now(),
	})
}

// parseTwitchLine splits an IRCv3 line with optional leading @tags into its
// components. params keeps the trailing text after " :".
func parseTwitchLine(line string) (tags map[string]string, prefix, cmd, params string) {
	rest := line
	if strings.HasPrefix(rest, "@") {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return nil, "", "", ""
		}
		tags = parseTags(rest[1:i])
		rest = rest[i+1:]
	}
	if strings.HasPrefix(rest, ":") {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return tags, "", "", ""
		}
		prefix = rest[1:i]
		rest = rest[i+1:]
	}
	i := strings.IndexByte(rest, ' ')
	if i < 0 {
		return tags, prefix, rest, ""
	}
	return tags, prefix, rest[:i], rest[i+1:]
}

// parseTags parses an IRCv3 tag list ("k1=v1;k2=v2") into a map, unescaping
// the few escapes Twitch uses (\s for space).
func parseTags(s string) map[string]string {
	tags := make(map[string]string)
	for _, kv := range strings.Split(s, ";") {
		k, v, _ := strings.Cut(kv, "=")
		v = strings.ReplaceAll(v, "\\s", " ")
		tags[k] = v
	}
	return tags
}

// splitMessage splits text into chunks of at most maxBytes without breaking
// multi-byte UTF-8 runes.
func splitMessage(text string, maxBytes int) []string {
	if len(text) <= maxBytes {
		return []string{text}
	}
	var chunks []string
	var cur strings.Builder
	curLen := 0
	for _, r := range text {
		size := len(string(r))
		if curLen+size > maxBytes && curLen > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
			curLen = 0
		}
		cur.WriteRune(r)
		curLen += size
	}
	if curLen > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}

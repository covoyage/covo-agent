package email

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "email"

	defaultPollInterval = 30 * time.Second
	defaultSMTPPort     = "587"
	defaultPOP3Port     = "995"
	defaultFilterFolder = "INBOX"
	dialTimeout         = 15 * time.Second
	ioTimeout           = 30 * time.Second
	maxSeenUIDs         = 10000
)

type Adapter struct {
	smtpHost     string
	smtpPort     string
	smtpUser     string
	smtpPass     string
	popHost      string // EMAIL_IMAP_HOST (POP3 protocol despite the env name)
	popPort      string // EMAIL_IMAP_PORT
	from         string
	pollInterval time.Duration
	logger       *slog.Logger

	// Test hooks: seenPath/popTLS let unit tests redirect the seen-UID file
	// and use a plain TCP fake POP3 server instead of POP3S.
	seenPath string
	popTLS   bool

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)

	seenMu   sync.Mutex
	seenUIDs map[string]bool
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	pollInterval := defaultPollInterval
	if v := strings.TrimSpace(os.Getenv("EMAIL_POLL_INTERVAL_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pollInterval = time.Duration(n) * time.Second
		}
	}

	// NOTE: EMAIL_FILTER_FOLDER is intentionally not read. POP3 (the only
	// inbound protocol here, despite the EMAIL_IMAP_* env names) has no
	// folder concept, so a folder filter would be dead configuration.

	smtpPort := strings.TrimSpace(os.Getenv("EMAIL_SMTP_PORT"))
	if smtpPort == "" {
		smtpPort = defaultSMTPPort
	}
	popPort := strings.TrimSpace(os.Getenv("EMAIL_IMAP_PORT"))
	if popPort == "" {
		popPort = defaultPOP3Port
	}

	return &Adapter{
		smtpHost:     strings.TrimSpace(os.Getenv("EMAIL_SMTP_HOST")),
		smtpPort:     smtpPort,
		smtpUser:     strings.TrimSpace(os.Getenv("EMAIL_SMTP_USER")),
		smtpPass:     os.Getenv("EMAIL_SMTP_PASS"),
		popHost:      strings.TrimSpace(os.Getenv("EMAIL_IMAP_HOST")),
		popPort:      popPort,
		from:         strings.TrimSpace(os.Getenv("EMAIL_FROM")),
		pollInterval: pollInterval,
		seenUIDs:     make(map[string]bool),
		popTLS:       true,
		logger:       logger,
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
	if a.smtpHost == "" || a.smtpUser == "" {
		return fmt.Errorf("EMAIL_SMTP_HOST and EMAIL_SMTP_USER must be set in environment")
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
		return fmt.Errorf("email: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("email: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	if err := a.loadSeen(); err != nil {
		a.logger.Warn("email: failed to load seen-UID file", "error", err)
	}

	go a.pollLoop(ctx)

	a.logger.Info("email: adapter started",
		"smtp_host", a.smtpHost, "pop_host", a.popHost,
		"poll_interval", a.pollInterval)
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
	a.logger.Info("email: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

// SendMessage sends an email. channelID is the recipient's email address.
// The Subject can be passed via msg.ParseMode (text/plain body, UTF-8).
func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if channelID == "" {
		return fmt.Errorf("email: empty recipient address")
	}
	if msg.Text == "" {
		return nil
	}
	if _, err := mail.ParseAddress(channelID); err != nil {
		return fmt.Errorf("email: invalid recipient address %q: %w", channelID, err)
	}

	from := a.from
	if from == "" {
		from = a.smtpUser
	}
	subject := msg.ParseMode // Subject may be provided via ParseMode

	raw := buildMessage(from, channelID, subject, msg.Text)
	if err := a.sendMail(ctx, from, channelID, raw); err != nil {
		return fmt.Errorf("email: send to %s: %w", channelID, err)
	}
	return nil
}

// buildMessage constructs a MIME text/plain UTF-8 message with a base64 body.
func buildMessage(from, to, subject, text string) []byte {
	var b bytes.Buffer
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")

	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	return b.Bytes()
}

// sendMail delivers a fully-built message over SMTP. Port 465 uses implicit
// TLS (SMTPS); other ports connect in plaintext and upgrade via STARTTLS when
// the server advertises it.
func (a *Adapter) sendMail(ctx context.Context, from, to string, raw []byte) error {
	addr := net.JoinHostPort(a.smtpHost, a.smtpPort)
	d := &net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	host, _, _ := net.SplitHostPort(addr)
	if a.smtpPort == "465" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("smtps handshake with %s: %w", addr, err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp greeting from %s: %w", addr, err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok && a.smtpPort != "465" {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}

	if ok, mechs := client.Extension("AUTH"); ok {
		auth := smtp.PlainAuth("", a.smtpUser, a.smtpPass, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
		_ = mechs
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return fmt.Errorf("write message body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("end DATA: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("QUIT: %w", err)
	}
	return nil
}

// pollLoop polls the POP3 server at the configured interval until ctx is
// cancelled.
func (a *Adapter) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	a.poll(ctx) // poll immediately on startup
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.poll(ctx)
		}
	}
}

// poll opens one POP3 session, fetches unseen messages and dispatches
// callbacks for each new mail.
func (a *Adapter) poll(ctx context.Context) {
	if a.popHost == "" {
		a.logger.Warn("email: no POP3 host configured, skipping poll")
		return
	}
	if ctx.Err() != nil {
		return
	}

	sess, err := a.dialPOP3(ctx)
	if err != nil {
		if ctx.Err() == nil {
			a.logger.Warn("email: pop3 connect failed", "error", err)
		}
		return
	}
	defer sess.close()

	ok, err := sess.command("USER " + a.smtpUser)
	if err != nil {
		a.logger.Warn("email: pop3 USER failed", "error", err)
		return
	}
	_ = ok
	if _, err := sess.command("PASS " + a.smtpPass); err != nil {
		a.logger.Warn("email: pop3 PASS failed", "error", err)
		return
	}
	if _, err := sess.command("STAT"); err != nil {
		a.logger.Warn("email: pop3 STAT failed", "error", err)
		return
	}

	// UIDL lists "n uid" lines; uid without an index is a unique session id.
	if _, err := sess.command("UIDL"); err != nil {
		a.logger.Warn("email: pop3 UIDL failed", "error", err)
		return
	}
	lines, err := sess.readMultiline()
	if err != nil {
		a.logger.Warn("email: pop3 UIDL failed", "error", err)
		return
	}

	for _, line := range lines {
		if ctx.Err() != nil {
			return
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		index, uid := fields[0], fields[1]
		if a.isSeen(uid) {
			continue
		}

		if _, err := sess.command("RETR " + index); err != nil {
			a.logger.Warn("email: pop3 RETR failed", "uid", uid, "error", err)
			continue
		}
		rawLines, err := sess.readMultiline()
		if err != nil {
			a.logger.Warn("email: pop3 read message failed", "uid", uid, "error", err)
			continue
		}

		msg, err := parseMail(strings.Join(rawLines, "\r\n"))
		if err != nil {
			a.logger.Warn("email: parse message failed", "uid", uid, "error", err)
			a.markSeen(uid)
			continue
		}
		a.dispatch(*msg)
		a.markSeen(uid)
	}

	_, _ = sess.command("QUIT")
}

// dialPOP3 opens a POP3 (TLS by default) connection and reads the greeting.
func (a *Adapter) dialPOP3(ctx context.Context) (*pop3Session, error) {
	addr := net.JoinHostPort(a.popHost, a.popPort)
	d := &net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	if a.popTLS {
		host, _, _ := net.SplitHostPort(addr)
		tlsConn := tls.Client(raw, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("tls handshake with %s: %w", addr, err)
		}
		raw = tlsConn
	}

	sess := &pop3Session{
		conn: raw,
		r:    bufio.NewReader(raw),
		w:    bufio.NewWriter(raw),
	}
	// Read the server greeting; it must be a +OK line.
	greeting, err := sess.readLine()
	if err != nil {
		sess.close()
		return nil, fmt.Errorf("read greeting: %w", err)
	}
	if !strings.HasPrefix(greeting, "+OK") {
		sess.close()
		return nil, fmt.Errorf("bad greeting: %s", greeting)
	}
	return sess, nil
}

// dispatch sends an incoming email to the registered callback.
func (a *Adapter) dispatch(m plugin.IncomingMessage) {
	a.mu.Lock()
	callback := a.onMessage
	a.mu.Unlock()
	if callback != nil {
		callback(m)
	}
}

// isSeen reports whether a UID has already been processed.
func (a *Adapter) isSeen(uid string) bool {
	a.seenMu.Lock()
	defer a.seenMu.Unlock()
	return a.seenUIDs[uid]
}

// markSeen records a UID and persists the seen set.
func (a *Adapter) markSeen(uid string) {
	a.seenMu.Lock()
	a.seenUIDs[uid] = true
	a.seenMu.Unlock()

	if err := a.saveSeen(); err != nil {
		a.logger.Warn("email: failed to persist seen UIDs", "error", err)
	}
}

// loadSeen reads the persisted seen-UID set from disk.
func (a *Adapter) loadSeen() error {
	path := a.seenFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("email: read %s: %w", path, err)
	}
	var uids []string
	if err := json.Unmarshal(data, &uids); err != nil {
		return fmt.Errorf("email: parse %s: %w", path, err)
	}
	a.seenMu.Lock()
	if a.seenUIDs == nil {
		a.seenUIDs = make(map[string]bool)
	}
	for _, uid := range uids {
		a.seenUIDs[uid] = true
	}
	a.seenMu.Unlock()
	return nil
}

// saveSeen writes the seen-UID set to disk (bounded to the newest entries).
func (a *Adapter) saveSeen() error {
	path := a.seenFilePath()
	if path == "" {
		return nil
	}

	a.seenMu.Lock()
	uids := make([]string, 0, len(a.seenUIDs))
	for uid := range a.seenUIDs {
		uids = append(uids, uid)
	}
	a.seenMu.Unlock()
	if len(uids) > maxSeenUIDs {
		uids = uids[len(uids)-maxSeenUIDs:]
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("email: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.Marshal(uids)
	if err != nil {
		return fmt.Errorf("email: marshal seen uids: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("email: write %s: %w", path, err)
	}
	return nil
}

// seenFilePath returns the persistence path (overridable for tests).
func (a *Adapter) seenFilePath() string {
	if a.seenPath != "" {
		return a.seenPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".covo-agent", "email_seen.json")
}

// pop3Session is a minimal POP3 client over a single connection.
type pop3Session struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func (s *pop3Session) readLine() (string, error) {
	_ = s.conn.SetReadDeadline(time.Now().Add(ioTimeout))
	line, err := s.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// command sends a command and reads the single-line status response.
func (s *pop3Session) command(line string) (string, error) {
	_ = s.conn.SetWriteDeadline(time.Now().Add(ioTimeout))
	if _, err := s.w.WriteString(line + "\r\n"); err != nil {
		return "", fmt.Errorf("write %q: %w", line, err)
	}
	if err := s.w.Flush(); err != nil {
		return "", fmt.Errorf("flush %q: %w", line, err)
	}
	resp, err := s.readLine()
	if err != nil {
		return "", fmt.Errorf("read response to %q: %w", line, err)
	}
	if !strings.HasPrefix(resp, "+OK") {
		return resp, fmt.Errorf("pop3 command %q rejected: %s", line, resp)
	}
	return resp, nil
}

// readMultiline reads a multiline response terminated by a lone "." line,
// undoing byte-stuffing (lines starting with "..").
func (s *pop3Session) readMultiline() ([]string, error) {
	var lines []string
	for {
		line, err := s.readLine()
		if err != nil {
			return nil, fmt.Errorf("read multiline: %w", err)
		}
		if line == "." {
			return lines, nil
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		lines = append(lines, line)
	}
}

func (s *pop3Session) close() { _ = s.conn.Close() }

// parseMail parses a raw RFC 822 message and extracts the text/plain body.
func parseMail(raw string) (*plugin.IncomingMessage, error) {
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("email: parse headers: %w", err)
	}

	text, err := extractText(msg.Body, textproto.MIMEHeader(msg.Header))
	if err != nil {
		return nil, err
	}
	text = strings.TrimRight(text, "\r\n")

	from := msg.Header.Get("From")
	var senderAddr, senderName string
	if addr, err := mail.ParseAddress(from); err == nil {
		senderAddr = addr.Address
		senderName = addr.Name
	} else {
		senderAddr = strings.TrimSpace(from)
	}
	if senderName == "" {
		senderName = senderAddr
	}

	ts := time.Now()
	if date, err := mail.ParseDate(msg.Header.Get("Date")); err == nil {
		ts = date
	}

	return &plugin.IncomingMessage{
		Platform:  PlatformName,
		ChannelID: senderAddr,
		UserID:    senderAddr,
		UserName:  senderName,
		Text:      text,
		Timestamp: ts,
	}, nil
}

// extractText walks the MIME tree of a message and returns the body as
// plain text: the first text/plain part, or — for HTML-only mail — the
// first text/html part converted via stripHTML (mirrors hermes
// adapter.py's _extract_text_body fallback).
func extractText(body io.Reader, header textproto.MIMEHeader) (string, error) {
	var plain, htmlText string
	if err := collectText(body, header, &plain, &htmlText); err != nil {
		return "", err
	}
	if plain != "" {
		return plain, nil
	}
	if htmlText != "" {
		return stripHTML(htmlText), nil
	}
	return "", nil
}

// collectText recursively walks the MIME tree, capturing the first
// text/plain and text/html part (decoded for CTE and charset).
func collectText(body io.Reader, header textproto.MIMEHeader, plain, htmlText *string) error {
	ct := header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return fmt.Errorf("email: walk multipart: %w", err)
			}
			if err := collectText(part, part.Header, plain, htmlText); err != nil {
				return err
			}
		}
	}

	switch mediaType {
	case "text/plain":
		data, err := decodeCTE(body, header.Get("Content-Transfer-Encoding"))
		if err != nil {
			return err
		}
		if *plain == "" {
			*plain = decodeCharset(data, params["charset"])
		}
	case "text/html":
		data, err := decodeCTE(body, header.Get("Content-Transfer-Encoding"))
		if err != nil {
			return err
		}
		if *htmlText == "" {
			*htmlText = decodeCharset(data, params["charset"])
		}
	default:
		// Skip other parts (attachments, ...).
		_, _ = io.Copy(io.Discard, body)
	}
	return nil
}

var (
	stripStyleScriptRe = regexp.MustCompile(`(?is)<(style|script)\b[^>]*>.*?(</(style|script)>|$)`)
	stripBreakRe       = regexp.MustCompile(`(?i)<br\s*/?>`)
	stripParaRe        = regexp.MustCompile(`(?i)</?p[^>]*>`)
	stripTagRe         = regexp.MustCompile(`<[^>]*>`)
	stripNewlinesRe    = regexp.MustCompile(`\n{3,}`)
)

// stripHTML converts an HTML body to readable plain text: style/script
// blocks are dropped entirely, block tags become newlines, remaining tags
// are stripped, entities decoded and whitespace collapsed (mirrors hermes
// adapter.py's _strip_html).
func stripHTML(s string) string {
	s = stripStyleScriptRe.ReplaceAllString(s, "")
	s = stripBreakRe.ReplaceAllString(s, "\n")
	s = stripParaRe.ReplaceAllString(s, "\n")
	s = stripTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ") // &nbsp; → space
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = stripNewlinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// decodeCTE decodes the Content-Transfer-Encoding of a part.
func decodeCTE(r io.Reader, cte string) ([]byte, error) {
	cte = strings.ToLower(strings.TrimSpace(cte))
	switch cte {
	case "base64":
		dec := base64.NewDecoder(base64.StdEncoding, r)
		data, err := io.ReadAll(dec)
		if err != nil {
			return nil, fmt.Errorf("email: decode base64 body: %w", err)
		}
		return data, nil
	case "quoted-printable":
		data, err := io.ReadAll(quotedprintable.NewReader(r))
		if err != nil {
			return nil, fmt.Errorf("email: decode quoted-printable body: %w", err)
		}
		return data, nil
	default:
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("email: read body: %w", err)
		}
		return data, nil
	}
}

// normalizeCharset lowercases and maps charset labels seen in the wild to
// names this adapter can decode (mirrors hermes adapter.py's
// _CHARSET_ALIASES: gb2312/gbk → gb18030 superset, RFC 1428 placeholder
// labels → utf-8, latin aliases → iso-8859-1).
func normalizeCharset(charset string) string {
	cs := strings.ToLower(strings.TrimSpace(charset))
	cs = strings.Trim(cs, "\"'")
	cs = strings.TrimSpace(cs)
	switch cs {
	case "", "utf8", "us-ascii", "ascii", "unknown-8bit", "unknown", "x-unknown", "default":
		return "utf-8"
	case "gb2312", "gbk", "gb18030", "csgb2312":
		return "gb18030"
	case "big5", "big5-hkscs", "big-five":
		return "big5"
	case "latin-1", "latin1", "cp819", "iso8859-1":
		return "iso-8859-1"
	case "windows-1252", "cp1252":
		return "windows-1252"
	}
	return cs
}

// decodeCharset converts raw bytes to a UTF-8 string, decoding the CJK and
// latin charsets common in the wild (GB2312/GBK/GB18030, Big5, ISO-8859-*).
// Decoding never fails the message: on any error the payload degrades to
// latin-1 (mirrors hermes adapter.py's _safe_decode fallback chain).
func decodeCharset(data []byte, charset string) string {
	switch normalizeCharset(charset) {
	case "utf-8":
		if utf8.Valid(data) {
			return string(data)
		}
		// Invalid bytes: replace rather than propagate garbage (the
		// errors="replace" half of hermes's _safe_decode).
		return strings.ToValidUTF8(string(data), "\uFFFD")
	case "gb18030":
		return decodeWith(simplifiedchinese.GB18030, data)
	case "big5":
		return decodeWith(traditionalchinese.Big5, data)
	case "iso-8859-1":
		return decodeWith(charmap.ISO8859_1, data)
	case "iso-8859-15":
		return decodeWith(charmap.ISO8859_15, data)
	case "windows-1252":
		return decodeWith(charmap.Windows1252, data)
	default:
		// Remaining ISO-8859 family members and anything unmapped: fall
		// back to latin-1 semantics, then UTF-8 passthrough.
		if strings.HasPrefix(normalizeCharset(charset), "iso-8859-") {
			return decodeWith(charmap.ISO8859_1, data)
		}
		if utf8.Valid(data) {
			return string(data)
		}
		return decodeWith(charmap.ISO8859_1, data)
	}
}

// decodeWith decodes data with the given encoding, degrading to latin-1
// (which never fails) on error or invalid output.
func decodeWith(dec encoding.Encoding, data []byte) string {
	out, err := dec.NewDecoder().Bytes(data)
	if err != nil || !utf8.Valid(out) {
		out, _ = charmap.ISO8859_1.NewDecoder().Bytes(data)
	}
	return string(out)
}

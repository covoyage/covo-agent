package email

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

type nullWriter struct{}

func (nullWriter) Write(p []byte) (int, error) { return len(p), nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nullWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 100}))
}

// fakeSMTPServer is a minimal SMTP server over plain TCP (no STARTTLS
// advertised, AUTH PLAIN offered — net/smtp permits PlainAuth on localhost).
type fakeSMTPServer struct {
	listener net.Listener

	mu     sync.Mutex
	data   []string // full DATA payloads received
	rcpt   []string
	rcptOK bool // when false, RCPT TO gets a 550

	closeOnce sync.Once
}

func newFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{listener: l, rcptOK: true}
	go s.serve()
	t.Cleanup(func() { s.closeOnce.Do(func() { _ = l.Close() }) })
	return s
}

func (s *fakeSMTPServer) addr() string { return s.listener.Addr().String() }

func (s *fakeSMTPServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	w := func(line string) { fmt.Fprint(conn, line+"\r\n") }
	w("220 test ESMTP ready")

	inData := false
	var dataLines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false
				s.mu.Lock()
				s.data = append(s.data, strings.Join(dataLines, "\r\n"))
				s.mu.Unlock()
				w("250 OK message accepted")
			} else {
				dataLines = append(dataLines, line)
			}
			continue
		}

		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			w("250-test")
			w("250 AUTH PLAIN")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			w("235 2.7.0 Accepted")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			w("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mu.Lock()
			accepted := s.rcptOK
			s.rcpt = append(s.rcpt, line)
			s.mu.Unlock()
			if accepted {
				w("250 OK")
			} else {
				w("550 no such user")
			}
		case strings.HasPrefix(upper, "DATA"):
			inData = true
			w("354 go ahead")
		case strings.HasPrefix(upper, "QUIT"):
			w("221 bye")
			return
		default:
			w("250 OK")
		}
	}
}

func (s *fakeSMTPServer) captured() (payloads, rcpts []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.data...), append([]string(nil), s.rcpt...)
}

func (s *fakeSMTPServer) setRcptOK(ok bool) {
	s.mu.Lock()
	s.rcptOK = ok
	s.mu.Unlock()
}

// fakePOP3Server is a minimal POP3 server over plain TCP with two messages.
type fakePOP3Server struct {
	listener net.Listener

	messages []string

	closeOnce sync.Once
}

func newFakePOP3Server(t *testing.T, messages []string) *fakePOP3Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakePOP3Server{listener: l, messages: messages}
	go s.serve()
	t.Cleanup(func() { s.closeOnce.Do(func() { _ = l.Close() }) })
	return s
}

func (s *fakePOP3Server) addr() string { return s.listener.Addr().String() }

func (s *fakePOP3Server) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	w := func(line string) { fmt.Fprint(conn, line+"\r\n") }
	w("+OK test POP3 ready")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "USER"), strings.HasPrefix(upper, "PASS"):
			w("+OK")
		case strings.HasPrefix(upper, "STAT"):
			w(fmt.Sprintf("+OK %d %d", len(s.messages), 100*len(s.messages)))
		case strings.HasPrefix(upper, "UIDL"):
			w("+OK")
			for i, _ := range s.messages {
				w(fmt.Sprintf("%d uid-%d", i+1, i+1))
			}
			w(".")
		case strings.HasPrefix(upper, "RETR"):
			var n int
			fmt.Sscanf(strings.Fields(line)[1], "%d", &n)
			w("+OK")
			// Byte-stuff lines starting with '.' (none in our fixtures).
			fmt.Fprint(conn, s.messages[n-1]+"\r\n.\r\n")
		case strings.HasPrefix(upper, "QUIT"):
			w("+OK bye")
			return
		default:
			w("+OK")
		}
	}
}

func newTestAdapter(smtpAddr, popAddr string) *Adapter {
	smtpHost, smtpPort, _ := net.SplitHostPort(smtpAddr)
	popHost, popPort, _ := net.SplitHostPort(popAddr)
	return &Adapter{
		smtpHost:     smtpHost,
		smtpPort:     smtpPort,
		smtpUser:     "user@test.local",
		smtpPass:     "secret",
		popHost:      popHost,
		popPort:      popPort,
		from:         "user@test.local",
		pollInterval: 50 * time.Millisecond,
		popTLS:       false,
		seenUIDs:     make(map[string]bool),
		logger:       discardLogger(),
	}
}

func TestSendMessagePlainText(t *testing.T) {
	srv := newFakeSMTPServer(t)
	a := newTestAdapter(srv.addr(), "127.0.0.1:1")

	body := "Hello,\n\nThis is a test with UTF-8: 你好世界\n- covo"
	if err := a.SendMessage(context.Background(), "dest@example.com", plugin.OutgoingMessage{
		Text:      body,
		ParseMode: "Weekly Report",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	payloads, rcpts := srv.captured()
	if len(payloads) != 1 {
		t.Fatalf("expected 1 DATA payload, got %d", len(payloads))
	}
	if len(rcpts) != 1 || !strings.Contains(rcpts[0], "dest@example.com") {
		t.Errorf("RCPT TO not captured correctly: %v", rcpts)
	}

	raw := payloads[0]
	if !strings.Contains(raw, "To: dest@example.com") {
		t.Errorf("missing To header in:\n%s", raw)
	}
	if !strings.Contains(raw, "MIME-Version: 1.0") {
		t.Error("missing MIME-Version header")
	}
	if !strings.Contains(raw, "Content-Type: text/plain; charset=UTF-8") {
		t.Error("missing text/plain UTF-8 content type")
	}
	if !strings.Contains(raw, "Content-Transfer-Encoding: base64") {
		t.Error("missing base64 CTE header")
	}

	// Subject must be the Q-encoded ParseMode value and decodable.
	found := false
	var decErr error
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(line, "Subject:") {
			dec, err := new(mime.WordDecoder).DecodeHeader(strings.TrimSpace(strings.TrimPrefix(line, "Subject:")))
			if err != nil || dec != "Weekly Report" {
				decErr = fmt.Errorf("Subject decode = %q, err %v", dec, err)
			}
			found = true
		}
	}
	if !found {
		t.Error("missing Subject header")
	}
	if decErr != nil {
		t.Error(decErr)
	}

	// Body must decode to the original text.
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatal("malformed message: no body separator")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.NewReplacer("\r\n", "").Replace(parts[1]))
	if err != nil {
		t.Fatalf("body base64 decode: %v", err)
	}
	if string(decoded) != body {
		t.Errorf("decoded body = %q, want %q", string(decoded), body)
	}
}

func TestSendMessageRecipientRejected(t *testing.T) {
	srv := newFakeSMTPServer(t)
	srv.setRcptOK(false)
	a := newTestAdapter(srv.addr(), "127.0.0.1:1")

	err := a.Send(context.Background(), "nobody@invalid.test", "hi")
	if err == nil {
		t.Fatal("expected error when RCPT rejected, got nil")
	}
	if !strings.Contains(err.Error(), "email:") {
		t.Errorf("error not wrapped with platform prefix: %v", err)
	}
}

func TestSendMessageEmptyText(t *testing.T) {
	a := newTestAdapter("127.0.0.1:1", "127.0.0.1:1")
	if err := a.Send(context.Background(), "a@b.c", ""); err != nil {
		t.Errorf("empty text should be a no-op, got %v", err)
	}
	if err := a.Send(context.Background(), "not-an-email", "hi"); err == nil {
		t.Error("expected error for invalid recipient address")
	}
}

func TestReceiveViaPOP3(t *testing.T) {
	const rawMsg1 = "From: Alice <alice@example.com>\r\n" +
		"To: user@test.local\r\n" +
		"Subject: =?UTF-8?q?Hello?=\r\n" +
		"Date: Sat, 12 Sep 2026 10:00:00 +0000\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"Hi there,=20\r\nthis is message one.\r\n"

	rawMsg2 := "From: bob@example.com\r\n" +
		"Subject: Two\r\n" +
		"Date: Sat, 12 Sep 2026 11:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"BOUND\"\r\n" +
		"\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"\r\n" +
		"<b>ignore html</b>\r\n" +
		"--BOUND\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		base64.StdEncoding.EncodeToString([]byte("message two body 你好")) + "\r\n" +
		"--BOUND--\r\n"

	srv := newFakePOP3Server(t, []string{rawMsg1, rawMsg2})
	a := newTestAdapter("127.0.0.1:1", srv.addr())

	seenPath := filepath.Join(t.TempDir(), "email_seen.json")
	a.seenPath = seenPath

	var mu sync.Mutex
	var got []plugin.IncomingMessage
	done := make(chan struct{})
	a.OnMessage(func(m plugin.IncomingMessage) {
		mu.Lock()
		got = append(got, m)
		n := len(got)
		mu.Unlock()
		if n == 2 {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		mu.Lock()
		n := len(got)
		mu.Unlock()
		t.Fatalf("expected 2 incoming messages, got %d", n)
	}

	mu.Lock()
	msgs := append([]plugin.IncomingMessage(nil), got...)
	mu.Unlock()

	m1 := msgs[0]
	if m1.ChannelID != "alice@example.com" {
		t.Errorf("msg1 ChannelID = %q, want alice@example.com", m1.ChannelID)
	}
	if m1.UserID != "alice@example.com" {
		t.Errorf("msg1 UserID = %q", m1.UserID)
	}
	if m1.UserName != "Alice" {
		t.Errorf("msg1 UserName = %q, want Alice", m1.UserName)
	}
	if m1.Text != "Hi there, \r\nthis is message one." {
		t.Errorf("msg1 Text = %q", m1.Text)
	}

	m2 := msgs[1]
	if m2.ChannelID != "bob@example.com" {
		t.Errorf("msg2 ChannelID = %q, want bob@example.com", m2.ChannelID)
	}
	if m2.Text != "message two body 你好" {
		t.Errorf("msg2 Text = %q, want text/plain part only", m2.Text)
	}

	// Seen UIDs must be persisted (markSeen happens after dispatch).
	persistDeadline := time.Now().Add(5 * time.Second)
	var seenData []byte
	for time.Now().Before(persistDeadline) {
		data, err := os.ReadFile(seenPath)
		if err == nil && strings.Contains(string(data), "uid-1") && strings.Contains(string(data), "uid-2") {
			seenData = data
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if seenData == nil {
		t.Fatalf("seen file missing UIDs (see %s)", seenPath)
	}

	// A later poll must not re-deliver the same messages (UID dedup).
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 2 {
		t.Errorf("expected no duplicate deliveries, got %d messages", n)
	}
}

func TestSeenPersistenceAcrossRestart(t *testing.T) {
	srv := newFakePOP3Server(t, []string{
		"From: a@example.com\r\nSubject: A\r\nDate: Sat, 12 Sep 2026 10:00:00 +0000\r\n\r\nbody a\r\n",
	})
	a := newTestAdapter("127.0.0.1:1", srv.addr())
	seenPath := filepath.Join(t.TempDir(), "email_seen.json")
	a.seenPath = seenPath

	var got int
	var mu sync.Mutex
	a.OnMessage(func(plugin.IncomingMessage) {
		mu.Lock()
		got++
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := got
		mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.Stop()
	cancel()

	// "Restart": fresh adapter with the same seen file should not re-deliver.
	b := newTestAdapter("127.0.0.1:1", srv.addr())
	b.seenPath = seenPath
	if err := b.loadSeen(); err != nil {
		t.Fatalf("loadSeen: %v", err)
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	defer b.Stop()
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	n := got
	mu.Unlock()
	if n != 1 {
		t.Errorf("expected exactly 1 delivery across restarts, got %d", n)
	}
}

func TestValidate(t *testing.T) {
	a := &Adapter{}
	if err := a.Validate(); err == nil || !strings.Contains(err.Error(), "EMAIL_SMTP_HOST") || !strings.Contains(err.Error(), "EMAIL_SMTP_USER") {
		t.Errorf("Validate() = %v, want EMAIL_SMTP_HOST/EMAIL_SMTP_USER error", err)
	}
	a.smtpHost = "smtp.example.com"
	a.smtpUser = "u"
	if err := a.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestNewDefaults(t *testing.T) {
	a := New()
	if a.smtpPort != "587" {
		t.Errorf("default smtp port = %q, want 587", a.smtpPort)
	}
	if a.popPort != "995" {
		t.Errorf("default pop port = %q, want 995", a.popPort)
	}
	if a.pollInterval != 30*time.Second {
		t.Errorf("default poll interval = %v", a.pollInterval)
	}
	if !a.popTLS {
		t.Error("popTLS should default to true")
	}

	t.Setenv("EMAIL_POLL_INTERVAL_SECONDS", "7")
	t.Setenv("EMAIL_SMTP_PORT", "25")
	b := New()
	if b.pollInterval != 7*time.Second || b.smtpPort != "25" {
		t.Errorf("env overrides not applied: %v %q", b.pollInterval, b.smtpPort)
	}
}

func TestDecodeCharsetGBKFamily(t *testing.T) {
	// "你好" in GBK/GB2312/GB18030 (shared encoding for these chars).
	gbkBytes := []byte{0xC4, 0xE3, 0xBA, 0xC3}
	for _, cs := range []string{"gbk", "gb2312", "GB18030", " csGB2312 "} {
		if got := decodeCharset(gbkBytes, cs); got != "你好" {
			t.Errorf("charset %q: got %q, want 你好", cs, got)
		}
	}

	// "你好" in Big5.
	big5Bytes := []byte{0xA7, 0x41, 0xA6, 0x6E}
	if got := decodeCharset(big5Bytes, "big5"); got != "你好" {
		t.Errorf("big5: got %q, want 你好", got)
	}

	// Latin-1 é (0xE9).
	if got := decodeCharset([]byte{'c', 'a', 'f', 0xE9}, "iso-8859-1"); got != "café" {
		t.Errorf("iso-8859-1: got %q, want café", got)
	}

	// Alias normalization: quotes, whitespace, latin-1 alias.
	if got := decodeCharset([]byte{'c', 'a', 'f', 0xE9}, `" Latin-1 "`); got != "café" {
		t.Errorf("alias normalization: got %q, want café", got)
	}

	// Unknown charset labels degrade to UTF-8 (with replacement for
	// invalid bytes) instead of raising on a missing codec.
	if got := decodeCharset([]byte("hello"), "unknown-8bit"); got != "hello" {
		t.Errorf("unknown-8bit passthrough: %q", got)
	}
	if got := decodeCharset([]byte{'c', 'a', 'f', 0xE9}, "x-unknown"); got != "caf\uFFFD" {
		t.Errorf("x-unknown replacement: %q", got)
	}
}

func TestStripHTML(t *testing.T) {
	in := "<html><head><style>body{color:red}</style></head>" +
		"<body><p>Hello <b>world</b> &amp; friends</p>" +
		"<script>alert('x')</script>" +
		"<br/>line two&nbsp;&nbsp;end</body></html>"
	got := stripHTML(in)
	if strings.Contains(got, "color:red") {
		t.Errorf("style block content leaked: %q", got)
	}
	if strings.Contains(got, "alert") {
		t.Errorf("script block content leaked: %q", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Errorf("tags not stripped: %q", got)
	}
	for _, want := range []string{"Hello world & friends", "line two  end"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestHTMLOnlyBodies(t *testing.T) {
	// Multipart with only a text/html part: body must come from stripHTML.
	multipartHTML := "From: carol@example.com\r\n" +
		"Subject: HTML only\r\n" +
		"Date: Sat, 12 Sep 2026 12:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"BND\"\r\n" +
		"\r\n" +
		"--BND\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"\r\n" +
		"<p>Important<br/>notice</p>\r\n" +
		"--BND--\r\n"

	msg, err := parseMail(multipartHTML)
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	if msg.Text == "" {
		t.Fatal("HTML-only multipart produced empty body")
	}
	if msg.Text != "Important\nnotice" {
		t.Errorf("multipart html text = %q, want %q", msg.Text, "Important\nnotice")
	}

	// Single-part text/html message.
	singleHTML := "From: dave@example.com\r\n" +
		"Subject: Single\r\n" +
		"Date: Sat, 12 Sep 2026 13:00:00 +0000\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"<p>style=<style>x{y}</style>gone</p><b>plain&gt;text</b>\r\n"

	msg2, err := parseMail(singleHTML)
	if err != nil {
		t.Fatalf("parseMail single: %v", err)
	}
	if msg2.Text != "style=gone\nplain>text" {
		t.Errorf("single html text = %q", msg2.Text)
	}
}

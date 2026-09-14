package wecomcallback

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	_, encKey := testAESKey(t)
	return &Adapter{
		corpID:         "corp123",
		corpSecret:     "secret",
		agentID:        "1000002",
		token:          "testtoken",
		encodingAESKey: encKey,
		listenAddr:     "127.0.0.1:0",
		httpClient:     &http.Client{Timeout: 5 * time.Second},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestValidate(t *testing.T) {
	a := newTestAdapter(t)
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	a2 := &Adapter{}
	err := a2.Validate()
	if err == nil || !strings.Contains(err.Error(), "WECOM_CALLBACK_CORP_ID, WECOM_CALLBACK_TOKEN and WECOM_CALLBACK_ENCODING_AES_KEY") {
		t.Fatalf("unexpected validate error: %v", err)
	}
}

func TestGETVerifyURL(t *testing.T) {
	a := newTestAdapter(t)
	key, err := aesKeyFromEncodingKey(a.encodingAESKey)
	if err != nil {
		t.Fatalf("aesKey: %v", err)
	}
	a.aesKey = key

	ts := httptest.NewServer(http.HandlerFunc(a.serveCallback))
	defer ts.Close()

	echoMsg := "echo-plaintext-42"
	echostr, err := encryptWeCom(key, []byte(echoMsg), a.corpID)
	if err != nil {
		t.Fatalf("encrypt echostr: %v", err)
	}
	sig := computeSignature(a.token, "1409659813", "nonce123", echostr)

	// Positive: server must echo the decrypted plaintext verbatim.
	verifyURL := fmt.Sprintf("%s/?msg_signature=%s&timestamp=1409659813&nonce=nonce123&echostr=%s", ts.URL, sig, url.QueryEscape(echostr))
	resp, err := http.Get(verifyURL)
	if err != nil {
		t.Fatalf("GET verify: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != echoMsg {
		t.Fatalf("verify: status=%d body=%q want %q", resp.StatusCode, body, echoMsg)
	}

	// Negative: tampered signature must be rejected.
	badURL := fmt.Sprintf("%s/?msg_signature=%s&timestamp=1409659813&nonce=nonce123&echostr=%s", ts.URL, strings.Repeat("0", 40), url.QueryEscape(echostr))
	resp2, err := http.Get(badURL)
	if err != nil {
		t.Fatalf("GET bad verify: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad signature: expected 400, got %d", resp2.StatusCode)
	}
}

func TestPOSTReceiveEncryptedMessage(t *testing.T) {
	a := newTestAdapter(t)
	key, err := aesKeyFromEncodingKey(a.encodingAESKey)
	if err != nil {
		t.Fatalf("aesKey: %v", err)
	}
	a.aesKey = key

	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(msg plugin.IncomingMessage) { callbacks <- msg })

	ts := httptest.NewServer(http.HandlerFunc(a.serveCallback))
	defer ts.Close()

	innerXML := `<xml><ToUserName><![CDATA[corp123]]></ToUserName>` +
		`<FromUserName><![CDATA[zhangsan]]></FromUserName>` +
		`<CreateTime>1348831860</CreateTime>` +
		`<MsgType><![CDATA[text]]></MsgType>` +
		`<Content><![CDATA[hello wecom]]></Content>` +
		`<MsgId>1234567890123456</MsgId></xml>`

	encrypted, err := encryptWeCom(key, []byte(innerXML), a.corpID)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	body := fmt.Sprintf("<xml><Encrypt><![CDATA[%s]]></Encrypt><AgentID><![CDATA[1000002]]></AgentID></xml>", encrypted)
	sig := computeSignature(a.token, "1348831860", "nonce1", encrypted)

	// Positive: callback fires with decrypted fields.
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/?msg_signature=%s&timestamp=1348831860&nonce=nonce1", ts.URL, sig),
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST receive: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(respBody)) != "success" {
		t.Fatalf("receive: status=%d body=%q", resp.StatusCode, respBody)
	}

	select {
	case msg := <-callbacks:
		if msg.Platform != "wecom_callback" || msg.ChannelID != "zhangsan" ||
			msg.UserID != "zhangsan" || msg.Text != "hello wecom" {
			t.Fatalf("unexpected callback: %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback not invoked")
	}

	// Negative: wrong signature must be rejected and callback not fired.
	badSig := strings.Repeat("f", 40)
	req2, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/?msg_signature=%s&timestamp=1348831860&nonce=nonce1", ts.URL, badSig),
		strings.NewReader(body))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST bad sig: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad signature: expected 400, got %d", resp2.StatusCode)
	}
	select {
	case msg := <-callbacks:
		t.Fatalf("callback fired despite bad signature: %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSendMessageWithFakeAPI(t *testing.T) {
	var mu sync.Mutex
	var sentBodies []map[string]any
	sendErrcodes := []int{0, 0, 40014, 0}
	callCount := 0

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/gettoken"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"tok123","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/message/send"):
			body, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			mu.Lock()
			sentBodies = append(sentBodies, m)
			idx := callCount
			callCount++
			errcode := sendErrcodes[idx]
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"errcode":%d,"errmsg":"msg"}`, errcode)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()

	a := newTestAdapter(t)
	a.apiBase = fake.URL

	// Successful send.
	if err := a.SendMessage(context.Background(), "zhangsan", plugin.OutgoingMessage{Text: "hi there"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	mu.Lock()
	if len(sentBodies) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sentBodies))
	}
	b := sentBodies[0]
	mu.Unlock()
	if b["touser"] != "zhangsan" || b["msgtype"] != "text" || b["agentid"] != float64(1000002) {
		t.Fatalf("unexpected send body: %v", b)
	}
	if text, ok := b["text"].(map[string]any); !ok || text["content"] != "hi there" {
		t.Fatalf("unexpected text payload: %v", b["text"])
	}

	// Token expired (40014): refresh + retry once must succeed.
	if err := a.SendMessage(context.Background(), "zhangsan", plugin.OutgoingMessage{Text: "retry"}); err != nil {
		t.Fatalf("SendMessage after 40014: %v", err)
	}

	// Persistent failure surfaces as error.
	sendErrcodes[2] = 40011
	sendErrcodes[3] = 40011
	if err := a.SendMessage(context.Background(), "zhangsan", plugin.OutgoingMessage{Text: "fail"}); err == nil {
		t.Fatal("expected error for persistent errcode failure")
	} else if !strings.Contains(err.Error(), "40011") {
		t.Fatalf("error should contain errcode: %v", err)
	}
}

// buildEncryptedPost encrypts an inner XML message for corpID and returns
// the signed request body and URL.
func buildEncryptedPost(t *testing.T, key []byte, token, corpID, innerXML, timestamp, nonce string) (string, string) {
	t.Helper()
	encrypted, err := encryptWeCom(key, []byte(innerXML), corpID)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	sig := computeSignature(token, timestamp, nonce, encrypted)
	body := fmt.Sprintf("<xml><Encrypt><![CDATA[%s]]></Encrypt><AgentID><![CDATA[1000002]]></AgentID></xml>", encrypted)
	return body, fmt.Sprintf("?msg_signature=%s&timestamp=%s&nonce=%s", sig, timestamp, nonce)
}

func TestReceiveIDMismatchRejected(t *testing.T) {
	a := newTestAdapter(t)
	key, err := aesKeyFromEncodingKey(a.encodingAESKey)
	if err != nil {
		t.Fatalf("aesKey: %v", err)
	}
	a.aesKey = key

	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(msg plugin.IncomingMessage) { callbacks <- msg })

	ts := httptest.NewServer(http.HandlerFunc(a.serveCallback))
	defer ts.Close()

	innerXML := `<xml><ToUserName><![CDATA[corp123]]></ToUserName>` +
		`<FromUserName><![CDATA[zhangsan]]></FromUserName>` +
		`<MsgType><![CDATA[text]]></MsgType>` +
		`<Content><![CDATA[hello]]></Content>` +
		`<MsgId>1001</MsgId></xml>`

	// POST encrypted for a different corp: must be rejected.
	body, query := buildEncryptedPost(t, key, a.token, "attacker-corp", innerXML, "1348831860", "nonceX")
	resp, err := http.Post(ts.URL+query, "application/xml", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST mismatched receiveid: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("receive_id mismatch: expected 400, got %d", resp.StatusCode)
	}
	select {
	case msg := <-callbacks:
		t.Fatalf("callback fired despite receive_id mismatch: %+v", msg)
	case <-time.After(200 * time.Millisecond):
	}

	// GET verify URL with mismatched receiveid: same rejection.
	echostr, err := encryptWeCom(key, []byte("echo"), "attacker-corp")
	if err != nil {
		t.Fatalf("encrypt echostr: %v", err)
	}
	sig := computeSignature(a.token, "1409659813", "nonceY", echostr)
	resp2, err := http.Get(fmt.Sprintf("%s/?msg_signature=%s&timestamp=1409659813&nonce=nonceY&echostr=%s", ts.URL, sig, url.QueryEscape(echostr)))
	if err != nil {
		t.Fatalf("GET mismatched receiveid: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET receive_id mismatch: expected 400, got %d", resp2.StatusCode)
	}
}

func TestMsgIDDedup(t *testing.T) {
	a := newTestAdapter(t)
	key, err := aesKeyFromEncodingKey(a.encodingAESKey)
	if err != nil {
		t.Fatalf("aesKey: %v", err)
	}
	a.aesKey = key

	callbacks := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(msg plugin.IncomingMessage) { callbacks <- msg })

	ts := httptest.NewServer(http.HandlerFunc(a.serveCallback))
	defer ts.Close()

	innerXML := `<xml><ToUserName><![CDATA[corp123]]></ToUserName>` +
		`<FromUserName><![CDATA[zhangsan]]></FromUserName>` +
		`<MsgType><![CDATA[text]]></MsgType>` +
		`<Content><![CDATA[dedup me]]></Content>` +
		`<MsgId>999888777</MsgId></xml>`

	body, query := buildEncryptedPost(t, key, a.token, a.corpID, innerXML, "1348831860", "nonceD")

	// First delivery: callback fires.
	resp, err := http.Post(ts.URL+query, "application/xml", strings.NewReader(body))
	if err != nil {
		t.Fatalf("first POST: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(respBody)) != "success" {
		t.Fatalf("first POST: status=%d body=%q", resp.StatusCode, respBody)
	}
	select {
	case <-callbacks:
	case <-time.After(2 * time.Second):
		t.Fatal("first delivery missing")
	}

	// WeCom timeout retry: same MsgId delivered again → ack, no re-dispatch.
	resp2, err := http.Post(ts.URL+query, "application/xml", strings.NewReader(body))
	if err != nil {
		t.Fatalf("retry POST: %v", err)
	}
	respBody2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || strings.TrimSpace(string(respBody2)) != "success" {
		t.Fatalf("retry POST: status=%d body=%q", resp2.StatusCode, respBody2)
	}
	select {
	case msg := <-callbacks:
		t.Fatalf("duplicate MsgId dispatched twice: %+v", msg)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSendMessageTokenRetryOnOtherCodes(t *testing.T) {
	var mu sync.Mutex
	var sentBodies []map[string]any
	sendErrcodes := []int{40001, 0}
	callCount := 0
	tokenFetches := 0

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/gettoken"):
			mu.Lock()
			tokenFetches++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","access_token":"tokXYZ","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/message/send"):
			body, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			mu.Lock()
			sentBodies = append(sentBodies, m)
			idx := callCount
			callCount++
			errcode := sendErrcodes[idx]
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"errcode":%d,"errmsg":"msg"}`, errcode)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()

	a := newTestAdapter(t)
	a.apiBase = fake.URL

	if err := a.SendMessage(context.Background(), "zhangsan", plugin.OutgoingMessage{Text: "retry-40001"}); err != nil {
		t.Fatalf("SendMessage after 40001: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sentBodies) != 2 {
		t.Fatalf("expected 2 send attempts after 40001, got %d", len(sentBodies))
	}
	if tokenFetches != 2 {
		t.Fatalf("expected token to be force-refreshed once, got %d fetches", tokenFetches)
	}
}

func TestTruncateRunes(t *testing.T) {
	long := strings.Repeat("好", 1500) // 3 bytes each = 4500 bytes
	got := truncateRunes(long, 2048)
	if len(got) != 2046 { // 2048 % 3 = 2 → last rune would split, drop 2 bytes
		t.Fatalf("truncated length = %d, want 2046", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated string is not valid UTF-8")
	}
	if got != strings.Repeat("好", 682) {
		t.Fatalf("truncated content mismatch: rune count %d", utf8.RuneCountInString(got))
	}
	if truncateRunes("short", 2048) != "short" {
		t.Fatal("short string must pass through unchanged")
	}
	if truncateRunes("你好", 1) != "" {
		t.Fatal("expected empty string when maxBytes < rune size")
	}
}

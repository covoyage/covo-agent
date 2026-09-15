package tlon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeShip emulates the Eyre endpoints used by the adapter:
//
//	POST /~/login        -> 200 + Set-Cookie
//	POST /~/channel/*    -> poke: 204; subscribe: SSE stream of chat facts
type fakeShip struct {
	mu        sync.Mutex
	pokes     [][]map[string]any
	subscribe [][]map[string]any
	updates   chan map[string]any
	srv       *httptest.Server
}

func newFakeShip(t *testing.T) *fakeShip {
	fs := &fakeShip{updates: make(chan map[string]any, 8)}
	mux := http.NewServeMux()

	mux.HandleFunc("/~/login", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("password") == "" {
			http.Error(w, "bad login", http.StatusBadRequest)
			return
		}
		// Set the raw header: "@" is an invalid cookie-name token, so both
		// http.SetCookie (server) and resp.Cookies() (client) would drop it.
		w.Header().Add("Set-Cookie", "@urbit-auth-cookie=0v3.testcookievalue")
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/~/channel/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var actions []map[string]any
		if json.Unmarshal(body, &actions) != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		isSubscribe := false
		for _, act := range actions {
			if act["action"] == "poke" {
				fs.mu.Lock()
				fs.pokes = append(fs.pokes, actions)
				fs.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if act["action"] == "subscribe" {
				isSubscribe = true
			}
		}
		if isSubscribe {
			fs.mu.Lock()
			fs.subscribe = append(fs.subscribe, actions)
			fs.mu.Unlock()
			// Keep the connection open and stream SSE data.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			w.Write([]byte(":ok\n\n"))
			if fl != nil {
				fl.Flush()
			}
			for {
				select {
				case u := <-fs.updates:
					data, _ := json.Marshal(u)
					w.Write([]byte("data: " + string(data) + "\n\n"))
					if fl != nil {
						fl.Flush()
					}
				case <-r.Context().Done():
					return
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})

	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *fakeShip) pushUpdate(u map[string]any) {
	fs.updates <- u
}

func (fs *fakeShip) pokeCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.pokes)
}

func (fs *fakeShip) lastPoke() []map[string]any {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.pokes) == 0 {
		return nil
	}
	return fs.pokes[len(fs.pokes)-1]
}

func (fs *fakeShip) lastSubscribe() []map[string]any {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.subscribe) == 0 {
		return nil
	}
	return fs.subscribe[len(fs.subscribe)-1]
}

func newTestAdapter(shipURL string) *Adapter {
	return &Adapter{
		shipURL:      shipURL,
		apiKey:       "lidlut-tabwed-pillex-ridrup",
		ourShip:      "~nus",
		defaultChann: "~zod/chat-1234",
		watchTargets: "~zod,~tex/chat-99",
		httpClient:   &http.Client{Timeout: 5 * time.Second},
		logger:       newTestLogger(),
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestLoginAndDMPoke verifies the chat-dm-action poke structure (aligned
// with the reference implementation extensions/tlon send.ts sendDmWithStory).
func TestLoginAndDMPoke(t *testing.T) {
	fs := newFakeShip(t)
	a := newTestAdapter(fs.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()

	if a.cookie() != "0v3.testcookievalue" {
		t.Fatalf("auth cookie = %q", a.cookie())
	}

	if err := a.SendMessage(ctx, "~zod", plugin.OutgoingMessage{Text: "hello urbit"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	waitFor(t, "poke", func() bool { return fs.pokeCount() > 0 })
	poke := fs.lastPoke()
	if len(poke) != 1 {
		t.Fatalf("poke actions = %d", len(poke))
	}
	act := poke[0]
	if act["action"] != "poke" || act["ship"] != "~nus" || act["app"] != "chat" {
		t.Errorf("poke action: %v", act)
	}
	if act["mark"] != "chat-dm-action" {
		t.Errorf("poke mark = %v, want chat-dm-action", act["mark"])
	}
	payload, ok := act["json"].(map[string]any)
	if !ok {
		t.Fatalf("poke json missing: %v", act)
	}
	if payload["ship"] != "~zod" {
		t.Errorf("json ship = %v, want ~zod (DM target)", payload["ship"])
	}
	diff, _ := payload["diff"].(map[string]any)
	if diff == nil {
		t.Fatalf("json diff missing: %v", payload)
	}
	// id = "<author>/<@ud sent-ms>" with dots every three digits.
	id, _ := diff["id"].(string)
	if !strings.HasPrefix(id, "~nus/1.") || !strings.Contains(id, ".") {
		t.Errorf("diff id = %q, want ~nus/<@ud timestamp>", id)
	}
	delta, _ := diff["delta"].(map[string]any)
	add, _ := delta["add"].(map[string]any)
	memo, _ := add["memo"].(map[string]any)
	if memo == nil {
		t.Fatalf("delta.add.memo missing: %v", diff)
	}
	if memo["author"] != "~nus" {
		t.Errorf("memo author = %v, want ~nus", memo["author"])
	}
	sent, ok := memo["sent"].(float64)
	if !ok || sent <= 0 {
		t.Errorf("memo sent = %v, want a millisecond timestamp", memo["sent"])
	}
	content, _ := memo["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("memo content verses = %d, want 1", len(content))
	}
	verse, _ := content[0].(map[string]any)
	inline, _ := verse["inline"].([]any)
	if len(inline) != 1 || inline[0] != "hello urbit" {
		t.Errorf("story inline verse = %v, want [{inline: [hello urbit]}]", verse)
	}
	if _, hasKind := add["kind"]; !hasKind {
		t.Errorf("delta.add.kind (null) missing: %v", add)
	}
}

// TestGroupPoke verifies the channel-action-1 poke structure for group
// channels (aligned with the reference implementation send.ts sendGroupMessageWithStory).
func TestGroupPoke(t *testing.T) {
	fs := newFakeShip(t)
	a := newTestAdapter(fs.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()

	if err := a.SendMessage(ctx, "~tex/chat-99", plugin.OutgoingMessage{Text: "hi group"}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	waitFor(t, "poke", func() bool { return fs.pokeCount() > 0 })
	act := fs.lastPoke()[0]
	if act["app"] != "channels" || act["mark"] != "channel-action-1" {
		t.Errorf("poke app/mark = %v / %v, want channels / channel-action-1", act["app"], act["mark"])
	}
	payload, _ := act["json"].(map[string]any)
	channel, _ := payload["channel"].(map[string]any)
	if channel == nil {
		t.Fatalf("json channel missing: %v", payload)
	}
	if channel["nest"] != "chat/~tex/chat-99" {
		t.Errorf("channel nest = %v, want chat/~tex/chat-99", channel["nest"])
	}
	action, _ := channel["action"].(map[string]any)
	post, _ := action["post"].(map[string]any)
	add, _ := post["add"].(map[string]any)
	if add == nil {
		t.Fatalf("channel.action.post.add missing: %v", channel)
	}
	if add["author"] != "~nus" || add["kind"] != "/chat" {
		t.Errorf("post add author/kind = %v / %v", add["author"], add["kind"])
	}
	content, _ := add["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("post add content = %v", add["content"])
	}
	verse, _ := content[0].(map[string]any)
	inline, _ := verse["inline"].([]any)
	if len(inline) != 1 || inline[0] != "hi group" {
		t.Errorf("post add story = %v", verse)
	}
}

// TestSubscribeBody verifies the SSE subscribe action body: ship/wire/app/
// path per the chat protocol.
func TestSubscribeBody(t *testing.T) {
	fs := newFakeShip(t)
	a := newTestAdapter(fs.srv.URL) // watch: ~zod (DM) + ~tex/chat-99 (group)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()

	waitFor(t, "subscription", func() bool {
		return len(fs.lastSubscribe()) == 2
	})
	subs := fs.lastSubscribe()
	if len(subs) != 2 {
		t.Fatalf("subscribe actions = %d, want 2", len(subs))
	}

	dm := subs[0]
	if dm["app"] != "chat" || dm["ship"] != "~nus" {
		t.Errorf("DM subscribe app/ship: %v", dm)
	}
	if dm["path"] != "/dm/~zod" {
		t.Errorf("DM subscribe path = %v, want /dm/~zod", dm["path"])
	}
	if dm["wire"] != "/chat/~nus/dm/~zod" {
		t.Errorf("DM subscribe wire = %v, want /chat/~nus/dm/~zod", dm["wire"])
	}

	group := subs[1]
	if group["app"] != "chat" || group["ship"] != "~nus" {
		t.Errorf("group subscribe app/ship: %v", group)
	}
	if group["path"] != "/channel/chat/~tex/chat-99" {
		t.Errorf("group subscribe path = %v, want /channel/chat/~tex/chat-99", group["path"])
	}
	if group["wire"] != "/chat/~nus/chat/~tex/chat-99" {
		t.Errorf("group subscribe wire = %v", group["wire"])
	}
}

// TestSubscribeReceivesChatUpdate verifies inbound SSE parsing: a
// chat-update fact carrying a diff/add memo with story content reaches the
// callback; the adapter's own echoes are dropped.
func TestSubscribeReceivesChatUpdate(t *testing.T) {
	fs := newFakeShip(t)
	a := newTestAdapter(fs.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()

	waitFor(t, "subscription", func() bool { return len(fs.lastSubscribe()) > 0 })

	got := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) {
		select {
		case got <- m:
		default:
		}
	})

	sent := time.Now().UnixMilli()
	fs.pushUpdate(map[string]any{
		"chat-update": map[string]any{
			"diff": map[string]any{
				"add": map[string]any{
					"memo": map[string]any{
						"author": "~zod",
						"sent":   sent,
						"content": []any{
							map[string]any{"inline": []any{"hi from the ship"}},
						},
					},
				},
			},
		},
	})

	select {
	case m := <-got:
		if m.Text != "hi from the ship" {
			t.Errorf("text = %q", m.Text)
		}
		if m.UserID != "~zod" {
			t.Errorf("userID = %q", m.UserID)
		}
		if m.ChannelID != "~zod" {
			t.Errorf("channelID = %q, want DM target ~zod", m.ChannelID)
		}
		if !m.Timestamp.Equal(time.UnixMilli(sent)) {
			t.Errorf("timestamp = %v, want %v", m.Timestamp, time.UnixMilli(sent))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("callback not invoked")
	}
}

// TestSubscribeIgnoresSelfEcho verifies our own outbound memo echoes are
// not forwarded.
func TestSubscribeIgnoresSelfEcho(t *testing.T) {
	fs := newFakeShip(t)
	a := newTestAdapter(fs.srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = a.Stop() }()

	waitFor(t, "subscription", func() bool { return len(fs.lastSubscribe()) > 0 })

	got := make(chan plugin.IncomingMessage, 4)
	a.OnMessage(func(m plugin.IncomingMessage) {
		select {
		case got <- m:
		default:
		}
	})

	fs.pushUpdate(map[string]any{
		"chat-update": map[string]any{
			"diff": map[string]any{
				"add": map[string]any{
					"memo": map[string]any{
						"author":  "~nus",
						"sent":    time.Now().UnixMilli(),
						"content": []any{map[string]any{"inline": []any{"self echo"}}},
					},
				},
			},
		},
	})

	select {
	case m := <-got:
		t.Errorf("self echo forwarded: %+v", m)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestResolveTarget verifies channelID parsing: "~ship/name" is a group,
// a bare ship is a DM.
func TestResolveTarget(t *testing.T) {
	a := newTestAdapter("http://unused")

	tgt, err := a.resolveTarget("~zod/chat-1234")
	if err != nil || tgt.kind != kindGroup || tgt.hostShip != "~zod" || tgt.name != "chat-1234" {
		t.Errorf("group target: %+v %v", tgt, err)
	}
	if tgt.nest() != "chat/~zod/chat-1234" {
		t.Errorf("nest = %q", tgt.nest())
	}

	tgt, err = a.resolveTarget("tex/general")
	if err != nil || tgt.kind != kindGroup || tgt.hostShip != "~tex" || tgt.name != "general" {
		t.Errorf("no-tilde group: %+v %v", tgt, err)
	}

	tgt, err = a.resolveTarget("~zod")
	if err != nil || tgt.kind != kindDM || tgt.ship != "~zod" {
		t.Errorf("DM target: %+v %v", tgt, err)
	}

	tgt, err = a.resolveTarget("zod")
	if err != nil || tgt.kind != kindDM || tgt.ship != "~zod" {
		t.Errorf("bare DM target: %+v %v", tgt, err)
	}

	tgt, err = a.resolveTarget("")
	if err != nil || tgt.kind != kindGroup || tgt.hostShip != "~zod" || tgt.name != "chat-1234" {
		t.Errorf("default from env: %+v %v", tgt, err)
	}

	a2 := &Adapter{ourShip: "~nus", logger: newTestLogger()}
	if _, err := a2.resolveTarget(""); err == nil {
		t.Error("expected error when no channel and no default")
	}
}

// TestFormatUd verifies @ud formatting (dots every three digits).
func TestFormatUd(t *testing.T) {
	cases := map[int64]string{
		0:             "0",
		7:             "7",
		70:            "70",
		700:           "700",
		7000:          "7.000",
		1700000000000: "1.700.000.000.000",
	}
	for in, want := range cases {
		if got := formatUd(in); got != want {
			t.Errorf("formatUd(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestStoryToText verifies story rendering for inline, mention and code
// verses.
func TestStoryToText(t *testing.T) {
	story := []any{
		map[string]any{"inline": []any{"see ", map[string]any{"ship": "~zod"}, " and ", map[string]any{"link": map[string]any{"href": "https://x", "content": "x site"}}}},
		map[string]any{"block": map[string]any{"code": map[string]any{"lang": "go", "code": "println()"}}},
	}
	got := storyToText(story)
	if !strings.Contains(got, "see ~zod and x site") {
		t.Errorf("inline rendering = %q", got)
	}
	if !strings.Contains(got, "```go\nprintln()\n```") {
		t.Errorf("code block rendering = %q", got)
	}
	if storyToText("not a list") != "" {
		t.Error("non-story content should render empty")
	}
}

// TestChannelUIDFormat verifies the per-connection channel UID shape.
func TestChannelUIDFormat(t *testing.T) {
	a := newTestAdapter("http://unused")
	uid := a.channelUID()
	if !strings.HasPrefix(uid, "~nus/") {
		t.Errorf("channel UID = %q, want ~nus/<epoch>", uid)
	}
	suffix := strings.TrimPrefix(uid, "~nus/")
	if _, err := strconv.ParseInt(suffix, 10, 64); err != nil {
		t.Errorf("channel UID epoch = %q, want an integer", suffix)
	}
}

func TestShipNameFromURLFallback(t *testing.T) {
	t.Setenv("TLON_SHIP_URL", "http://zod.arvo.network")
	t.Setenv("TLON_API_KEY", "code")
	t.Setenv("TLON_SHIP", "")
	t.Setenv("TLON_CHANNEL_NAME", "")
	a := New()
	if a.ourShip != "~zod" {
		t.Errorf("ourShip = %q, want ~zod from URL host", a.ourShip)
	}
	if a.defaultChann != "" {
		t.Errorf("defaultChann = %q", a.defaultChann)
	}
	_ = os.Unsetenv("TLON_SHIP_URL")
}

// Package tlon implements a Urbit (Tlon) ship platform adapter over the Eyre
// HTTP channel API, speaking the current chat protocol (app "chat" /
// app "channels"). The poke and subscribe payload structures are aligned
// with openclaw extensions/tlon (src/urbit/send.ts, src/urbit/sse-client.ts
// and src/monitor/).
//
// Environment:
//
//	TLON_SHIP_URL      base URL of the ship, e.g. http://ship.example.com (required)
//	TLON_API_KEY       the ship's +login code (required)
//	TLON_SHIP          our own ship name (e.g. "~zod"); optional, used as the
//	                   subscriber ship and the outbound author. If unset, the
//	                   first DNS label of the ship URL host is used as a
//	                   best-effort fallback.
//	TLON_CHANNEL_NAME  default group channel ("<ship>/<name>", e.g.
//	                   "~zod/chat-1234"); used as the outbound target when a
//	                   channelID is empty and as a watch target.
//	TLON_WATCH         comma-separated watch targets to subscribe inbound
//	                   events for ("~ship" for DMs, "~host/name" for group
//	                   channels).
//
// Outbound DMs poke app "chat" with mark "chat-dm-action":
//
//	{"ship": <target>, "diff": {"id": "<self>/<@ud sent-ms>", "delta":
//	 {"add": {"memo": {"content": <story>, "author": <self>, "sent": <ms>},
//	  "kind": null, "time": null}}}}
//
// Outbound group messages poke app "channels" with mark "channel-action-1":
//
//	{"channel": {"nest": "chat/<host>/<name>", "action": {"post":
//	 {"add": {"content": <story>, "author": <self>, "sent": <ms>,
//	  "kind": "/chat", "blob": null, "meta": null}}}}}
//
// Inbound: an Eyre channel subscription to app "chat" (DM path /dm/~<ship>
// or group path /channel/chat/<host>/<name>), consumed as a
// text/event-stream; chat-update facts carrying a diff/add memo with story
// content are extracted and forwarded to the message callback.
package tlon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/plugin"
)

const (
	PlatformName = "tlon"

	defaultChannelName = "chat"
	reconnectMin       = 1 * time.Second
	reconnectMax       = 30 * time.Second
	stableConnAfter    = time.Minute
)

type Adapter struct {
	shipURL      string // e.g. http://ship.example.com
	apiKey       string // login code
	ourShip      string // e.g. ~zod
	defaultChann string // default group channel, e.g. ~zod/chat-1234
	watchTargets string // TLON_WATCH: comma-separated inbound watch targets
	httpClient   *http.Client
	logger       *slog.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(plugin.IncomingMessage)

	authMu   sync.Mutex
	authCook string // @urbit-auth-cookie value
}

func New() *Adapter {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logutil.ResolveLevel(slog.LevelInfo)}))

	shipURL := strings.TrimRight(strings.TrimSpace(os.Getenv("TLON_SHIP_URL")), "/")
	ourShip := strings.TrimSpace(os.Getenv("TLON_SHIP"))
	if ourShip == "" {
		// Best-effort: use the first DNS label of the ship host (e.g.
		// "zod" from "zod.arvo.network"). Ships named after their URL label
		// is a common hosting convention; override with TLON_SHIP.
		if u, err := url.Parse(shipURL); err == nil {
			host := u.Hostname()
			if i := strings.Index(host, "."); i > 0 {
				host = host[:i]
			}
			if host != "" {
				ourShip = "~" + strings.TrimPrefix(host, "~")
			}
		}
	}

	return &Adapter{
		shipURL:      shipURL,
		apiKey:       strings.TrimSpace(os.Getenv("TLON_API_KEY")),
		ourShip:      ourShip,
		defaultChann: strings.TrimSpace(os.Getenv("TLON_CHANNEL_NAME")),
		watchTargets: strings.TrimSpace(os.Getenv("TLON_WATCH")),
		httpClient:   &http.Client{Timeout: 30 * time.Second},
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
	if a.shipURL == "" {
		return fmt.Errorf("TLON_SHIP_URL not set in environment")
	}
	if a.apiKey == "" {
		return fmt.Errorf("TLON_API_KEY not set in environment")
	}
	return nil
}

func (a *Adapter) OnMessage(callback func(plugin.IncomingMessage)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onMessage = callback
}

func (a *Adapter) callback(in plugin.IncomingMessage) {
	a.mu.Lock()
	cb := a.onMessage
	a.mu.Unlock()
	if cb != nil {
		cb(in)
	}
}

func (a *Adapter) Start(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("tlon: validation failed: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("tlon: already running")
	}
	a.running = true
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	if err := a.login(ctx); err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		return fmt.Errorf("tlon: login failed: %w", err)
	}

	a.logger.Info("tlon: adapter started", "ship", a.shipURL)

	go a.subscribeLoop(ctx)
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}
	a.running = false
	if a.cancel != nil {
		a.cancel()
	}
	a.logger.Info("tlon: adapter stopped")
	return nil
}

func (a *Adapter) Send(ctx context.Context, channelID string, text string) error {
	return a.SendMessage(ctx, channelID, plugin.OutgoingMessage{Text: text})
}

// targetKind distinguishes DM and group channel targets.
type targetKind string

const (
	kindDM    targetKind = "dm"
	kindGroup targetKind = "group"
)

// tlonTarget is a resolved outbound/inbound target. Group channels are
// "<host>/<name>" (host includes the leading ~); everything else is a DM
// ship (structure aligned with openclaw extensions/tlon targets.ts).
type tlonTarget struct {
	kind     targetKind
	ship     string // DM target ship, includes ~
	hostShip string // group host ship, includes ~
	name     string // group channel name
}

// nest returns the group channel nest "chat/<host>/<name>".
func (t tlonTarget) nest() string {
	return "chat/" + t.hostShip + "/" + t.name
}

// channelID renders the target as a "<ship>" or "<host>/<name>" string.
func (t tlonTarget) channelID() string {
	if t.kind == kindGroup {
		return t.hostShip + "/" + t.name
	}
	return t.ship
}

// resolveTarget parses a channelID: "<ship>/<name>" addresses a group
// channel, a bare "<ship>" addresses a DM. An empty channelID falls back to
// TLON_CHANNEL_NAME (group) when set.
func (a *Adapter) resolveTarget(channelID string) (tlonTarget, error) {
	spec := strings.TrimSpace(channelID)
	if spec == "" {
		spec = a.defaultChann
	}
	if spec == "" {
		return tlonTarget{}, fmt.Errorf("no channel ID and TLON_CHANNEL_NAME not set")
	}
	if i := strings.Index(spec, "/"); i >= 0 {
		host := "~" + strings.TrimPrefix(strings.TrimSpace(spec[:i]), "~")
		name := strings.TrimSpace(spec[i+1:])
		if name == "" {
			name = defaultChannelName
		}
		if host == "~" {
			return tlonTarget{}, fmt.Errorf("cannot determine target from channel %q", channelID)
		}
		return tlonTarget{kind: kindGroup, hostShip: host, name: name}, nil
	}
	return tlonTarget{kind: kindDM, ship: "~" + strings.TrimPrefix(spec, "~")}, nil
}

// SendMessage posts a text message via a chat poke: DMs poke app "chat"
// (mark "chat-dm-action"), group channels poke app "channels"
// (mark "channel-action-1"). The payload structures are aligned with
// openclaw extensions/tlon src/urbit/send.ts.
func (a *Adapter) SendMessage(ctx context.Context, channelID string, msg plugin.OutgoingMessage) error {
	if msg.Text == "" {
		return nil
	}
	if err := a.Validate(); err != nil {
		return fmt.Errorf("tlon: %w", err)
	}

	target, err := a.resolveTarget(channelID)
	if err != nil {
		return fmt.Errorf("tlon: %w", err)
	}

	author := a.ourShip
	if author == "" {
		if target.kind == kindGroup {
			author = target.hostShip
		} else {
			return fmt.Errorf("tlon: TLON_SHIP not set; cannot determine outbound author")
		}
	}

	sentAt := time.Now().UnixMilli()
	// Story: a single inline verse carrying the plain text (openclaw
	// markdownToStory for plain text yields [{inline: [text]}]).
	story := []map[string]any{{"inline": []any{msg.Text}}}

	var app, mark string
	var payload map[string]any
	if target.kind == kindDM {
		// chat-dm-action: id is "<author>/<@ud sent>" (@ud = dots every 3
		// digits), per openclaw send.ts sendDmWithStory.
		app = "chat"
		mark = "chat-dm-action"
		payload = map[string]any{
			"ship": target.ship,
			"diff": map[string]any{
				"id": author + "/" + formatUd(sentAt),
				"delta": map[string]any{
					"add": map[string]any{
						"memo": map[string]any{
							"content": story,
							"author":  author,
							"sent":    sentAt,
						},
						"kind": nil,
						"time": nil,
					},
				},
			},
		}
	} else {
		app = "channels"
		mark = "channel-action-1"
		payload = map[string]any{
			"channel": map[string]any{
				"nest": target.nest(),
				"action": map[string]any{
					"post": map[string]any{
						"add": map[string]any{
							"content": story,
							"author":  author,
							"sent":    sentAt,
							"kind":    "/chat",
							"blob":    nil,
							"meta":    nil,
						},
					},
				},
			},
		}
	}

	action := map[string]any{
		"id":     1,
		"action": "poke",
		"ship":   a.selfShip(),
		"app":    app,
		"mark":   mark,
		"json":   payload,
	}

	if err := a.poke(ctx, action); err != nil {
		return fmt.Errorf("tlon: poke %s: %w", target.channelID(), err)
	}
	return nil
}

// selfShip returns our ship name with a best-effort default; Eyre requires
// a ship value on channel actions.
func (a *Adapter) selfShip() string {
	if a.ourShip != "" {
		return a.ourShip
	}
	return "zod"
}

// formatUd renders an integer as an Urbit @ud atom: decimal digits with a
// dot every three digits from the right (e.g. 1700000000000 ->
// "1.700.000.000.000").
func formatUd(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// login exchanges the +code for an auth cookie via POST /~/login.
func (a *Adapter) login(ctx context.Context) error {
	form := url.Values{"password": {a.apiKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.shipURL+"/~/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("login failed (status %d)", resp.StatusCode)
	}

	// The Urbit auth cookie is named "@urbit-auth-cookie"; "@" is not a valid
	// cookie-name token, so Go's resp.Cookies() drops it. Parse the raw
	// Set-Cookie headers instead.
	for _, sc := range resp.Header["Set-Cookie"] {
		if i := strings.Index(sc, "="); i > 0 {
			value := sc[i+1:]
			if j := strings.Index(value, ";"); j >= 0 {
				value = value[:j]
			}
			value = strings.TrimSpace(value)
			if value != "" {
				a.authMu.Lock()
				a.authCook = value
				a.authMu.Unlock()
				return nil
			}
		}
	}
	return fmt.Errorf("no auth cookie in login response")
}

// cookie returns the current auth cookie value.
func (a *Adapter) cookie() string {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	return a.authCook
}

// channelUID generates a per-connection Eyre channel UID of the form
// "~ship/epoch" (aligned with openclaw sse-client channel naming).
func (a *Adapter) channelUID() string {
	ship := strings.TrimPrefix(a.selfShip(), "~")
	return fmt.Sprintf("~%s/%d", ship, time.Now().UnixNano())
}

// channelRequest POSTs Eyre channel actions and returns the (still-open,
// unread) HTTP response so the caller can consume an SSE stream if desired.
func (a *Adapter) channelRequest(ctx context.Context, actions []map[string]any) (*http.Response, error) {
	body, err := json.Marshal(actions)
	if err != nil {
		return nil, fmt.Errorf("marshal actions: %w", err)
	}

	endpoint := fmt.Sprintf("%s/~/channel/%s", a.shipURL, a.channelUID())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c := a.cookie(); c != "" {
		req.Header.Set("Cookie", "@urbit-auth-cookie="+c)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("channel request failed (status %d): %s", resp.StatusCode, string(b))
	}
	return resp, nil
}

// poke sends a single poke action and discards the response.
func (a *Adapter) poke(ctx context.Context, action map[string]any) error {
	resp, err := a.channelRequest(ctx, []map[string]any{action})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// watchSpec is one inbound subscription derived from a watch target.
type watchSpec struct {
	target tlonTarget
	wire   string
	path   string
}

// watchSpecs resolves the inbound watch targets into Eyre subscriptions:
// DM targets subscribe to "/dm/~<ship>", group channels to
// "/channel/chat/<host>/<name>"; the wire is "/chat/<self>/<dm-or-nest>".
func (a *Adapter) watchSpecs() []watchSpec {
	raw := a.watchTargets
	if raw == "" {
		raw = a.defaultChann
	}
	if raw == "" {
		return nil
	}
	specs := []watchSpec{}
	for _, item := range strings.Split(raw, ",") {
		target, err := a.resolveTarget(item)
		if err != nil {
			a.logger.Warn("tlon: ignoring invalid watch target", "target", item, "error", err)
			continue
		}
		if target.kind == kindDM {
			specs = append(specs, watchSpec{
				target: target,
				wire:   "/chat/" + a.selfShip() + "/dm/" + target.ship,
				path:   "/dm/" + target.ship,
			})
		} else {
			specs = append(specs, watchSpec{
				target: target,
				wire:   "/chat/" + a.selfShip() + "/" + target.nest(),
				path:   "/channel/" + target.nest(),
			})
		}
	}
	return specs
}

// subscribeLoop maintains SSE subscriptions to app "chat", reconnecting
// with exponential backoff until ctx is cancelled.
func (a *Adapter) subscribeLoop(ctx context.Context) {
	backoff := reconnectMin
	for {
		start := time.Now()
		err := a.subscribeSession(ctx)
		if ctx.Err() != nil {
			return
		}
		a.logger.Warn("tlon: subscription disconnected, reconnecting", "error", err)

		if time.Since(start) > stableConnAfter {
			backoff = reconnectMin
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

// subscribeSession opens a channel, issues subscribe actions for the watch
// targets and consumes the SSE stream until an error occurs.
func (a *Adapter) subscribeSession(ctx context.Context) error {
	specs := a.watchSpecs()
	if len(specs) == 0 {
		return fmt.Errorf("no watch targets configured (set TLON_WATCH or TLON_CHANNEL_NAME)")
	}

	actions := make([]map[string]any, 0, len(specs))
	for i, spec := range specs {
		actions = append(actions, map[string]any{
			"id":     i + 1,
			"action": "subscribe",
			"ship":   a.selfShip(),
			"wire":   spec.wire,
			"app":    "chat",
			"path":   spec.path,
		})
	}

	resp, err := a.channelRequest(ctx, actions)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read SSE: %w", err)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			a.handleSSEData(strings.TrimSpace(data), specs)
		} else if data, ok := strings.CutPrefix(line, "data:"); ok {
			a.handleSSEData(strings.TrimSpace(data), specs)
		}
	}
}

// handleSSEData parses one SSE data payload and dispatches chat facts to
// the message callback. Facts arrive as JSON objects (optionally wrapped,
// e.g. in a "chat-update" key); we look for diff/add memo structures and
// essay structures (chat firehose shape) carrying story content.
func (a *Adapter) handleSSEData(data string, specs []watchSpec) {
	if data == "" || data == "...listening..." {
		return
	}
	var top any
	if err := json.Unmarshal([]byte(data), &top); err != nil {
		a.logger.Debug("tlon: ignoring non-JSON SSE data")
		return
	}

	for _, spec := range specs {
		a.dispatchFacts(top, spec)
	}
}

// dispatchFacts walks a decoded fact for memo/essay message structures and
// forwards found messages attributed to the subscription target.
func (a *Adapter) dispatchFacts(fact any, spec watchSpec) {
	memos := findMemos(fact)
	for _, memo := range memos {
		author, _ := memo["author"].(string)
		if author == "" || author == a.ourShip {
			// Ignore our own outbound echoes.
			continue
		}
		text := storyToText(memo["content"])
		if strings.TrimSpace(text) == "" {
			continue
		}
		ts := time.Now()
		if sent, ok := memo["sent"].(float64); ok && sent > 0 {
			ts = time.UnixMilli(int64(sent))
		}
		a.callback(plugin.IncomingMessage{
			Platform:  PlatformName,
			ChannelID: spec.target.channelID(),
			UserID:    author,
			UserName:  author,
			Text:      text,
			Timestamp: ts,
			Raw:       memo,
		})
	}
}

// findMemos recursively collects "memo" objects reached via a "diff.add"
// path (chat-update shape) plus "essay" objects (chat /v3 firehose shape).
func findMemos(v any) []map[string]any {
	var out []map[string]any
	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case []any:
			for _, item := range n {
				walk(item)
			}
		case map[string]any:
			if diff, ok := n["diff"].(map[string]any); ok {
				if add, ok := diff["add"].(map[string]any); ok {
					if memo, ok := add["memo"].(map[string]any); ok {
						out = append(out, memo)
						return
					}
				}
			}
			if essay, ok := n["essay"].(map[string]any); ok {
				out = append(out, essay)
			}
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(v)
	return out
}

// storyToText renders a story (list of verses) to plain text, aligned with
// openclaw tlon monitor utils extractMessageText for the common cases.
func storyToText(content any) string {
	verses, ok := content.([]any)
	if !ok {
		return ""
	}
	var lines []string
	for _, verse := range verses {
		v, ok := verse.(map[string]any)
		if !ok {
			continue
		}
		if inline, ok := v["inline"].([]any); ok {
			var b strings.Builder
			for _, item := range inline {
				b.WriteString(renderInlineItem(item))
			}
			lines = append(lines, b.String())
			continue
		}
		if block, ok := v["block"].(map[string]any); ok {
			if img, ok := block["image"].(map[string]any); ok {
				if src, _ := img["src"].(string); src != "" {
					alt, _ := img["alt"].(string)
					if alt != "" {
						lines = append(lines, "\n"+src+" ("+alt+")\n")
					} else {
						lines = append(lines, "\n"+src+"\n")
					}
				}
				continue
			}
			if code, ok := block["code"].(map[string]any); ok {
				lang, _ := code["lang"].(string)
				body, _ := code["code"].(string)
				lines = append(lines, "\n```"+lang+"\n"+body+"\n```\n")
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// renderInlineItem renders one story inline item: plain strings pass
// through; ship mentions, links and inline code render to their text.
func renderInlineItem(item any) string {
	switch v := item.(type) {
	case string:
		return v
	case map[string]any:
		if s, ok := v["ship"].(string); ok {
			return s
		}
		if s, ok := v["inline-code"].(string); ok {
			return s
		}
		if link, ok := v["link"].(map[string]any); ok {
			if content, _ := link["content"].(string); content != "" {
				return content
			}
			href, _ := link["href"].(string)
			return href
		}
		return ""
	default:
		return ""
	}
}

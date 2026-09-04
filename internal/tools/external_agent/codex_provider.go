package externalagent

// codexProvider drives the OpenAI Codex CLI over its app-server protocol
// (`codex app-server --stdio`), the same JSON-RPC-over-stdio transport used
// by the official Codex SDKs and the VS Code extension. The app-server owns
// the full agent loop (tool execution, context management, sandbox); this
// provider implements the client side: the initialize handshake, one
// ephemeral thread, one turn, unattended approval decisions, and
// final-answer selection.
//
// Wire format: JSON-RPC "lite" over newline-delimited JSON on stdin/stdout
// (the "jsonrpc" header is omitted):
//
//	out:  {"id":1,"method":"initialize","params":{...}}
//	in:   {"id":1,"result":{...}}
//	out:  {"method":"initialized","params":{}}
//	out:  {"id":2,"method":"thread/start","params":{"cwd":...,"ephemeral":true}}
//	in:   {"id":2,"result":{"thread":{"id":"thr_..","ephemeral":true}}}
//	out:  {"id":3,"method":"turn/start","params":{"threadId":"thr_..","input":[{...}]}}
//	in:   {"id":99,"method":"item/commandExecution/requestApproval","params":{...}}   (server request, must reply)
//	out:  {"id":99,"result":{"decision":"cancel"}}
//	in:   {"method":"item/completed","params":{"item":{"type":"agentMessage","text":"..","phase":"final_answer"}}}
//	in:   {"method":"turn/completed","params":{"threadId":"thr_..","turn":{"id":"..","status":"completed"}}}
//	out:  {"id":4,"method":"turn/interrupt","params":{"threadId":"thr_..","turnId":".."}}

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// minCodexVersion is the oldest Codex CLI that ships the app-server --stdio
// transport used here.
const minCodexVersion = "0.136.0"

// codexInitTimeout bounds the handshake (initialize + thread + turn start).
const codexInitTimeout = 30 * time.Second

// codexResultGrace is how long to wait for a terminal turn/completed after
// sending turn/interrupt before killing the app-server process.
const codexResultGrace = 20 * time.Second

var codexIDCounter atomic.Int64

type codexProvider struct{}

// codexResult is what the reader loop reports back to the run loop.
type codexResult struct {
	text    string
	isError bool
	err     error
}

func (p *codexProvider) Name() string { return "codex" }

func (p *codexProvider) Available() (string, bool) {
	if _, err := exec.LookPath("codex"); err != nil {
		return "codex CLI not found on PATH (install and sign in to use this provider)", false
	}
	return "", true
}

func (p *codexProvider) Run(ctx context.Context, task, cwd string) (string, error) {
	return p.RunOpt(ctx, task, cwd, RunOptions{})
}

func (p *codexProvider) RunOpt(ctx context.Context, task, cwd string, opts RunOptions) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("codex: empty task")
	}
	if err := p.verifyVersion(ctx); err != nil {
		return "", err
	}
	if opts.Cwd != "" {
		cwd = opts.Cwd
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("external agent %q: %w", "codex", err)
	}
	// Note: NOT CommandContext — cancelling ctx must not SIGKILL the app-server
	// before we can deliver turn/interrupt. Lifecycle is managed manually
	// below (interrupt protocol, then kill as a fallback).
	cmd := exec.Command(path, "app-server", "--stdio")
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("codex: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("codex: stdout pipe: %w", err)
	}
	stderr := newBoundedBuffer(64 << 10)
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("codex: start: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	// waited is closed once the process has fully exited and its stderr has
	// been flushed. cmd.Wait() is owned by the reader goroutine (see below):
	// it must run only after stdout has been read to EOF, because Wait closes
	// the stdout pipe's read end.
	waited := make(chan struct{})

	conn := &codexConn{
		stdin:    stdin,
		pending:  make(map[int64]chan json.RawMessage),
		resultCh: make(chan codexResult, 1),
		allowAll: opts.PermissionMode == "bypassPermissions",
		fatalCh:  make(chan error, 1),
	}

	readerDone := make(chan struct{})
	var readerErr error
	go func() {
		defer close(readerDone)
		readerErr = conn.readLoop(stdout)
		// stdout is drained to EOF. Only now is it safe to call cmd.Wait():
		// it closes the stdout pipe's read end (no reads pending) and blocks
		// until the stderr copy goroutine has flushed, so the stderr tail read
		// below is complete.
		_ = cmd.Wait()
		close(waited)
		if !conn.resultSent.Load() {
			// The process exited (or the stream died) before settling the
			// turn — fail every in-flight request so the run loop and the
			// handshake do not hang waiting for a reply that never comes.
			msg := "codex app-server exited without producing a result"
			if readerErr != nil && readerErr != io.EOF {
				msg = readerErr.Error()
			}
			if tail := strings.TrimSpace(stderr.String()); tail != "" {
				msg += ": " + truncate(tail, 2000)
			}
			conn.fail(errors.New(msg))
		}
	}()

	// Handshake, thread creation, and turn start share one timeout so a
	// wedged app-server fails fast instead of hanging the tool.
	initCtx, initCancel := context.WithTimeout(ctx, codexInitTimeout)
	defer initCancel()
	if err := conn.initialize(initCtx); err != nil {
		return "", fmt.Errorf("codex: initialize: %w", err)
	}
	if err := conn.startThread(initCtx, cwd, opts.Model); err != nil {
		return "", fmt.Errorf("codex: thread/start: %w", err)
	}
	if err := conn.startTurn(initCtx, task, opts.Model); err != nil {
		return "", fmt.Errorf("codex: turn/start: %w", err)
	}

	// Wait for the authoritative terminal turn/completed, or interrupt on
	// cancellation.
	var res codexResult
	select {
	case res = <-conn.resultCh:
	case err := <-conn.fatalCh:
		return "", fmt.Errorf("codex: %w", err)
	case <-ctx.Done():
		conn.interrupt()
		select {
		case res = <-conn.resultCh:
		case err := <-conn.fatalCh:
			return "", fmt.Errorf("codex: interrupted: %w", err)
		case <-time.After(codexResultGrace):
			_ = cmd.Process.Kill()
			<-readerDone
			return "", fmt.Errorf("codex: interrupted: %w", ctx.Err())
		}
	}

	// Close stdin to end the wire; wait for the app-server to exit.
	_ = stdin.Close()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-waited
	}

	if res.isError {
		if ctx.Err() != nil {
			return "", fmt.Errorf("codex: interrupted: %w", ctx.Err())
		}
		return "", res.err
	}
	return strings.TrimSpace(res.text), nil
}

func (p *codexProvider) verifyVersion(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "codex", "--version").Output()
	if err != nil {
		return fmt.Errorf("codex: version check failed: %w", err)
	}
	got := firstVersionToken(strings.TrimSpace(string(out)))
	if !versionAtLeast(got, minCodexVersion) {
		return fmt.Errorf("codex: CLI version %s is too old; app-server --stdio protocol requires >= %s (upgrade Codex CLI)", got, minCodexVersion)
	}
	return nil
}

// firstVersionToken extracts the first parseable dotted version from output
// like "0.147.0" or "codex-cli 0.147.0".
func firstVersionToken(s string) string {
	for _, tok := range strings.Fields(s) {
		tok = strings.TrimPrefix(tok, "v")
		if _, ok := parseVersion(tok); ok {
			return tok
		}
	}
	return s
}

// codexConn owns the JSON-RPC reader loop, request correlation, server
// request handling (approvals), and terminal-answer selection.
type codexConn struct {
	stdin io.WriteCloser

	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[int64]chan json.RawMessage

	resultCh   chan codexResult
	resultSent atomic.Bool

	allowAll bool // bypassPermissions: auto-approve command/file approvals

	mu           sync.Mutex
	threadID     string
	turnID       string
	lastFinal    string
	lastUnphased string

	fatalCh   chan error
	fatalOnce sync.Once
}

func (c *codexConn) fail(err error) {
	c.fatalOnce.Do(func() {
		select {
		case c.fatalCh <- err:
		default:
		}
	})
}

func (c *codexConn) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// request sends a client method and waits for its correlated response.
func (c *codexConn) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := codexIDCounter.Add(1)
	ch := make(chan json.RawMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.writeJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case line := <-ch:
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(line, &resp)
		if resp.Error != nil {
			return nil, fmt.Errorf("server error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-c.fatalCh:
		return nil, err
	}
}

// initialize completes the required initialize → initialized handshake.
func (c *codexConn) initialize(ctx context.Context) error {
	if _, err := c.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "covo-agent",
			"title":   "Covo Agent",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": false,
		},
	}); err != nil {
		return err
	}
	return c.writeJSON(map[string]any{"method": "initialized", "params": map[string]any{}})
}

// startThread creates the run's private ephemeral thread in cwd.
func (c *codexConn) startThread(ctx context.Context, cwd, model string) error {
	params := map[string]any{"ephemeral": true}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if model != "" {
		params["model"] = model
	}
	resp, err := c.request(ctx, "thread/start", params)
	if err != nil {
		return err
	}
	var r struct {
		Thread struct {
			ID        string `json:"id"`
			Ephemeral bool   `json:"ephemeral"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("invalid thread/start response: %w", err)
	}
	if r.Thread.ID == "" {
		return errors.New("invalid thread/start response: missing thread id")
	}
	if !r.Thread.Ephemeral {
		return errors.New("app-server did not create an ephemeral thread")
	}
	c.mu.Lock()
	c.threadID = r.Thread.ID
	c.mu.Unlock()
	return nil
}

// startTurn submits the task and records the turn id used for interrupts.
func (c *codexConn) startTurn(ctx context.Context, task, model string) error {
	c.mu.Lock()
	threadID := c.threadID
	c.mu.Unlock()
	params := map[string]any{
		"threadId": threadID,
		"input": []map[string]any{{
			"type":          "text",
			"text":          task,
			"text_elements": []any{},
		}},
	}
	if model != "" {
		params["model"] = model
	}
	if c.allowAll {
		// Ask the server to surface every approval request so our
		// bypassPermissions allow-all decision can actually approve them.
		params["approvalPolicy"] = "onRequest"
	}
	resp, err := c.request(ctx, "turn/start", params)
	if err != nil {
		return err
	}
	var r struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return fmt.Errorf("invalid turn/start response: %w", err)
	}
	if r.Turn.ID == "" {
		return errors.New("invalid turn/start response: missing turn id")
	}
	c.mu.Lock()
	c.turnID = r.Turn.ID
	c.mu.Unlock()
	return nil
}

// interrupt best-effort cancels the in-flight turn. Local settlement and
// process teardown remain authoritative when the wire is already broken.
func (c *codexConn) interrupt() {
	c.mu.Lock()
	threadID, turnID := c.threadID, c.turnID
	c.mu.Unlock()
	if threadID == "" || turnID == "" {
		return
	}
	_ = c.writeJSON(map[string]any{
		"id":     codexIDCounter.Add(1),
		"method": "turn/interrupt",
		"params": map[string]any{"threadId": threadID, "turnId": turnID},
	})
}

// readLoop consumes the app-server's stdout JSON-lines stream: routing
// responses, answering server requests, and observing terminal events.
func (c *codexConn) readLoop(stdout io.Reader) error {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256<<10), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Non-JSON noise on stdout: ignore.
			continue
		}
		if method := rawString(msg["method"]); method != "" {
			if id, ok := msg["id"]; ok {
				// Server request: must reply or the turn stalls.
				c.handleServerRequest(id, method, msg["params"])
			} else {
				c.handleNotification(method, msg["params"])
			}
			continue
		}
		// Correlated response.
		rawID, ok := msg["id"]
		if !ok {
			continue
		}
		var id int64
		if err := json.Unmarshal(rawID, &id); err != nil {
			continue
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[id]
		delete(c.pending, id)
		c.pendingMu.Unlock()
		if ok {
			select {
			case ch <- line:
			default:
			}
		}
	}
	if err := scanner.Err(); err != nil {
		c.fail(fmt.Errorf("codex: reading stdout: %w", err))
		return err
	}
	return io.EOF
}

func (c *codexConn) handleServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	var result any
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		result = map[string]any{"decision": c.approvalDecision(params)}
	case "item/permissions/requestApproval":
		// Grant nothing; a delegated run never needs a permission upgrade.
		result = map[string]any{"permissions": map[string]any{}, "scope": "turn"}
	case "item/tool/requestUserInput":
		result = map[string]any{"answers": map[string]any{}}
	case "mcpServer/elicitation/request":
		result = map[string]any{"action": "decline", "content": nil, "_meta": nil}
	default:
		// No user interface exists to answer an unexpected server request.
		c.fail(fmt.Errorf("codex: unsupported server request %q", method))
		c.writeResponse(id, nil, -32601, "method not found")
		return
	}
	c.writeResponse(id, result, 0, "")
}

// approvalDecision picks the unattended reply for a command/file approval:
// bypassPermissions allows, otherwise the request's own offered
// non-approval decision is preferred (cancel, then decline), falling back to
// decline when the request offers none.
func (c *codexConn) approvalDecision(params json.RawMessage) string {
	if c.allowAll {
		return "allow"
	}
	var p struct {
		AvailableDecisions []string `json:"availableDecisions"`
	}
	_ = json.Unmarshal(params, &p)
	if len(p.AvailableDecisions) == 0 {
		return "decline"
	}
	for _, d := range p.AvailableDecisions {
		if d == "cancel" {
			return "cancel"
		}
	}
	for _, d := range p.AvailableDecisions {
		if d == "decline" {
			return "decline"
		}
	}
	c.fail(errors.New("codex: approval offered no unattended decision"))
	return "decline"
}

func (c *codexConn) writeResponse(id json.RawMessage, result any, errCode int, errMsg string) {
	resp := map[string]any{}
	if id != nil {
		var v any
		if json.Unmarshal(id, &v) == nil {
			resp["id"] = v
		}
	}
	if errCode != 0 {
		resp["error"] = map[string]any{"code": errCode, "message": errMsg}
	} else {
		resp["result"] = result
	}
	_ = c.writeJSON(resp)
}

func (c *codexConn) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "turn/completed":
		c.handleTurnCompleted(params)
	case "item/completed":
		c.handleItemCompleted(params)
	default:
		// turn/started, thread/started, item/* delta and updated events need
		// no special handling for a synchronous run.
	}
}

// handleTurnCompleted settles the run on the authoritative terminal fact.
func (c *codexConn) handleTurnCompleted(params json.RawMessage) {
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message        string `json:"message"`
				CodexErrorInfo string `json:"codexErrorInfo"`
			} `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		c.fail(fmt.Errorf("codex: invalid turn/completed: %w", err))
		return
	}
	c.mu.Lock()
	if c.threadID != "" && p.ThreadID != c.threadID {
		c.mu.Unlock()
		c.fail(errors.New("codex: turn/completed referenced another thread"))
		return
	}
	if c.turnID != "" && p.Turn.ID != c.turnID {
		c.mu.Unlock()
		c.fail(errors.New("codex: turn/completed referenced another turn"))
		return
	}
	if c.turnID == "" {
		c.turnID = p.Turn.ID
	}
	c.mu.Unlock()

	if p.Turn.Status == "failed" && p.Turn.Error != nil && p.Turn.Error.CodexErrorInfo == "contextWindowExceeded" {
		c.settle(codexResult{isError: true, err: errors.New("codex: context window exceeded")})
		return
	}
	switch p.Turn.Status {
	case "completed":
		c.mu.Lock()
		text := c.lastFinal
		if text == "" {
			text = c.lastUnphased
		}
		c.mu.Unlock()
		if strings.TrimSpace(text) == "" {
			c.settle(codexResult{isError: true, err: errors.New("codex: turn completed without a final answer")})
			return
		}
		c.settle(codexResult{text: text})
	case "interrupted":
		c.settle(codexResult{isError: true, err: errors.New("codex: turn interrupted")})
	case "failed":
		msg := "codex: turn failed"
		if p.Turn.Error != nil && p.Turn.Error.Message != "" {
			msg = "codex: turn failed: " + p.Turn.Error.Message
		}
		c.settle(codexResult{isError: true, err: errors.New(msg)})
	default:
		c.fail(fmt.Errorf("codex: invalid terminal turn status %q", p.Turn.Status))
	}
}

// handleItemCompleted watches for completed agentMessage items and records
// the best answer candidate.
func (c *codexConn) handleItemCompleted(params json.RawMessage) {
	var p struct {
		Item struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Phase json.RawMessage `json:"phase"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if p.Item.Type != "agentMessage" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch phase := string(p.Item.Phase); phase {
	case `"final_answer"`:
		c.lastFinal = p.Item.Text
	case "null", "":
		c.lastUnphased = p.Item.Text
	case `"commentary"`:
		// Commentary never replaces an answer.
	default:
		c.fail(fmt.Errorf("codex: unknown agent message phase %s", phase))
	}
}

func (c *codexConn) settle(res codexResult) {
	c.resultSent.Store(true)
	select {
	case c.resultCh <- res:
	default:
	}
}

func rawString(v json.RawMessage) string {
	if len(v) == 0 || v[0] != '"' {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return ""
}

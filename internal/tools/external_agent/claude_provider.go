package externalagent

// claudeProvider drives the Claude Code CLI over its stream-json control
// protocol, mirroring the official Claude Agent SDK's local (subprocess)
// transport. The CLI subprocess owns the full agent loop (tool execution,
// context management, retries, stopping conditions); this provider implements
// the control-protocol side: the initialize handshake, sending the user
// prompt, answering tool-permission control requests, interrupting on
// cancellation, and collecting the final result.
//
// Wire format (JSON-lines over stdin/stdout), matching
// anthropics/claude-agent-sdk:
//
//	out:  {"type":"control_request","request_id":"req_1_..","request":{...}}
//	in:   {"type":"control_response","response":{"subtype":"success",..}}
//	out:  {"type":"user","message":{"role":"user","content":".."}}
//	in:   {"type":"assistant","message":{..}} (streamed tool-use messages)
//	in:   {"type":"control_request","request_id":"..","request":{"subtype":"can_use_tool",..}}
//	out:  {"type":"control_response","response":{"subtype":"success","response":{"behavior":"allow|deny"}}}
//	in:   {"type":"result","subtype":"success","result":".."}
//	out:  {"type":"control_request","request_id":"..","request":{"subtype":"interrupt"}}

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// minClaudeVersion is the oldest Claude Code CLI that speaks the control
// protocol used here (same floor as the official Agent SDK).
const minClaudeVersion = "2.0.0"

// claudeInitTimeout bounds the initialize handshake (MCP servers may take
// time to start).
const claudeInitTimeout = 30 * time.Second

// claudeResultGrace is how long to wait for a result after sending an
// interrupt before killing the CLI process.
const claudeResultGrace = 20 * time.Second

// readOnlyTools are auto-approved without prompting; a fully autonomous
// delegated task must not stall on read-only operations.
var readOnlyTools = map[string]bool{
	"Read": true, "Glob": true, "Grep": true, "List": true, "LS": true,
	"WebFetch": true, "WebSearch": true, "Task": true, "TodoWrite": true,
}

var claudeReqCounter atomic.Int64

type claudeProvider struct{}

// claudeRunResult is what the reader loop reports back to the run loop.
type claudeRunResult struct {
	text    string
	subtype string
	isError bool
	errors  []string
	costUSD float64
}

// controlResponseMsg is the response envelope from the CLI.
type controlResponseMsg struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response"`
	Error     string          `json:"error"`
}

// canUseToolRequest is an incoming permission control request.
type canUseToolRequest struct {
	ToolName   string          `json:"tool_name"`
	Input      json.RawMessage `json:"input"`
	ToolUseID  string          `json:"tool_use_id"`
	BlockedPkg string          `json:"blocked_pkg"`
}

func (p *claudeProvider) Name() string { return "claude" }

func (p *claudeProvider) Available() (string, bool) {
	if _, err := exec.LookPath("claude"); err != nil {
		return "claude CLI not found on PATH (install Claude Code and sign in to use this provider)", false
	}
	return "", true
}

func (p *claudeProvider) Run(ctx context.Context, task, cwd string) (string, error) {
	return p.RunOpt(ctx, task, cwd, RunOptions{})
}

func (p *claudeProvider) RunOpt(ctx context.Context, task, cwd string, opts RunOptions) (string, error) {
	if err := p.verifyVersion(ctx); err != nil {
		return "", err
	}
	args := buildClaudeArgs(opts)
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("external agent %q: %w", "claude", err)
	}
	// Note: NOT CommandContext — cancelling ctx must not SIGKILL the CLI
	// before we can deliver the interrupt control request. Lifecycle is
	// managed manually below (interrupt protocol, then kill as a fallback).
	cmd := exec.Command(path, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("claude: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("claude: stdout pipe: %w", err)
	}
	stderr := newBoundedBuffer(64 << 10)
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude: start: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	conn := &claudeConn{
		stdin:       stdin,
		pending:     make(map[string]chan controlResponseMsg),
		resultCh:    make(chan claudeRunResult, 1),
		permOutcome: opts.autoAllowSet,
	}

	readerDone := make(chan struct{})
	var readerErr error
	go func() {
		defer close(readerDone)
		readerErr = conn.readLoop(ctx, stdout)
		if !conn.resultSent.Load() {
			// The process exited (or the stream died) before producing a
			// result — report it so the run loop does not hang.
			msg := "claude process exited without producing a result"
			if readerErr != nil && readerErr != io.EOF {
				msg = readerErr.Error()
			}
			if tail := strings.TrimSpace(stderr.String()); tail != "" {
				msg += ": " + truncate(tail, 2000)
			}
			select {
			case conn.resultCh <- claudeRunResult{isError: true, subtype: "error", errors: []string{msg}}:
			default:
			}
		}
	}()

	// Initialize handshake.
	initResp, err := conn.sendControlRequest(ctx, claudeInitTimeout, map[string]any{
		"subtype": "initialize",
		"hooks":   nil,
	})
	if err != nil {
		return "", fmt.Errorf("claude: initialize: %w", err)
	}
	if cr, ok := initResp.(controlResponseMsg); ok && cr.Subtype == "error" {
		return "", fmt.Errorf("claude: initialize failed: %s", cr.Error)
	}

	// Send the user prompt.
	userMsg := map[string]any{
		"type":               "user",
		"session_id":         "",
		"message":            map[string]any{"role": "user", "content": task},
		"parent_tool_use_id": nil,
	}
	if err := conn.writeJSON(userMsg); err != nil {
		return "", fmt.Errorf("claude: send prompt: %w", err)
	}

	// Wait for the result, or interrupt on cancellation.
	var res claudeRunResult
	select {
	case res = <-conn.resultCh:
	case <-ctx.Done():
		// Best-effort interrupt: write the control request and wait briefly
		// for the CLI to return a (possibly error) result.
		_ = conn.writeJSON(map[string]any{
			"type":       "control_request",
			"request_id": claudeRequestID(),
			"request":    map[string]any{"subtype": "interrupt"},
		})
		select {
		case res = <-conn.resultCh:
		case <-time.After(claudeResultGrace):
			_ = cmd.Process.Kill()
			<-readerDone
			return "", fmt.Errorf("claude: interrupted: %w", ctx.Err())
		}
	}

	// Close stdin to signal the end of the conversation; wait for the
	// process to exit.
	_ = stdin.Close()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case <-waitCh:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-waitCh
	}

	if res.isError {
		msg := "unknown error"
		if len(res.errors) > 0 {
			msg = strings.Join(res.errors, "; ")
		} else if res.subtype != "" {
			msg = res.subtype
		}
		return "", fmt.Errorf("claude: %s: %s", res.subtype, msg)
	}
	return strings.TrimSpace(res.text), nil
}

func buildClaudeArgs(opts RunOptions) []string {
	args := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json"}
	if opts.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(opts.MaxTurns))
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}
	if len(opts.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(opts.DisallowedTools, ","))
	}
	return args
}

func (p *claudeProvider) verifyVersion(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		return fmt.Errorf("claude: version check failed: %w", err)
	}
	got := strings.TrimSpace(string(out))
	got = strings.TrimPrefix(got, "v")
	if !versionAtLeast(got, minClaudeVersion) {
		return fmt.Errorf("claude: CLI version %s is too old; stream-json control protocol requires >= %s (upgrade Claude Code)", got, minClaudeVersion)
	}
	return nil
}

// versionAtLeast compares dotted numeric versions. v1 >= v2 is false when
// either version is unparseable.
func versionAtLeast(got, want string) bool {
	gv, ok1 := parseVersion(got)
	wv, ok2 := parseVersion(want)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if gv[i] != wv[i] {
			return gv[i] > wv[i]
		}
	}
	return true
}

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(s, ".")
	if len(parts) == 0 {
		return v, false
	}
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			break
		}
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// claudeConn owns the reader loop and pending control-response routing.
type claudeConn struct {
	stdin       io.WriteCloser
	writeMu     sync.Mutex
	pendingMu   sync.Mutex
	pending     map[string]chan controlResponseMsg
	resultCh    chan claudeRunResult
	resultSent  atomic.Bool
	permOutcome func(tool string, input json.RawMessage) bool // allow? nil => default policy
}

func (c *claudeConn) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// sendControlRequest sends a control request and waits for its response.
func (c *claudeConn) sendControlRequest(ctx context.Context, timeout time.Duration, request map[string]any) (any, error) {
	reqID := claudeRequestID()
	ch := make(chan controlResponseMsg, 1)
	c.pendingMu.Lock()
	c.pending[reqID] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, reqID)
		c.pendingMu.Unlock()
	}()

	msg := map[string]any{
		"type":       "control_request",
		"request_id": reqID,
		"request":    request,
	}
	if err := c.writeJSON(msg); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, fmt.Errorf("timed out waiting for control response")
	}
}

// readLoop consumes the CLI's stdout JSON-lines stream. It routes control
// responses, answers permission requests, and forwards the final result.
func (c *claudeConn) readLoop(ctx context.Context, stdout io.Reader) error {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256<<10), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			// Non-JSON noise on stdout: ignore (matches SDK's lenient parsing).
			continue
		}
		switch msg.Type {
		case "control_response":
			var m struct {
				Response controlResponseMsg `json:"response"`
			}
			if err := json.Unmarshal(line, &m); err != nil {
				continue
			}
			c.pendingMu.Lock()
			if ch, ok := c.pending[m.Response.RequestID]; ok {
				select {
				case ch <- m.Response:
				default:
				}
			}
			c.pendingMu.Unlock()

		case "control_request":
			var m struct {
				RequestID string          `json:"request_id"`
				Request   json.RawMessage `json:"request"`
			}
			if err := json.Unmarshal(line, &m); err != nil {
				continue
			}
			var sub struct {
				Subtype string `json:"subtype"`
			}
			if err := json.Unmarshal(m.Request, &sub); err != nil {
				continue
			}
			switch sub.Subtype {
			case "can_use_tool":
				var req canUseToolRequest
				if err := json.Unmarshal(m.Request, &req); err != nil {
					continue
				}
				c.answerPermission(m.RequestID, req)
			default:
				// Unsupported control request (e.g. hook_callback): deny
				// without crashing the conversation.
				c.answerPermission(m.RequestID, canUseToolRequest{ToolName: sub.Subtype})
			}

		case "result":
			var m struct {
				Subtype   string   `json:"subtype"`
				Result    string   `json:"result"`
				IsError   bool     `json:"is_error"`
				Errors    []string `json:"errors"`
				TotalCost float64  `json:"total_cost_usd"`
			}
			_ = json.Unmarshal(line, &m)
			c.resultSent.Store(true)
			c.resultCh <- claudeRunResult{
				text:    m.Result,
				subtype: m.Subtype,
				isError: m.IsError,
				errors:  m.Errors,
				costUSD: m.TotalCost,
			}

		default:
			// system/assistant/user/stream_event/add_comment/control_cancel_request
			// need no special handling for a synchronous run.
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("claude: reading stdout: %w", err)
	}
	return io.EOF
}

// answerPermission replies to a can_use_tool control request.
func (c *claudeConn) answerPermission(requestID string, req canUseToolRequest) {
	behavior := "deny"
	msg := "denied by covo-agent external agent bridge"
	if c.permOutcome != nil {
		if c.permOutcome(req.ToolName, req.Input) {
			behavior = "allow"
			msg = ""
		}
	} else if readOnlyTools[req.ToolName] {
		behavior = "allow"
		msg = ""
	}
	response := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response": map[string]any{
				"behavior": behavior,
				"message":  msg,
			},
		},
	}
	_ = c.writeJSON(response)
}

func claudeRequestID() string {
	n := claudeReqCounter.Add(1)
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("req_%d_%s", n, hex.EncodeToString(b[:]))
}

// boundedBuffer keeps the tail of stderr for diagnostics without blocking
// the CLI process.
type boundedBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	maxLen int
}

func newBoundedBuffer(maxLen int) *boundedBuffer {
	return &boundedBuffer{maxLen: maxLen}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
	if b.buf.Len() > b.maxLen {
		// Keep the last maxLen bytes.
		keep := b.buf.Bytes()[b.buf.Len()-b.maxLen:]
		b.buf.Reset()
		b.buf.Write(keep)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// optsAutoAllow builds the permission override used by RunOptions when the
// tool sets allowed tools.
func (opts RunOptions) autoAllowSet(tool string, input json.RawMessage) bool {
	for _, t := range opts.AllowedTools {
		if t == tool {
			return true
		}
	}
	if opts.PermissionMode == "bypassPermissions" {
		return true
	}
	return readOnlyTools[tool]
}

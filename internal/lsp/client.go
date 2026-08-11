package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
)

const (
	diagnosticsDocumentWait = 5 * time.Second
	diagnosticsFullWait     = 10 * time.Second
	pushDebounce            = 150 * time.Millisecond
	shutdownGrace           = 2 * time.Second
	initializeTimeout       = 30 * time.Second
)

type ClientID struct {
	ServerID      string
	WorkspaceRoot string
}

type Client struct {
	serverDef     *ServerDef
	workspaceRoot string
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdout        *bufio.Reader
	stderr        io.ReadCloser
	nextID        atomic.Int64
	mu            sync.Mutex
	writeMu       sync.Mutex // serializes writes to stdin
	running       bool
	cancel        context.CancelFunc
	diags         map[string][]Diagnostic
	diagsMu       sync.RWMutex
	lastOpen      map[string]time.Time
	pending       map[int]chan rpcResult // in-flight requests keyed by id
	pendingMu     sync.Mutex
	logger        *slog.Logger
}

// rpcResult carries a routed response back to the waiting sendRequest caller.
type rpcResult struct {
	data json.RawMessage
	err  error
}

// Location is a resolved source location (definition/reference target).
type Location struct {
	Path  string
	Range Range
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func NewClient(serverDef *ServerDef, workspaceRoot string) *Client {
	return &Client{
		serverDef:     serverDef,
		workspaceRoot: workspaceRoot,
		diags:         make(map[string][]Diagnostic),
		lastOpen:      make(map[string]time.Time),
		pending:       make(map[int]chan rpcResult),
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
	}
}

func (c *Client) ID() ClientID {
	return ClientID{ServerID: c.serverDef.ID, WorkspaceRoot: c.workspaceRoot}
}

func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("lsp client already running: %s", c.serverDef.ID)
	}

	cmd := findCommand(c.serverDef.Command)
	if cmd == "" {
		return fmt.Errorf("lsp server %q not found on PATH", c.serverDef.Command)
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.cmd = exec.CommandContext(ctx, cmd, c.serverDef.Args...)
	c.cmd.Dir = c.workspaceRoot
	c.cmd.Env = os.Environ()

	var err error
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	c.stdout = bufio.NewReader(stdout)

	c.stderr, err = c.cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start %s: %w", c.serverDef.Command, err)
	}

	go c.readStderr()
	go c.readLoop()

	if err := c.initialize(ctx); err != nil {
		cancel()
		c.cmd.Process.Kill()
		return fmt.Errorf("initialize %s: %w", c.serverDef.Command, err)
	}

	c.running = true
	c.logger.Info("lsp client started", "server", c.serverDef.ID, "workspace", c.workspaceRoot)
	return nil
}

func (c *Client) readStderr() {
	buf := make([]byte, 4096)
	for {
		n, err := c.stderr.Read(buf)
		if n > 0 {
			c.logger.Debug("lsp stderr", "server", c.serverDef.ID, "data", string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) initialize(ctx context.Context) error {
	initParams := map[string]any{
		"processId":        os.Getpid(),
		"rootUri":          fileURI(c.workspaceRoot),
		"rootPath":         c.workspaceRoot,
		"workspaceFolders": []map[string]any{{"uri": fileURI(c.workspaceRoot), "name": "workspace"}},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{"relatedInformation": true},
			},
			"workspace": map[string]any{
				"configuration": true,
			},
		},
		"initializationOptions": c.serverDef.InitOptions,
	}

	resp, err := c.sendRequest(ctx, "initialize", initParams, initializeTimeout)
	if err != nil {
		return fmt.Errorf("initialize request: %w", err)
	}

	_ = resp

	notif := &Notification{
		JSONRPC: "2.0",
		Method:  "initialized",
		Params:  map[string]any{},
	}
	c.sendNotification(notif)

	return nil
}

func (c *Client) readLoop() {
	for {
		data, err := readMessage(c.stdout)
		if err != nil {
			if err != io.EOF {
				c.logger.Error("lsp read error", "server", c.serverDef.ID, "error", err)
			}
			c.failPending(err)
			return
		}

		msgType, id, method, _ := classifyMessage(data)
		switch msgType {
		case "notification":
			if method == "textDocument/publishDiagnostics" {
				c.handlePublishDiagnostics(data)
			}
		case "response", "error":
			c.deliverResponse(id, data)
		}
	}
}

// deliverResponse routes a server response to the waiting sendRequest caller.
func (c *Client) deliverResponse(id int, data []byte) {
	c.pendingMu.Lock()
	ch := c.pending[id]
	c.pendingMu.Unlock()
	if ch == nil {
		return
	}
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		ch <- rpcResult{err: err}
		return
	}
	if resp.Error != nil {
		ch <- rpcResult{err: resp.Error}
		return
	}
	b, _ := json.Marshal(resp.Result)
	ch <- rpcResult{data: b}
}

// failPending fails all in-flight requests when the read loop terminates.
func (c *Client) failPending(err error) {
	if err == nil {
		err = io.EOF
	}
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		select {
		case ch <- rpcResult{err: err}:
		default:
		}
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

func (c *Client) handlePublishDiagnostics(data []byte) {
	var notif struct {
		Params struct {
			URI         string       `json:"uri"`
			Diagnostics []Diagnostic `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &notif); err != nil {
		return
	}

	path := uriToPath(notif.Params.URI)

	c.diagsMu.Lock()
	c.diags[path] = notif.Params.Diagnostics
	c.diagsMu.Unlock()
}

func (c *Client) OpenFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	uri := fileURI(path)
	langID := LanguageIDFor(path)

	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": langID,
			"version":    1,
			"text":       string(content),
		},
	}

	c.sendNotification(&Notification{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  params,
	})

	c.lastOpen[path] = time.Now()

	watched := map[string]any{
		"changes": []map[string]any{
			{"uri": uri, "type": 2},
		},
	}
	c.sendNotification(&Notification{
		JSONRPC: "2.0",
		Method:  "workspace/didChangeWatchedFiles",
		Params:  watched,
	})

	return nil
}

func (c *Client) ChangeFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	uri := fileURI(path)

	params := map[string]any{
		"textDocument": map[string]any{
			"uri":     uri,
			"version": 2,
		},
		"contentChanges": []map[string]any{
			{"text": string(content)},
		},
	}

	c.sendNotification(&Notification{
		JSONRPC: "2.0",
		Method:  "textDocument/didChange",
		Params:  params,
	})

	watched := map[string]any{
		"changes": []map[string]any{
			{"uri": uri, "type": 2},
		},
	}
	c.sendNotification(&Notification{
		JSONRPC: "2.0",
		Method:  "workspace/didChangeWatchedFiles",
		Params:  watched,
	})

	return nil
}

func (c *Client) DiagnosticsFor(path string) []Diagnostic {
	c.diagsMu.RLock()
	defer c.diagsMu.RUnlock()
	return c.diags[path]
}

// AllDiagnostics returns a snapshot of all known diagnostics keyed by file path.
// The returned map is a copy and safe to use without holding the lock.
func (c *Client) AllDiagnostics() map[string][]Diagnostic {
	c.diagsMu.RLock()
	defer c.diagsMu.RUnlock()
	out := make(map[string][]Diagnostic, len(c.diags))
	for k, v := range c.diags {
		out[k] = v
	}
	return out
}

func (c *Client) WaitForDiagnostics(ctx context.Context, path string, timeout time.Duration) []Diagnostic {
	deadline := time.After(timeout)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return c.DiagnosticsFor(path)
		case <-deadline:
			return c.DiagnosticsFor(path)
		case <-ticker.C:
			diags := c.DiagnosticsFor(path)
			if len(diags) > 0 {
				return diags
			}
		}
	}
}

func (c *Client) sendRequest(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := int(c.nextID.Add(1))

	req := &Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := encodeMessage(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan rpcResult, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	c.writeMu.Lock()
	_, werr := c.stdin.Write(data)
	c.writeMu.Unlock()
	if werr != nil {
		return nil, fmt.Errorf("write request: %w", werr)
	}

	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-readCtx.Done():
		return nil, fmt.Errorf("request %s timeout after %v", method, timeout)
	case r := <-ch:
		return r.data, r.err
	}
}

func (c *Client) sendNotification(notif *Notification) {
	data, err := encodeMessage(notif)
	if err != nil {
		c.logger.Error("encode notification", "error", err)
		return
	}
	c.writeMu.Lock()
	_, werr := c.stdin.Write(data)
	c.writeMu.Unlock()
	if werr != nil {
		c.logger.Error("write notification", "error", werr)
	}
}

const navTimeout = 10 * time.Second

// Definition resolves the definition location(s) for the symbol at the given
// 0-based line/character in path.
func (c *Client) Definition(ctx context.Context, path string, line, char int) ([]Location, error) {
	raw, err := c.sendRequest(ctx, "textDocument/definition", positionParams(path, line, char), navTimeout)
	if err != nil {
		return nil, err
	}
	return parseLocations(raw), nil
}

// References finds all references to the symbol at the given 0-based position.
func (c *Client) References(ctx context.Context, path string, line, char int, includeDecl bool) ([]Location, error) {
	params := positionParams(path, line, char)
	params["context"] = map[string]any{"includeDeclaration": includeDecl}
	raw, err := c.sendRequest(ctx, "textDocument/references", params, navTimeout)
	if err != nil {
		return nil, err
	}
	return parseLocations(raw), nil
}

// Hover returns the hover documentation for the symbol at the given 0-based
// position, as plain/markdown text.
func (c *Client) Hover(ctx context.Context, path string, line, char int) (string, error) {
	raw, err := c.sendRequest(ctx, "textDocument/hover", positionParams(path, line, char), navTimeout)
	if err != nil {
		return "", err
	}
	return parseHover(raw), nil
}

func positionParams(path string, line, char int) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": fileURI(path)},
		"position":     map[string]any{"line": line, "character": char},
	}
}

// parseLocations decodes a definition/references result, which may be null, a
// single Location, an array of Location, or an array of LocationLink.
func parseLocations(raw json.RawMessage) []Location {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		var out []Location
		for _, item := range arr {
			if loc, ok := parseOneLocation(item); ok {
				out = append(out, loc)
			}
		}
		return out
	}
	if loc, ok := parseOneLocation(raw); ok {
		return []Location{loc}
	}
	return nil
}

func parseOneLocation(raw json.RawMessage) (Location, bool) {
	var l struct {
		URI                  string `json:"uri"`
		Range                Range  `json:"range"`
		TargetURI            string `json:"targetUri"`
		TargetRange          Range  `json:"targetRange"`
		TargetSelectionRange Range  `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return Location{}, false
	}
	if l.URI != "" {
		return Location{Path: uriToPath(l.URI), Range: l.Range}, true
	}
	if l.TargetURI != "" {
		r := l.TargetSelectionRange
		if r == (Range{}) {
			r = l.TargetRange
		}
		return Location{Path: uriToPath(l.TargetURI), Range: r}, true
	}
	return Location{}, false
}

// parseHover extracts text from a hover result (MarkupContent, MarkedString,
// MarkedString[], or plain string).
func parseHover(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var h struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(raw, &h); err != nil || len(h.Contents) == 0 {
		return ""
	}
	return strings.TrimSpace(extractMarkup(h.Contents))
}

func extractMarkup(raw json.RawMessage) string {
	var mc struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &mc); err == nil && mc.Value != "" {
		return mc.Value
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return s
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		var parts []string
		for _, item := range arr {
			if p := extractMarkup(item); p != "" {
				parts = append(parts, p)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func (c *Client) Shutdown() error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	c.running = false
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	req := &Request{
		JSONRPC: "2.0",
		ID:      999,
		Method:  "shutdown",
	}
	data, _ := encodeMessage(req)
	c.stdin.Write(data)

	exitNotif := &Notification{
		JSONRPC: "2.0",
		Method:  "exit",
	}
	exitData, _ := encodeMessage(exitNotif)
	c.stdin.Write(exitData)

	done := make(chan struct{})
	safego.SafeGo(func() {
		c.cmd.Wait()
		close(done)
	}, nil)

	select {
	case <-ctx.Done():
		c.cmd.Process.Kill()
		<-done
	case <-done:
	}

	if c.cancel != nil {
		c.cancel()
	}

	c.logger.Info("lsp client stopped", "server", c.serverDef.ID)
	return nil
}

func fileURI(path string) string {
	abs, _ := filepath.Abs(path)
	return "file://" + abs
}

func uriToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return uri[7:]
	}
	return uri
}

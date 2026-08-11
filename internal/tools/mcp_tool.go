package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

type MCPStore struct {
	mu      sync.Mutex
	servers map[string]*mcpServer
}

func NewMCPStore() *MCPStore {
	return &MCPStore{servers: make(map[string]*mcpServer)}
}

// AutoConnect connects to an MCP server with the given config and stores
// it for use by the mcp tool. Returns the number of tools discovered.
func (s *MCPStore) AutoConnect(ctx context.Context, name, command string, args []string, envVarNames []string, timeoutSec int) (int, error) {
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	connectCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Resolve env var names to KEY=VALUE pairs from the current process
	// environment. This allows MCP servers to receive API keys etc.
	// without storing secret values in config.yaml.
	var env []string
	for _, name := range envVarNames {
		if val := os.Getenv(name); val != "" {
			env = append(env, name+"="+val)
		}
	}

	var client mcpClient
	var err error

	client, err = newStdioClient(connectCtx, command, args, env)
	if err != nil {
		return 0, fmt.Errorf("stdio connect: %w", err)
	}

	tools, err := client.ListTools(connectCtx)
	if err != nil {
		client.Close()
		return 0, fmt.Errorf("list tools: %w", err)
	}

	s.mu.Lock()
	s.servers[name] = &mcpServer{
		config: mcpServerConfig{
			Name:    name,
			Command: command,
		},
		client:    client,
		closeConn: client.Close,
		tools:     tools,
		connected: true,
	}
	s.mu.Unlock()

	return len(tools), nil
}

type mcpServer struct {
	config    mcpServerConfig
	closeConn func()
	client    mcpClient
	toolsMu   sync.RWMutex
	tools     []mcpToolDef
	connected bool
}

type mcpServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     []string
	Timeout int
}

type mcpToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// mcpClient is the interface for MCP transport clients.
type mcpClient interface {
	ListTools(ctx context.Context) ([]mcpToolDef, error)
	CallTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error)
	Close()
}

type stdioClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	mu      sync.Mutex
	pending map[int64]chan<- rpcResponse
	nextID  int64
	closeMu sync.Mutex
	closed  bool
}

type httpClient struct {
	endpoint  string
	httpCli   *http.Client
	sessionID string
	mu        sync.Mutex
	nextID    int64
	closeMu   sync.Mutex
	closed    bool
	headers   map[string]string
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *int64           `json:"id,omitempty"`
	Result  *json.RawMessage `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var safeEnvPrefixes = []string{
	"PATH", "HOME", "USER", "LANG", "LC_", "TERM", "TMPDIR", "TMP", "TEMP",
	"SHELL", "LOGNAME", "XDG_",
}

func buildSafeEnv(extra []string) []string {
	extraSet := make(map[string]bool)
	for _, e := range extra {
		name, _, _ := strings.Cut(e, "=")
		extraSet[name] = true
	}
	var env []string
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if extraSet[name] {
			continue // will be appended below with user value
		}
		upper := strings.ToUpper(name)
		for _, p := range safeEnvPrefixes {
			if strings.HasPrefix(upper, p) {
				env = append(env, e)
				break
			}
		}
	}
	env = append(env, extra...)
	return env
}

func newStdioClient(ctx context.Context, command string, args, env []string) (*stdioClient, error) {
	if _, err := exec.LookPath(command); err != nil {
		return nil, fmt.Errorf("command not found: %s", command)
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = buildSafeEnv(env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}

	c := &stdioClient{
		cmd:     cmd,
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
		pending: make(map[int64]chan<- rpcResponse),
	}
	c.scanner.Buffer(nil, 10*1024*1024)

	go c.readLoop()

	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *stdioClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "covo-agent",
			"version": "1.0",
		},
	}
	result, err := c.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &initResult); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}

	c.sendNotification("notifications/initialized", nil)
	return nil
}

func (c *stdioClient) ListTools(ctx context.Context) ([]mcpToolDef, error) {
	raw, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var listResult struct {
		Tools []mcpToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listResult); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	return listResult.Tools, nil
}

func (c *stdioClient) CallTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	return c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

func (c *stdioClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("client is closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		close(ch)
		c.mu.Unlock()
	}()

	req, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	c.mu.Lock()
	_, err = fmt.Fprintf(c.stdin, "%s\n", req)
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	timeout := 120 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout < 0 {
			return nil, ctx.Err()
		}
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("client closed while waiting for response")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if resp.Result == nil {
			return json.RawMessage("null"), nil
		}
		return *resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, fmt.Errorf("MCP call timeout (%s)", method)
	}
}

func (c *stdioClient) sendNotification(method string, params any) error {
	req, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	_, err = fmt.Fprintf(c.stdin, "%s\n", req)
	c.mu.Unlock()
	return err
}

func (c *stdioClient) readLoop() {
	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		if resp.ID == nil {
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[*resp.ID]
		c.mu.Unlock()
		if ok {
			select {
			case ch <- resp:
			default:
			}
		}
	}
}

func (c *stdioClient) Close() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return
	}
	c.closed = true

	// Drain and close all pending channels so waiting callers don't timeout
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()

	c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
}

// --- HTTP MCP Client ---

func newHTTPClient(ctx context.Context, endpoint string, headers map[string]string) (*httpClient, error) {
	c := &httpClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		httpCli:  &http.Client{Timeout: 120 * time.Second},
		headers:  headers,
	}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *httpClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "covo-agent",
			"version": "1.0",
		},
	}
	result, err := c.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &initResult); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}
	return nil
}

func (c *httpClient) ListTools(ctx context.Context) ([]mcpToolDef, error) {
	raw, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var listResult struct {
		Tools []mcpToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &listResult); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	return listResult.Tools, nil
}

func (c *httpClient) CallTool(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	return c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

func (c *httpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("client is closed")
	}
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	reqBody, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.httpCli.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP POST: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Capture session ID from response header
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if rpcResp.Result == nil {
		return json.RawMessage("null"), nil
	}
	return *rpcResp.Result, nil
}

func (c *httpClient) Close() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	c.closed = true
}

func buildMCPTool(store *MCPStore) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "mcp",
		Description: strings.Join([]string{
			"Manage MCP (Model Context Protocol) server connections and call their tools.",
			"MCP servers provide additional capabilities like filesystem access, GitHub",
			"operations, database queries, and more.",
			"",
			"Actions:",
			"- connect:   Connect to an MCP server and discover its tools",
			"- disconnect: Disconnect from a server",
			"- list:      List connected servers and their available tools",
			"- call:      Call a specific tool on a connected server",
			"- refresh:   Re-discover tools from a server",
			"",
			"Transport (connect action):",
			"- stdio (default): Spawn a local subprocess (command + args)",
			"- http:           Connect to a remote MCP HTTP endpoint (endpoint URL)",
			"",
			"Example connect (stdio):",
			`  {"action":"connect","name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}`,
			"",
			"Example connect (http):",
			`  {"action":"connect","name":"remote","transport":"http","endpoint":"http://localhost:3000/mcp","headers":{"Authorization":"Bearer token"}}`,
			"",
			"Example call:",
			`  {"action":"call","server":"fs","tool":"read_file","arguments":{"path":"/tmp/test.txt"}}`,
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Operation: connect, disconnect, list, call, refresh",
					"enum":        []string{"connect", "disconnect", "list", "call", "refresh"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Server name (required for connect, disconnect, refresh)",
				},
				"transport": map[string]any{
					"type":        "string",
					"description": "Transport: 'stdio' (default) or 'http'",
					"enum":        []string{"stdio", "http"},
				},
				"endpoint": map[string]any{
					"type":        "string",
					"description": "MCP HTTP endpoint URL (required for http transport)",
				},
				"headers": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "Optional HTTP headers for http transport",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "MCP server command (required for stdio connect, e.g. 'npx', 'uvx')",
				},
				"args": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Command arguments for stdio connect",
				},
				"env": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "Optional environment variables for the server process",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Tool call timeout in seconds (default: 120)",
				},
				"server": map[string]any{
					"type":        "string",
					"description": "Server name to call a tool on (required for call)",
				},
				"tool": map[string]any{
					"type":        "string",
					"description": "Tool name to call (required for call)",
				},
				"arguments": map[string]any{
					"type":        "object",
					"description": "Arguments to pass to the tool (required for call)",
				},
			},
			"required": []string{"action"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Action    string            `json:"action"`
				Name      string            `json:"name"`
				Transport string            `json:"transport"`
				Endpoint  string            `json:"endpoint"`
				Headers   map[string]string `json:"headers"`
				Command   string            `json:"command"`
				Args      []string          `json:"args"`
				Env       map[string]string `json:"env"`
				Timeout   int               `json:"timeout"`
				Server    string            `json:"server"`
				Tool      string            `json:"tool"`
				Arguments map[string]any    `json:"arguments"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			switch params.Action {
			case "connect":
				return handleConnect(ctx, store, params)
			case "disconnect":
				return handleDisconnect(store, params)
			case "list":
				return handleList(store)
			case "call":
				return handleCall(ctx, store, params)
			case "refresh":
				return handleRefresh(ctx, store, params)
			default:
				return nil, fmt.Errorf("unknown action: %s", params.Action)
			}
		},
	}
}

func handleConnect(ctx context.Context, store *MCPStore, params struct {
	Action    string            `json:"action"`
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Endpoint  string            `json:"endpoint"`
	Headers   map[string]string `json:"headers"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Timeout   int               `json:"timeout"`
	Server    string            `json:"server"`
	Tool      string            `json:"tool"`
	Arguments map[string]any    `json:"arguments"`
}) (any, error) {
	if params.Name == "" {
		return nil, fmt.Errorf("name is required for connect")
	}

	transport := params.Transport
	if transport == "" {
		transport = "stdio"
	}

	timeout := 120
	if params.Timeout > 0 {
		timeout = params.Timeout
	}

	var client mcpClient
	var err error

	switch transport {
	case "http":
		if params.Endpoint == "" {
			return nil, fmt.Errorf("endpoint is required for http transport")
		}
		connectCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		client, err = newHTTPClient(connectCtx, params.Endpoint, params.Headers)
		if err != nil {
			return nil, fmt.Errorf("connect to MCP HTTP server %q: %w", params.Name, err)
		}
	case "stdio":
		if params.Command == "" {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		var env []string
		for k, v := range params.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		connectCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		client, err = newStdioClient(connectCtx, params.Command, params.Args, env)
		if err != nil {
			return nil, fmt.Errorf("connect to MCP server %q: %w", params.Name, err)
		}
	default:
		return nil, fmt.Errorf("unknown transport %q (supported: stdio, http)", transport)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("list tools from %q: %w", params.Name, err)
	}

	closeConn := client.Close

	store.mu.Lock()
	if existing, ok := store.servers[params.Name]; ok {
		existing.closeConn()
	}
	store.servers[params.Name] = &mcpServer{
		config: mcpServerConfig{
			Name:    params.Name,
			Command: params.Command,
			Args:    params.Args,
			Env:     nil,
			Timeout: timeout,
		},
		closeConn: closeConn,
		client:    client,
		tools:     tools,
		connected: true,
	}
	store.mu.Unlock()

	var toolNames []string
	for _, t := range tools {
		toolNames = append(toolNames, t.Name)
	}

	return map[string]any{
		"status":     "connected",
		"server":     params.Name,
		"transport":  transport,
		"tool_count": len(tools),
		"tools":      toolNames,
	}, nil
}

func handleDisconnect(store *MCPStore, params struct {
	Action    string            `json:"action"`
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Endpoint  string            `json:"endpoint"`
	Headers   map[string]string `json:"headers"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Timeout   int               `json:"timeout"`
	Server    string            `json:"server"`
	Tool      string            `json:"tool"`
	Arguments map[string]any    `json:"arguments"`
}) (any, error) {
	name := params.Name
	if name == "" {
		name = params.Server
	}
	if name == "" {
		return nil, fmt.Errorf("name or server is required for disconnect")
	}

	store.mu.Lock()
	server, ok := store.servers[name]
	if ok {
		delete(store.servers, name)
	}
	store.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("server %q not found", name)
	}

	server.closeConn()
	return map[string]string{"status": "disconnected", "server": name}, nil
}

func handleList(store *MCPStore) (any, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.servers) == 0 {
		return map[string]any{
			"status":  "ok",
			"servers": []string{},
			"message": "No MCP servers connected. Use the connect action to connect to one.",
		}, nil
	}

	type toolInfo struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		InputSchema map[string]any `json:"input_schema,omitempty"`
	}
	type serverInfo struct {
		Name      string     `json:"name"`
		Command   string     `json:"command"`
		ToolCount int        `json:"tool_count"`
		Tools     []toolInfo `json:"tools"`
	}

	var servers []serverInfo
	for _, s := range store.servers {
		s.toolsMu.RLock()
		tools := s.tools
		s.toolsMu.RUnlock()

		var infos []toolInfo
		for _, t := range tools {
			infos = append(infos, toolInfo{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
		servers = append(servers, serverInfo{
			Name:      s.config.Name,
			Command:   s.config.Command,
			ToolCount: len(tools),
			Tools:     infos,
		})
	}

	return map[string]any{
		"status":       "ok",
		"server_count": len(servers),
		"servers":      servers,
	}, nil
}

func handleCall(ctx context.Context, store *MCPStore, params struct {
	Action    string            `json:"action"`
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Endpoint  string            `json:"endpoint"`
	Headers   map[string]string `json:"headers"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Timeout   int               `json:"timeout"`
	Server    string            `json:"server"`
	Tool      string            `json:"tool"`
	Arguments map[string]any    `json:"arguments"`
}) (any, error) {
	if params.Server == "" {
		return nil, fmt.Errorf("server is required for call")
	}
	if params.Tool == "" {
		return nil, fmt.Errorf("tool is required for call")
	}

	store.mu.Lock()
	server, ok := store.servers[params.Server]
	store.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("server %q not connected — use connect action first", params.Server)
	}

	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	timeout := server.config.Timeout
	if params.Timeout > 0 {
		timeout = params.Timeout
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	result, err := server.client.CallTool(callCtx, params.Tool, params.Arguments)
	if err != nil {
		return nil, fmt.Errorf("call tool %q on %q: %w", params.Tool, params.Server, err)
	}

	var toolResult struct {
		Content []struct {
			Type string           `json:"type"`
			Text string           `json:"text,omitempty"`
			Data *json.RawMessage `json:"data,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}

	output := string(result)
	if err := json.Unmarshal(result, &toolResult); err == nil {
		var textParts []string
		for _, c := range toolResult.Content {
			if c.Text != "" {
				textParts = append(textParts, c.Text)
			}
		}
		if len(textParts) > 0 {
			output = strings.Join(textParts, "\n")
		}
		if toolResult.IsError {
			return map[string]any{
				"status":  "error",
				"output":  output,
				"isError": true,
			}, nil
		}
	}

	return map[string]any{
		"status": "ok",
		"output": output,
	}, nil
}

func handleRefresh(ctx context.Context, store *MCPStore, params struct {
	Action    string            `json:"action"`
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Endpoint  string            `json:"endpoint"`
	Headers   map[string]string `json:"headers"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Timeout   int               `json:"timeout"`
	Server    string            `json:"server"`
	Tool      string            `json:"tool"`
	Arguments map[string]any    `json:"arguments"`
}) (any, error) {
	name := params.Name
	if name == "" {
		name = params.Server
	}
	if name == "" {
		return nil, fmt.Errorf("name or server is required for refresh")
	}

	store.mu.Lock()
	server, ok := store.servers[name]
	store.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("server %q not connected", name)
	}

	tools, err := server.client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("refresh tools from %q: %w", name, err)
	}

	server.toolsMu.Lock()
	server.tools = tools
	server.toolsMu.Unlock()

	var toolNames []string
	for _, t := range tools {
		toolNames = append(toolNames, t.Name)
	}

	return map[string]any{
		"status":     "refreshed",
		"server":     name,
		"tool_count": len(tools),
		"tools":      toolNames,
	}, nil
}

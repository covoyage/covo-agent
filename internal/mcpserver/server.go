package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/covoyage/covo-agent/internal/logutil"
	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/covoyage/covonaut/agentcore"
)

const protocolVersion = "2025-11-25"

type rpcID struct {
	Num  int64
	Str  string
	Null bool
}

func (id rpcID) MarshalJSON() ([]byte, error) {
	switch {
	case id.Null:
		return []byte("null"), nil
	case id.Str != "":
		return json.Marshal(id.Str)
	default:
		return json.Marshal(id.Num)
	}
}

func (id *rpcID) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		id.Null = true
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &id.Str)
	}
	return json.Unmarshal(b, &id.Num)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      rpcID           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      rpcID     `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

type mcpToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolListResult struct {
	Tools []mcpToolSchema `json:"tools"`
}

type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type callToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type callToolResult struct {
	Content []callToolContent `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

type initializeResult struct {
	ProtocolVersion string            `json:"protocolVersion"`
	Capabilities    map[string]any    `json:"capabilities"`
	ServerInfo      map[string]string `json:"serverInfo"`
}

type toolBridge interface {
	ToolNames() []string
	GetTool(name string) (*agentcore.Tool, bool)
}

type toolHandlerFunc func(ctx context.Context, args json.RawMessage) (any, error)

type registeredTool struct {
	Schema  mcpToolSchema
	Handler toolHandlerFunc
}

type Server struct {
	provider    toolBridge
	logger      *slog.Logger
	scanner     *bufio.Scanner
	mu          sync.Mutex
	output      io.Writer
	initialized bool
	extraTools  map[string]registeredTool
}

func NewServer(provider toolBridge) *Server {
	return &Server{
		provider: provider,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: logutil.ResolveLevel(slog.LevelWarn),
		})),
		scanner:    bufio.NewScanner(os.Stdin),
		output:     os.Stdout,
		extraTools: make(map[string]registeredTool),
	}
}

// RegisterTool registers an extra tool that will be merged into tools/list and
// dispatched in tools/call before falling through to agent tools.
func (s *Server) RegisterTool(schema mcpToolSchema, handler toolHandlerFunc) {
	s.extraTools[schema.Name] = registeredTool{Schema: schema, Handler: handler}
}

func NewServerFromAgent(agent *agentcore.Agent) *Server {
	return NewServer(agent)
}

func (s *Server) Run(ctx context.Context) error {
	s.scanner.Buffer(nil, 10*1024*1024)

	type scanResult struct {
		bytes []byte
		err   error
	}
	scanCh := make(chan scanResult, 1)

	safego.SafeGo(func() {
		for s.scanner.Scan() {
			b := make([]byte, len(s.scanner.Bytes()))
			copy(b, s.scanner.Bytes())
			select {
			case scanCh <- scanResult{bytes: b}:
			case <-ctx.Done():
				return
			}
		}
		if err := s.scanner.Err(); err != nil {
			select {
			case scanCh <- scanResult{err: err}:
			case <-ctx.Done():
			}
		}
		close(scanCh)
	}, nil)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result, ok := <-scanCh:
			if !ok {
				return nil
			}
			if result.err != nil {
				return fmt.Errorf("stdin read error: %w", result.err)
			}
			line := result.bytes
			if len(line) == 0 {
				continue
			}
			if isRequest(line) {
				var req rpcRequest
				if err := json.Unmarshal(line, &req); err != nil {
					s.logger.Error("failed to parse request", "error", err)
					s.writeError(rpcID{Null: true}, -32700, "Parse error")
					continue
				}
				if req.JSONRPC != "2.0" {
					s.writeError(req.ID, -32600, "Invalid Request: jsonrpc must be \"2.0\"")
					continue
				}
				s.handleRequest(ctx, &req)
			} else {
				var notif rpcNotification
				if err := json.Unmarshal(line, &notif); err != nil {
					s.logger.Warn("failed to parse notification", "error", err)
					continue
				}
				s.handleNotification(ctx, &notif)
			}
		}
	}
}

// isRequest returns true if the JSON has a non-null "id" field (i.e. it's a request, not a notification).
func isRequest(line []byte) bool {
	var idCheck struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(line, &idCheck) != nil {
		return false
	}
	return len(idCheck.ID) > 0 && string(idCheck.ID) != "null"
}

func (s *Server) handleRequest(ctx context.Context, req *rpcRequest) {
	if req.Method != "initialize" && !s.initialized {
		s.writeError(req.ID, -32002, "Server not initialized")
		return
	}

	switch req.Method {
	case "initialize":
		if s.initialized {
			s.writeError(req.ID, -32002, "Server already initialized")
			return
		}
		s.handleInitialize(req.ID)
	case "tools/list":
		s.handleToolsList(req.ID)
	case "tools/call":
		s.handleToolsCall(ctx, req.ID, req.Params)
	default:
		s.writeError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *Server) handleNotification(ctx context.Context, notif *rpcNotification) {
	switch notif.Method {
	case "notifications/initialized":
		s.initialized = true
		s.logger.Info("MCP client initialized")
	default:
		s.logger.Debug("unhandled notification", "method", notif.Method)
	}
}

func (s *Server) handleInitialize(id rpcID) {
	names := s.provider.ToolNames()
	hasTools := len(names) > 0

	caps := map[string]any{}
	if hasTools {
		caps["tools"] = map[string]any{}
	}

	s.writeResponse(id, initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    caps,
		ServerInfo: map[string]string{
			"name":    "covo-agent",
			"version": "1.0.0",
		},
	})
}

func (s *Server) handleToolsList(id rpcID) {
	names := s.provider.ToolNames()
	tools := make([]mcpToolSchema, 0, len(names)+len(s.extraTools))

	for _, name := range names {
		tool, ok := s.provider.GetTool(name)
		if !ok {
			continue
		}
		schema := tool.Parameters
		if schema == nil {
			schema = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		tools = append(tools, mcpToolSchema{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}

	for _, rt := range s.extraTools {
		tools = append(tools, rt.Schema)
	}

	s.writeResponse(id, toolListResult{Tools: tools})
}

func (s *Server) handleToolsCall(ctx context.Context, id rpcID, rawParams json.RawMessage) {
	var params callToolParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		s.writeError(id, -32602, fmt.Sprintf("Invalid params: %s", err))
		return
	}

	// Check extra registered tools first
	if rt, ok := s.extraTools[params.Name]; ok {
		argsJSON, err := json.Marshal(params.Arguments)
		if err != nil {
			s.writeError(id, -32602, fmt.Sprintf("Invalid arguments: %s", err))
			return
		}
		result, err := rt.Handler(ctx, argsJSON)
		if err != nil {
			s.writeResponse(id, callToolResult{
				Content: []callToolContent{{Type: "text", Text: err.Error()}},
				IsError: true,
			})
			return
		}
		var text string
		switch v := result.(type) {
		case string:
			text = v
		default:
			data, _ := json.Marshal(v)
			text = string(data)
		}
		s.writeResponse(id, callToolResult{
			Content: []callToolContent{{Type: "text", Text: text}},
		})
		return
	}

	tool, ok := s.provider.GetTool(params.Name)
	if !ok {
		s.writeError(id, -32602, fmt.Sprintf("Unknown tool: %s", params.Name))
		return
	}

	args, err := json.Marshal(params.Arguments)
	if err != nil {
		s.writeError(id, -32602, fmt.Sprintf("Invalid arguments: %s", err))
		return
	}

	result, err := tool.Func(ctx, args)
	if err != nil {
		s.writeResponse(id, callToolResult{
			Content: []callToolContent{{Type: "text", Text: err.Error()}},
			IsError: true,
		})
		return
	}

	var text string
	switch v := result.(type) {
	case string:
		text = v
	default:
		data, _ := json.Marshal(v)
		text = string(data)
	}

	s.writeResponse(id, callToolResult{
		Content: []callToolContent{{Type: "text", Text: text}},
	})
}

func (s *Server) writeResponse(id rpcID, result any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("failed to marshal response", "error", err)
		return
	}

	fmt.Fprintln(s.output, string(data))
}

func (s *Server) writeError(id rpcID, code int64, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("failed to marshal error", "error", err)
		return
	}

	fmt.Fprintln(s.output, string(data))
}

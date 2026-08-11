package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// WSRelayConfig configures the ACP WebSocket relay server.
type WSRelayConfig struct {
	// Addr is the listen address (default: ":17891").
	Addr string
	// BinaryPath is the path to the covo-agent binary (default: os.Executable).
	BinaryPath string
	// Logger for relay events.
	Logger *slog.Logger
}

// WSRelay is a WebSocket-to-stdio bridge for the ACP server. It allows IDE
// plugins and other remote clients to connect to covo-agent's ACP server over
// WebSocket instead of stdio, enabling:
//   - Remote IDE integration across different machines
//   - Multiple concurrent client connections
//   - Network-transparent ACP access
//
// Each WebSocket connection spawns a dedicated `covo-agent acp` subprocess,
// with stdin/stdout relayed bidirectionally. This ensures full isolation
// between clients and leverages the existing stdio transport without
// modifying the ACP library.
type WSRelay struct {
	cfg       WSRelayConfig
	server    *http.Server
	logger    *slog.Logger
	mu        sync.Mutex
	listener  net.Listener
	conns     map[string]*wsConn
	connCount int
}

type wsConn struct {
	id     string
	cancel context.CancelFunc
}

// NewWSRelay creates a new WebSocket relay server.
func NewWSRelay(cfg WSRelayConfig) (*WSRelay, error) {
	if cfg.Addr == "" {
		cfg.Addr = ":17891"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.BinaryPath == "" {
		if exe, err := os.Executable(); err == nil {
			cfg.BinaryPath = exe
		} else {
			cfg.BinaryPath = "covo-agent"
		}
	}

	return &WSRelay{
		cfg:    cfg,
		logger: cfg.Logger,
		conns:  make(map[string]*wsConn),
	}, nil
}

// Start begins listening for WebSocket connections.
func (r *WSRelay) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", r.handleWebSocket)
	mux.HandleFunc("/health", r.handleHealth)
	mux.HandleFunc("/", r.handleRoot)

	listener, err := net.Listen("tcp", r.cfg.Addr)
	if err != nil {
		return fmt.Errorf("ws relay listen %s: %w", r.cfg.Addr, err)
	}

	r.mu.Lock()
	r.listener = listener
	r.mu.Unlock()

	r.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  0, // no timeout for long-lived connections
		WriteTimeout: 0,
	}

	r.logger.Info("ACP WebSocket relay listening",
		"addr", r.cfg.Addr,
		"binary", r.cfg.BinaryPath,
	)

	go func() {
		<-ctx.Done()
		r.Shutdown()
	}()

	return r.server.Serve(listener)
}

// Shutdown gracefully stops the relay server.
func (r *WSRelay) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Info("shutting down WebSocket relay", "connections", len(r.conns))

	for _, conn := range r.conns {
		conn.cancel()
	}

	if r.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.server.Shutdown(ctx)
	}
}

// handleWebSocket handles a single WebSocket connection by spawning an ACP
// subprocess and relaying messages bidirectionally.
func (r *WSRelay) handleWebSocket(w http.ResponseWriter, req *http.Request) {
	conn, _, _, err := ws.UpgradeHTTP(req, w)
	if err != nil {
		r.logger.Warn("websocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	connID := r.nextConnID()
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	r.mu.Lock()
	r.conns[connID] = &wsConn{id: connID, cancel: cancel}
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.conns, connID)
		r.mu.Unlock()
	}()

	r.logger.Info("websocket client connected", "conn_id", connID, "remote", req.RemoteAddr)

	// Spawn the ACP server subprocess
	cmd := exec.CommandContext(ctx, r.cfg.BinaryPath, "acp")
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		r.logger.Error("create stdin pipe", "err", err, "conn_id", connID)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.logger.Error("create stdout pipe", "err", err, "conn_id", connID)
		return
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		r.logger.Error("start ACP subprocess", "err", err, "conn_id", connID)
		return
	}

	r.logger.Info("ACP subprocess started", "conn_id", connID, "pid", cmd.Process.Pid)

	// Relay: WebSocket → subprocess stdin
	go func() {
		defer func() {
			stdin.Close()
			cancel()
		}()
		for {
			data, err := wsutil.ReadClientText(conn)
			if err != nil {
				return
			}
			// Write the raw JSON-RPC message to the subprocess stdin
			if _, err := stdin.Write(data); err != nil {
				return
			}
			// Ensure newline-delimited JSON-RPC
			if len(data) > 0 && data[len(data)-1] != '\n' {
				stdin.Write([]byte("\n"))
			}
		}
	}()

	// Relay: subprocess stdout → WebSocket
	// Use a buffered reader to read line-by-line (JSON-RPC is newline-delimited)
	go func() {
		scanner := bufio.NewReaderSize(stdout, 65536)
		for {
			line, err := scanner.ReadBytes('\n')
			if len(line) > 0 {
				if werr := wsutil.WriteServerText(conn, line); werr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				cancel()
				return
			}
		}
	}()

	// Wait for the subprocess to exit
	err = cmd.Wait()
	r.logger.Info("ACP subprocess exited", "conn_id", connID, "err", err)
}

// handleHealth returns a simple health check response.
func (r *WSRelay) handleHealth(w http.ResponseWriter, _ *http.Request) {
	r.mu.Lock()
	connCount := len(r.conns)
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":      "ok",
		"connections": connCount,
		"version":     Version,
	})
}

// handleRoot returns basic information about the relay.
func (r *WSRelay) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintln(w, `<!DOCTYPE html>
<html>
<head><title>covo-agent ACP Relay</title></head>
<body>
<h1>covo-agent ACP WebSocket Relay</h1>
<p>Connect to <code>ws://`+r.cfg.Addr+`/ws</code> to start an ACP session.</p>
<p>Health check: <a href="/health">/health</a></p>
</body>
</html>`)
}

func (r *WSRelay) nextConnID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connCount++
	return fmt.Sprintf("ws-%d", r.connCount)
}

// Addr returns the actual listening address.
func (r *WSRelay) Addr() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener == nil {
		return r.cfg.Addr
	}
	return r.listener.Addr().String()
}

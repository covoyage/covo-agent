package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/covoyage/covo-agent/internal/plugin"
)

// DefaultSocketName is the unix socket file (relative to the agent home dir)
// the gateway IPC server listens on. The mcpserver session tools and the CLI
// talk to the running gateway through it.
const DefaultSocketName = "gateway.sock"

// IPCSocketPath returns the well-known gateway socket path for a home dir.
func IPCSocketPath(homeDir string) string {
	return filepath.Join(homeDir, DefaultSocketName)
}

// ipcServer exposes a small JSON-over-unix-socket HTTP API so other
// processes (mcpserver session tools, CLI) can drive the running gateway:
// send outbound messages, poll events, and decide permission requests.
type ipcServer struct {
	gw  *Gateway
	srv *http.Server
	ln  net.Listener
}

func newIPCServer(gw *Gateway, socketPath string) (*ipcServer, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	// A stale socket from a crashed gateway would break Listen.
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", socketPath, err)
	}
	// Only the local user may talk to the gateway.
	_ = os.Chmod(socketPath, 0o600)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/send", func(w http.ResponseWriter, r *http.Request) {
		ipcHandle(gw, w, r, handleSend)
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		ipcHandle(gw, w, r, handleEvents)
	})
	mux.HandleFunc("GET /api/permissions", func(w http.ResponseWriter, r *http.Request) {
		ipcHandle(gw, w, r, handlePermissionsList)
	})
	mux.HandleFunc("POST /api/permissions/respond", func(w http.ResponseWriter, r *http.Request) {
		ipcHandle(gw, w, r, handlePermissionRespond)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		ipcHandle(gw, w, r, handleStatus)
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return &ipcServer{gw: gw, srv: srv, ln: ln}, nil
}

func (s *ipcServer) serve() {
	if err := s.srv.Serve(s.ln); err != nil && err != http.ErrServerClosed {
		slog.Warn("gateway: ipc server error", "error", err)
	}
}

func (s *ipcServer) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	_ = s.ln.Close()
}

// ---- handlers ----

type ipcHandler func(g *Gateway, r *http.Request) (int, any)

func ipcHandle(g *Gateway, w http.ResponseWriter, r *http.Request, h ipcHandler) {
	status, body := h(g, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type sendRequest struct {
	Platform  string `json:"platform,omitempty"`
	ChannelID string `json:"channel_id"`
	Text      string `json:"text"`
}

func handleSend(g *Gateway, r *http.Request) (int, any) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()}
	}
	if strings.TrimSpace(req.ChannelID) == "" || strings.TrimSpace(req.Text) == "" {
		return http.StatusBadRequest, map[string]string{"error": "channel_id and text are required"}
	}

	platform, err := g.resolvePlatform(req.Platform)
	if err != nil {
		return http.StatusBadRequest, map[string]string{"error": err.Error()}
	}

	ctx := r.Context()
	if err := platform.SendMessage(ctx, req.ChannelID, plugin.OutgoingMessage{Text: req.Text}); err != nil {
		return http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("send via %s: %v", platform.Name(), err),
		}
	}
	g.events.Append(Event{
		Type: EventReplySent, Platform: platform.Name(),
		ChannelID: req.ChannelID, Text: req.Text, Detail: "ipc send",
	})
	return http.StatusOK, map[string]any{
		"status": "sent", "platform": platform.Name(), "channel_id": req.ChannelID,
	}
}

func handleEvents(g *Gateway, r *http.Request) (int, any) {
	q := r.URL.Query()
	cursor, _ := strconv.ParseUint(q.Get("cursor"), 10, 64)
	waitSeconds, _ := strconv.Atoi(q.Get("wait_seconds"))
	var events []Event
	if waitSeconds > 0 {
		events = g.events.Wait(r.Context(), cursor, time.Duration(waitSeconds)*time.Second)
	} else {
		events = g.events.Since(cursor)
	}
	if events == nil {
		events = []Event{}
	}
	next := cursor
	for _, e := range events {
		if e.Seq > next {
			next = e.Seq
		}
	}
	return http.StatusOK, map[string]any{
		"events": events, "cursor": next, "latest": g.events.LastSeq(),
	}
}

func handlePermissionsList(g *Gateway, r *http.Request) (int, any) {
	pending := g.perms.ListOpen()
	if pending == nil {
		pending = []PermissionRequest{}
	}
	return http.StatusOK, map[string]any{"pending": pending}
}

type permissionRespondRequest struct {
	ID     string `json:"id"`
	Choice string `json:"choice"`
}

func handlePermissionRespond(g *Gateway, r *http.Request) (int, any) {
	var req permissionRespondRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()}
	}
	switch req.Choice {
	case "once", "session", "always", "deny":
	default:
		return http.StatusBadRequest, map[string]string{
			"error": "choice must be one of: once, session, always, deny",
		}
	}
	if !g.perms.Respond(req.ID, req.Choice) {
		return http.StatusNotFound, map[string]string{
			"error": "permission request not found or already decided: " + req.ID,
		}
	}
	return http.StatusOK, map[string]any{"status": "ok", "id": req.ID, "choice": req.Choice}
}

func handleStatus(g *Gateway, r *http.Request) (int, any) {
	return http.StatusOK, g.Status()
}

// ---- helpers ----

// resolvePlatform picks the platform adapter for an outbound send by name.
// With a single configured platform the name may be omitted.
func (g *Gateway) resolvePlatform(name string) (plugin.PlatformProvider, error) {
	g.mu.Lock()
	platforms := g.platformMap
	g.mu.Unlock()
	if len(platforms) == 0 {
		return nil, fmt.Errorf("no platforms configured")
	}
	if name == "" {
		if len(platforms) == 1 {
			for _, p := range platforms {
				return p, nil
			}
		}
		names := make([]string, 0, len(platforms))
		for n := range platforms {
			names = append(names, n)
		}
		return nil, fmt.Errorf("multiple platforms configured, specify one of: %s",
			strings.Join(sortedNames(names), ", "))
	}
	p, ok := platforms[name]
	if !ok {
		return nil, fmt.Errorf("unknown platform %q (configured: %s)", name,
			strings.Join(sortedNames(mapKeys(platforms)), ", "))
	}
	return p, nil
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedNames(names []string) []string {
	out := append([]string(nil), names...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

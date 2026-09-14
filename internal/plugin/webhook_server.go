package plugin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
)

type WebhookHandler func(ctx context.Context, body []byte, headers http.Header) (*IncomingMessage, error)

// WebhookResponse lets a handler customize the HTTP response (status and
// body) and/or deliver an incoming message. If Body is nil the default
// {"ok":true} response is written.
type WebhookResponse struct {
	StatusCode int
	Body       any // string -> written as plain text; otherwise JSON-marshalled
	Message    *IncomingMessage
}

// WebhookHandlerWithRequest is like WebhookHandler but receives the full
// *http.Request (for URL query parameters, method, etc.) and can return a
// custom WebhookResponse.
type WebhookHandlerWithRequest func(ctx context.Context, body []byte, r *http.Request) (*WebhookResponse, error)

type WebhookServer struct {
	mu        sync.Mutex
	addr      string
	server    *http.Server
	mux       *http.ServeMux
	onMessage func(IncomingMessage)
	logger    *slog.Logger
}

func NewWebhookServer(addr string, logger *slog.Logger) *WebhookServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookServer{
		addr:   addr,
		mux:    http.NewServeMux(),
		logger: logger,
	}
}

func (s *WebhookServer) RegisterRoute(path string, handler WebhookHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		s.handleWebhook(w, r, func(ctx context.Context, body []byte, req *http.Request) (*WebhookResponse, error) {
			msg, err := handler(ctx, body, req.Header)
			if err != nil {
				return nil, err
			}
			return &WebhookResponse{Message: msg}, nil
		})
	})
}

// RegisterRouteWithRequest registers a handler that sees the full request and
// may return a custom WebhookResponse (e.g. a challenge echo).
func (s *WebhookServer) RegisterRouteWithRequest(path string, handler WebhookHandlerWithRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		s.handleWebhook(w, r, handler)
	})
}

func (s *WebhookServer) SetOnMessage(callback func(IncomingMessage)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onMessage = callback
}

func (s *WebhookServer) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.server != nil {
		s.mu.Unlock()
		return nil
	}
	s.server = &http.Server{
		Addr:    s.addr,
		Handler: s.mux,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}
	s.mu.Unlock()

	s.logger.Info("webhook server starting", "addr", s.addr)

	errCh := make(chan error, 1)
	safego.SafeGo(func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}, s.logger)

	select {
	case err := <-errCh:
		return err
	case <-time.After(100 * time.Millisecond):
	}

	return nil
}

func (s *WebhookServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	server := s.server
	s.server = nil
	s.mu.Unlock()

	if server != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
	return nil
}

func (s *WebhookServer) Addr() string {
	return s.addr
}

func (s *WebhookServer) handleWebhook(w http.ResponseWriter, r *http.Request, handler WebhookHandlerWithRequest) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.logger.Warn("webhook: read body failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	resp, err := handler(r.Context(), body, r)
	if err != nil {
		s.logger.Warn("webhook: handler failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if resp == nil {
		resp = &WebhookResponse{}
	}

	if resp.Message != nil {
		s.mu.Lock()
		callback := s.onMessage
		s.mu.Unlock()
		if callback != nil {
			callback(*resp.Message)
		}
	}

	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}

	if resp.Body != nil {
		if text, ok := resp.Body.(string); ok {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(text))
			return
		}
		data, err := json.Marshal(resp.Body)
		if err != nil {
			s.logger.Warn("webhook: marshal response", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(data)
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

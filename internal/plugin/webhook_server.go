package plugin

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
)

type WebhookHandler func(ctx context.Context, body []byte, headers http.Header) (*IncomingMessage, error)

type WebhookServer struct {
	mu        sync.Mutex
	addr      string
	server    *http.Server
	mux       *http.ServeMux
	routes    map[string]WebhookHandler
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
		routes: make(map[string]WebhookHandler),
		logger: logger,
	}
}

func (s *WebhookServer) RegisterRoute(path string, handler WebhookHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = handler
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

func (s *WebhookServer) handleWebhook(w http.ResponseWriter, r *http.Request, handler WebhookHandler) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.logger.Warn("webhook: read body failed", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	msg, err := handler(r.Context(), body, r.Header)
	if err != nil {
		s.logger.Warn("webhook: handler failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if msg == nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
		return
	}

	s.mu.Lock()
	callback := s.onMessage
	s.mu.Unlock()

	if callback != nil {
		callback(*msg)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true}`))
}

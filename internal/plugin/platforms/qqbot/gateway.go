package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/covoyage/covo-agent/internal/safego"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// runGatewaySession dials the websocket gateway, performs the Identify or
// Resume handshake and pumps messages until the connection drops or ctx is
// done. Reconnection (op 7 / op 9) is handled by the caller loop: when a
// session id and last dispatch seq are known, the next Hello is answered with
// op 6 Resume instead of a full op 2 Identify.
func (a *Adapter) runGatewaySession(ctx context.Context, gwURL string) error {
	dialer := &ws.Dialer{
		Header: ws.HandshakeHeaderHTTP(http.Header{
			"Authorization": []string{"QQBot " + a.cachedToken()},
		}),
	}
	conn, br, _, err := dialer.Dial(ctx, gwURL)
	if err != nil {
		return fmt.Errorf("qqbot: dial gateway: %w", err)
	}
	defer conn.Close()

	// br may already hold bytes read from conn during the handshake (e.g. an
	// early server frame). Keep reading through it — it falls back to conn
	// transparently once its buffer is drained. Writes still go to conn.
	rw := readWriter{Reader: conn, Writer: conn}
	if br != nil {
		rw.Reader = br
	}

	a.logger.Info("qqbot: gateway connected", "url", gwURL)

	errCh := make(chan error, 1)
	events := make(chan gwEvent, 16)
	helloCh := make(chan int, 1)

	safego.SafeGo(func() {
		defer close(events)
		for {
			data, _, err := wsutil.ReadServerData(rw)
			if err != nil {
				errCh <- err
				return
			}

			var payload struct {
				Op int             `json:"op"`
				D  json.RawMessage `json:"d"`
				S  *int64          `json:"s"`
				T  string          `json:"t"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				a.logger.Warn("qqbot: decode gateway frame failed", "error", err)
				continue
			}
			if payload.S != nil {
				a.trackSeq(*payload.S)
			}

			switch payload.Op {
			case opHello:
				var hello struct {
					HeartbeatInterval int `json:"heartbeat_interval"`
				}
				_ = json.Unmarshal(payload.D, &hello)
				if hello.HeartbeatInterval <= 0 {
					hello.HeartbeatInterval = 30000
				}
				helloCh <- hello.HeartbeatInterval
			case opDispatch:
				if payload.T == "READY" {
					// Store the session id so a later reconnect can Resume.
					var ready struct {
						SessionID string `json:"session_id"`
					}
					_ = json.Unmarshal(payload.D, &ready)
					a.setWebsocketSession(ready.SessionID)
				}
				events <- gwEvent{Type: payload.T, Data: payload.D}
			case opHeartbeatACK:
				// heartbeat acknowledged; nothing to do
			case opReconnect:
				errCh <- fmt.Errorf("qqbot: server requested reconnect (op 7)")
				return
			case opInvalidSession:
				// d=true: session resumable; d=false/absent: re-Identify from
				// scratch (the reference adapter adapter.py op 9 handling).
				var resumable bool
				_ = json.Unmarshal(payload.D, &resumable)
				if !resumable {
					a.logger.Info("qqbot: invalid session (op 9, not resumable), clearing session")
					a.clearWebsocketSession()
				} else {
					a.logger.Info("qqbot: invalid session (op 9, resumable)")
				}
				errCh <- fmt.Errorf("qqbot: invalid session (op 9)")
				return
			}
		}
	}, a.logger)

	var interval int
	select {
	case interval = <-helloCh:
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}

	// Reply to Hello with Resume when a session is available, else Identify.
	if sessionID, seq, canResume := a.websocketSession(); canResume {
		resume := map[string]any{
			"op": opResume,
			"d": map[string]any{
				"token":      "QQBot " + a.cachedToken(),
				"session_id": sessionID,
				"seq":        seq,
			},
		}
		resumeData, _ := json.Marshal(resume)
		if err := wsutil.WriteClientText(conn, resumeData); err != nil {
			return fmt.Errorf("qqbot: send resume: %w", err)
		}
		a.logger.Info("qqbot: resume sent", "session_id", sessionID, "seq", seq)
	} else {
		identify := map[string]any{
			"op": opIdentify,
			"d": map[string]any{
				"token":   "QQBot " + a.cachedToken(),
				"intents": intents,
				"shard":   []int{0, 1},
			},
		}
		identifyData, _ := json.Marshal(identify)
		if err := wsutil.WriteClientText(conn, identifyData); err != nil {
			return fmt.Errorf("qqbot: send identify: %w", err)
		}
	}

	heartbeat := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case ev, ok := <-events:
			if !ok {
				return fmt.Errorf("qqbot: gateway reader stopped")
			}
			a.dispatch(ctx, ev.Type, ev.Data)
		case <-heartbeat.C:
			hbData, _ := json.Marshal(map[string]any{"op": opHeartbeat, "d": a.currentSeq()})
			if err := wsutil.WriteClientText(conn, hbData); err != nil {
				return fmt.Errorf("qqbot: send heartbeat: %w", err)
			}
		}
	}
}

// cachedToken returns the current access token without blocking on refresh;
// identification uses the cached value (refresh happens via REST).
func (a *Adapter) cachedToken() string {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()
	if a.botToken != "" {
		return a.botToken
	}
	return a.accessToken
}

type gwEvent struct {
	Type string
	Data json.RawMessage
}

// readWriter pairs a read side (possibly a bufio.Reader wrapping conn) with a
// write side (conn itself).
type readWriter struct {
	io.Reader
	io.Writer
}

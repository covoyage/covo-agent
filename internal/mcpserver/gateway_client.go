package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// gatewayClient talks to the local gateway IPC API (JSON over a unix
// socket). The gateway exposes send/events/permissions endpoints that back
// the messages_send, events_poll/events_wait and permissions_* session
// tools. When the gateway is not running, calls fail fast and the tools
// report an actionable offline status instead of pretending success.
type gatewayClient struct {
	socketPath string
	http       *http.Client
}

func defaultGatewaySocket() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".covo-agent", "gateway.sock")
}

func newGatewayClient(socketPath string) *gatewayClient {
	if socketPath == "" {
		socketPath = defaultGatewaySocket()
	}
	c := &gatewayClient{socketPath: socketPath}
	c.http = &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
	return c
}

// gatewayCall performs a JSON request against the gateway IPC API. It
// returns (decoded body, nil) on any HTTP response, and an error only when
// the gateway cannot be reached at all.
func (c *gatewayClient) gatewayCall(ctx context.Context, method, path string, payload any) (int, map[string]any, error) {
	var bodyReader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://gateway"+path, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("decode gateway response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// offlineStatus reports that no gateway process is listening. This is the
// honest replacement for the old unconditional "gateway_required" stub: the
// check is real — it probes the well-known socket.
func (c *gatewayClient) offlineStatus(err error) map[string]any {
	return map[string]any{
		"status":  "unavailable",
		"code":    "gateway_offline",
		"message": fmt.Sprintf("gateway is not reachable at %s: %v. Start it with `covo-agent gateway start`.", c.socketPath, err),
	}
}

// Send posts an outbound message through the gateway.
func (c *gatewayClient) Send(ctx context.Context, platform, channelID, text string) (map[string]any, error) {
	payload := map[string]string{"channel_id": channelID, "text": text}
	if platform != "" {
		payload["platform"] = platform
	}
	status, body, err := c.gatewayCall(ctx, http.MethodPost, "/api/send", payload)
	if err != nil {
		return c.offlineStatus(err), nil
	}
	if status != http.StatusOK {
		return body, nil
	}
	return map[string]any{
		"status": "sent", "platform": body["platform"], "channel_id": body["channel_id"],
	}, nil
}

// Events polls (waitSeconds == 0) or long-polls (waitSeconds > 0) gateway
// events after the given cursor.
func (c *gatewayClient) Events(ctx context.Context, cursor uint64, waitSeconds int) (map[string]any, error) {
	path := "/api/events?cursor=" + strconv.FormatUint(cursor, 10)
	if waitSeconds > 0 {
		path += "&wait_seconds=" + strconv.Itoa(waitSeconds)
	}
	status, body, err := c.gatewayCall(ctx, http.MethodGet, path, nil)
	if err != nil {
		return c.offlineStatus(err), nil
	}
	if status != http.StatusOK {
		return body, nil
	}
	timedOut := false
	if evs, ok := body["events"].([]any); ok && len(evs) == 0 && waitSeconds > 0 {
		timedOut = true
	}
	return map[string]any{
		"events": body["events"], "cursor": body["cursor"],
		"latest": body["latest"], "timed_out": timedOut,
	}, nil
}

// PermissionsOpen lists pending gateway permission requests.
func (c *gatewayClient) PermissionsOpen(ctx context.Context) (map[string]any, error) {
	status, body, err := c.gatewayCall(ctx, http.MethodGet, "/api/permissions", nil)
	if err != nil {
		return c.offlineStatus(err), nil
	}
	if status != http.StatusOK {
		return body, nil
	}
	return map[string]any{"pending": body["pending"]}, nil
}

// PermissionRespond delivers a decision for a pending permission request.
func (c *gatewayClient) PermissionRespond(ctx context.Context, id, choice string) (map[string]any, error) {
	payload := map[string]string{"id": id, "choice": choice}
	status, body, err := c.gatewayCall(ctx, http.MethodPost, "/api/permissions/respond", payload)
	if err != nil {
		return c.offlineStatus(err), nil
	}
	if status != http.StatusOK {
		return body, nil
	}
	return map[string]any{"status": "ok", "id": id, "choice": choice}, nil
}

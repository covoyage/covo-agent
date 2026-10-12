package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

func TestWebFetchDNSPrecheckHonorsDeadline(t *testing.T) {
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	t.Cleanup(func() { net.DefaultResolver = original })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	webFetch := agenttools.BuildWebFetchTool(&SafeRoundTripper{transport: http.DefaultTransport})
	done := make(chan error, 1)
	go func() {
		_, err := webFetch.Func(ctx, json.RawMessage(`{"url":"https://deadline-check.invalid/unique"}`))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cannot resolve host") || !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("DNS deadline error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DNS precheck ignored request deadline")
	}
}

func TestWebFetchCanceledContextStopsPrecheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &SafeRoundTripper{transport: http.DefaultTransport}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://canceled-check.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request not rejected before DNS/dial: %v", err)
	}
}

// TestWebFetchToolBlocksInternalAddrs verifies that the web_fetch tool, when
// wired with the safe HTTP transport, rejects requests to loopback and
// link-local (cloud metadata) addresses before any connection is dialed.
func TestWebFetchToolBlocksInternalAddrs(t *testing.T) {
	webFetch := agenttools.BuildWebFetchTool(NewSafeClient().Transport)

	for _, url := range []string{
		"http://127.0.0.1:9/x",
		"http://169.254.169.254/latest/meta-data/",
	} {
		args, err := json.Marshal(map[string]string{"url": url})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		_, err = webFetch.Func(context.Background(), args)
		if err == nil {
			t.Errorf("expected %s to be blocked", url)
			continue
		}
		if strings.Contains(err.Error(), "connect") || strings.Contains(err.Error(), "refused") {
			t.Errorf("%s: request reached the dial stage instead of being rejected: %v", url, err)
		}
	}
}

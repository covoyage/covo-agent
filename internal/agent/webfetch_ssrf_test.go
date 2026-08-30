package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agenttools "github.com/covoyage/covo-agent/internal/tools"
)

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

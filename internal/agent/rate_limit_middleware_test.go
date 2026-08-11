package agent

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/covoyage/covonaut/agentcore"
)

func TestRateLimitToolMiddleware(t *testing.T) {
	tests := []struct {
		name      string
		headers   http.Header
		wantBlock bool
	}{
		{
			name: "active exhausted window",
			headers: http.Header{
				"X-Ratelimit-Limit-Requests":     []string{"10"},
				"X-Ratelimit-Remaining-Requests": []string{"0"},
				"X-Ratelimit-Reset-Requests":     []string{"60"},
			},
			wantBlock: true,
		},
		{
			name: "capacity available",
			headers: http.Header{
				"X-Ratelimit-Limit-Requests":     []string{"10"},
				"X-Ratelimit-Remaining-Requests": []string{"1"},
				"X-Ratelimit-Reset-Requests":     []string{"60"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := NewRateLimitState()
			state.Capture(test.headers)
			agent := &CovoAgent{rateLimitState: state}
			called := false
			next := func(context.Context, agentcore.ToolCall) (string, error) {
				called = true
				return "ok", nil
			}
			result, err := agent.rateLimitToolMiddleware()(next)(context.Background(), agentcore.ToolCall{})
			if test.wantBlock {
				if err == nil || !strings.Contains(err.Error(), "rate limit exhausted") || called {
					t.Fatalf("blocked call = (%q, %v, called=%v)", result, err, called)
				}
				return
			}
			if err != nil || result != "ok" || !called {
				t.Fatalf("allowed call = (%q, %v, called=%v)", result, err, called)
			}
		})
	}
}

func TestRateLimitStateAllowsExpiredWindow(t *testing.T) {
	state := NewRateLimitState()
	state.RequestsMin = RateLimitBucket{
		Limit:      10,
		Remaining:  0,
		ResetSecs:  1,
		CapturedAt: time.Now().Add(-2 * time.Second),
	}
	if state.ShouldBackoff() {
		t.Fatal("ShouldBackoff = true for expired reset window")
	}
}

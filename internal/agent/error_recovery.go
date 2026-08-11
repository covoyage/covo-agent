package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/covoyage/covo-agent/internal/agent/recovery"
	"github.com/covoyage/covonaut/agentcore"
)

// compactionRetryInstruction is injected after a compaction-triggered retry
// to tell the model to continue from the compacted state rather than restart.
const compactionRetryInstruction = `The conversation history was compacted to fit within context limits. Continue with the task using the summarized conversation above. Do NOT restart from scratch or redo work already completed.`

// errorRecoveryMiddleware handles errors that the agentcore retry loop cannot:
// context overflow → compact + retry, credential exhaustion → mark for rotation.
// Transient errors (rate limits, server errors, timeouts) are left to agentcore's
// callProviderWithRetry so we don't double-retry.
type errorRecoveryMiddleware struct {
	inner  agentcore.Provider
	ca     *CovoAgent
	logger *slog.Logger
}

func NewErrorRecoveryMiddleware(ca *CovoAgent) ProviderMiddleware {
	return func(inner agentcore.Provider) agentcore.Provider {
		return &errorRecoveryMiddleware{
			inner:  inner,
			ca:     ca,
			logger: ca.baseCfg.Logger,
		}
	}
}

func (m *errorRecoveryMiddleware) Complete(ctx context.Context, req *agentcore.ProviderRequest) (*agentcore.ProviderResponse, error) {
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := m.inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}

		ce := recovery.ClassifyError(err, 0, m.ca.providerName, m.ca.model)

		// Mark credential exhausted for future calls (doesn't affect this call)
		if ce.ShouldRotateCred && m.ca.credentialPool != nil {
			if current := m.ca.GetActiveCredential(); current != nil {
				m.ca.credentialPool.MarkExhausted(current.ID, fmt.Sprintf("%d", ce.StatusCode))
			}
		}

		// Context overflow: compact and retry.
		if ce.ShouldCompress {
			if compErr := m.ca.core.ForceCompact(ctx); compErr != nil {
				return nil, fmt.Errorf("compact failed: %w / original: %w", compErr, err)
			}
			// Rebuild messages from compacted state and inject retry instruction
			// so the model continues from the summary rather than restarting.
			msgs := m.ca.core.State().Messages()
			msgs = append(msgs, agentcore.Message{
				Role:    agentcore.RoleUser,
				Content: compactionRetryInstruction,
			})
			req.Messages = msgs
			continue
		}

		// Suspend session on rate limit after all retries exhausted.
		if ce.IsRateLimit() && m.ca.sessionSuspendFn != nil {
			sessionKey := m.ca.sessionMgr.CurrentID()
			if sessionKey != "" {
				m.ca.sessionSuspendFn(sessionKey, "rate_limited", 60*time.Second)
				slog.Warn("agent: session suspended due to rate limit",
					"session", sessionKey, "ttl", "60s")
			}
		}

		// Let agentcore's callProviderWithRetry handle transient errors
		return nil, err
	}
	return nil, fmt.Errorf("recovery failed after 3 attempts")
}

func (m *errorRecoveryMiddleware) Stream(ctx context.Context, req *agentcore.ProviderRequest) (<-chan agentcore.StreamDelta, error) {
	return m.inner.Stream(ctx, req)
}

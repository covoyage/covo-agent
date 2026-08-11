package visibility

import "context"

// Mode controls how session content is shared across channels.
type Mode int

const (
	// Isolated (default): each channel only sees its own session content.
	// session_search and session_fts return results only from the current channel's session.
	Isolated Mode = iota

	// Shared: all channels can see all other channels' session content.
	// session_search and session_fts return results from all sessions.
	Shared

	// Whitelist: only explicitly listed channels can see each other's content.
	// The AllowedPeers list defines which channel keys may cross-reference each other.
	Whitelist
)

type contextKey string

const ctxPolicy contextKey = "visibility_policy"

// Policy defines cross-channel session visibility rules.
type Policy struct {
	Mode         Mode
	CurrentKey   string   // current channel key (platform:channelID)
	AllowedPeers []string // channel keys allowed for cross-session access (whitelist mode)
}

// ShouldAllow returns true if the current channel is allowed to see content
// from the target channel key.
func (p *Policy) ShouldAllow(targetKey string) bool {
	if p == nil {
		return true // no policy = everything allowed
	}
	if p.CurrentKey == targetKey {
		return true // always allow own session
	}
	switch p.Mode {
	case Shared:
		return true
	case Whitelist:
		for _, peer := range p.AllowedPeers {
			if peer == targetKey {
				return true
			}
		}
		return false
	default: // Isolated
		return false
	}
}

// WithPolicy stores the policy in the context.
func WithPolicy(ctx context.Context, p *Policy) context.Context {
	return context.WithValue(ctx, ctxPolicy, p)
}

// PolicyFromContext retrieves the policy from the context.
// Returns nil if no policy is set (implies full visibility).
func PolicyFromContext(ctx context.Context) *Policy {
	p, _ := ctx.Value(ctxPolicy).(*Policy)
	return p
}

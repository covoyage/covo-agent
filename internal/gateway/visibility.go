package gateway

import (
	"context"

	"github.com/covoyage/covo-agent/internal/visibility"
)

// VisibilityPolicy is an alias for visibility.Policy for backward compatibility.
type VisibilityPolicy = visibility.Policy

// VisibilityMode is an alias for visibility.Mode.
type VisibilityMode = visibility.Mode

// Re-export visibility constants for use in gateway config.
const (
	VisibilityIsolated  = visibility.Isolated
	VisibilityShared    = visibility.Shared
	VisibilityWhitelist = visibility.Whitelist
)

// WithVisibilityPolicy stores the policy in the context.
func WithVisibilityPolicy(ctx context.Context, vp *visibility.Policy) context.Context {
	return visibility.WithPolicy(ctx, vp)
}

// VisibilityPolicyFromContext retrieves the policy from the context.
func VisibilityPolicyFromContext(ctx context.Context) *visibility.Policy {
	return visibility.PolicyFromContext(ctx)
}

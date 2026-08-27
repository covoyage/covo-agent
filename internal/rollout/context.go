package rollout

import "context"

// interactionKindKey carries an explicit interaction kind annotation through
// the call context so the Recorder can name auxiliary calls precisely (e.g.
// "compression", "title", "review") instead of lumping them into "aux".
type interactionKindKey struct{}

// WithInteractionKind returns a context annotated with an interaction kind.
// The Recorder's classifyKind prefers this explicit annotation over its
// model-run-info heuristic, so auxiliary tasks can name themselves.
func WithInteractionKind(ctx context.Context, kind string) context.Context {
	return context.WithValue(ctx, interactionKindKey{}, kind)
}

// interactionKindFromContext returns the annotated interaction kind, if any.
func interactionKindFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(interactionKindKey{}).(string)
	return v, ok && v != ""
}

// parentRolloutIDKey carries the parent agent's rollout ID through the context
// so a spawned subagent can link its own rollout to the parent's trace.
type parentRolloutIDKey struct{}

// WithParentRolloutID returns a context annotated with the parent rollout ID.
func WithParentRolloutID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, parentRolloutIDKey{}, id)
}

// ParentRolloutIDFromContext returns the annotated parent rollout ID, if any.
func ParentRolloutIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(parentRolloutIDKey{}).(string)
	return v
}

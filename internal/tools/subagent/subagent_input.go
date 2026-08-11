package subagent

import "context"

type subagentInputCtxKey struct{}

// WithSubagentInput stores a SubagentRun in the context so that
// DrainSubagentInput can retrieve pending input messages.
func WithSubagentInput(ctx context.Context, run *SubagentRun) context.Context {
	return context.WithValue(ctx, subagentInputCtxKey{}, run)
}

// DrainSubagentInput drains all pending input messages sent via send_input.
// Should be called by the agent loop before each LLM call to check for
// parent-to-child messages.
func DrainSubagentInput(ctx context.Context) []string {
	val := ctx.Value(subagentInputCtxKey{})
	if val == nil {
		return nil
	}
	run, ok := val.(*SubagentRun)
	if !ok {
		return nil
	}
	return run.DrainInput()
}

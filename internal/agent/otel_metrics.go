package agent

import (
	"context"

	"github.com/covoyage/covonaut/agentcore"
)

// metricsSink is the union of agentcore.Metrics (usage/duration/errors, backed
// by the library's model metrics middleware) and the cost recording extension,
// so auxiliary providers can feed both from one sink. *telemetry.
// ModelMetricsRecorder satisfies it; the interface keeps the middleware
// testable without a live OTel pipeline.
type metricsSink interface {
	agentcore.Metrics
	RecordCost(ctx context.Context, model string, amountUSD float64, status, source string)
}

// costMetricsSink records USD cost for completed LLM calls. Narrower than
// metricsSink: the cost middleware only needs the cost dimension.
type costMetricsSink interface {
	RecordCost(ctx context.Context, model string, amountUSD float64, status, source string)
}

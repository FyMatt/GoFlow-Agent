package agent

import (
	"context"
	"strings"
)

type agentRunIDContextKey struct{}

// WithAgentRunID attaches a durable ordinary agent run id to an execution context.
func WithAgentRunID(ctx context.Context, runID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, agentRunIDContextKey{}, runID)
}

func agentRunIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	runID, _ := ctx.Value(agentRunIDContextKey{}).(string)
	return strings.TrimSpace(runID)
}

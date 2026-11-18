package interfaces

import (
	"context"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// MCPClient describes the capabilities needed from the MCP layer.
type MCPClient interface {
	ListTools(ctx context.Context) ([]schema.Tool, error)
	RefreshTools(ctx context.Context) ([]schema.Tool, error)
	CallTool(ctx context.Context, name string, arguments []byte) (schema.ToolResult, error)
	HealthStatus(ctx context.Context) map[string]string
	ToolNames() []string
}

// MCPServerCallMetrics describes current runtime pressure on one MCP server.
type MCPServerCallMetrics struct {
	MaxConcurrentCalls int
	ActiveCalls        int
	QueuedCalls        int
	AvailableCallSlots int
}

// MCPMetricsReporter is implemented by MCP clients that expose live runtime metrics.
type MCPMetricsReporter interface {
	MCPCallMetrics() map[string]MCPServerCallMetrics
}

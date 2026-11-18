package mcp

import (
	"context"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Router provides a narrow tool routing API for the agent layer.
type Router struct {
	manager *Manager
}

// NewRouter constructs a tool router.
func NewRouter(manager *Manager) *Router {
	return &Router{manager: manager}
}

// ListTools returns all currently available tools.
func (r *Router) ListTools(ctx context.Context) ([]schema.Tool, error) {
	return r.manager.ListTools(ctx)
}

// Call forwards a request to the appropriate tool backend.
func (r *Router) Call(ctx context.Context, name string, arguments []byte) (schema.ToolResult, error) {
	return r.manager.CallTool(ctx, name, arguments)
}

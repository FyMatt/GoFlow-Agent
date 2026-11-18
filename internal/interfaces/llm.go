package interfaces

import (
	"context"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// StreamHandler receives incremental provider output.
type StreamHandler func(event schema.StreamEvent) error

// LLMClient describes the capabilities needed from the LLM layer.
type LLMClient interface {
	Chat(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error)
	StreamChat(ctx context.Context, req schema.ChatRequest, handler StreamHandler) (schema.ChatResponse, error)
	Capabilities() []string
}

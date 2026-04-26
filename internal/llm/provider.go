package llm

import (
	"context"
	"fmt"
	"sort"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Registry wraps supported model backends.
type Registry struct {
	providers map[string]interfaces.LLMClient
	configs   map[string]config.LLMConfig
}

// NewRegistry builds the configured provider set.
func NewRegistry(defaultLLM config.LLMConfig, named map[string]config.LLMConfig) (*Registry, error) {
	providers := make(map[string]interfaces.LLMClient)
	configs := make(map[string]config.LLMConfig)
	for name, cfg := range named {
		client, err := buildClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("build provider %s: %w", name, err)
		}
		providers[name] = client
		configs[name] = cfg
	}
	if len(providers) == 0 {
		client, err := buildClient(defaultLLM)
		if err != nil {
			return nil, err
		}
		name := defaultProviderName(defaultLLM)
		providers[name] = client
		configs[name] = defaultLLM
	}
	return &Registry{providers: providers, configs: configs}, nil
}

func buildClient(cfg config.LLMConfig) (interfaces.LLMClient, error) {
	switch cfg.Provider {
	case "", "openai-compatible":
		return NewClient(cfg), nil
	case "anthropic":
		return NewAnthropicClient(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}

func defaultProviderName(cfg config.LLMConfig) string {
	if cfg.Provider == "" {
		return "default"
	}
	return cfg.Provider
}

func (r *Registry) Client(name string) (interfaces.LLMClient, error) {
	resolvedName := name
	if resolvedName == "" && len(r.providers) == 1 {
		for providerName := range r.providers {
			resolvedName = providerName
			break
		}
	}
	provider, ok := r.providers[resolvedName]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}
	return &fallbackClient{primaryName: resolvedName, primary: provider, registry: r}, nil
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Config(name string) (config.LLMConfig, bool) {
	cfg, ok := r.configs[name]
	return cfg, ok
}

type fallbackClient struct {
	primaryName string
	primary     interfaces.LLMClient
	registry    *Registry
	used        bool
}

func (c *fallbackClient) Chat(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	resp, err := c.primary.Chat(ctx, req)
	if err == nil {
		c.used = false
		return resp, nil
	}
	fallback, fallbackErr := c.fallback()
	if fallbackErr != nil {
		return schema.ChatResponse{}, err
	}
	resp, err = fallback.Chat(ctx, req)
	if err == nil {
		c.used = true
	}
	return resp, err
}

func (c *fallbackClient) StreamChat(ctx context.Context, req schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	resp, err := c.primary.StreamChat(ctx, req, handler)
	if err == nil {
		c.used = false
		return resp, nil
	}
	fallback, fallbackErr := c.fallback()
	if fallbackErr != nil {
		return schema.ChatResponse{}, err
	}
	resp, err = fallback.StreamChat(ctx, req, handler)
	if err == nil {
		c.used = true
	}
	return resp, err
}

func (c *fallbackClient) Capabilities() []string {
	return c.primary.Capabilities()
}

func (c *fallbackClient) FallbackUsed() bool {
	if c == nil {
		return false
	}
	return c.used
}

func (c *fallbackClient) fallback() (interfaces.LLMClient, error) {
	if c == nil || c.registry == nil {
		return nil, fmt.Errorf("fallback registry is not configured")
	}
	cfg, ok := c.registry.configs[c.primaryName]
	if !ok || cfg.FallbackProvider == "" {
		return nil, fmt.Errorf("fallback provider not configured")
	}
	provider, ok := c.registry.providers[cfg.FallbackProvider]
	if !ok {
		return nil, fmt.Errorf("fallback provider not found: %s", cfg.FallbackProvider)
	}
	return provider, nil
}

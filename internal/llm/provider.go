package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/promptguard"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Registry wraps supported model backends.
type Registry struct {
	providers map[string]interfaces.LLMClient
	configs   map[string]config.LLMConfig
	localOnly bool
}

// NewRegistry builds the configured provider set.
func NewRegistry(defaultLLM config.LLMConfig, named map[string]config.LLMConfig) (*Registry, error) {
	return NewRegistryWithPolicy(defaultLLM, named, false)
}

// NewRegistryWithPolicy builds providers with workspace egress policy.
func NewRegistryWithPolicy(defaultLLM config.LLMConfig, named map[string]config.LLMConfig, localOnly bool) (*Registry, error) {
	providers := make(map[string]interfaces.LLMClient)
	configs := make(map[string]config.LLMConfig)
	for name, cfg := range named {
		client, err := buildClient(name, cfg)
		if err != nil {
			return nil, fmt.Errorf("build provider %s: %w", name, err)
		}
		providers[name] = client
		configs[name] = cfg
	}
	if len(providers) == 0 {
		name := defaultProviderName(defaultLLM)
		client, err := buildClient(name, defaultLLM)
		if err != nil {
			return nil, err
		}
		providers[name] = client
		configs[name] = defaultLLM
	}
	return &Registry{providers: providers, configs: configs, localOnly: localOnly}, nil
}

func buildClient(name string, cfg config.LLMConfig) (interfaces.LLMClient, error) {
	if reasons := providerSetupIssues(cfg); len(reasons) > 0 {
		return setupRequiredClient{name: name, provider: cfg.Provider, issues: reasons}, nil
	}
	switch cfg.Provider {
	case "", "openai-compatible":
		return NewClient(cfg), nil
	case "anthropic":
		return NewAnthropicClient(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
}

func providerSetupIssues(cfg config.LLMConfig) []string {
	issues := make([]string, 0, 3)
	provider := strings.TrimSpace(cfg.Provider)
	switch provider {
	case "", "openai-compatible", "anthropic":
		if strings.TrimSpace(cfg.BaseURL) == "" {
			issues = append(issues, "base_url")
		}
		if strings.TrimSpace(cfg.Model) == "" {
			issues = append(issues, "model")
		}
		if strings.TrimSpace(cfg.APIKey) == "" {
			issues = append(issues, "api_key")
		}
	}
	return issues
}

type setupRequiredClient struct {
	name     string
	provider string
	issues   []string
}

func (c setupRequiredClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, c.err()
}

func (c setupRequiredClient) StreamChat(context.Context, schema.ChatRequest, interfaces.StreamHandler) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, c.err()
}

func (c setupRequiredClient) Capabilities() []string {
	return []string{"setup_required"}
}

func (c setupRequiredClient) err() error {
	return newProviderSetupError(c.name, c.provider, c.issues)
}

type providerSetupError struct {
	name     string
	provider string
	issues   []string
}

func newProviderSetupError(name, provider string, issues []string) error {
	copied := append([]string(nil), issues...)
	return providerSetupError{name: strings.TrimSpace(name), provider: strings.TrimSpace(provider), issues: copied}
}

func (e providerSetupError) Error() string {
	provider := strings.TrimSpace(e.provider)
	if provider == "" {
		provider = "openai-compatible"
	}
	fields := strings.Join(e.issues, ", ")
	if fields == "" {
		fields = "provider settings"
	}
	name := strings.TrimSpace(e.name)
	if name == "" {
		name = provider
	}
	return fmt.Sprintf("model provider setup required: configure %s for provider %s (%s) in Web Studio Settings/Resources or configs/providers/*.yaml", fields, name, provider)
}

func isProviderSetupRequired(err error) bool {
	var setupErr providerSetupError
	return errors.As(err, &setupErr)
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
	used        atomic.Bool
}

func (c *fallbackClient) Chat(ctx context.Context, req schema.ChatRequest) (schema.ChatResponse, error) {
	c.used.Store(false)
	req, _ = promptguard.SanitizeChatRequest(req)
	if err := c.ensureProviderAllowed(c.primaryName); err != nil {
		return schema.ChatResponse{}, err
	}
	resp, err := c.primary.Chat(ctx, req)
	if err == nil {
		return resp, nil
	}
	if isProviderSetupRequired(err) {
		return schema.ChatResponse{}, err
	}
	fallback, fallbackErr := c.fallback()
	if fallbackErr != nil {
		return schema.ChatResponse{}, err
	}
	if fallbackName, ok := c.fallbackProviderName(); ok {
		if err := c.ensureProviderAllowed(fallbackName); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	resp, err = fallback.Chat(ctx, req)
	if err == nil {
		c.used.Store(true)
		resp.FallbackUsed = true
	}
	return resp, err
}

func (c *fallbackClient) StreamChat(ctx context.Context, req schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	c.used.Store(false)
	req, _ = promptguard.SanitizeChatRequest(req)
	if err := c.ensureProviderAllowed(c.primaryName); err != nil {
		return schema.ChatResponse{}, err
	}
	resp, err := c.primary.StreamChat(ctx, req, handler)
	if err == nil {
		return resp, nil
	}
	if isProviderSetupRequired(err) {
		return schema.ChatResponse{}, err
	}
	fallback, fallbackErr := c.fallback()
	if fallbackErr != nil {
		return schema.ChatResponse{}, err
	}
	if fallbackName, ok := c.fallbackProviderName(); ok {
		if err := c.ensureProviderAllowed(fallbackName); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	resp, err = fallback.StreamChat(ctx, req, handler)
	if err == nil {
		c.used.Store(true)
		resp.FallbackUsed = true
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
	return c.used.Load()
}

func (c *fallbackClient) FallbackUsageIsRequestScoped() bool { return true }

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

func (c *fallbackClient) fallbackProviderName() (string, bool) {
	if c == nil || c.registry == nil {
		return "", false
	}
	cfg, ok := c.registry.configs[c.primaryName]
	if !ok || strings.TrimSpace(cfg.FallbackProvider) == "" {
		return "", false
	}
	return strings.TrimSpace(cfg.FallbackProvider), true
}

func (c *fallbackClient) ensureProviderAllowed(name string) error {
	if c == nil || c.registry == nil || !c.registry.localOnly {
		return nil
	}
	cfg, ok := c.registry.configs[name]
	if !ok {
		return nil
	}
	if IsLocalBaseURL(cfg.BaseURL) {
		return nil
	}
	return fmt.Errorf("local-only workspace blocks non-local model provider %q at %s", name, strings.TrimSpace(cfg.BaseURL))
}

// IsLocalBaseURL recognizes loopback and Unix-socket provider endpoints.
func IsLocalBaseURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Scheme, "unix") {
		return true
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

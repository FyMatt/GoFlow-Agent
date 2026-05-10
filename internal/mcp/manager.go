package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Manager owns configured MCP clients.
type Manager struct {
	mu                  sync.RWMutex
	clients             map[string]*Client
	toolIndex           map[string]*Client
	ambiguousShortNames map[string][]string
	cachedTools         []schema.Tool
}

// NewManager builds a manager from config.
func NewManager(cfgs []config.MCPServerRef) *Manager {
	clients := make(map[string]*Client)
	for _, cfg := range cfgs {
		if !cfg.Enabled {
			continue
		}
		clients[cfg.Name] = NewClient(cfg)
	}
	return &Manager{clients: clients, toolIndex: make(map[string]*Client), ambiguousShortNames: make(map[string][]string)}
}

// ListTools returns cached tools when available and refreshes lazily on first use.
func (m *Manager) ListTools(ctx context.Context) ([]schema.Tool, error) {
	m.mu.RLock()
	if len(m.cachedTools) > 0 {
		cached := append([]schema.Tool(nil), m.cachedTools...)
		m.mu.RUnlock()
		return cached, nil
	}
	m.mu.RUnlock()
	return m.RefreshTools(ctx)
}

// RefreshTools rebuilds the cached tool index from all registered servers.
func (m *Manager) RefreshTools(ctx context.Context) ([]schema.Tool, error) {
	var tools []schema.Tool
	index := make(map[string]*Client)
	shortNames := make(map[string][]string)
	clientNames := make([]string, 0, len(m.clients))
	for name := range m.clients {
		clientNames = append(clientNames, name)
	}
	sort.Strings(clientNames)
	for _, name := range clientNames {
		client := m.clients[name]
		serverTools, err := client.ListTools(ctx)
		if err != nil {
			return nil, err
		}
		for _, tool := range serverTools {
			if err := validateDiscoveredTool(tool); err != nil {
				return nil, fmt.Errorf("mcp server %s advertised invalid tool %q: %w", name, tool.Name, err)
			}
			tool.Name = strings.TrimSpace(tool.Name)
			tool.Kind = strings.TrimSpace(tool.Kind)
			toolName := tool.Name
			qualified := qualifiedToolName(tool)
			if _, exists := index[qualified]; exists {
				return nil, fmt.Errorf("mcp server %s advertised duplicate tool %q", name, toolName)
			}
			tools = append(tools, tool)
			if qualified != "" {
				index[qualified] = client
			}
			shortNames[toolName] = append(shortNames[toolName], qualified)
		}
	}
	ambiguous := make(map[string][]string)
	for shortName, qualifiedNames := range shortNames {
		qualifiedNames = uniqueSortedNonEmpty(qualifiedNames)
		if len(qualifiedNames) == 1 {
			index[shortName] = index[qualifiedNames[0]]
			continue
		}
		ambiguous[shortName] = qualifiedNames
		delete(index, shortName)
	}
	m.mu.Lock()
	m.cachedTools = append([]schema.Tool(nil), tools...)
	m.toolIndex = index
	m.ambiguousShortNames = ambiguous
	m.mu.Unlock()
	return append([]schema.Tool(nil), tools...), nil
}

// CallTool routes a tool call to the appropriate server.
func (m *Manager) CallTool(ctx context.Context, name string, arguments []byte) (schema.ToolResult, error) {
	client, toolName, err := m.findToolClient(ctx, name)
	if err != nil {
		return schema.ToolResult{}, err
	}
	return client.CallTool(ctx, toolName, arguments)
}

func (m *Manager) findToolClient(ctx context.Context, fullName string) (*Client, string, error) {
	fullName = strings.TrimSpace(fullName)
	m.mu.RLock()
	if matches, ambiguous := m.ambiguousShortNames[fullName]; ambiguous {
		m.mu.RUnlock()
		return nil, "", fmt.Errorf("ambiguous tool name %q; use qualified name: %s", fullName, strings.Join(matches, ", "))
	}
	client, ok := m.toolIndex[fullName]
	m.mu.RUnlock()
	if !ok {
		if _, err := m.RefreshTools(ctx); err != nil {
			return nil, "", err
		}
		m.mu.RLock()
		if matches, ambiguous := m.ambiguousShortNames[fullName]; ambiguous {
			m.mu.RUnlock()
			return nil, "", fmt.Errorf("ambiguous tool name %q; use qualified name: %s", fullName, strings.Join(matches, ", "))
		}
		client, ok = m.toolIndex[fullName]
		m.mu.RUnlock()
		if !ok {
			return nil, "", fmt.Errorf("tool not found: %s", fullName)
		}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, tool := range m.cachedTools {
		if tool.Name == fullName || fmt.Sprintf("%s/%s", tool.Server, tool.Name) == fullName {
			return client, tool.Name, nil
		}
	}
	return nil, "", fmt.Errorf("tool not found: %s", fullName)
}

// HealthStatus reports basic server availability.
func (m *Manager) HealthStatus(ctx context.Context) map[string]string {
	status := make(map[string]string, len(m.clients))
	for name, client := range m.clients {
		status[name] = client.HealthStatus(ctx)
	}
	if len(status) == 0 {
		status["none"] = "disabled"
	}
	return status
}

// MCPCallMetrics reports live queue and active call-slot pressure per server.
func (m *Manager) MCPCallMetrics() map[string]interfaces.MCPServerCallMetrics {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	clients := make(map[string]*Client, len(m.clients))
	for name, client := range m.clients {
		clients[name] = client
	}
	m.mu.RUnlock()

	metrics := make(map[string]interfaces.MCPServerCallMetrics, len(clients))
	for name, client := range clients {
		metrics[name] = client.CallMetrics()
	}
	return metrics
}

// ToolNames returns cached tool names in sorted order.
func (m *Manager) ToolNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.cachedTools))
	for _, tool := range m.cachedTools {
		if tool.Server != "" {
			names = append(names, fmt.Sprintf("%s/%s", tool.Server, tool.Name))
			continue
		}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// Close stops all managed MCP server processes and clears runtime indexes.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.clients = make(map[string]*Client)
	m.toolIndex = make(map[string]*Client)
	m.ambiguousShortNames = make(map[string][]string)
	m.cachedTools = nil
	m.mu.Unlock()

	var joined error
	for _, client := range clients {
		if err := client.Close(); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func qualifiedToolName(tool schema.Tool) string {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return ""
	}
	server := strings.TrimSpace(tool.Server)
	if server == "" {
		return name
	}
	return fmt.Sprintf("%s/%s", server, name)
}

func uniqueSortedNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

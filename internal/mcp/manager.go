package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	scopedClients       map[string]*Client
	optional            map[string]bool
	degraded            map[string]string
	toolIndex           map[string]*Client
	ambiguousShortNames map[string][]string
	cachedTools         []schema.Tool
}

// UpsertServer hot-plugs or replaces one MCP server process and refreshes tools.
func (m *Manager) UpsertServer(cfg config.MCPServerRef) error {
	if m == nil {
		return fmt.Errorf("mcp manager not configured")
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	m.mu.Lock()
	old := m.clients[name]
	if !cfg.Enabled {
		delete(m.clients, name)
		delete(m.optional, name)
		m.cachedTools = nil
		m.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		_, err := m.RefreshTools(context.Background())
		return err
	}
	client := NewClient(cfg)
	if m.clients == nil {
		m.clients = map[string]*Client{}
	}
	if m.optional == nil {
		m.optional = map[string]bool{}
	}
	m.clients[name] = client
	m.optional[name] = cfg.Optional
	m.cachedTools = nil
	m.mu.Unlock()
	if old != nil && old != client {
		_ = old.Close()
	}
	_, err := m.RefreshTools(context.Background())
	return err
}

// RemoveServer stops and unregisters one MCP server.
func (m *Manager) RemoveServer(name string) error {
	if m == nil {
		return fmt.Errorf("mcp manager not configured")
	}
	name = strings.TrimSpace(name)
	m.mu.Lock()
	old := m.clients[name]
	delete(m.clients, name)
	delete(m.optional, name)
	m.cachedTools = nil
	m.mu.Unlock()
	if old != nil {
		if err := old.Close(); err != nil {
			return err
		}
	}
	_, err := m.RefreshTools(context.Background())
	return err
}

// NewManager builds a manager from config.
func NewManager(cfgs []config.MCPServerRef) *Manager {
	clients := make(map[string]*Client)
	optional := make(map[string]bool)
	for _, cfg := range cfgs {
		if !cfg.Enabled {
			continue
		}
		clients[cfg.Name] = NewClient(cfg)
		optional[cfg.Name] = cfg.Optional
	}
	return &Manager{
		clients:             clients,
		scopedClients:       make(map[string]*Client),
		optional:            optional,
		degraded:            make(map[string]string),
		toolIndex:           make(map[string]*Client),
		ambiguousShortNames: make(map[string][]string),
	}
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
			if m.isOptional(name) {
				m.markDegraded(name, err)
				continue
			}
			return nil, err
		}
		m.clearDegraded(name)
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

// CallToolInWorkspace routes a tool call through a client whose workspace
// environment is bound to one task staging directory. It never falls back to
// the global workspace client when the scoped client cannot be prepared.
func (m *Manager) CallToolInWorkspace(ctx context.Context, name string, arguments []byte, workspaceRoot string) (schema.ToolResult, error) {
	client, toolName, err := m.findToolClient(ctx, name)
	if err != nil {
		return schema.ToolResult{}, err
	}
	scoped, err := m.scopedClientForWorkspace(client, workspaceRoot)
	if err != nil {
		return schema.ToolResult{}, err
	}
	return scoped.CallTool(ctx, toolName, arguments)
}

// CallToolInTaskWorkspace binds the MCP process to the task filesystem scope.
// New local tasks use the confirmed live workspace; legacy staged tasks may
// also distinguish read-only input, writable output, and private runtime files.
func (m *Manager) CallToolInTaskWorkspace(ctx context.Context, name string, arguments []byte, scope schema.TaskWorkspaceScope) (schema.ToolResult, error) {
	client, toolName, err := m.findToolClient(ctx, name)
	if err != nil {
		return schema.ToolResult{}, err
	}
	scoped, err := m.scopedClientForTaskWorkspace(client, scope)
	if err != nil {
		return schema.ToolResult{}, err
	}
	return scoped.CallTool(ctx, toolName, arguments)
}

func (m *Manager) scopedClientForWorkspace(client *Client, workspaceRoot string) (*Client, error) {
	return m.scopedClientForTaskWorkspace(client, schema.TaskWorkspaceScope{Root: workspaceRoot, Workspace: workspaceRoot})
}

func (m *Manager) scopedClientForTaskWorkspace(client *Client, scope schema.TaskWorkspaceScope) (*Client, error) {
	if m == nil || client == nil {
		return nil, fmt.Errorf("workspace-scoped MCP client is unavailable")
	}
	resolved, err := normalizeMCPTaskWorkspaceScope(scope)
	if err != nil {
		return nil, err
	}
	key := client.name + "\x00" + strings.ToLower(filepath.Clean(resolved.Root)) + "\x00" + strings.ToLower(filepath.Clean(resolved.Workspace))
	m.mu.RLock()
	scoped := m.scopedClients[key]
	m.mu.RUnlock()
	if scoped != nil {
		return scoped, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if scoped = m.scopedClients[key]; scoped != nil {
		return scoped, nil
	}
	scoped = client.cloneForTaskWorkspace(resolved)
	if scoped == nil {
		return nil, fmt.Errorf("create workspace-scoped MCP client")
	}
	if m.scopedClients == nil {
		m.scopedClients = make(map[string]*Client)
	}
	m.scopedClients[key] = scoped
	return scoped, nil
}

func normalizeMCPTaskWorkspaceScope(scope schema.TaskWorkspaceScope) (schema.TaskWorkspaceScope, error) {
	root, err := resolveMCPTaskDirectory(scope.Root, "task root")
	if err != nil {
		return schema.TaskWorkspaceScope{}, err
	}
	workspace, err := resolveMCPTaskDirectory(scope.Workspace, "task workspace")
	if err != nil {
		return schema.TaskWorkspaceScope{}, err
	}
	if !mcpPathWithin(root, workspace) {
		return schema.TaskWorkspaceScope{}, fmt.Errorf("task workspace escapes the task root")
	}
	input := ""
	if strings.TrimSpace(scope.Input) != "" {
		input, err = resolveMCPTaskDirectory(scope.Input, "task input")
		if err != nil {
			return schema.TaskWorkspaceScope{}, err
		}
		if !mcpPathWithin(root, input) {
			return schema.TaskWorkspaceScope{}, fmt.Errorf("task input escapes the task root")
		}
		if mcpPathWithin(input, workspace) || mcpPathWithin(workspace, input) {
			return schema.TaskWorkspaceScope{}, fmt.Errorf("task input and workspace must not overlap")
		}
	}
	dependencies := ""
	if strings.TrimSpace(scope.Dependencies) != "" {
		dependencies, err = resolveMCPTaskDirectory(scope.Dependencies, "task dependency cache")
		if err != nil {
			return schema.TaskWorkspaceScope{}, err
		}
		if !mcpPathWithin(root, dependencies) {
			return schema.TaskWorkspaceScope{}, fmt.Errorf("task dependency cache escapes the task root")
		}
		if mcpPathWithin(dependencies, workspace) || mcpPathWithin(workspace, dependencies) || (input != "" && (mcpPathWithin(input, dependencies) || mcpPathWithin(dependencies, input))) {
			return schema.TaskWorkspaceScope{}, fmt.Errorf("task dependency cache must not overlap task input or workspace")
		}
	}
	return schema.TaskWorkspaceScope{
		RunID:        strings.TrimSpace(scope.RunID),
		Root:         root,
		Input:        input,
		Dependencies: dependencies,
		Workspace:    workspace,
	}, nil
}

func resolveMCPTaskDirectory(path, label string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("workspace-scoped MCP call requires a %s", label)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve %s links: %w", label, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", label)
	}
	return filepath.Clean(resolved), nil
}

func mcpPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// ReleaseWorkspace stops all cached MCP processes bound to a completed task
// workspace while leaving global clients and other active tasks untouched.
func (m *Manager) ReleaseWorkspace(workspaceRoot string) error {
	if m == nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil
	}
	absolute, err := filepath.Abs(filepath.Clean(workspaceRoot))
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return err
	}
	suffix := "\x00" + strings.ToLower(filepath.Clean(resolved))
	m.mu.Lock()
	clients := make([]*Client, 0)
	for key, client := range m.scopedClients {
		if strings.HasSuffix(key, suffix) {
			clients = append(clients, client)
			delete(m.scopedClients, key)
		}
	}
	m.mu.Unlock()
	var joined error
	for _, client := range clients {
		if err := client.Close(); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
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
	m.mu.RLock()
	clients := make(map[string]*Client, len(m.clients))
	degraded := make(map[string]string, len(m.degraded))
	for name, client := range m.clients {
		clients[name] = client
	}
	for name, reason := range m.degraded {
		degraded[name] = reason
	}
	m.mu.RUnlock()

	status := make(map[string]string, len(clients))
	for name, client := range clients {
		if reason, ok := degraded[name]; ok {
			status[name] = "degraded: " + reason
			continue
		}
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
	clients := make([]*Client, 0, len(m.clients)+len(m.scopedClients))
	seen := make(map[*Client]struct{}, len(m.clients)+len(m.scopedClients))
	for _, client := range m.clients {
		if _, ok := seen[client]; !ok {
			clients = append(clients, client)
			seen[client] = struct{}{}
		}
	}
	for _, client := range m.scopedClients {
		if _, ok := seen[client]; !ok {
			clients = append(clients, client)
			seen[client] = struct{}{}
		}
	}
	m.clients = make(map[string]*Client)
	m.scopedClients = make(map[string]*Client)
	m.optional = make(map[string]bool)
	m.degraded = make(map[string]string)
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

func (m *Manager) isOptional(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.optional[name]
}

func (m *Manager) markDegraded(name string, err error) {
	reason := "unavailable"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		reason = err.Error()
	}
	m.mu.Lock()
	if m.degraded == nil {
		m.degraded = make(map[string]string)
	}
	m.degraded[name] = reason
	m.mu.Unlock()
}

func (m *Manager) clearDegraded(name string) {
	m.mu.Lock()
	delete(m.degraded, name)
	m.mu.Unlock()
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

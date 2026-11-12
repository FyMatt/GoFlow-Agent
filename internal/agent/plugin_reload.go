package agent

import (
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

type mcpCatalogMutator interface {
	UpsertServer(cfg config.MCPServerRef) error
	RemoveServer(name string) error
}

// UpsertAgentProfile hot-plugs an agent profile when its provider client is already loaded.
func (r *Runtime) UpsertAgentProfile(id string, profile config.AgentProfile) error {
	if r == nil {
		return fmt.Errorf("runtime not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("agent id is required")
	}
	provider := strings.TrimSpace(profile.Provider)
	client, ok := r.clients[provider]
	if !ok {
		return fmt.Errorf("provider %q is not loaded; restart GoFlow before this agent is active", provider)
	}
	r.pluginMu.Lock()
	defer r.pluginMu.Unlock()
	if r.cfg != nil {
		if r.cfg.Agents == nil {
			r.cfg.Agents = map[string]config.AgentProfile{}
		}
		r.cfg.Agents[id] = profile
	}
	if previous, exists := r.runners[id]; exists {
		// Running stages retain their runner. Publish a replacement so their
		// profile and provider client remain stable through a hot reload.
		r.runners[id] = &AgentRunner{
			id: id, profile: profile, llm: client,
			executor: previous.executor, fallbackUsed: previous.fallbackUsed,
		}
		return nil
	}
	if r.runners == nil {
		r.runners = map[string]*AgentRunner{}
	}
	r.runners[id] = &AgentRunner{
		id:       id,
		profile:  profile,
		llm:      client,
		executor: NewExecutor(r.mcp),
	}
	return nil
}

// RemoveAgentProfile unloads a hot-plugged agent profile.
func (r *Runtime) RemoveAgentProfile(id string) error {
	if r == nil {
		return fmt.Errorf("runtime not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("agent id is required")
	}
	r.pluginMu.Lock()
	defer r.pluginMu.Unlock()
	if r.cfg != nil && r.cfg.Agents != nil {
		delete(r.cfg.Agents, id)
	}
	delete(r.runners, id)
	if r.active == id {
		r.active = r.defaultAgentIDLocked()
	}
	return nil
}

// UpsertMCPServer hot-plugs one MCP server configuration into the live catalog.
func (r *Runtime) UpsertMCPServer(cfg config.MCPServerRef) error {
	if r == nil {
		return fmt.Errorf("runtime not configured")
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	r.pluginMu.Lock()
	defer r.pluginMu.Unlock()
	if r.cfg != nil {
		replaced := false
		for i, current := range r.cfg.MCP {
			if strings.TrimSpace(current.Name) != name {
				continue
			}
			r.cfg.MCP[i] = cfg
			replaced = true
			break
		}
		if !replaced {
			r.cfg.MCP = append(r.cfg.MCP, cfg)
		}
	}
	mutator, ok := r.mcp.(mcpCatalogMutator)
	if !ok {
		return nil
	}
	return mutator.UpsertServer(cfg)
}

// RemoveMCPServer unloads a hot-plugged MCP server.
func (r *Runtime) RemoveMCPServer(name string) error {
	if r == nil {
		return fmt.Errorf("runtime not configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	r.pluginMu.Lock()
	defer r.pluginMu.Unlock()
	if r.cfg != nil {
		filtered := make([]config.MCPServerRef, 0, len(r.cfg.MCP))
		for _, current := range r.cfg.MCP {
			if strings.TrimSpace(current.Name) == name {
				continue
			}
			filtered = append(filtered, current)
		}
		r.cfg.MCP = filtered
	}
	mutator, ok := r.mcp.(mcpCatalogMutator)
	if !ok {
		return nil
	}
	return mutator.RemoveServer(name)
}

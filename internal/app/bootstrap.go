package app

import (
	"context"
	"path/filepath"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/llm"
	"github.com/FyMatt/GoFlow-Agent/internal/mcp"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/skill"
)

type RuntimeApp struct {
	Runtime       *agent.Runtime
	Config        *config.Config
	ConfigPath    string
	WorkspaceRoot string
	SessionState  *session.State
	SkillManager  interfaces.SkillManager
	MCPClient     interfaces.MCPClient
}

type BootstrapOptions struct {
	RuntimeHome   string
	WorkspaceRoot string
	ConfigPath    string
}

type NewRuntimeAppOptions struct {
	Config        *config.Config
	ConfigPath    string
	WorkspaceRoot string
	SkillManager  interfaces.SkillManager
	MCPClient     interfaces.MCPClient
	Clients       map[string]interfaces.LLMClient
	SessionState  *session.State
	AuditLogger   *runtime.AuditLogger
}

func LoadRuntimeConfig(runtimeHome, workspaceRoot string) (*config.Config, string, error) {
	return LoadRuntimeConfigFromPath(runtimeHome, workspaceRoot, "")
}

func LoadRuntimeConfigFromPath(runtimeHome, workspaceRoot, configPath string) (*config.Config, string, error) {
	if configPath == "" {
		configPath = filepath.Join(runtimeHome, "configs", "agent.yaml")
	}
	cfg, err := config.LoadForWorkspace(configPath, workspaceRoot)
	if err != nil {
		return nil, "", err
	}
	return cfg, configPath, nil
}

func NewRuntimeApp(opts NewRuntimeAppOptions) (*RuntimeApp, error) {
	runtimeRef, err := agent.NewRuntime(opts.Config, opts.Clients, opts.SkillManager, opts.MCPClient, opts.SessionState, opts.AuditLogger)
	if err != nil {
		return nil, err
	}
	return &RuntimeApp{
		Runtime:       runtimeRef,
		Config:        opts.Config,
		ConfigPath:    opts.ConfigPath,
		WorkspaceRoot: opts.WorkspaceRoot,
		SessionState:  opts.SessionState,
		SkillManager:  opts.SkillManager,
		MCPClient:     opts.MCPClient,
	}, nil
}

func Bootstrap(ctx context.Context, opts BootstrapOptions) (*RuntimeApp, error) {
	cfg, configPath, err := LoadRuntimeConfigFromPath(opts.RuntimeHome, opts.WorkspaceRoot, opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	skillManager, err := skill.NewManager(cfg.Skill.Directory)
	if err != nil {
		return nil, err
	}
	mcpManager := mcp.NewManager(cfg.MCP)
	if _, err := mcpManager.RefreshTools(ctx); err != nil {
		return nil, err
	}
	providerRegistry, err := llm.NewRegistry(cfg.LLM, cfg.Providers)
	if err != nil {
		return nil, err
	}
	clients := make(map[string]interfaces.LLMClient)
	for _, name := range providerRegistry.Names() {
		client, err := providerRegistry.Client(name)
		if err != nil {
			return nil, err
		}
		clients[name] = client
	}
	sessionState := session.New(cfg.Session.MaxHistory)
	if err := sessionState.Load(cfg.Session.PersistPath); err != nil {
		return nil, err
	}
	auditLogger := runtime.NewAuditLogger(cfg.Audit.Enabled, cfg.Audit.RedactContent)
	return NewRuntimeApp(NewRuntimeAppOptions{
		Config:        cfg,
		ConfigPath:    configPath,
		WorkspaceRoot: opts.WorkspaceRoot,
		SkillManager:  skillManager,
		MCPClient:     mcpManager,
		Clients:       clients,
		SessionState:  sessionState,
		AuditLogger:   auditLogger,
	})
}

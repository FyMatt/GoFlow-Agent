package app

import (
	"context"
	"path/filepath"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/llm"
	"github.com/FyMatt/GoFlow-Agent/internal/mcp"
	"github.com/FyMatt/GoFlow-Agent/internal/memory"
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
	MemoryStore   *memory.Store
	ArtifactStore *session.ArtifactObjectStore
	SkillManager  interfaces.SkillManager
	MCPClient     interfaces.MCPClient
}

// SaveSession persists the current workspace-scoped session when configured.
func (a *RuntimeApp) SaveSession() error {
	if a == nil || a.SessionState == nil || a.Config == nil {
		return nil
	}
	return a.SessionState.Save(a.Config.Session.PersistPath)
}

// Close releases runtime-owned resources such as MCP child processes.
func (a *RuntimeApp) Close() error {
	if a == nil || a.MCPClient == nil {
		return nil
	}
	if closer, ok := a.MCPClient.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
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
	MemoryStore   *memory.Store
	ArtifactStore *session.ArtifactObjectStore
	AuditLogger   *runtime.AuditLogger
}

func LoadRuntimeConfig(runtimeHome, workspaceRoot string) (*config.Config, string, error) {
	return LoadRuntimeConfigFromPath(runtimeHome, workspaceRoot, "")
}

func LoadRuntimeConfigFromPath(runtimeHome, workspaceRoot, configPath string) (*config.Config, string, error) {
	if configPath == "" {
		configPath = filepath.Join(runtimeHome, "configs", "goflow.yaml")
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
	runtimeRef.SetMemoryStore(opts.MemoryStore)
	return &RuntimeApp{
		Runtime:       runtimeRef,
		Config:        opts.Config,
		ConfigPath:    opts.ConfigPath,
		WorkspaceRoot: opts.WorkspaceRoot,
		SessionState:  opts.SessionState,
		MemoryStore:   opts.MemoryStore,
		ArtifactStore: opts.ArtifactStore,
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
	artifactStore := session.NewArtifactObjectStore(cfg.WorkspaceRoot)
	if err := artifactStore.Ensure(); err != nil {
		return nil, err
	}
	sessionState.SetArtifactObjectStore(artifactStore)
	if err := sessionState.Load(cfg.Session.PersistPath); err != nil {
		return nil, err
	}
	memoryStore := memory.NewStore(cfg.WorkspaceRoot)
	if err := memoryStore.Ensure(); err != nil {
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
		MemoryStore:   memoryStore,
		ArtifactStore: artifactStore,
		AuditLogger:   auditLogger,
	})
}

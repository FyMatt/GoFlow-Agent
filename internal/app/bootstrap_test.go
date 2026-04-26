package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type bootstrapStubSkillManager struct{}

func (bootstrapStubSkillManager) Match(string) (*schema.Skill, bool) { return nil, false }
func (bootstrapStubSkillManager) MatchWithDiagnostics(string) (*schema.Skill, schema.SkillMatchDiagnostic, bool) {
	return nil, schema.SkillMatchDiagnostic{}, false
}
func (bootstrapStubSkillManager) List() []schema.Skill { return nil }
func (bootstrapStubSkillManager) Reload() error        { return nil }

type bootstrapStubMCP struct{}

func (bootstrapStubMCP) ListTools(context.Context) ([]schema.Tool, error)    { return nil, nil }
func (bootstrapStubMCP) RefreshTools(context.Context) ([]schema.Tool, error) { return nil, nil }
func (bootstrapStubMCP) CallTool(context.Context, string, []byte) (schema.ToolResult, error) {
	return schema.ToolResult{}, nil
}
func (bootstrapStubMCP) HealthStatus(context.Context) map[string]string {
	return map[string]string{"stub": "ready"}
}
func (bootstrapStubMCP) ToolNames() []string { return nil }

type bootstrapStubLLM struct{}

func (bootstrapStubLLM) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, nil
}
func (bootstrapStubLLM) StreamChat(context.Context, schema.ChatRequest, interfaces.StreamHandler) (schema.ChatResponse, error) {
	return schema.ChatResponse{}, nil
}
func (bootstrapStubLLM) Capabilities() []string { return []string{"chat", "stream"} }

func TestNewRuntimeAppUsesWorkspaceRuntimeAndConfigPath(t *testing.T) {
	runtimeHome := t.TempDir()
	workspaceRoot := t.TempDir()
	cfg := &config.Config{
		Session:      config.SessionConfig{MaxHistory: 8},
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat":    {Provider: "primary", Mode: "chat"},
			"planner": {Provider: "primary", Mode: "plan"},
		},
	}
	configPath := filepath.Join(runtimeHome, "configs", "agent.yaml")
	clients := map[string]interfaces.LLMClient{"primary": bootstrapStubLLM{}}
	state := session.New(8)
	audit := runtime.NewAuditLogger(false, false)

	app, err := NewRuntimeApp(NewRuntimeAppOptions{
		Config:        cfg,
		ConfigPath:    configPath,
		WorkspaceRoot: workspaceRoot,
		SkillManager:  bootstrapStubSkillManager{},
		MCPClient:     bootstrapStubMCP{},
		Clients:       clients,
		SessionState:  state,
		AuditLogger:   audit,
	})
	if err != nil {
		t.Fatalf("NewRuntimeApp: %v", err)
	}
	if app.Runtime == nil {
		t.Fatal("expected runtime")
	}
	if app.WorkspaceRoot != workspaceRoot {
		t.Fatalf("expected workspace %q, got %q", workspaceRoot, app.WorkspaceRoot)
	}
	if app.ConfigPath != configPath {
		t.Fatalf("expected config path %q, got %q", configPath, app.ConfigPath)
	}
}

func TestLoadRuntimeConfigLoadsWorkspaceScopedConfig(t *testing.T) {
	runtimeHome := t.TempDir()
	configDir := filepath.Join(runtimeHome, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "agent.yaml")
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(configPath, []byte(`providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    api_key: secret
    model: test-model
agents:
  planner:
    provider: primary
skill:
  directory: ./skills
session:
  max_history: 8
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, resolvedConfigPath, err := LoadRuntimeConfig(runtimeHome, workspaceRoot)
	if err != nil {
		t.Fatalf("LoadRuntimeConfig: %v", err)
	}
	if resolvedConfigPath != configPath {
		t.Fatalf("expected config path %q, got %q", configPath, resolvedConfigPath)
	}
	if cfg.WorkspaceRoot != workspaceRoot {
		t.Fatalf("expected workspace root %q, got %q", workspaceRoot, cfg.WorkspaceRoot)
	}
}

func TestLoadRuntimeConfigFromPathUsesExplicitConfig(t *testing.T) {
	runtimeHome := t.TempDir()
	configDir := filepath.Join(runtimeHome, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "agent.docker.yaml")
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(configPath, []byte(`providers:
  primary:
    provider: openai-compatible
    base_url: http://localhost:9999/v1
    api_key: secret
    model: test-model
agents:
  chat:
    provider: primary
skill:
  directory: ./skills
session:
  max_history: 8
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, resolvedConfigPath, err := LoadRuntimeConfigFromPath(runtimeHome, workspaceRoot, configPath)
	if err != nil {
		t.Fatalf("LoadRuntimeConfigFromPath: %v", err)
	}
	if resolvedConfigPath != configPath {
		t.Fatalf("expected config path %q, got %q", configPath, resolvedConfigPath)
	}
	if cfg.RuntimeHome != runtimeHome {
		t.Fatalf("expected runtime home %q, got %q", runtimeHome, cfg.RuntimeHome)
	}
}

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

type bootstrapCloseableMCP struct {
	bootstrapStubMCP
	closed bool
}

func (m *bootstrapCloseableMCP) Close() error {
	m.closed = true
	return nil
}

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
	configPath := filepath.Join(runtimeHome, "configs", "goflow.yaml")
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

func TestRuntimeAppCloseClosesMCPClientWhenSupported(t *testing.T) {
	mcpClient := &bootstrapCloseableMCP{}
	app := &RuntimeApp{MCPClient: mcpClient}

	if err := app.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !mcpClient.closed {
		t.Fatal("expected Close to call MCP client close hook")
	}
}

func TestRuntimeAppSaveSessionPersistsConfiguredSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goflow", "session.json")
	state := session.New(8)
	state.SetActiveAgent("chat")
	state.SetMode("chat")
	state.AddPrompt("hello")
	app := &RuntimeApp{
		Config:       &config.Config{Session: config.SessionConfig{PersistPath: path}},
		SessionState: state,
	}

	if err := app.SaveSession(); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved session: %v", err)
	}
	if !strings.Contains(string(data), `"active_agent": "chat"`) || !strings.Contains(string(data), "hello") {
		t.Fatalf("expected saved session content, got %s", string(data))
	}
}

func TestLoadRuntimeConfigLoadsWorkspaceScopedConfig(t *testing.T) {
	runtimeHome := t.TempDir()
	configDir := filepath.Join(runtimeHome, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "goflow.yaml")
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
	configPath := filepath.Join(configDir, "goflow.docker.yaml")
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

func TestBootstrapStartsWithIncompleteProviderSetup(t *testing.T) {
	runtimeHome := t.TempDir()
	configDir := filepath.Join(runtimeHome, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "goflow.yaml")
	workspaceRoot := t.TempDir()
	skillDir := filepath.Join(runtimeHome, "skills", "first-run")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: first-run
description: Minimal bootstrap test skill.
version: 1.0.0
author: GoFlow
activation:
  keywords: ["hello"]
  embedding_description: bootstrap test
---

## Workflow

Answer normally.
`), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`providers:
  primary:
    provider: openai-compatible
agents:
  chat:
    provider: primary
    mode: chat
    max_iterations: 1
skill:
  directory: ./skills
session:
  max_history: 8
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app, err := Bootstrap(context.Background(), BootstrapOptions{RuntimeHome: runtimeHome, WorkspaceRoot: workspaceRoot, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("Bootstrap should allow first-run provider setup diagnostics: %v", err)
	}
	defer app.Close()
	if app.Runtime == nil {
		t.Fatal("expected runtime")
	}
	_, err = app.Runtime.RunStream(context.Background(), "hello", nil)
	if err == nil || !strings.Contains(err.Error(), "model provider setup required") {
		t.Fatalf("expected setup-required run error, got %v", err)
	}
}

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadExpandsEnvAndDefaults(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("GOFLOW_API_KEY", "secret")
	t.Setenv("GOFLOW_MODEL", "test-model")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  provider: openai-compatible
  base_url: ${GOFLOW_BASE_URL}
  api_key: ${GOFLOW_API_KEY}
  model: ${GOFLOW_MODEL}
skill:
  directory: ./skills
mcp_servers:
  - name: file_tools
    command: go
    args: ["run", "./mcp_servers/file_tools"]
    enabled: true
    allowed_commands: [go]
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.LLM.BaseURL != "http://localhost:9999/v1" {
		t.Fatalf("unexpected base url: %s", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "secret" {
		t.Fatalf("unexpected api key: %s", cfg.LLM.APIKey)
	}
	if cfg.Agent.MaxIterations != 8 {
		t.Fatalf("expected default max iterations, got %d", cfg.Agent.MaxIterations)
	}
	if cfg.MCP[0].MaxRequestBytes != defaultMCPMaxRequestBytes {
		t.Fatalf("expected default max request bytes, got %d", cfg.MCP[0].MaxRequestBytes)
	}
	if cfg.MCP[0].MaxResponseBytes != defaultMCPMaxResponseBytes {
		t.Fatalf("expected default max response bytes, got %d", cfg.MCP[0].MaxResponseBytes)
	}
	if cfg.MCP[0].MaxConcurrentCalls != defaultMCPMaxConcurrent {
		t.Fatalf("expected default max concurrent calls, got %d", cfg.MCP[0].MaxConcurrentCalls)
	}
}

func TestLoadExpandsBackupAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "https://api.deepseek.com/v1")
	t.Setenv("GOFLOW_API_KEY", "primary-secret")
	t.Setenv("GOFLOW_MODEL", "deepseek-chat")
	t.Setenv("GOFLOW_BACKUP_BASE_URL", "https://api.deepseek.com/v1")
	t.Setenv("GOFLOW_BACKUP_API_KEY", "backup-secret")
	t.Setenv("GOFLOW_BACKUP_MODEL", "deepseek-chat")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    provider: openai-compatible
    base_url: ${GOFLOW_BASE_URL}
    api_key: ${GOFLOW_API_KEY}
    model: ${GOFLOW_MODEL}
    fallback_provider: backup
  backup:
    provider: openai-compatible
    base_url: ${GOFLOW_BACKUP_BASE_URL}
    api_key: ${GOFLOW_BACKUP_API_KEY}
    model: ${GOFLOW_BACKUP_MODEL}
agents:
  planner:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Providers["primary"].APIKey != "primary-secret" {
		t.Fatalf("unexpected primary api key: %q", cfg.Providers["primary"].APIKey)
	}
	if cfg.Providers["backup"].APIKey != "backup-secret" {
		t.Fatalf("unexpected backup api key: %q", cfg.Providers["backup"].APIKey)
	}
}

func TestLoadNormalizesVerifierProviderAndModel(t *testing.T) {
	t.Setenv("GOFLOW_VERIFIER_MODEL", "cheap-checker")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: primary-model
  cheap:
    base_url: http://localhost:9998/v1
    model: provider-cheap-model
agents:
  fixer:
    provider: primary
    mode: fix
  auditor:
    provider: primary
    mode: audit
default_agent: fixer
verifier:
  enabled: true
  agent: auditor
  provider: cheap
  model: ${GOFLOW_VERIFIER_MODEL}
  modes: [fix]
  max_tokens: 256
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Verifier.Provider != "cheap" {
		t.Fatalf("expected verifier provider override, got %#v", cfg.Verifier)
	}
	if cfg.Verifier.Model != "cheap-checker" {
		t.Fatalf("expected expanded verifier model override, got %#v", cfg.Verifier)
	}
	if cfg.Verifier.MaxTokens != 256 {
		t.Fatalf("expected verifier max tokens, got %#v", cfg.Verifier)
	}
}

func TestLoadFillsVerifierModelFromProvider(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: primary-model
  cheap:
    base_url: http://localhost:9998/v1
    model: provider-cheap-model
agents:
  fixer:
    provider: primary
    mode: fix
  auditor:
    provider: primary
    mode: audit
default_agent: fixer
verifier:
  enabled: true
  agent: auditor
  provider: cheap
  modes: [fix]
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Verifier.Model != "provider-cheap-model" {
		t.Fatalf("expected verifier model from provider, got %#v", cfg.Verifier)
	}
}

func TestLoadRejectsUnknownVerifierProvider(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: primary-model
agents:
  fixer:
    provider: primary
    mode: fix
  auditor:
    provider: primary
    mode: audit
default_agent: fixer
verifier:
  enabled: true
  agent: auditor
  provider: missing
  modes: [fix]
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("expected unknown verifier provider validation error, got %v", err)
	}
}

func TestLoadNormalizesCostControlRoutes(t *testing.T) {
	t.Setenv("GOFLOW_SUMMARIZER_MODEL", "cheap-summary")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: primary-model
  cheap:
    base_url: http://localhost:9998/v1
    model: cheap-router
agents:
  chat:
    provider: primary
default_agent: chat
cost_control:
  router:
    enabled: true
    provider: cheap
  summarizer:
    enabled: true
    provider: cheap
    model: ${GOFLOW_SUMMARIZER_MODEL}
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.CostControl.Router.Model != "cheap-router" || cfg.CostControl.Router.MaxTokens != 96 {
		t.Fatalf("expected router route defaults, got %#v", cfg.CostControl.Router)
	}
	if cfg.CostControl.Summarizer.Model != "cheap-summary" || cfg.CostControl.Summarizer.MaxTokens != 512 {
		t.Fatalf("expected summarizer route defaults, got %#v", cfg.CostControl.Summarizer)
	}
}

func TestLoadRejectsUnknownCostControlProvider(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: primary-model
agents:
  chat:
    provider: primary
default_agent: chat
cost_control:
  router:
    enabled: true
    provider: missing
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "cost_control.router") {
		t.Fatalf("expected cost control provider validation error, got %v", err)
	}
}

func TestLoadExpandsBootstrapEnvExampleValues(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "https://api.deepseek.com/v1")
	t.Setenv("GOFLOW_API_KEY", "example-primary")
	t.Setenv("GOFLOW_MODEL", "deepseek-chat")
	t.Setenv("GOFLOW_BACKUP_BASE_URL", "https://api.deepseek.com/v1")
	t.Setenv("GOFLOW_BACKUP_API_KEY", "example-primary")
	t.Setenv("GOFLOW_BACKUP_MODEL", "deepseek-chat")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    provider: openai-compatible
    base_url: ${GOFLOW_BASE_URL}
    api_key: ${GOFLOW_API_KEY}
    model: ${GOFLOW_MODEL}
    fallback_provider: backup
  backup:
    provider: openai-compatible
    base_url: ${GOFLOW_BACKUP_BASE_URL}
    api_key: ${GOFLOW_BACKUP_API_KEY}
    model: ${GOFLOW_BACKUP_MODEL}
agents:
  planner:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Providers["primary"].BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("unexpected primary base url: %q", cfg.Providers["primary"].BaseURL)
	}
	if cfg.Providers["backup"].Model != "deepseek-chat" {
		t.Fatalf("unexpected backup model: %q", cfg.Providers["backup"].Model)
	}
}

func TestLoadNormalizesLegacyLLMIntoProviderAndDefaultAgent(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`agent:
  name: Legacy Agent
  max_iterations: 5
llm:
  provider: openai-compatible
  base_url: http://localhost:9999/v1
  api_key: secret
  model: test-model
  temperature: 0.4
  max_tokens: 1234
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.DefaultAgent != "default" {
		t.Fatalf("expected default agent 'default', got %q", cfg.DefaultAgent)
	}
	provider, ok := cfg.Providers["openai-compatible"]
	if !ok {
		t.Fatal("expected normalized provider")
	}
	if provider.Model != "test-model" {
		t.Fatalf("unexpected provider model: %s", provider.Model)
	}
	agent, ok := cfg.Agents["default"]
	if !ok {
		t.Fatal("expected normalized default agent")
	}
	if agent.Provider != "openai-compatible" {
		t.Fatalf("unexpected agent provider: %s", agent.Provider)
	}
	if agent.Name != "Legacy Agent" {
		t.Fatalf("unexpected agent name: %s", agent.Name)
	}
	if agent.Mode != "chat" {
		t.Fatalf("unexpected agent mode: %s", agent.Mode)
	}
	if agent.MaxIterations != 5 {
		t.Fatalf("unexpected agent iterations: %d", agent.MaxIterations)
	}
}

func TestLoadDefaultsToFirstSortedAgent(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  fixer:
    provider: primary
  auditor:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.DefaultAgent != "auditor" {
		t.Fatalf("expected first sorted agent 'auditor', got %q", cfg.DefaultAgent)
	}
}

func TestLoadParsesAllowedToolsForAgentProfiles(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  fixer:
    provider: primary
    mode: fix
    tool_policy: confirm
    allowed_tool_kinds: [read, write, network]
    allowed_tools: [read_file, write_file, fetch_url]
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	agent := cfg.Agents["fixer"]
	if got, want := agent.AllowedTools, []string{"read_file", "write_file", "fetch_url"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected allowed tools: got %#v want %#v", got, want)
	}
}

func TestLoadParsesToolRiskPolicy(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  fixer:
    provider: primary
    mode: fix
skill:
  directory: ./skills
tool_risk_policy:
  require_approval_for_unsandboxed_risky_tools: true
  disable_remember_for_unsandboxed_risky_tools: true
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.ToolRiskPolicy.RequireApprovalForUnsandboxedRiskyTools {
		t.Fatal("expected require_approval_for_unsandboxed_risky_tools to parse")
	}
	if !cfg.ToolRiskPolicy.DisableRememberForUnsandboxedRiskyTools {
		t.Fatal("expected disable_remember_for_unsandboxed_risky_tools to parse")
	}
}

func TestLoadMergesModularProvidersAndAgents(t *testing.T) {
	runtimeHome := t.TempDir()
	configDir := filepath.Join(runtimeHome, "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "providers"), 0o755); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "agents"), 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	configPath := filepath.Join(configDir, "goflow.yaml")
	if err := os.WriteFile(configPath, []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: base-model
agents:
  chat:
    provider: primary
default_agent: custom
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "providers", "custom.yaml"), []byte(`custom:
  provider: openai-compatible
  base_url: http://localhost:9998/v1
  api_key: ${CUSTOM_PROVIDER_KEY}
  model: custom-model
  timeout: 45s
`), 0o644); err != nil {
		t.Fatalf("write provider module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "agents", "custom.yaml"), []byte(`custom:
  name: Custom Agent
  provider: custom
  mode: audit
  allowed_tool_kinds: [read, network]
  tool_policy: confirm
`), 0o644); err != nil {
		t.Fatalf("write agent module: %v", err)
	}
	t.Setenv("CUSTOM_PROVIDER_KEY", "custom-secret")

	cfg, err := LoadForWorkspace(configPath, t.TempDir())
	if err != nil {
		t.Fatalf("LoadForWorkspace: %v", err)
	}
	if cfg.Providers["custom"].APIKey != "custom-secret" {
		t.Fatalf("expected expanded modular provider api key, got %#v", cfg.Providers["custom"])
	}
	if cfg.Providers["custom"].Model != "custom-model" {
		t.Fatalf("expected modular provider model, got %#v", cfg.Providers["custom"])
	}
	if cfg.Agents["custom"].Provider != "custom" || cfg.Agents["custom"].Mode != "audit" {
		t.Fatalf("expected modular agent, got %#v", cfg.Agents["custom"])
	}
	if cfg.DefaultAgent != "custom" {
		t.Fatalf("expected modular default agent to validate, got %q", cfg.DefaultAgent)
	}
}

func TestLoadMergesModularMCPServers(t *testing.T) {
	runtimeHome := t.TempDir()
	configDir := filepath.Join(runtimeHome, "configs")
	if err := os.MkdirAll(filepath.Join(configDir, "mcp_servers"), 0o755); err != nil {
		t.Fatalf("mkdir mcp servers: %v", err)
	}
	configPath := filepath.Join(configDir, "goflow.yaml")
	if err := os.WriteFile(configPath, []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    provider: primary
default_agent: chat
mcp_servers:
  - name: helper
    command: python
    args: [./mcp_servers/helper_override.py]
    enabled: true
    allowed_commands: [python]
skill:
  directory: ./skills
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers", "helper.yaml"), []byte(`mcp_servers:
  - name: helper
    command: python
    args: [./mcp_servers/helper.py]
    enabled: true
    allowed_commands: [python]
  - name: extra
    command: python
    args: [./mcp_servers/extra.py]
    enabled: true
    allowed_commands: [python]
`), 0o644); err != nil {
		t.Fatalf("write mcp server module: %v", err)
	}

	cfg, err := LoadForWorkspace(configPath, t.TempDir())
	if err != nil {
		t.Fatalf("LoadForWorkspace: %v", err)
	}
	if cfg.ConfigPath != filepath.Clean(configPath) {
		t.Fatalf("expected config path %q, got %q", filepath.Clean(configPath), cfg.ConfigPath)
	}
	servers := map[string]MCPServerRef{}
	for _, server := range cfg.MCP {
		servers[server.Name] = server
	}
	if len(servers) != 2 {
		t.Fatalf("expected merged mcp servers, got %#v", cfg.MCP)
	}
	if got := servers["helper"].Args; len(got) != 1 || !strings.HasSuffix(got[0], filepath.Join("mcp_servers", "helper_override.py")) {
		t.Fatalf("expected main config to override modular helper args, got %#v", got)
	}
	if _, ok := servers["extra"]; !ok {
		t.Fatalf("expected modular extra mcp server, got %#v", servers)
	}
}

func TestLoadRejectsDefaultAgentMissingFromAgents(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
default_agent: chat
agents:
  planner:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil {
		t.Fatal("expected missing default_agent profile validation error")
	}
}

func TestLoadRejectsUnknownAgentProvider(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  planner:
    provider: missing
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil {
		t.Fatal("expected unknown provider validation error")
	}
}

func TestLoadRejectsCommandOutsideAllowedPath(t *testing.T) {
	original := execLookPath
	execLookPath = func(file string) (string, error) {
		return filepath.Join(string(filepath.Separator), "unsafe", file), nil
	}
	defer func() { execLookPath = original }()

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: file_tools
    command: go
    enabled: true
    allowed_command_paths:
      - /safe/bin
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil {
		t.Fatal("expected allowed_command_paths validation error")
	}
}

func TestLoadAppliesMCPIsolationDefaults(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: file_tools
    command: go
    enabled: true
    allowed_commands: [go]
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MCP[0].RestartLimit != defaultMCPRestartLimit {
		t.Fatalf("expected default restart limit, got %d", cfg.MCP[0].RestartLimit)
	}
	if cfg.MCP[0].Cooldown != defaultMCPCooldown {
		t.Fatalf("expected default cooldown, got %s", cfg.MCP[0].Cooldown)
	}
	if cfg.MCP[0].MaxConcurrentCalls != defaultMCPMaxConcurrent {
		t.Fatalf("expected default max concurrent calls, got %d", cfg.MCP[0].MaxConcurrentCalls)
	}
	if cfg.MCP[0].WorkDir != tmp {
		t.Fatalf("expected default workdir %q, got %q", tmp, cfg.MCP[0].WorkDir)
	}
}

func TestLoadAcceptsMCPMaxConcurrentCalls(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: file_tools
    command: go
    enabled: true
    allowed_commands: [go]
    max_concurrent_calls: 2
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MCP[0].MaxConcurrentCalls != 2 {
		t.Fatalf("expected configured max concurrent calls, got %d", cfg.MCP[0].MaxConcurrentCalls)
	}
}

func TestValidateMCPServerRefRejectsInvalidMaxConcurrentCalls(t *testing.T) {
	server := MCPServerRef{
		Name:                "file_tools",
		Command:             "go",
		Enabled:             true,
		WorkDir:             ".",
		AllowedCommands:     []string{"go"},
		RestartLimit:        1,
		Cooldown:            time.Second,
		MaxConcurrentCalls:  0,
		MaxRequestBytes:     1,
		MaxResponseBytes:    1,
		AllowedCommandPaths: nil,
	}
	if err := ValidateMCPServerRef(server); err == nil || !strings.Contains(err.Error(), "max_concurrent_calls") {
		t.Fatalf("expected max_concurrent_calls validation error, got %v", err)
	}
}

func TestLoadAcceptsMCPProcessGroupIsolation(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: process_group
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MCP[0].Isolation != "process_group" {
		t.Fatalf("expected process_group isolation, got %q", cfg.MCP[0].Isolation)
	}
}

func TestLoadAcceptsMCPWindowsJobIsolationOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows_job isolation is Windows-only")
	}
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: windows_job
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MCP[0].Isolation != "windows_job" {
		t.Fatalf("expected windows_job isolation, got %q", cfg.MCP[0].Isolation)
	}
}

func TestLoadAcceptsMCPWindowsRestrictedTokenIsolationOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows_restricted_token isolation is Windows-only")
	}
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: windows_restricted_token
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MCP[0].Isolation != "windows_restricted_token" {
		t.Fatalf("expected windows_restricted_token isolation, got %q", cfg.MCP[0].Isolation)
	}
}

func TestLoadRejectsMCPWindowsRestrictedTokenIsolationOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows_restricted_token isolation is supported on Windows")
	}
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: windows_restricted_token
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "only supported on Windows") {
		t.Fatalf("expected Windows-only validation error, got %v", err)
	}
}

func TestLoadRejectsUnimplementedMCPWindowsAppContainerIsolation(t *testing.T) {
	for _, isolation := range []string{"appcontainer", "app_container", "windows_appcontainer", "windows_app_container"} {
		t.Run(isolation, func(t *testing.T) {
			tmp := t.TempDir()
			configPath := filepath.Join(tmp, "goflow.yaml")
			content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: ` + isolation + `
`)
			if err := os.WriteFile(configPath, content, 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}

			if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "use isolation: container") {
				t.Fatalf("expected AppContainer unsupported validation error, got %v", err)
			}
		})
	}
}

func TestLoadAcceptsMCPLinuxCgroupIsolationOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux_cgroup isolation is Linux-only")
	}
	t.Setenv("GOFLOW_CGROUP_PARENT", "/sys/fs/cgroup/goflow-test")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: linux_cgroup
    isolation_options:
      cgroup_parent: ${GOFLOW_CGROUP_PARENT}
      cgroup_name: helper
      memory_max: 256M
      pids_max: "64"
      cpu_max: "50000 100000"
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MCP[0].Isolation != "linux_cgroup" {
		t.Fatalf("expected linux_cgroup isolation, got %q", cfg.MCP[0].Isolation)
	}
	if cfg.MCP[0].IsolationOptions["cgroup_parent"] != "/sys/fs/cgroup/goflow-test" {
		t.Fatalf("expected expanded cgroup_parent, got %#v", cfg.MCP[0].IsolationOptions)
	}
}

func TestLoadAcceptsMCPLinuxNetworkNamespaceIsolationOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux_netns isolation is Linux-only")
	}

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: linux_netns
    isolation_options:
      unshare_command: /usr/bin/unshare
      map_root_user: true
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MCP[0].Isolation != "linux_netns" {
		t.Fatalf("expected linux_netns isolation, got %q", cfg.MCP[0].Isolation)
	}
	if cfg.MCP[0].IsolationOptions["unshare_command"] != "/usr/bin/unshare" {
		t.Fatalf("expected unshare command option, got %#v", cfg.MCP[0].IsolationOptions)
	}
}

func TestLoadRejectsMCPLinuxCgroupIsolationOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux_cgroup is supported on Linux")
	}

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: linux_cgroup
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "only supported on Linux") {
		t.Fatalf("expected Linux-only validation error, got %v", err)
	}
}

func TestLoadRejectsMCPLinuxNetworkNamespaceIsolationOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux_netns is supported on Linux")
	}

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: linux_netns
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "only supported on Linux") {
		t.Fatalf("expected Linux-only validation error, got %v", err)
	}
}

func TestLoadRejectsUnknownMCPIsolationOption(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: linux_cgroup
    isolation_options:
      image: goflow/mcp:latest
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "isolation_options.image") {
		t.Fatalf("expected invalid isolation option validation error, got %v", err)
	}
}

func TestLoadRejectsIsolationOptionsWithoutLinuxCgroup(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: process_group
    isolation_options:
      memory_max: 256M
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "only supported with linux_cgroup, linux_netns, or container") {
		t.Fatalf("expected isolation_options mode validation error, got %v", err)
	}
}

func TestDockerConfigLoadsWithCompiledMCPCommands(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("GOFLOW_API_KEY", "secret")
	t.Setenv("GOFLOW_MODEL", "test-model")
	t.Setenv("GOFLOW_BACKUP_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("GOFLOW_BACKUP_API_KEY", "secret")
	t.Setenv("GOFLOW_BACKUP_MODEL", "test-model")

	originalLookPath := execLookPath
	execLookPath = func(file string) (string, error) {
		switch file {
		case "/app/bin/file_tools", "/app/bin/skill_runner", "/app/bin/web_tools", "/app/bin/network_tools":
			return file, nil
		case "python3":
			return "/usr/bin/python3", nil
		default:
			return originalLookPath(file)
		}
	}
	t.Cleanup(func() { execLookPath = originalLookPath })

	repoRoot := filepath.Join("..", "..")
	configPath := filepath.Join(repoRoot, "configs", "goflow.docker.yaml")
	workspaceRoot := t.TempDir()
	cfg, err := LoadForWorkspace(configPath, workspaceRoot)
	if err != nil {
		t.Fatalf("LoadForWorkspace docker config: %v", err)
	}
	if cfg.Skill.Directory != filepath.Clean("/app/skills") {
		t.Fatalf("expected docker skill dir, got %q", cfg.Skill.Directory)
	}
	if cfg.Session.PersistPath != filepath.Clean("/workspace/.goflow/session.json") {
		t.Fatalf("expected docker session path, got %q", cfg.Session.PersistPath)
	}
	if len(cfg.MCP) != 5 {
		t.Fatalf("expected 5 docker MCP servers, got %d", len(cfg.MCP))
	}
	servers := map[string]MCPServerRef{}
	for _, server := range cfg.MCP {
		servers[server.Name] = server
	}
	if servers["file_tools"].Command != "/app/bin/file_tools" {
		t.Fatalf("expected compiled file_tools command, got %#v", servers["file_tools"])
	}
	if servers["web_tools"].Command != "/app/bin/web_tools" {
		t.Fatalf("expected compiled web_tools command, got %#v", servers["web_tools"])
	}
	if servers["network_tools"].Command != "/app/bin/network_tools" {
		t.Fatalf("expected compiled network_tools command, got %#v", servers["network_tools"])
	}
	if servers["skill_runner"].Command != "/app/bin/skill_runner" {
		t.Fatalf("expected compiled skill_runner command, got %#v", servers["skill_runner"])
	}
	if servers["python_notes"].Command != "python3" {
		t.Fatalf("expected python3 command, got %#v", servers["python_notes"])
	}
}

func TestBinaryConfigLoadsWithLauncherEnv(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("GOFLOW_API_KEY", "secret")
	t.Setenv("GOFLOW_MODEL", "test-model")
	t.Setenv("GOFLOW_BACKUP_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("GOFLOW_BACKUP_API_KEY", "secret")
	t.Setenv("GOFLOW_BACKUP_MODEL", "test-model")
	t.Setenv("GOFLOW_FILE_TOOLS_CMD", "/opt/goflow/bin/file_tools")
	t.Setenv("GOFLOW_SKILL_RUNNER_CMD", "/opt/goflow/bin/skill_runner")
	t.Setenv("GOFLOW_WEB_TOOLS_CMD", "/opt/goflow/bin/web_tools")
	t.Setenv("GOFLOW_NETWORK_TOOLS_CMD", "/opt/goflow/bin/network_tools")
	t.Setenv("GOFLOW_PYTHON_CMD", "python3")
	t.Setenv("GOFLOW_PYTHON_NOTES_PATH", "/opt/goflow/mcp_servers/python_notes.py")

	repoRoot := filepath.Join("..", "..")
	configPath := filepath.Join(repoRoot, "configs", "goflow.binary.yaml")
	workspaceRoot := t.TempDir()
	cfg, err := LoadForWorkspace(configPath, workspaceRoot)
	if err != nil {
		t.Fatalf("LoadForWorkspace binary config: %v", err)
	}
	servers := map[string]MCPServerRef{}
	for _, server := range cfg.MCP {
		servers[server.Name] = server
	}
	if servers["file_tools"].Command != "/opt/goflow/bin/file_tools" {
		t.Fatalf("expected binary file_tools command, got %#v", servers["file_tools"])
	}
	if servers["web_tools"].Command != "/opt/goflow/bin/web_tools" {
		t.Fatalf("expected binary web_tools command, got %#v", servers["web_tools"])
	}
	if servers["network_tools"].Command != "/opt/goflow/bin/network_tools" {
		t.Fatalf("expected binary network_tools command, got %#v", servers["network_tools"])
	}
	if servers["skill_runner"].Command != "/opt/goflow/bin/skill_runner" {
		t.Fatalf("expected binary skill_runner command, got %#v", servers["skill_runner"])
	}
	if servers["python_notes"].Command != "python3" {
		t.Fatalf("expected python3 command, got %#v", servers["python_notes"])
	}
	if len(servers["python_notes"].Args) != 1 || servers["python_notes"].Args[0] != "/opt/goflow/mcp_servers/python_notes.py" {
		t.Fatalf("expected python notes path arg, got %#v", servers["python_notes"])
	}
}

func TestLoadAcceptsMCPContainerIsolation(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: container
    network_disabled: true
    isolation_options:
      image: goflow/mcp-tools:latest
      runtime: docker
      workspace_mount: ro
      workspace_target: /workspace
      container_workdir: /app
      network: disabled
      ipc: none
      userns: auto
      memory: 256m
      memory_swap: 256m
      cpus: "0.5"
      pids_limit: "64"
      pull_policy: missing
      readonly_rootfs: "true"
      no_new_privileges: "true"
      cap_drop: all
      security_opt: seccomp=/etc/goflow/seccomp.json;apparmor=goflow-mcp
      tmpfs: /tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m
      init: "true"
      tool_source: ${TOOL_SOURCE}
      tool_target: /goflow-tools/helper.py
      tool_mount: ro
`)
	t.Setenv("TOOL_SOURCE", filepath.Join(tmp, "mcp_servers", "helper.py"))
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MCP[0].Isolation != "container" || cfg.MCP[0].IsolationOptions["image"] != "goflow/mcp-tools:latest" {
		t.Fatalf("expected container isolation options, got %#v", cfg.MCP[0])
	}
	if cfg.MCP[0].IsolationOptions["tool_source"] != filepath.Join(tmp, "mcp_servers", "helper.py") {
		t.Fatalf("expected expanded tool_source, got %#v", cfg.MCP[0].IsolationOptions)
	}
	if cfg.MCP[0].IsolationOptions["tmpfs"] != "/tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m" {
		t.Fatalf("expected tmpfs option, got %#v", cfg.MCP[0].IsolationOptions)
	}
	if cfg.MCP[0].IsolationOptions["ipc"] != "none" || cfg.MCP[0].IsolationOptions["userns"] != "auto" || cfg.MCP[0].IsolationOptions["memory_swap"] != "256m" || cfg.MCP[0].IsolationOptions["pull_policy"] != "missing" {
		t.Fatalf("expected container hardening options, got %#v", cfg.MCP[0].IsolationOptions)
	}
}

func TestLoadAppliesMCPContainerIsolationProfileDefaultsAndOverrides(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: python
    enabled: true
    allowed_commands: [python]
    isolation: container
    isolation_profile: readonly
    isolation_options:
      image: goflow/mcp-helper:1.0.0
      memory: 512m
      tool_source: ${TOOL_SOURCE}
      tool_target: /goflow-tools/helper.py
`)
	t.Setenv("TOOL_SOURCE", filepath.Join(tmp, "mcp_servers", "helper.py"))
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	server := cfg.MCP[0]
	if server.IsolationProfile != "readonly" {
		t.Fatalf("expected readonly profile, got %#v", server)
	}
	if server.IsolationOptions["workspace_mount"] != "ro" ||
		server.IsolationOptions["network"] != "disabled" ||
		server.IsolationOptions["ipc"] != "none" ||
		server.IsolationOptions["user"] != "65532:65532" ||
		server.IsolationOptions["memory_swap"] != "256m" ||
		server.IsolationOptions["tool_mount"] != "ro" {
		t.Fatalf("expected readonly profile defaults, got %#v", server.IsolationOptions)
	}
	if server.IsolationOptions["memory"] != "512m" {
		t.Fatalf("expected explicit memory override to win, got %#v", server.IsolationOptions)
	}
	if server.IsolationOptions["tool_source"] != filepath.Join(tmp, "mcp_servers", "helper.py") {
		t.Fatalf("expected expanded tool_source, got %#v", server.IsolationOptions)
	}
}

func TestValidateMCPServerRefAcceptsContainerIsolationProfile(t *testing.T) {
	server := MCPServerRef{
		Name:               "helper",
		Command:            "python",
		Enabled:            true,
		WorkDir:            ".",
		Isolation:          "container",
		IsolationProfile:   "production",
		AllowedCommands:    []string{"python"},
		RestartLimit:       1,
		Cooldown:           time.Second,
		MaxConcurrentCalls: 1,
		MaxRequestBytes:    1,
		MaxResponseBytes:   1,
		IsolationOptions: map[string]string{
			"image": "goflow/mcp-helper@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	if err := ValidateMCPServerRef(server); err != nil {
		t.Fatalf("expected production profile to validate, got %v", err)
	}
}

func TestLoadRejectsMCPContainerIsolationWithoutImage(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: container
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "isolation_options.image") {
		t.Fatalf("expected missing container image validation error, got %v", err)
	}
}

func TestLoadRejectsMCPContainerNetworkConflict(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: container
    network_disabled: true
    isolation_options:
      image: goflow/mcp-tools:latest
      network: bridge
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "conflicts with network_disabled") {
		t.Fatalf("expected container network conflict validation error, got %v", err)
	}
}

func TestLoadRejectsMCPContainerInvalidMemory(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: container
    isolation_options:
      image: goflow/mcp-tools:latest
      memory: many
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "isolation_options.memory is invalid") {
		t.Fatalf("expected invalid container memory validation error, got %v", err)
	}
}

func TestLoadRejectsMCPContainerInvalidIPCAndUserNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "ipc host",
			body: "      ipc: host\n",
			want: "isolation_options.ipc is invalid",
		},
		{
			name: "userns host",
			body: "      userns: host\n",
			want: "isolation_options.userns is invalid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			configPath := filepath.Join(tmp, "goflow.yaml")
			content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: helper
    command: go
    enabled: true
    allowed_commands: [go]
    isolation: container
    isolation_options:
      image: goflow/mcp-tools:latest
` + tc.body)
			if err := os.WriteFile(configPath, content, 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s validation error, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateContainerMemory(t *testing.T) {
	valid := []string{"", "1", "256m", "512M", "1g", "64k", "1024b", "2T"}
	for _, value := range valid {
		if err := validateContainerMemory(value); err != nil {
			t.Fatalf("expected memory value %q to be valid, got %v", value, err)
		}
	}
	invalid := []string{"0", "-1", "1.5g", "many", "1mb", "g"}
	for _, value := range invalid {
		if err := validateContainerMemory(value); err == nil {
			t.Fatalf("expected memory value %q to be invalid", value)
		}
	}
}

func TestValidateContainerUser(t *testing.T) {
	valid := []string{"", "65532", "65532:65532", "nobody", "app-user:app.group", "root:root"}
	for _, value := range valid {
		if err := validateContainerUser(value); err != nil {
			t.Fatalf("expected user value %q to be valid, got %v", value, err)
		}
	}
	invalid := []string{"user name", "user:group:extra", ":1000", "1000:", "user/name", "user\nname"}
	for _, value := range invalid {
		if err := validateContainerUser(value); err == nil {
			t.Fatalf("expected user value %q to be invalid", value)
		}
	}
}

func TestValidateContainerCapDrop(t *testing.T) {
	valid := []string{"", "all", "ALL", "NET_ADMIN", "net_admin,sys_admin", "CHOWN;DAC_OVERRIDE"}
	for _, value := range valid {
		if err := validateContainerCapDrop(value); err != nil {
			t.Fatalf("expected cap_drop value %q to be valid, got %v", value, err)
		}
	}
	invalid := []string{"CAP-NET", "NET/ADMIN", "NET_ADMIN=1", "\"ALL\""}
	for _, value := range invalid {
		if err := validateContainerCapDrop(value); err == nil {
			t.Fatalf("expected cap_drop value %q to be invalid", value)
		}
	}
}

func TestLoadRejectsEnabledMCPWithoutCommandAllowlist(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: file_tools
    command: go
    enabled: true
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "allowed_commands or allowed_command_paths") {
		t.Fatalf("expected missing command allowlist validation error, got %v", err)
	}
}

func TestLoadRejectsDuplicateEnabledMCPServerNames(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: file_tools
    command: go
    enabled: true
    allowed_commands: [go]
  - name: file_tools
    command: python
    enabled: true
    allowed_commands: [python]
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "defined more than once") {
		t.Fatalf("expected duplicate mcp server validation error, got %v", err)
	}
}

func TestLoadDefaultsSessionPersistPath(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  planner:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadForWorkspace(configPath, tmp)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	wantSessionPath := filepath.Join(tmp, ".goflow", "session.json")
	if cfg.Session.PersistPath != wantSessionPath {
		t.Fatalf("expected default session persist path %q, got %q", wantSessionPath, cfg.Session.PersistPath)
	}
}

func TestLoadExpandsMCPIsolationEnv(t *testing.T) {
	original := execLookPath
	execLookPath = func(file string) (string, error) {
		return filepath.Join(string(filepath.Separator), "safe", "bin", file), nil
	}
	defer func() { execLookPath = original }()

	t.Setenv("GOFLOW_MCP_WORKDIR", "./sandbox")
	t.Setenv("GOFLOW_ALLOWED_PATH", "/safe/bin")
	t.Setenv("GOFLOW_ALLOWED_ENV", "PATH")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
mcp_servers:
  - name: file_tools
    command: go
    enabled: true
    workdir: ${GOFLOW_MCP_WORKDIR}
    env_allowlist:
      - ${GOFLOW_ALLOWED_ENV}
    allowed_command_paths:
      - ${GOFLOW_ALLOWED_PATH}
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	wantWorkDir := filepath.Join(filepath.Dir(tmp), "sandbox")
	if cfg.MCP[0].WorkDir != wantWorkDir {
		t.Fatalf("expected expanded workdir %q, got %q", wantWorkDir, cfg.MCP[0].WorkDir)
	}
	if len(cfg.MCP[0].EnvAllowlist) != 1 || cfg.MCP[0].EnvAllowlist[0] != "PATH" {
		t.Fatalf("unexpected env allowlist: %#v", cfg.MCP[0].EnvAllowlist)
	}
	if len(cfg.MCP[0].AllowedCommandPaths) != 1 || cfg.MCP[0].AllowedCommandPaths[0] != "/safe/bin" {
		t.Fatalf("unexpected allowed command paths: %#v", cfg.MCP[0].AllowedCommandPaths)
	}
}

func TestLoadResolvesRuntimeRelativePathsFromConfigDirectory(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "runtime")
	configPath := filepath.Join(configDir, "configs", "goflow.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  planner:
    provider: primary
skill:
  directory: ../skills
mcp_servers:
  - name: file_tools
    command: go
    args: ["run", "./mcp_servers/file_tools"]
    workdir: .
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	wantSkillDir := filepath.Join(configDir, "skills")
	if cfg.Skill.Directory != wantSkillDir {
		t.Fatalf("expected skill dir %q, got %q", wantSkillDir, cfg.Skill.Directory)
	}
	wantWorkDir := filepath.Join(configDir, "configs")
	if cfg.MCP[0].WorkDir != wantWorkDir {
		t.Fatalf("expected MCP workdir %q, got %q", wantWorkDir, cfg.MCP[0].WorkDir)
	}
}

func TestLoadResolvesSessionPersistPathFromWorkspaceRoot(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  planner:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	workspaceRoot := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	cfg, err := LoadForWorkspace(configPath, workspaceRoot)
	if err != nil {
		t.Fatalf("load config for workspace: %v", err)
	}
	want := filepath.Join(workspaceRoot, ".goflow", "session.json")
	if cfg.Session.PersistPath != want {
		t.Fatalf("expected session path %q, got %q", want, cfg.Session.PersistPath)
	}
}

func TestLoadForWorkspaceRejectsRelativeWorkspaceRoot(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  planner:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := LoadForWorkspace(configPath, "."); err == nil {
		t.Fatal("expected relative workspace root validation error")
	}
}

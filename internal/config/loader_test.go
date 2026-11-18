package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/nativesandbox"
	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
)

func TestNormalizeReasoningEffortMapsOperatorAliases(t *testing.T) {
	for input, want := range map[string]string{
		"":       "",
		"mid":    "medium",
		"MID":    "medium",
		"med":    "medium",
		"medium": "medium",
		"low":    "low",
		"high":   "high",
		"xhigh":  "high",
		"max":    "high",
	} {
		if got := NormalizeReasoningEffort(input); got != want {
			t.Fatalf("NormalizeReasoningEffort(%q)=%q, want %q", input, got, want)
		}
	}
}

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

func TestLoadAllowsMissingProviderSecretForFirstRunSetup(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    provider: openai-compatible
    base_url: https://api.example.test/v1
    api_key: secret://providers/primary/api_key
    model: test-model
agents:
  chat:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("first-run config with missing key must load: %v", err)
	}
	if got := cfg.Providers["primary"].APIKey; got != "" {
		t.Fatalf("missing secret should produce an unconfigured provider, got %q", got)
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

func TestLoadNormalizesProviderPricingAndAgentInheritance(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    provider: openai-compatible
    model: strong-model
    pricing:
      currency: usd
      input_per_million_tokens: 1.5
      output_per_million_tokens: 4.5
      cached_input_per_million_tokens: 0.25
      source: operator-rate-card
  override:
    provider: openai-compatible
    model: cheap-model
agents:
  planner:
    provider: primary
    mode: plan
  fixer:
    provider: override
    mode: fix
    pricing:
      input_per_million_tokens: 0.5
      output_per_million_tokens: 1.25
      source: custom-agent-rate
default_agent: planner
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	planner := cfg.Agents["planner"].Pricing
	if planner.Currency != "USD" || planner.InputPerMillionTokens != 1.5 || planner.OutputPerMillionTokens != 4.5 || planner.CachedInputPerMillionTokens != 0.25 || planner.Source != "operator-rate-card" {
		t.Fatalf("expected planner to inherit normalized provider pricing, got %#v", planner)
	}
	fixer := cfg.Agents["fixer"].Pricing
	if fixer.Currency != "USD" || fixer.InputPerMillionTokens != 0.5 || fixer.OutputPerMillionTokens != 1.25 || fixer.Source != "custom-agent-rate" {
		t.Fatalf("expected fixer agent pricing to be normalized without provider inheritance, got %#v", fixer)
	}
}

func TestLoadRejectsNegativePricing(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`providers:
  primary:
    provider: openai-compatible
    model: test-model
    pricing:
      input_per_million_tokens: -1
agents:
  planner:
    provider: primary
default_agent: planner
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "provider primary.pricing.input_per_million_tokens") {
		t.Fatalf("expected negative provider pricing to be rejected, got %v", err)
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

func TestResolveAutomaticMCPIsolationUsesTrustedHostBoundary(t *testing.T) {
	server, err := ResolveAutomaticMCPIsolation(MCPServerRef{Name: "office_tools", Isolation: "auto"})
	if err != nil {
		t.Fatalf("resolve office auto isolation: %v", err)
	}
	if server.Isolation != "process_group" {
		t.Fatalf("expected Office to use controlled host execution, got %q", server.Isolation)
	}
}

func TestResolveAutomaticMCPIsolationRefusesUnknownHostFallback(t *testing.T) {
	server, err := ResolveAutomaticMCPIsolation(MCPServerRef{Name: "third_party_helper", Isolation: "auto"})
	if err != nil {
		if !strings.Contains(err.Error(), "原生强沙箱") || !strings.Contains(err.Error(), "拒绝") {
			t.Fatalf("expected unknown automatic server to require the native strong sandbox, got %v", err)
		}
		return
	}
	if server.Isolation != "native_strong" {
		t.Fatalf("unknown automatic server must resolve only to native_strong, got %#v", server)
	}
}

func TestResolveAutomaticMCPIsolationUsesNativeStrongForUnknownServerWhenReady(t *testing.T) {
	server, err := resolveAutomaticMCPIsolation(MCPServerRef{Name: "third_party_helper", Isolation: "auto"}, nativesandbox.Report{
		StrongReady: true,
		Level:       "strong",
	})
	if err != nil {
		t.Fatalf("resolve strong automatic isolation: %v", err)
	}
	if server.Isolation != "native_strong" || !server.NetworkDisabled || len(server.IsolationOptions) != 0 {
		t.Fatalf("unexpected native strong automatic isolation: %#v", server)
	}
}

func TestNotificationConnectorUsesDeclaredDataRootRatherThanTaskSandbox(t *testing.T) {
	server, err := resolveAutomaticMCPIsolation(MCPServerRef{Name: "notification_tools", Isolation: "auto"}, nativesandbox.Report{})
	if err != nil || server.Isolation != "process_group" || server.NetworkDisabled {
		t.Fatalf("bundled notification connector needs its data-root boundary and normal network approval: %#v err=%v", server, err)
	}
}

func TestValidateMCPServerPlatformAcceptsAutomaticIsolation(t *testing.T) {
	if err := ValidateMCPServerPlatform(MCPServerRef{Name: "helper", Isolation: "auto"}); err != nil {
		t.Fatalf("expected auto isolation to be accepted before runtime resolution, got %v", err)
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

func TestValidateMCPServerRefValidatesHostTargetScope(t *testing.T) {
	server := MCPServerRef{
		Name:               "host_tools",
		TargetScope:        " HOST ",
		Command:            "go",
		Enabled:            true,
		WorkDir:            ".",
		AllowedCommands:    []string{"go"},
		RestartLimit:       1,
		Cooldown:           time.Second,
		MaxConcurrentCalls: 1,
		MaxRequestBytes:    1,
		MaxResponseBytes:   1,
	}
	ApplyMCPServerDefaults(&server)
	if server.TargetScope != "host" {
		t.Fatalf("expected normalized host target scope, got %q", server.TargetScope)
	}
	if err := ValidateMCPServerRef(server); err != nil {
		t.Fatalf("expected host target scope to validate: %v", err)
	}
	server.TargetScope = "remote"
	if err := ValidateMCPServerRef(server); err == nil || !strings.Contains(err.Error(), "target_scope") {
		t.Fatalf("expected invalid target_scope rejection, got %v", err)
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

func TestBinaryConfigLoadsWithLauncherEnv(t *testing.T) {
	t.Setenv("GOFLOW_DATA_ROOT", t.TempDir())
	t.Setenv("GOFLOW_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("GOFLOW_API_KEY", "secret")
	t.Setenv("GOFLOW_MODEL", "test-model")
	t.Setenv("GOFLOW_BACKUP_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("GOFLOW_BACKUP_API_KEY", "secret")
	t.Setenv("GOFLOW_BACKUP_MODEL", "test-model")
	t.Setenv("GOFLOW_FILE_TOOLS_CMD", "/opt/goflow/bin/file_tools")
	t.Setenv("GOFLOW_COMMAND_TOOLS_CMD", "/opt/goflow/bin/command_tools")
	t.Setenv("GOFLOW_SKILL_RUNNER_CMD", "/opt/goflow/bin/skill_runner")
	t.Setenv("GOFLOW_WEB_TOOLS_CMD", "/opt/goflow/bin/web_tools")
	t.Setenv("GOFLOW_NETWORK_TOOLS_CMD", "/opt/goflow/bin/network_tools")
	t.Setenv("GOFLOW_OFFICE_TOOLS_CMD", "/opt/goflow/bin/office_tools")
	t.Setenv("GOFLOW_HOST_TOOLS_CMD", "/opt/goflow/bin/host_tools")
	t.Setenv("GOFLOW_SSH_TOOLS_CMD", "/opt/goflow/bin/ssh_tools")
	t.Setenv("GOFLOW_NOTIFICATION_TOOLS_CMD", "/opt/goflow/bin/notification_tools")
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
	if servers["command_tools"].Command != "/opt/goflow/bin/command_tools" {
		t.Fatalf("expected binary command_tools command, got %#v", servers["command_tools"])
	}
	if servers["web_tools"].Command != "/opt/goflow/bin/web_tools" {
		t.Fatalf("expected binary web_tools command, got %#v", servers["web_tools"])
	}
	if servers["network_tools"].Command != "/opt/goflow/bin/network_tools" {
		t.Fatalf("expected binary network_tools command, got %#v", servers["network_tools"])
	}
	if servers["office_tools"].Command != "/opt/goflow/bin/office_tools" {
		t.Fatalf("expected binary office_tools command, got %#v", servers["office_tools"])
	}
	if servers["host_tools"].Command != "/opt/goflow/bin/host_tools" || !servers["host_tools"].Enabled || servers["host_tools"].TargetScope != "host" {
		t.Fatalf("expected enabled host-scoped binary diagnostics, got %#v", servers["host_tools"])
	}
	if servers["ssh_tools"].Command != "/opt/goflow/bin/ssh_tools" || !servers["ssh_tools"].Enabled || servers["ssh_tools"].TargetScope != "host" {
		t.Fatalf("expected enabled host-scoped binary SSH tools, got %#v", servers["ssh_tools"])
	}
	if servers["skill_runner"].Command != "/opt/goflow/bin/skill_runner" {
		t.Fatalf("expected binary skill_runner command, got %#v", servers["skill_runner"])
	}
	notifications := servers["notification_tools"]
	if notifications.Command != "/opt/goflow/bin/notification_tools" || len(notifications.Args) != 0 || !notifications.Enabled || !notifications.Optional || notifications.WorkDir != cfg.RuntimeHome {
		t.Fatalf("expected optional compiled notification connector in runtime home, got %#v", notifications)
	}
	if !slices.Contains(notifications.EnvAllowlist, "GOFLOW_DATA_ROOT") || !slices.Contains(notifications.EnvAllowlist, "GOFLOW_NETWORK_DISABLED") || !slices.Contains(notifications.AllowedCommands, notifications.Command) {
		t.Fatalf("notification execution boundaries missing: %#v", notifications)
	}
	if servers["python_notes"].Command != "python3" {
		t.Fatalf("expected python3 command, got %#v", servers["python_notes"])
	}
	if len(servers["python_notes"].Args) != 1 || servers["python_notes"].Args[0] != "/opt/goflow/mcp_servers/python_notes.py" {
		t.Fatalf("expected python notes path arg, got %#v", servers["python_notes"])
	}
}

func TestNotificationSourceConfigResolvesRelativeDataRootFromRuntimeHome(t *testing.T) {
	runtimeHome := filepath.Join(t.TempDir(), "runtime")
	configDir := filepath.Join(runtimeHome, "configs")
	moduleDir := filepath.Join(configDir, "mcp_servers")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join("..", "..", "configs", "mcp_servers", "notification_tools.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "notification_tools.yaml"), module, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "goflow.yaml")
	if err := os.WriteFile(path, []byte("agent: {name: fixture}\nllm: {base_url: 'http://127.0.0.1:9999/v1', model: fixture}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_DATA_ROOT", filepath.Join("..", "user-data"))
	cfg, err := LoadForWorkspace(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCP) != 1 || cfg.MCP[0].WorkDir != runtimeHome || cfg.DataRoot != ResolveDataRoot(cfg.MCP[0].WorkDir) {
		t.Fatalf("notification launcher and application data roots disagree: data=%q, servers=%#v", cfg.DataRoot, cfg.MCP)
	}
}

func TestValidateMCPServerPlatformRejectsRemovedContainerIsolation(t *testing.T) {
	server := MCPServerRef{Name: "helper", Isolation: "container"}
	if err := ValidateMCPServerPlatform(server); err == nil || !strings.Contains(err.Error(), "has been removed") {
		t.Fatalf("expected removed container isolation error, got %v", err)
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

func TestLoadWorkspaceLocalOnly(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
workspace:
  local_only: true
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Workspace.LocalOnly {
		t.Fatalf("expected workspace.local_only to be true")
	}
}

func TestLoadExpandsHTTPTokenEnvironmentReference(t *testing.T) {
	t.Setenv("GOFLOW_TEST_HTTP_TOKEN", "local-test-token")
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`llm:
  base_url: http://localhost:9999/v1
  model: test-model
skill:
  directory: ./skills
http:
  token: ${GOFLOW_TEST_HTTP_TOKEN}
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTP.Token != "local-test-token" {
		t.Fatalf("expected expanded HTTP token, got %q", cfg.HTTP.Token)
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

func TestLoadDataRootUserModuleOverridesBuiltin(t *testing.T) {
	runtimeHome := filepath.Join(t.TempDir(), "runtime")
	configPath := filepath.Join(runtimeHome, "configs", "goflow.yaml")
	dataRoot := filepath.Join(t.TempDir(), "data")
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	for _, dir := range []string{
		filepath.Dir(configPath),
		filepath.Join(runtimeHome, "configs", "agents"),
		filepath.Join(dataRoot, "resources", "agents"),
		workspaceRoot,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mainConfig := []byte(`providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  chat:
    name: Chat
    provider: primary
skill:
  directory: ../skills
`)
	if err := os.WriteFile(configPath, mainConfig, 0o644); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeHome, "configs", "agents", "review.yaml"), []byte(`review:
  name: Built-in review
  description: Built-in description
  provider: primary
`), 0o644); err != nil {
		t.Fatalf("write built-in agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "resources", "agents", "review.yaml"), []byte(`review:
  name: User review
  description: User override
  provider: primary
`), 0o644); err != nil {
		t.Fatalf("write user agent: %v", err)
	}
	t.Setenv("GOFLOW_DATA_ROOT", dataRoot)
	cfg, err := LoadForWorkspace(configPath, workspaceRoot)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DataRoot != filepath.Clean(dataRoot) {
		t.Fatalf("unexpected data root %q", cfg.DataRoot)
	}
	if cfg.Agents["review"].Description != "User override" {
		t.Fatalf("expected user override, got %#v", cfg.Agents["review"])
	}
	if cfg.WorkspaceDataRoot == workspaceRoot || !strings.HasPrefix(cfg.WorkspaceDataRoot, filepath.Join(dataRoot, "workspaces")) {
		t.Fatalf("unexpected workspace data root %q", cfg.WorkspaceDataRoot)
	}
	wantSession := filepath.Join(cfg.WorkspaceDataRoot, ".goflow", "session.json")
	if cfg.Session.PersistPath != wantSession {
		t.Fatalf("expected data-root session path %q, got %q", wantSession, cfg.Session.PersistPath)
	}
	store := persistence.New(dataRoot)
	if err := store.SetTombstone("agents", "review", true); err != nil {
		t.Fatalf("set Agent tombstone: %v", err)
	}
	cfg, err = LoadForWorkspace(configPath, workspaceRoot)
	if err != nil {
		t.Fatalf("reload with tombstone: %v", err)
	}
	if _, ok := cfg.Agents["review"]; ok {
		t.Fatalf("expected Agent tombstone to hide built-in and override")
	}
}

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadExpandsEnvAndDefaults(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("GOFLOW_API_KEY", "secret")
	t.Setenv("GOFLOW_MODEL", "test-model")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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
}

func TestLoadExpandsBackupAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "https://api.deepseek.com/v1")
	t.Setenv("GOFLOW_API_KEY", "primary-secret")
	t.Setenv("GOFLOW_MODEL", "deepseek-chat")
	t.Setenv("GOFLOW_BACKUP_BASE_URL", "https://api.deepseek.com/v1")
	t.Setenv("GOFLOW_BACKUP_API_KEY", "backup-secret")
	t.Setenv("GOFLOW_BACKUP_MODEL", "deepseek-chat")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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

func TestLoadExpandsBootstrapEnvExampleValues(t *testing.T) {
	t.Setenv("GOFLOW_BASE_URL", "https://api.deepseek.com/v1")
	t.Setenv("GOFLOW_API_KEY", "example-primary")
	t.Setenv("GOFLOW_MODEL", "deepseek-chat")
	t.Setenv("GOFLOW_BACKUP_BASE_URL", "https://api.deepseek.com/v1")
	t.Setenv("GOFLOW_BACKUP_API_KEY", "example-primary")
	t.Setenv("GOFLOW_BACKUP_MODEL", "deepseek-chat")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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

func TestLoadRejectsDefaultAgentMissingFromAgents(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	if cfg.MCP[0].WorkDir != tmp {
		t.Fatalf("expected default workdir %q, got %q", tmp, cfg.MCP[0].WorkDir)
	}
}

func TestLoadAcceptsMCPProcessGroupIsolation(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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

func TestLoadAcceptsMCPLinuxCgroupIsolationOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux_cgroup isolation is Linux-only")
	}
	t.Setenv("GOFLOW_CGROUP_PARENT", "/sys/fs/cgroup/goflow-test")

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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

func TestLoadRejectsMCPLinuxCgroupIsolationOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux_cgroup is supported on Linux")
	}

	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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

func TestLoadRejectsUnknownMCPIsolationOption(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "only supported with linux_cgroup") {
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
		case "/app/bin/file_tools", "/app/bin/web_tools":
			return file, nil
		case "python3":
			return "/usr/bin/python3", nil
		default:
			return originalLookPath(file)
		}
	}
	t.Cleanup(func() { execLookPath = originalLookPath })

	repoRoot := filepath.Join("..", "..")
	configPath := filepath.Join(repoRoot, "configs", "agent.docker.yaml")
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
	if len(cfg.MCP) != 3 {
		t.Fatalf("expected 3 docker MCP servers, got %d", len(cfg.MCP))
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
	t.Setenv("GOFLOW_WEB_TOOLS_CMD", "/opt/goflow/bin/web_tools")
	t.Setenv("GOFLOW_PYTHON_CMD", "python3")
	t.Setenv("GOFLOW_PYTHON_NOTES_PATH", "/opt/goflow/mcp_servers/python_notes.py")

	repoRoot := filepath.Join("..", "..")
	configPath := filepath.Join(repoRoot, "configs", "agent.binary.yaml")
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
	if servers["python_notes"].Command != "python3" {
		t.Fatalf("expected python3 command, got %#v", servers["python_notes"])
	}
	if len(servers["python_notes"].Args) != 1 || servers["python_notes"].Args[0] != "/opt/goflow/mcp_servers/python_notes.py" {
		t.Fatalf("expected python notes path arg, got %#v", servers["python_notes"])
	}
}

func TestLoadRejectsUnknownMCPIsolation(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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

	if _, err := Load(configPath); err == nil || !strings.Contains(err.Error(), "isolation") || !strings.Contains(err.Error(), "linux_cgroup") {
		t.Fatalf("expected invalid isolation validation error, got %v", err)
	}
}

func TestLoadRejectsEnabledMCPWithoutCommandAllowlist(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(configDir, "configs", "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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
	configPath := filepath.Join(tmp, "agent.yaml")
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

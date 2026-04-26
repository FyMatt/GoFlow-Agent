package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultMCPMaxRequestBytes  = 64 * 1024
	defaultMCPMaxResponseBytes = 2 * 1024 * 1024
	defaultSessionMaxHistory   = 6
	defaultMCPRestartLimit     = 3
	defaultMCPCooldown         = 10 * time.Second
)

// Load reads the main YAML config file into the runtime Config.
func Load(path string) (*Config, error) {
	workspaceRoot, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get current directory: %w", err)
	}
	return LoadForWorkspace(path, workspaceRoot)
}

func LoadForWorkspace(path, workspaceRoot string) (*Config, error) {
	if !filepath.IsAbs(path) {
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve config path: %w", err)
		}
		path = absolutePath
	}
	if strings.TrimSpace(workspaceRoot) == "" || !filepath.IsAbs(workspaceRoot) {
		return nil, fmt.Errorf("workspace root must be an absolute path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.RuntimeHome = filepath.Dir(filepath.Dir(path))
	cfg.WorkspaceRoot = filepath.Clean(workspaceRoot)
	applyDefaults(&cfg)
	expandEnv(&cfg)
	resolveRuntimePaths(&cfg, cfg.RuntimeHome, filepath.Dir(path))
	resolveWorkspacePaths(&cfg)
	normalizeConfig(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Agent.Name == "" {
		cfg.Agent.Name = "GoFlow Agent"
	}
	if cfg.Agent.MaxIterations <= 0 {
		cfg.Agent.MaxIterations = 8
	}
	if cfg.Skill.Directory == "" {
		cfg.Skill.Directory = filepath.Join("skills")
	}
	if cfg.Skill.MatchThreshold <= 0 {
		cfg.Skill.MatchThreshold = 1
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "text"
	}
	if cfg.Session.MaxHistory <= 0 {
		cfg.Session.MaxHistory = defaultSessionMaxHistory
	}
	if strings.TrimSpace(cfg.Session.PersistPath) == "" {
		cfg.Session.PersistPath = filepath.Join(".goflow", "session.json")
	}
	applyLLMDefaults(&cfg.LLM)
	for name, provider := range cfg.Providers {
		applyLLMDefaults(&provider)
		cfg.Providers[name] = provider
	}
	for i := range cfg.MCP {
		if cfg.MCP[i].MaxRequestBytes <= 0 {
			cfg.MCP[i].MaxRequestBytes = defaultMCPMaxRequestBytes
		}
		if cfg.MCP[i].MaxResponseBytes <= 0 {
			cfg.MCP[i].MaxResponseBytes = defaultMCPMaxResponseBytes
		}
		if cfg.MCP[i].RestartLimit <= 0 {
			cfg.MCP[i].RestartLimit = defaultMCPRestartLimit
		}
		if cfg.MCP[i].Cooldown <= 0 {
			cfg.MCP[i].Cooldown = defaultMCPCooldown
		}
		if strings.TrimSpace(cfg.MCP[i].WorkDir) == "" {
			cfg.MCP[i].WorkDir = "."
		}
	}
}

func applyLLMDefaults(cfg *LLMConfig) {
	if cfg == nil {
		return
	}
	if cfg.Provider == "" {
		cfg.Provider = "openai-compatible"
	}
}

func expandEnv(cfg *Config) {
	expandLLMEnv(&cfg.LLM)
	cfg.Skill.Directory = os.ExpandEnv(cfg.Skill.Directory)
	cfg.DefaultAgent = os.ExpandEnv(cfg.DefaultAgent)
	for name, provider := range cfg.Providers {
		expandLLMEnv(&provider)
		cfg.Providers[name] = provider
	}
	for name, profile := range cfg.Agents {
		profile.Name = os.ExpandEnv(profile.Name)
		profile.Description = os.ExpandEnv(profile.Description)
		profile.SystemPrompt = os.ExpandEnv(profile.SystemPrompt)
		profile.Provider = os.ExpandEnv(profile.Provider)
		profile.Model = os.ExpandEnv(profile.Model)
		if profile.SkillOverride != nil {
			profile.SkillOverride.Directory = os.ExpandEnv(profile.SkillOverride.Directory)
		}
		cfg.Agents[name] = profile
	}
	for i := range cfg.MCP {
		cfg.MCP[i].Command = os.ExpandEnv(cfg.MCP[i].Command)
		cfg.MCP[i].WorkDir = os.ExpandEnv(cfg.MCP[i].WorkDir)
		for j := range cfg.MCP[i].Args {
			cfg.MCP[i].Args[j] = os.ExpandEnv(cfg.MCP[i].Args[j])
		}
		for j := range cfg.MCP[i].EnvAllowlist {
			cfg.MCP[i].EnvAllowlist[j] = os.ExpandEnv(cfg.MCP[i].EnvAllowlist[j])
		}
		for j := range cfg.MCP[i].AllowedCommandPaths {
			cfg.MCP[i].AllowedCommandPaths[j] = os.ExpandEnv(cfg.MCP[i].AllowedCommandPaths[j])
		}
		for j := range cfg.MCP[i].AllowedCommands {
			cfg.MCP[i].AllowedCommands[j] = os.ExpandEnv(cfg.MCP[i].AllowedCommands[j])
		}
		cfg.MCP[i].Isolation = os.ExpandEnv(cfg.MCP[i].Isolation)
		if len(cfg.MCP[i].IsolationOptions) > 0 {
			expanded := make(map[string]string, len(cfg.MCP[i].IsolationOptions))
			for key, value := range cfg.MCP[i].IsolationOptions {
				key = strings.ToLower(strings.TrimSpace(key))
				expanded[key] = os.ExpandEnv(value)
			}
			cfg.MCP[i].IsolationOptions = expanded
		}
	}
}

func expandLLMEnv(cfg *LLMConfig) {
	if cfg == nil {
		return
	}
	cfg.BaseURL = os.ExpandEnv(cfg.BaseURL)
	cfg.APIKey = os.ExpandEnv(cfg.APIKey)
	cfg.Model = os.ExpandEnv(cfg.Model)
}

func resolveRuntimePaths(cfg *Config, runtimeHome, configDir string) {
	cfg.Skill.Directory = resolveRuntimePath(runtimeHome, configDir, cfg.Skill.Directory)
	for i := range cfg.MCP {
		cfg.MCP[i].WorkDir = resolveRuntimePath(runtimeHome, configDir, cfg.MCP[i].WorkDir)
		for j := range cfg.MCP[i].Args {
			cfg.MCP[i].Args[j] = resolveRuntimeArg(runtimeHome, cfg.MCP[i].Args[j])
		}
	}
}

func resolveWorkspacePaths(cfg *Config) {
	cfg.Session.PersistPath = resolveRelativeTo(cfg.WorkspaceRoot, cfg.Session.PersistPath)
	for i := range cfg.MCP {
		cfg.MCP[i].WorkspaceRoot = cfg.WorkspaceRoot
	}
}

func resolveRelativeTo(baseDir, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return filepath.Clean(trimmed)
	}
	if isConfigAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(filepath.Join(baseDir, trimmed))
}

func resolveRuntimePath(runtimeHome, configDir, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || isConfigAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	if strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, ".\\") {
		return filepath.Clean(filepath.Join(runtimeHome, trimmed[2:]))
	}
	return filepath.Clean(filepath.Join(configDir, trimmed))
}

func resolveRuntimeArg(runtimeHome, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || isConfigAbs(trimmed) {
		return value
	}
	if strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, ".\\") {
		return filepath.Clean(filepath.Join(runtimeHome, trimmed[2:]))
	}
	return value
}

func isConfigAbs(value string) bool {
	return filepath.IsAbs(value) || strings.HasPrefix(strings.TrimSpace(value), "/")
}

func normalizeConfig(cfg *Config) {
	if cfg.Providers == nil {
		cfg.Providers = make(map[string]LLMConfig)
	}
	if len(cfg.Providers) == 0 && (cfg.LLM.BaseURL != "" || cfg.LLM.Model != "" || cfg.LLM.APIKey != "" || cfg.LLM.Provider != "") {
		providerName := defaultProviderName(cfg.LLM)
		cfg.Providers[providerName] = cfg.LLM
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]AgentProfile)
	}
	if len(cfg.Agents) == 0 {
		providerName := defaultProviderName(cfg.LLM)
		cfg.Agents["default"] = AgentProfile{
			Name:             cfg.Agent.Name,
			Provider:         providerName,
			Model:            cfg.LLM.Model,
			Temperature:      cfg.LLM.Temperature,
			MaxTokens:        cfg.LLM.MaxTokens,
			MaxIterations:    cfg.Agent.MaxIterations,
			AllowedToolKinds: []ToolKind{ToolKindRead, ToolKindWrite, ToolKindExec, ToolKindNetwork, ToolKindUnknown},
			ToolPolicy:       ToolPolicyAllow,
			Mode:             "chat",
		}
	}
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = firstSortedAgentName(cfg.Agents)
	}
	for name, profile := range cfg.Agents {
		if profile.Name == "" {
			profile.Name = name
		}
		if profile.Provider == "" {
			profile.Provider = defaultProviderName(cfg.LLM)
		}
		if providerCfg, ok := cfg.Providers[profile.Provider]; ok {
			if profile.Model == "" {
				profile.Model = providerCfg.Model
			}
			if profile.Temperature == 0 {
				profile.Temperature = providerCfg.Temperature
			}
			if profile.MaxTokens == 0 {
				profile.MaxTokens = providerCfg.MaxTokens
			}
		}
		if profile.MaxIterations <= 0 {
			profile.MaxIterations = cfg.Agent.MaxIterations
		}
		if len(profile.AllowedToolKinds) == 0 {
			profile.AllowedToolKinds = []ToolKind{ToolKindRead, ToolKindWrite, ToolKindExec, ToolKindNetwork, ToolKindUnknown}
		}
		if profile.ToolPolicy == "" {
			profile.ToolPolicy = ToolPolicyAllow
		}
		if profile.Mode == "" {
			profile.Mode = "chat"
		}
		cfg.Agents[name] = profile
	}
	normalizeVerifierConfig(cfg)
	cfg.LLM = cfg.Providers[defaultProviderName(cfg.LLM)]
}

func normalizeVerifierConfig(cfg *Config) {
	if cfg == nil || !cfg.Verifier.Enabled {
		return
	}
	if strings.TrimSpace(cfg.Verifier.Agent) == "" {
		if _, ok := cfg.Agents["auditor"]; ok {
			cfg.Verifier.Agent = "auditor"
		} else {
			cfg.Verifier.Agent = cfg.DefaultAgent
		}
	}
	if len(cfg.Verifier.Modes) == 0 {
		cfg.Verifier.Modes = []string{"fix", "audit"}
	}
	for i := range cfg.Verifier.Modes {
		cfg.Verifier.Modes[i] = strings.ToLower(strings.TrimSpace(cfg.Verifier.Modes[i]))
	}
	if cfg.Verifier.MaxTokens <= 0 {
		cfg.Verifier.MaxTokens = 768
	}
}

func defaultProviderName(cfg LLMConfig) string {
	if strings.TrimSpace(cfg.Provider) == "" {
		return "default"
	}
	return strings.TrimSpace(cfg.Provider)
}

func firstSortedAgentName(agents map[string]AgentProfile) string {
	if len(agents) == 0 {
		return ""
	}
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0]
}

func validate(cfg *Config) error {
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	if len(cfg.Agents) == 0 {
		return fmt.Errorf("at least one agent is required")
	}
	if strings.TrimSpace(cfg.DefaultAgent) == "" {
		return fmt.Errorf("default_agent is required")
	}
	if _, ok := cfg.Agents[cfg.DefaultAgent]; !ok {
		return fmt.Errorf("default_agent %q is not defined", cfg.DefaultAgent)
	}
	if cfg.Skill.Directory == "" {
		return fmt.Errorf("skill.directory is required")
	}
	if cfg.Agent.MaxIterations <= 0 {
		return fmt.Errorf("agent.max_iterations must be greater than 0")
	}
	for name, provider := range cfg.Providers {
		if strings.TrimSpace(provider.Model) == "" {
			return fmt.Errorf("provider %s model is required", name)
		}
		if strings.TrimSpace(provider.BaseURL) == "" {
			return fmt.Errorf("provider %s base_url is required", name)
		}
		if strings.TrimSpace(provider.FallbackProvider) != "" {
			if _, ok := cfg.Providers[provider.FallbackProvider]; !ok {
				return fmt.Errorf("provider %s references unknown fallback_provider %q", name, provider.FallbackProvider)
			}
			if provider.FallbackProvider == name {
				return fmt.Errorf("provider %s fallback_provider cannot reference itself", name)
			}
		}
	}
	for name, profile := range cfg.Agents {
		if strings.TrimSpace(profile.Provider) == "" {
			return fmt.Errorf("agent %s provider is required", name)
		}
		if _, ok := cfg.Providers[profile.Provider]; !ok {
			return fmt.Errorf("agent %s references unknown provider %q", name, profile.Provider)
		}
		if profile.MaxIterations <= 0 {
			return fmt.Errorf("agent %s max_iterations must be greater than 0", name)
		}
	}
	if cfg.Verifier.Enabled {
		if strings.TrimSpace(cfg.Verifier.Agent) == "" {
			return fmt.Errorf("verifier.agent is required when verifier is enabled")
		}
		if _, ok := cfg.Agents[cfg.Verifier.Agent]; !ok {
			return fmt.Errorf("verifier references unknown agent %q", cfg.Verifier.Agent)
		}
		if len(cfg.Verifier.Modes) == 0 {
			return fmt.Errorf("verifier.modes is required when verifier is enabled")
		}
	}
	enabledMCPNames := make(map[string]struct{})
	for _, server := range cfg.MCP {
		if !server.Enabled {
			continue
		}
		if strings.TrimSpace(server.Name) == "" {
			return fmt.Errorf("enabled mcp server name is required")
		}
		serverName := strings.TrimSpace(server.Name)
		if _, exists := enabledMCPNames[serverName]; exists {
			return fmt.Errorf("enabled mcp server %s is defined more than once", serverName)
		}
		enabledMCPNames[serverName] = struct{}{}
		if strings.TrimSpace(server.Command) == "" {
			return fmt.Errorf("mcp server %s command is required", server.Name)
		}
		if strings.TrimSpace(server.WorkDir) == "" {
			return fmt.Errorf("mcp server %s workdir is required", server.Name)
		}
		if server.RestartLimit <= 0 {
			return fmt.Errorf("mcp server %s restart_limit must be greater than 0", server.Name)
		}
		if server.Cooldown <= 0 {
			return fmt.Errorf("mcp server %s cooldown must be greater than 0", server.Name)
		}
		if server.MaxRequestBytes <= 0 {
			return fmt.Errorf("mcp server %s max_request_bytes must be greater than 0", server.Name)
		}
		if server.MaxResponseBytes <= 0 {
			return fmt.Errorf("mcp server %s max_response_bytes must be greater than 0", server.Name)
		}
		if len(server.AllowedCommands) == 0 && len(server.AllowedCommandPaths) == 0 {
			return fmt.Errorf("mcp server %s must define allowed_commands or allowed_command_paths", server.Name)
		}
		if len(server.AllowedCommands) > 0 && !containsTrimmed(server.AllowedCommands, server.Command) {
			return fmt.Errorf("mcp server %s command %q is not in allowed_commands", server.Name, server.Command)
		}
		if len(server.AllowedCommandPaths) > 0 {
			matched, err := commandAllowedByPath(server.Command, server.AllowedCommandPaths)
			if err != nil {
				return fmt.Errorf("mcp server %s command validation failed: %w", server.Name, err)
			}
			if !matched {
				return fmt.Errorf("mcp server %s command %q is outside allowed_command_paths", server.Name, server.Command)
			}
		}
		if err := validateMCPIsolation(server); err != nil {
			return err
		}
	}
	return nil
}

func validateMCPIsolation(server MCPServerRef) error {
	mode := strings.ToLower(strings.TrimSpace(server.Isolation))
	if mode == "" {
		mode = "none"
	}
	if err := validateMCPIsolationOptions(server.Name, mode, server.IsolationOptions); err != nil {
		return err
	}
	switch mode {
	case "none", "process_group":
		return nil
	case "windows_job":
		if runtime.GOOS != "windows" {
			return fmt.Errorf("mcp server %s isolation %q is only supported on Windows", server.Name, server.Isolation)
		}
		return nil
	case "linux_cgroup":
		if runtime.GOOS != "linux" {
			return fmt.Errorf("mcp server %s isolation %q is only supported on Linux", server.Name, server.Isolation)
		}
		return nil
	default:
		return fmt.Errorf("mcp server %s isolation %q is invalid; expected none, process_group, windows_job, or linux_cgroup", server.Name, server.Isolation)
	}
}

func validateMCPIsolationOptions(serverName, mode string, options map[string]string) error {
	if len(options) == 0 {
		return nil
	}
	if mode != "linux_cgroup" {
		return fmt.Errorf("mcp server %s isolation_options are only supported with linux_cgroup", serverName)
	}
	allowed := map[string]struct{}{
		"cgroup_parent": {},
		"cgroup_name":   {},
		"memory_max":    {},
		"pids_max":      {},
		"cpu_max":       {},
	}
	for key := range options {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := allowed[normalized]; !ok {
			return fmt.Errorf("mcp server %s isolation_options.%s is invalid; expected cgroup_parent, cgroup_name, memory_max, pids_max, or cpu_max", serverName, key)
		}
	}
	return nil
}

func containsTrimmed(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func commandAllowedByPath(command string, allowed []string) (bool, error) {
	resolvedCommand, err := execLookPath(command)
	if err != nil {
		return false, err
	}
	resolvedCommand = filepath.Clean(resolvedCommand)
	for _, candidate := range allowed {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		candidatePath := filepath.Clean(candidate)
		rel, err := filepath.Rel(candidatePath, resolvedCommand)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true, nil
		}
	}
	return false, nil
}

var execLookPath = exec.LookPath

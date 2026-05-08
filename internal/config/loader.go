package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	containerIsolationProfileReadonly   = "readonly"
	containerIsolationProfileWriter     = "writer"
	containerIsolationProfileNetwork    = "network"
	containerIsolationProfileProduction = "production"
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
	cfg.ConfigPath = filepath.Clean(path)
	if err := mergeModularConfig(&cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	expandEnv(&cfg)
	resolveRuntimePaths(&cfg, cfg.RuntimeHome, filepath.Dir(path))
	resolveWorkspacePaths(&cfg)
	normalizeConfig(&cfg)
	if err := applyMCPServerIsolationProfiles(&cfg); err != nil {
		return nil, err
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func mergeModularConfig(cfg *Config) error {
	if cfg == nil || strings.TrimSpace(cfg.RuntimeHome) == "" {
		return nil
	}
	if err := mergeProviderConfigDir(cfg, filepath.Join(cfg.RuntimeHome, "configs", "providers")); err != nil {
		return err
	}
	if err := mergeAgentConfigDir(cfg, filepath.Join(cfg.RuntimeHome, "configs", "agents")); err != nil {
		return err
	}
	if err := mergeMCPConfigDir(cfg, filepath.Join(cfg.RuntimeHome, "configs", "mcp_servers")); err != nil {
		return err
	}
	return nil
}

func mergeProviderConfigDir(cfg *Config, dir string) error {
	files, err := modularConfigFiles(dir)
	if err != nil {
		return fmt.Errorf("load provider config modules: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	if cfg.Providers == nil {
		cfg.Providers = make(map[string]LLMConfig)
	}
	for _, file := range files {
		items, err := readProviderModule(file)
		if err != nil {
			return err
		}
		for name, provider := range items {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("provider module %s has an empty provider name", file)
			}
			cfg.Providers[name] = provider
		}
	}
	return nil
}

func mergeAgentConfigDir(cfg *Config, dir string) error {
	files, err := modularConfigFiles(dir)
	if err != nil {
		return fmt.Errorf("load agent config modules: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]AgentProfile)
	}
	for _, file := range files {
		items, err := readAgentModule(file)
		if err != nil {
			return err
		}
		for name, profile := range items {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("agent module %s has an empty agent name", file)
			}
			cfg.Agents[name] = profile
		}
	}
	return nil
}

func mergeMCPConfigDir(cfg *Config, dir string) error {
	files, err := modularConfigFiles(dir)
	if err != nil {
		return fmt.Errorf("load mcp server config modules: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	moduleServers := make([]MCPServerRef, 0, len(files))
	for _, file := range files {
		items, err := readMCPModule(file)
		if err != nil {
			return err
		}
		for _, server := range items {
			if strings.TrimSpace(server.Name) == "" {
				return fmt.Errorf("mcp server module %s has an empty server name", file)
			}
			moduleServers = append(moduleServers, server)
		}
	}
	cfg.MCP = mergeMCPServers(moduleServers, cfg.MCP)
	return nil
}

func modularConfigFiles(dir string) ([]string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files, nil
}

func readProviderModule(path string) (map[string]LLMConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provider module %s: %w", path, err)
	}
	var wrapped struct {
		Providers map[string]LLMConfig `yaml:"providers"`
	}
	if err := yaml.Unmarshal(data, &wrapped); err != nil {
		return nil, fmt.Errorf("parse provider module %s: %w", path, err)
	}
	if len(wrapped.Providers) > 0 {
		return wrapped.Providers, nil
	}
	var direct map[string]LLMConfig
	if err := yaml.Unmarshal(data, &direct); err != nil {
		return nil, fmt.Errorf("parse provider module %s: %w", path, err)
	}
	if len(direct) == 0 {
		return nil, fmt.Errorf("provider module %s does not define any providers", path)
	}
	return direct, nil
}

func readAgentModule(path string) (map[string]AgentProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent module %s: %w", path, err)
	}
	var wrapped struct {
		Agents map[string]AgentProfile `yaml:"agents"`
	}
	if err := yaml.Unmarshal(data, &wrapped); err != nil {
		return nil, fmt.Errorf("parse agent module %s: %w", path, err)
	}
	if len(wrapped.Agents) > 0 {
		return wrapped.Agents, nil
	}
	var direct map[string]AgentProfile
	if err := yaml.Unmarshal(data, &direct); err != nil {
		return nil, fmt.Errorf("parse agent module %s: %w", path, err)
	}
	if len(direct) == 0 {
		return nil, fmt.Errorf("agent module %s does not define any agents", path)
	}
	return direct, nil
}

func readMCPModule(path string) ([]MCPServerRef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mcp server module %s: %w", path, err)
	}
	var wrapped struct {
		MCPServers []MCPServerRef `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal(data, &wrapped); err != nil {
		return nil, fmt.Errorf("parse mcp server module %s: %w", path, err)
	}
	if len(wrapped.MCPServers) > 0 {
		return wrapped.MCPServers, nil
	}
	var direct MCPServerRef
	if err := yaml.Unmarshal(data, &direct); err != nil {
		return nil, fmt.Errorf("parse mcp server module %s: %w", path, err)
	}
	if strings.TrimSpace(direct.Name) == "" {
		return nil, fmt.Errorf("mcp server module %s does not define any mcp servers", path)
	}
	return []MCPServerRef{direct}, nil
}

func mergeMCPServers(base, overrides []MCPServerRef) []MCPServerRef {
	merged := make([]MCPServerRef, 0, len(base)+len(overrides))
	indexByName := make(map[string]int)
	for _, server := range base {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			merged = append(merged, server)
			continue
		}
		key := strings.ToLower(name)
		if index, exists := indexByName[key]; exists {
			merged[index] = server
			continue
		}
		indexByName[key] = len(merged)
		merged = append(merged, server)
	}
	for _, server := range overrides {
		name := strings.TrimSpace(server.Name)
		if name == "" {
			merged = append(merged, server)
			continue
		}
		key := strings.ToLower(name)
		if index, exists := indexByName[key]; exists {
			merged[index] = server
			continue
		}
		indexByName[key] = len(merged)
		merged = append(merged, server)
	}
	return merged
}

func applyMCPServerIsolationProfiles(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	for i := range cfg.MCP {
		if err := ApplyContainerIsolationProfile(&cfg.MCP[i]); err != nil {
			return err
		}
	}
	return nil
}

func ApplyContainerIsolationProfile(server *MCPServerRef) error {
	if server == nil {
		return nil
	}
	profile := strings.ToLower(strings.TrimSpace(server.IsolationProfile))
	if profile == "" && server.IsolationOptions != nil {
		profile = strings.ToLower(strings.TrimSpace(server.IsolationOptions["profile"]))
	}
	if profile == "" {
		return nil
	}
	defaults := ContainerIsolationProfileDefaults(profile)
	if len(defaults) == 0 {
		return fmt.Errorf("mcp server %s isolation_profile %q is invalid; expected readonly, writer, network, or production", server.Name, profile)
	}
	if server.IsolationOptions == nil {
		server.IsolationOptions = make(map[string]string, len(defaults)+1)
	}
	for key, value := range defaults {
		if _, exists := server.IsolationOptions[key]; exists {
			continue
		}
		server.IsolationOptions[key] = value
	}
	delete(server.IsolationOptions, "profile")
	server.IsolationProfile = profile
	return nil
}

func mergeContainerIsolationProfile(server MCPServerRef) MCPServerRef {
	_ = ApplyContainerIsolationProfile(&server)
	return server
}

func ContainerIsolationProfileDefaults(profile string) map[string]string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case containerIsolationProfileReadonly:
		return map[string]string{
			"workspace_mount":   "ro",
			"network":           "disabled",
			"ipc":               "none",
			"userns":            "auto",
			"memory":            "256m",
			"memory_swap":       "256m",
			"cpus":              "0.5",
			"pids_limit":        "64",
			"pull_policy":       "missing",
			"readonly_rootfs":   "true",
			"no_new_privileges": "true",
			"cap_drop":          "all",
			"user":              "65532:65532",
			"tmpfs":             "/tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m",
			"init":              "true",
			"tool_mount":        "ro",
		}
	case containerIsolationProfileWriter:
		return map[string]string{
			"workspace_mount":   "rw",
			"network":           "disabled",
			"ipc":               "none",
			"userns":            "auto",
			"memory":            "256m",
			"memory_swap":       "256m",
			"cpus":              "0.5",
			"pids_limit":        "64",
			"pull_policy":       "missing",
			"readonly_rootfs":   "true",
			"no_new_privileges": "true",
			"cap_drop":          "all",
			"tmpfs":             "/tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m",
			"init":              "true",
			"tool_mount":        "ro",
		}
	case containerIsolationProfileNetwork:
		return map[string]string{
			"workspace_mount":   "ro",
			"network":           "bridge",
			"ipc":               "none",
			"userns":            "auto",
			"memory":            "256m",
			"memory_swap":       "256m",
			"cpus":              "0.5",
			"pids_limit":        "64",
			"pull_policy":       "missing",
			"readonly_rootfs":   "true",
			"no_new_privileges": "true",
			"cap_drop":          "all",
			"user":              "65532:65532",
			"tmpfs":             "/tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m",
			"init":              "true",
			"tool_mount":        "ro",
		}
	case containerIsolationProfileProduction:
		return map[string]string{
			"workspace_mount":   "ro",
			"network":           "disabled",
			"ipc":               "none",
			"userns":            "auto",
			"memory":            "256m",
			"memory_swap":       "256m",
			"cpus":              "0.5",
			"pids_limit":        "64",
			"pull_policy":       "never",
			"readonly_rootfs":   "true",
			"no_new_privileges": "true",
			"cap_drop":          "all",
			"user":              "65532:65532",
			"tmpfs":             "/tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m",
			"init":              "true",
			"tool_mount":        "ro",
		}
	default:
		return nil
	}
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
	cfg.Verifier.Agent = os.ExpandEnv(cfg.Verifier.Agent)
	cfg.Verifier.Provider = os.ExpandEnv(cfg.Verifier.Provider)
	cfg.Verifier.Model = os.ExpandEnv(cfg.Verifier.Model)
	expandAuxiliaryModelEnv(&cfg.CostControl.Router)
	expandAuxiliaryModelEnv(&cfg.CostControl.Summarizer)
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

func expandAuxiliaryModelEnv(cfg *AuxiliaryModelConfig) {
	if cfg == nil {
		return
	}
	cfg.Provider = os.ExpandEnv(cfg.Provider)
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
	normalizeCostControlConfig(cfg)
	cfg.LLM = cfg.Providers[defaultProviderName(cfg.LLM)]
}

func normalizeVerifierConfig(cfg *Config) {
	if cfg == nil || !cfg.Verifier.Enabled {
		return
	}
	cfg.Verifier.Agent = strings.TrimSpace(cfg.Verifier.Agent)
	cfg.Verifier.Provider = strings.TrimSpace(cfg.Verifier.Provider)
	cfg.Verifier.Model = strings.TrimSpace(cfg.Verifier.Model)
	if strings.TrimSpace(cfg.Verifier.Agent) == "" {
		if _, ok := cfg.Agents["auditor"]; ok {
			cfg.Verifier.Agent = "auditor"
		} else {
			cfg.Verifier.Agent = cfg.DefaultAgent
		}
	}
	if cfg.Verifier.Provider != "" && cfg.Verifier.Model == "" {
		if provider, ok := cfg.Providers[cfg.Verifier.Provider]; ok {
			cfg.Verifier.Model = strings.TrimSpace(provider.Model)
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

func normalizeCostControlConfig(cfg *Config) {
	if cfg == nil {
		return
	}
	normalizeAuxiliaryModelConfig(&cfg.CostControl.Router, cfg.Providers, 96)
	normalizeAuxiliaryModelConfig(&cfg.CostControl.Summarizer, cfg.Providers, 512)
}

func normalizeAuxiliaryModelConfig(route *AuxiliaryModelConfig, providers map[string]LLMConfig, defaultMaxTokens int) {
	if route == nil || !route.Enabled {
		return
	}
	route.Provider = strings.TrimSpace(route.Provider)
	route.Model = strings.TrimSpace(route.Model)
	if route.Provider != "" && route.Model == "" {
		if provider, ok := providers[route.Provider]; ok {
			route.Model = strings.TrimSpace(provider.Model)
		}
	}
	if route.MaxTokens <= 0 {
		route.MaxTokens = defaultMaxTokens
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
		if strings.TrimSpace(cfg.Verifier.Provider) != "" {
			if _, ok := cfg.Providers[cfg.Verifier.Provider]; !ok {
				return fmt.Errorf("verifier references unknown provider %q", cfg.Verifier.Provider)
			}
		}
		if len(cfg.Verifier.Modes) == 0 {
			return fmt.Errorf("verifier.modes is required when verifier is enabled")
		}
	}
	if err := validateAuxiliaryModelConfig("cost_control.router", cfg.CostControl.Router, cfg.Providers); err != nil {
		return err
	}
	if err := validateAuxiliaryModelConfig("cost_control.summarizer", cfg.CostControl.Summarizer, cfg.Providers); err != nil {
		return err
	}
	enabledMCPNames := make(map[string]struct{})
	for _, server := range cfg.MCP {
		if !server.Enabled {
			continue
		}
		serverName := strings.TrimSpace(server.Name)
		if _, exists := enabledMCPNames[serverName]; exists {
			return fmt.Errorf("enabled mcp server %s is defined more than once", serverName)
		}
		enabledMCPNames[serverName] = struct{}{}
		if err := ValidateMCPServerRef(server); err != nil {
			return err
		}
	}
	return nil
}

func validateAuxiliaryModelConfig(path string, route AuxiliaryModelConfig, providers map[string]LLMConfig) error {
	if !route.Enabled {
		return nil
	}
	if strings.TrimSpace(route.Provider) != "" {
		if _, ok := providers[route.Provider]; !ok {
			return fmt.Errorf("%s references unknown provider %q", path, route.Provider)
		}
	}
	return nil
}

// ValidateMCPServerRef validates one enabled MCP server reference after
// defaults, environment expansion, and path resolution have been applied.
func ValidateMCPServerRef(server MCPServerRef) error {
	if err := ApplyContainerIsolationProfile(&server); err != nil {
		return err
	}
	if strings.TrimSpace(server.Name) == "" {
		return fmt.Errorf("enabled mcp server name is required")
	}
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
	return nil
}

func validateMCPIsolation(server MCPServerRef) error {
	mode := strings.ToLower(strings.TrimSpace(server.Isolation))
	if mode == "" {
		mode = "none"
	}
	if isUnimplementedWindowsAppContainerIsolation(mode) {
		return fmt.Errorf("mcp server %s isolation %q is not supported; use isolation: container for the supported Docker/Podman sandbox boundary", server.Name, server.Isolation)
	}
	if err := validateMCPIsolationOptions(server.Name, mode, server.IsolationOptions); err != nil {
		return err
	}
	switch mode {
	case "none", "process_group":
		return nil
	case "windows_job", "windows_restricted_token":
		if runtime.GOOS != "windows" {
			return fmt.Errorf("mcp server %s isolation %q is only supported on Windows", server.Name, server.Isolation)
		}
		return nil
	case "linux_cgroup":
		if runtime.GOOS != "linux" {
			return fmt.Errorf("mcp server %s isolation %q is only supported on Linux", server.Name, server.Isolation)
		}
		return nil
	case "linux_netns":
		if runtime.GOOS != "linux" {
			return fmt.Errorf("mcp server %s isolation %q is only supported on Linux", server.Name, server.Isolation)
		}
		return nil
	case "container":
		switch runtime.GOOS {
		case "linux", "windows", "darwin":
			if strings.TrimSpace(server.IsolationProfile) == "" && server.IsolationOptions != nil {
				server.IsolationProfile = strings.TrimSpace(server.IsolationOptions["profile"])
			}
			if strings.TrimSpace(server.IsolationProfile) != "" {
				if defaults := ContainerIsolationProfileDefaults(server.IsolationProfile); len(defaults) == 0 {
					return fmt.Errorf("mcp server %s isolation_profile %q is invalid; expected readonly, writer, network, or production", server.Name, server.IsolationProfile)
				}
			}
			if server.NetworkDisabled {
				network := strings.ToLower(strings.TrimSpace(server.IsolationOptions["network"]))
				if network != "" && network != "default" && network != "disabled" && network != "none" {
					return fmt.Errorf("mcp server %s isolation_options.network conflicts with network_disabled; use disabled/none or clear network_disabled", server.Name)
				}
			}
			return nil
		default:
			return fmt.Errorf("mcp server %s isolation %q is only supported on Linux, Windows, or macOS with a container runtime", server.Name, server.Isolation)
		}
	default:
		return fmt.Errorf("mcp server %s isolation %q is invalid; expected none, process_group, windows_job, windows_restricted_token, linux_cgroup, linux_netns, or container", server.Name, server.Isolation)
	}
}

func isUnimplementedWindowsAppContainerIsolation(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "appcontainer", "app_container", "windows_appcontainer", "windows_app_container":
		return true
	default:
		return false
	}
}

func validateMCPIsolationOptions(serverName, mode string, options map[string]string) error {
	if len(options) == 0 {
		if mode == "container" {
			return fmt.Errorf("mcp server %s isolation container requires isolation_options.image", serverName)
		}
		return nil
	}
	switch mode {
	case "linux_cgroup":
		return validateLinuxCgroupIsolationOptions(serverName, options)
	case "linux_netns":
		return validateLinuxNetworkNamespaceIsolationOptions(serverName, options)
	case "windows_restricted_token":
		if len(options) > 0 {
			return fmt.Errorf("mcp server %s isolation_options are not supported with windows_restricted_token", serverName)
		}
		return nil
	case "container":
		return validateContainerIsolationOptions(serverName, options)
	default:
		return fmt.Errorf("mcp server %s isolation_options are only supported with linux_cgroup, linux_netns, or container", serverName)
	}
}

func validateLinuxCgroupIsolationOptions(serverName string, options map[string]string) error {
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

func validateLinuxNetworkNamespaceIsolationOptions(serverName string, options map[string]string) error {
	allowed := map[string]struct{}{
		"unshare_command": {},
		"map_root_user":   {},
	}
	for key, value := range options {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := allowed[normalized]; !ok {
			return fmt.Errorf("mcp server %s isolation_options.%s is invalid; expected unshare_command or map_root_user", serverName, key)
		}
		switch normalized {
		case "unshare_command":
			if !validLinuxNetworkNamespaceCommandConfig(value) {
				return fmt.Errorf("mcp server %s isolation_options.unshare_command is invalid; expected unshare or an absolute path", serverName)
			}
		case "map_root_user":
			if err := validateBoolString(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.%s is invalid: %w", serverName, key, err)
			}
		}
	}
	return nil
}

func validLinuxNetworkNamespaceCommandConfig(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "unshare" {
		return true
	}
	return filepath.IsAbs(value)
}

func validateContainerIsolationOptions(serverName string, options map[string]string) error {
	allowed := map[string]struct{}{
		"image":             {},
		"runtime":           {},
		"workspace_mount":   {},
		"workspace_target":  {},
		"container_workdir": {},
		"network":           {},
		"ipc":               {},
		"userns":            {},
		"memory":            {},
		"memory_swap":       {},
		"cpus":              {},
		"pids_limit":        {},
		"pull_policy":       {},
		"readonly_rootfs":   {},
		"user":              {},
		"cap_drop":          {},
		"no_new_privileges": {},
		"security_opt":      {},
		"tmpfs":             {},
		"init":              {},
		"tool_mount":        {},
		"tool_source":       {},
		"tool_target":       {},
	}
	for key, value := range options {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := allowed[normalized]; !ok {
			return fmt.Errorf("mcp server %s isolation_options.%s is invalid; expected one of: image, runtime, workspace_mount, workspace_target, container_workdir, network, ipc, userns, memory, memory_swap, cpus, pids_limit, pull_policy, readonly_rootfs, user, cap_drop, no_new_privileges, security_opt, tmpfs, init, tool_source, tool_target, tool_mount", serverName, key)
		}
		switch normalized {
		case "image":
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("mcp server %s isolation_options.image is required for container isolation", serverName)
			}
		case "runtime":
			if !validContainerRuntime(value) {
				return fmt.Errorf("mcp server %s isolation_options.runtime is invalid; expected docker, podman, or an absolute runtime path", serverName)
			}
		case "workspace_mount":
			if !validContainerWorkspaceMount(value) {
				return fmt.Errorf("mcp server %s isolation_options.workspace_mount is invalid; expected rw, ro, or none", serverName)
			}
		case "tool_mount":
			if !validContainerWorkspaceMount(value) {
				return fmt.Errorf("mcp server %s isolation_options.tool_mount is invalid; expected rw, ro, or none", serverName)
			}
		case "workspace_target", "container_workdir", "tool_target":
			if err := validateContainerAbsolutePath(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.%s is invalid: %w", serverName, key, err)
			}
		case "tool_source":
			if err := validateContainerHostPath(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.%s is invalid: %w", serverName, key, err)
			}
		case "network":
			if !validContainerNetworkMode(value) {
				return fmt.Errorf("mcp server %s isolation_options.network is invalid; expected default, disabled, none, bridge, host, or a safe Docker network name", serverName)
			}
		case "ipc":
			if !validContainerIPCMode(value) {
				return fmt.Errorf("mcp server %s isolation_options.ipc is invalid; expected private or none", serverName)
			}
		case "userns":
			if !validContainerUserNamespace(value) {
				return fmt.Errorf("mcp server %s isolation_options.userns is invalid; expected private, auto, nomap, or keep-id", serverName)
			}
		case "memory":
			if err := validateContainerMemory(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.memory is invalid: %w", serverName, err)
			}
		case "memory_swap":
			if err := validateContainerMemory(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.memory_swap is invalid: %w", serverName, err)
			}
		case "cpus":
			if err := validatePositiveFloat(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.cpus is invalid: %w", serverName, err)
			}
		case "pids_limit":
			if err := validatePositiveInteger(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.pids_limit is invalid: %w", serverName, err)
			}
		case "readonly_rootfs", "no_new_privileges":
			if err := validateBoolString(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.%s is invalid: %w", serverName, key, err)
			}
		case "pull_policy":
			if !validContainerPullPolicy(value) {
				return fmt.Errorf("mcp server %s isolation_options.pull_policy is invalid; expected always, missing, or never", serverName)
			}
		case "user":
			if err := validateContainerUser(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.user is invalid: %w", serverName, err)
			}
		case "cap_drop":
			if err := validateContainerCapDrop(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.cap_drop is invalid: %w", serverName, err)
			}
		case "init":
			if err := validateBoolString(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.%s is invalid: %w", serverName, key, err)
			}
		case "security_opt":
			if err := validateContainerSecurityOptions(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.%s is invalid: %w", serverName, key, err)
			}
		case "tmpfs":
			if err := validateContainerTmpfsOptions(value); err != nil {
				return fmt.Errorf("mcp server %s isolation_options.%s is invalid: %w", serverName, key, err)
			}
		}
	}
	if strings.TrimSpace(options["image"]) == "" {
		return fmt.Errorf("mcp server %s isolation container requires isolation_options.image", serverName)
	}
	return nil
}

func validContainerRuntime(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "docker" || value == "podman" {
		return true
	}
	return filepath.IsAbs(value)
}

func validContainerWorkspaceMount(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "rw", "readwrite", "ro", "readonly", "none":
		return true
	default:
		return false
	}
}

func validateContainerAbsolutePath(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("path contains a null byte")
	}
	if !strings.HasPrefix(value, "/") {
		return fmt.Errorf("path must be absolute inside the container")
	}
	if strings.Contains(value, "\\") {
		return fmt.Errorf("path must use forward slashes")
	}
	return nil
}

func validateContainerHostPath(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("path contains a null byte")
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("path must be absolute on the host")
	}
	return nil
}

func validateContainerSecurityOptions(value string) error {
	for _, item := range splitContainerSecurityOptionConfig(value) {
		if strings.ContainsRune(item, 0) {
			return fmt.Errorf("security option contains a null byte")
		}
		if strings.ContainsAny(item, "\r\n") {
			return fmt.Errorf("security option must be a single runtime argument")
		}
	}
	return nil
}

func splitContainerSecurityOptionConfig(value string) []string {
	return splitDelimitedConfigOption(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
}

func validateContainerTmpfsOptions(value string) error {
	for _, item := range splitContainerTmpfsOptionConfig(value) {
		if strings.ContainsRune(item, 0) {
			return fmt.Errorf("tmpfs option contains a null byte")
		}
		target := item
		if index := strings.Index(target, ":"); index >= 0 {
			target = target[:index]
		}
		if err := validateContainerAbsolutePath(target); err != nil {
			return err
		}
	}
	return nil
}

func splitContainerTmpfsOptionConfig(value string) []string {
	return splitDelimitedConfigOption(value, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r'
	})
}

func splitDelimitedConfigOption(value string, separator func(rune) bool) []string {
	fields := strings.FieldsFunc(value, separator)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		out = append(out, field)
	}
	return out
}

func validContainerNetworkMode(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "default" || value == "disabled" || value == "none" || value == "bridge" || value == "host" {
		return true
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		return false
	}
	return true
}

func validContainerIPCMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "private", "none":
		return true
	default:
		return false
	}
}

func validContainerUserNamespace(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "private", "auto", "nomap", "keep-id":
		return true
	default:
		return false
	}
}

func validContainerPullPolicy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "always", "missing", "never":
		return true
	default:
		return false
	}
}

func validatePositiveFloat(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		return fmt.Errorf("value must be a positive number")
	}
	return nil
}

func validateContainerMemory(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	suffix := value[len(value)-1]
	if suffix >= '0' && suffix <= '9' {
		return validatePositiveInteger(value)
	}
	switch suffix {
	case 'b', 'B', 'k', 'K', 'm', 'M', 'g', 'G', 't', 'T':
	default:
		return fmt.Errorf("value must be a positive integer with optional b, k, m, g, or t suffix")
	}
	number := strings.TrimSpace(value[:len(value)-1])
	if number == "" {
		return fmt.Errorf("value must include a positive integer before the suffix")
	}
	return validatePositiveInteger(number)
}

func validateContainerUser(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.ContainsRune(value, 0) || strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("value must be a single user or UID[:group/GID] token")
	}
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return fmt.Errorf("value must be user or UID with an optional group/GID")
	}
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("user and group segments must not be empty")
		}
		if !validContainerIdentityToken(part) {
			return fmt.Errorf("user and group segments may contain only letters, numbers, underscore, dot, or hyphen")
		}
	}
	return nil
}

func validContainerIdentityToken(value string) bool {
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '-' {
			continue
		}
		return false
	}
	return value != ""
}

func validateContainerCapDrop(value string) error {
	for _, item := range splitContainerCapDropOptionConfig(value) {
		if !validContainerCapabilityToken(item) {
			return fmt.Errorf("capability %q is invalid; use all or capability names such as NET_ADMIN", item)
		}
	}
	return nil
}

func splitContainerCapDropOptionConfig(value string) []string {
	return splitDelimitedConfigOption(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
}

func validContainerCapabilityToken(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.EqualFold(value, "all") {
		return true
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' {
			continue
		}
		return false
	}
	return true
}

func validatePositiveInteger(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fmt.Errorf("value must be a positive integer")
	}
	return nil
}

func validateBoolString(value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return nil
	}
	if value != "true" && value != "false" {
		return fmt.Errorf("value must be true or false")
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

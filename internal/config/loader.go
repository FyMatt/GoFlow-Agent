package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/nativesandbox"

	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
	"gopkg.in/yaml.v3"
)

const (
	defaultMCPMaxRequestBytes  = 64 * 1024
	defaultMCPMaxResponseBytes = 2 * 1024 * 1024
	defaultSessionMaxHistory   = 6
	defaultMCPRestartLimit     = 3
	defaultMCPCooldown         = 10 * time.Second
	defaultMCPMaxConcurrent    = 1
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
	cfg.DataRoot = resolveDataRoot(cfg.RuntimeHome)
	cfg.WorkspaceRoot = filepath.Clean(workspaceRoot)
	cfg.WorkspaceDataRoot = resolveWorkspaceDataRoot(cfg.RuntimeHome, cfg.DataRoot, cfg.WorkspaceRoot)
	cfg.ConfigPath = filepath.Clean(path)
	if err := mergeModularConfig(&cfg); err != nil {
		return nil, err
	}
	sessionPersistConfigured := strings.TrimSpace(cfg.Session.PersistPath) != ""
	applyDefaults(&cfg)
	expandEnv(&cfg)
	if err := resolveSecretReferences(&cfg); err != nil {
		return nil, err
	}
	resolveRuntimePaths(&cfg, cfg.RuntimeHome, filepath.Dir(path))
	resolveWorkspacePaths(&cfg, sessionPersistConfigured)
	normalizeConfig(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func resolveSecretReferences(cfg *Config) error {
	resolved, err := resolveConfiguredSecretReference(cfg.DataRoot, cfg.LLM.APIKey)
	if err != nil {
		return fmt.Errorf("resolve default provider api_key: %w", err)
	}
	cfg.LLM.APIKey = resolved
	if strings.TrimSpace(cfg.Notifications.Email.Password) != "" {
		resolved, err := resolveConfiguredSecretReference(cfg.DataRoot, cfg.Notifications.Email.Password)
		if err != nil {
			return fmt.Errorf("resolve notifications.email.password: %w", err)
		}
		cfg.Notifications.Email.Password = resolved
	}
	for name, provider := range cfg.Providers {
		resolved, err := resolveConfiguredSecretReference(cfg.DataRoot, provider.APIKey)
		if err != nil {
			return fmt.Errorf("resolve provider %s api_key: %w", name, err)
		}
		provider.APIKey = resolved
		cfg.Providers[name] = provider
	}
	return nil
}

// resolveConfiguredSecretReference treats a missing secret file as an
// unconfigured Provider. The Web Studio must be able to start before a user
// enters the first API key; malformed references and other read failures still
// fail configuration loading.
func resolveConfiguredSecretReference(dataRoot, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, secretReferencePrefix) {
		return value, nil
	}
	path, err := SecretReferencePath(dataRoot, value)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read secret reference: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
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
	if sameCleanPath(cfg.RuntimeHome, cfg.DataRoot) {
		return nil
	}
	if err := mergeProviderConfigDir(cfg, filepath.Join(cfg.DataRoot, "resources", "providers")); err != nil {
		return err
	}
	if err := mergeAgentConfigDir(cfg, filepath.Join(cfg.DataRoot, "resources", "agents")); err != nil {
		return err
	}
	if err := mergeMCPConfigDir(cfg, filepath.Join(cfg.DataRoot, "resources", "mcp")); err != nil {
		return err
	}
	return applyResourceTombstones(cfg)
}

func applyResourceTombstones(cfg *Config) error {
	store := persistence.New(cfg.DataRoot)
	for name := range cfg.Agents {
		hidden, err := store.IsTombstoned("agents", name)
		if err != nil {
			return fmt.Errorf("load Agent tombstones: %w", err)
		}
		if hidden {
			delete(cfg.Agents, name)
		}
	}
	for name := range cfg.Providers {
		hidden, err := store.IsTombstoned("providers", name)
		if err != nil {
			return fmt.Errorf("load Provider tombstones: %w", err)
		}
		if hidden {
			delete(cfg.Providers, name)
		}
	}
	servers := cfg.MCP[:0]
	for _, server := range cfg.MCP {
		hidden, err := store.IsTombstoned("mcp", server.Name)
		if err != nil {
			return fmt.Errorf("load MCP tombstones: %w", err)
		}
		if !hidden {
			servers = append(servers, server)
		}
	}
	cfg.MCP = servers
	return nil
}

func resolveDataRoot(runtimeHome string) string {
	value := strings.TrimSpace(os.Getenv("GOFLOW_DATA_ROOT"))
	if value == "" {
		return filepath.Clean(runtimeHome)
	}
	value = os.ExpandEnv(value)
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(runtimeHome, value))
}

// ResolveDataRoot returns the configured persistent data directory.
func ResolveDataRoot(runtimeHome string) string {
	return resolveDataRoot(runtimeHome)
}

func sameCleanPath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func resolveWorkspaceDataRoot(runtimeHome, dataRoot, workspaceRoot string) string {
	if sameCleanPath(runtimeHome, dataRoot) {
		return filepath.Clean(workspaceRoot)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(workspaceRoot)))
	key := hex.EncodeToString(sum[:8])
	return filepath.Join(dataRoot, "workspaces", key)
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
		ApplyMCPServerDefaults(&cfg.MCP[i])
	}
}

// ApplyMCPServerDefaults fills runtime-safe defaults for a single MCP server.
// Resource APIs use this when validating one modular server snippet outside the
// full config loader.
func ApplyMCPServerDefaults(server *MCPServerRef) {
	if server == nil {
		return
	}
	server.TargetScope = strings.ToLower(strings.TrimSpace(server.TargetScope))
	if server.MaxRequestBytes <= 0 {
		server.MaxRequestBytes = defaultMCPMaxRequestBytes
	}
	if server.MaxResponseBytes <= 0 {
		server.MaxResponseBytes = defaultMCPMaxResponseBytes
	}
	if server.RestartLimit <= 0 {
		server.RestartLimit = defaultMCPRestartLimit
	}
	if server.Cooldown <= 0 {
		server.Cooldown = defaultMCPCooldown
	}
	if server.MaxConcurrentCalls <= 0 {
		server.MaxConcurrentCalls = defaultMCPMaxConcurrent
	}
	if strings.TrimSpace(server.WorkDir) == "" {
		server.WorkDir = "."
	}
	if strings.TrimSpace(server.Isolation) == "" {
		server.Isolation = "auto"
	}
	if resolved, err := ResolveAutomaticMCPIsolation(*server); err == nil {
		*server = resolved
	}
}

// ResolveAutomaticMCPIsolation turns the compact "auto" setting into the
// smallest execution boundary that can actually satisfy the server's scope.
// It deliberately refuses to fall back to the host for an unknown server when
// the native strong-sandbox executor is not fully enforced.
func ResolveAutomaticMCPIsolation(server MCPServerRef) (MCPServerRef, error) {
	return resolveAutomaticMCPIsolation(server, nativesandbox.Probe())
}

func resolveAutomaticMCPIsolation(server MCPServerRef, report nativesandbox.Report) (MCPServerRef, error) {
	if !strings.EqualFold(strings.TrimSpace(server.Isolation), "auto") {
		return server, nil
	}
	name := strings.ToLower(strings.TrimSpace(server.Name))
	if strings.EqualFold(strings.TrimSpace(server.TargetScope), "host") || trustedHostMCPServer(name) {
		server.Isolation = "process_group"
		return server, nil
	}
	if trustedLocalMCPServer(name) {
		server.Isolation = "process_group"
		return server, nil
	}
	if report.StrongReady {
		server.Isolation = "native_strong"
		server.NetworkDisabled = true
		server.IsolationOptions = nil
		return server, nil
	}
	return server, fmt.Errorf("automatic isolation for MCP server %s refused: %s", server.Name, report.RefusalReason)
}

func trustedHostMCPServer(name string) bool {
	switch name {
	case "office_tools", "host_tools", "ssh_tools":
		return true
	default:
		return false
	}
}

func trustedLocalMCPServer(name string) bool {
	switch name {
	case "file_tools", "command_tools", "skill_runner", "python_notes", "web_tools", "network_tools", "notification_tools":
		return true
	default:
		return false
	}
}

func applyLLMDefaults(cfg *LLMConfig) {
	if cfg == nil {
		return
	}
	if cfg.Provider == "" {
		cfg.Provider = "openai-compatible"
	}
	cfg.WireAPI = normalizeWireAPI(cfg.WireAPI)
	cfg.ReasoningEffort = NormalizeReasoningEffort(cfg.ReasoningEffort)
	cfg.ProviderMessageFields = normalizeProviderMessageFields(cfg.ProviderMessageFields)
	cfg.Pricing = normalizePricingConfig(cfg.Pricing)
}

// NormalizeReasoningEffort maps operator aliases onto the OpenAI-compatible
// wire values. mid/med become medium; operator high aliases remain high.
func NormalizeReasoningEffort(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "none", "minimal", "low", "medium":
		return strings.ToLower(strings.TrimSpace(value))
	case "mid", "med":
		return "medium"
	case "high", "xhigh", "max", "heavy":
		return "high"
	default:
		return "medium"
	}
}

func normalizeWireAPI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "chat", "chat_completions", "chat-completions":
		return "chat_completions"
	case "responses", "response":
		return "responses"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizePricingConfig(pricing PricingConfig) PricingConfig {
	pricing.Currency = strings.ToUpper(strings.TrimSpace(pricing.Currency))
	pricing.Source = strings.TrimSpace(pricing.Source)
	if pricing.Currency == "" && pricingConfigured(pricing) {
		pricing.Currency = "USD"
	}
	return pricing
}

func pricingConfigured(pricing PricingConfig) bool {
	return pricing.InputPerMillionTokens > 0 ||
		pricing.OutputPerMillionTokens > 0 ||
		pricing.CachedInputPerMillionTokens > 0 ||
		strings.TrimSpace(pricing.Currency) != "" ||
		strings.TrimSpace(pricing.Source) != ""
}

func expandEnv(cfg *Config) {
	expandLLMEnv(&cfg.LLM)
	cfg.HTTP.Token = os.ExpandEnv(cfg.HTTP.Token)
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
	for i := range cfg.ProviderMessageFields {
		cfg.ProviderMessageFields[i] = os.ExpandEnv(cfg.ProviderMessageFields[i])
	}
}

func normalizeProviderMessageFields(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
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
		cfg.MCP[i].RuntimeHome = runtimeHome
		cfg.MCP[i].WorkDir = resolveRuntimePath(runtimeHome, configDir, cfg.MCP[i].WorkDir)
		for j := range cfg.MCP[i].Args {
			cfg.MCP[i].Args[j] = resolveRuntimeArg(runtimeHome, cfg.MCP[i].Args[j])
		}
	}
}

func resolveWorkspacePaths(cfg *Config, sessionPersistConfigured bool) {
	sessionRoot := cfg.WorkspaceRoot
	if !sessionPersistConfigured && strings.TrimSpace(cfg.WorkspaceDataRoot) != "" {
		sessionRoot = cfg.WorkspaceDataRoot
	}
	cfg.Session.PersistPath = resolveRelativeTo(sessionRoot, cfg.Session.PersistPath)
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
		profile.Kind = strings.ToLower(strings.TrimSpace(profile.Kind))
		profile.TargetScope = strings.ToLower(strings.TrimSpace(profile.TargetScope))
		if profile.Kind == "" {
			profile.Kind = AgentKindAgent
		}
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
			if !pricingConfigured(profile.Pricing) {
				profile.Pricing = providerCfg.Pricing
			}
		}
		profile.Pricing = normalizePricingConfig(profile.Pricing)
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
	for name, profile := range agents {
		if profile.IsSubagent() {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

func validateNotifications(cfg *Config) error {
	email := cfg.Notifications.Email
	if !email.Enabled {
		return nil
	}
	if strings.TrimSpace(email.SMTPHost) == "" {
		return fmt.Errorf("notifications.email.smtp_host is required when email notification is enabled")
	}
	if email.SMTPPort < 0 || email.SMTPPort > 65535 {
		return fmt.Errorf("notifications.email.smtp_port must be between 1 and 65535")
	}
	from := strings.TrimSpace(email.From)
	if from == "" {
		from = strings.TrimSpace(email.Username)
	}
	if from == "" {
		return fmt.Errorf("notifications.email.from is required when email notification is enabled")
	}
	if len(email.To) == 0 {
		return fmt.Errorf("notifications.email.to requires at least one recipient")
	}
	switch strings.ToLower(strings.TrimSpace(email.TLS)) {
	case "", "starttls", "tls", "none":
	default:
		return fmt.Errorf("notifications.email.tls must be starttls, tls or none")
	}
	return nil
}

func validate(cfg *Config) error {
	if err := validateNotifications(cfg); err != nil {
		return err
	}
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
	if cfg.Agents[cfg.DefaultAgent].IsSubagent() {
		return fmt.Errorf("default_agent %q cannot reference a subagent", cfg.DefaultAgent)
	}
	if cfg.Skill.Directory == "" {
		return fmt.Errorf("skill.directory is required")
	}
	if cfg.Agent.MaxIterations <= 0 {
		return fmt.Errorf("agent.max_iterations must be greater than 0")
	}
	for name, provider := range cfg.Providers {
		if err := validatePricingConfig("provider "+name+".pricing", provider.Pricing); err != nil {
			return err
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
		switch profile.Kind {
		case AgentKindAgent, AgentKindSubagent:
		default:
			return fmt.Errorf("agent %s kind must be agent or subagent", name)
		}
		if profile.TargetScope != "" && profile.TargetScope != "host" {
			return fmt.Errorf("agent %s target_scope must be empty or host", name)
		}
		if strings.TrimSpace(profile.Provider) == "" {
			return fmt.Errorf("agent %s provider is required", name)
		}
		if _, ok := cfg.Providers[profile.Provider]; !ok {
			return fmt.Errorf("agent %s references unknown provider %q", name, profile.Provider)
		}
		if profile.MaxIterations <= 0 {
			return fmt.Errorf("agent %s max_iterations must be greater than 0", name)
		}
		if err := validatePricingConfig("agent "+name+".pricing", profile.Pricing); err != nil {
			return err
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

func validatePricingConfig(path string, pricing PricingConfig) error {
	if pricing.InputPerMillionTokens < 0 {
		return fmt.Errorf("%s.input_per_million_tokens must not be negative", path)
	}
	if pricing.OutputPerMillionTokens < 0 {
		return fmt.Errorf("%s.output_per_million_tokens must not be negative", path)
	}
	if pricing.CachedInputPerMillionTokens < 0 {
		return fmt.Errorf("%s.cached_input_per_million_tokens must not be negative", path)
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
	if strings.TrimSpace(server.Name) == "" {
		return fmt.Errorf("enabled mcp server name is required")
	}
	if scope := strings.ToLower(strings.TrimSpace(server.TargetScope)); scope != "" && scope != "host" {
		return fmt.Errorf("mcp server %s target_scope must be empty or host", server.Name)
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
	if server.MaxConcurrentCalls <= 0 {
		return fmt.Errorf("mcp server %s max_concurrent_calls must be greater than 0", server.Name)
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

// ValidateMCPServerPlatform checks isolation options and host-platform support
// without requiring the rest of a fully defaulted MCP server definition.
func ValidateMCPServerPlatform(server MCPServerRef) error {
	return validateMCPIsolation(server)
}

func validateMCPIsolation(server MCPServerRef) error {
	mode := strings.ToLower(strings.TrimSpace(server.Isolation))
	if mode == "" {
		mode = "none"
	}
	if mode == "container" {
		return fmt.Errorf("mcp server %s isolation %q has been removed; use isolation: auto or native_strong", server.Name, server.Isolation)
	}
	if strings.TrimSpace(server.IsolationProfile) != "" {
		return fmt.Errorf("mcp server %s isolation_profile has been removed; use isolation: auto or native_strong", server.Name)
	}
	if isUnimplementedWindowsAppContainerIsolation(mode) {
		return fmt.Errorf("mcp server %s isolation %q is managed internally; use isolation: auto or native_strong", server.Name, server.Isolation)
	}
	if err := validateMCPIsolationOptions(server.Name, mode, server.IsolationOptions); err != nil {
		return err
	}
	switch mode {
	case "none", "process_group", "auto":
		return nil
	case "native_strong":
		report := nativesandbox.Probe()
		if !report.StrongReady {
			return fmt.Errorf("mcp server %s isolation native_strong is unavailable: %s", server.Name, report.RefusalReason)
		}
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
	default:
		return fmt.Errorf("mcp server %s isolation %q is invalid; expected auto, native_strong, process_group, windows_job, windows_restricted_token, linux_cgroup, or linux_netns", server.Name, server.Isolation)
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
		return nil
	}
	switch mode {
	case "native_strong":
		if len(options) > 0 {
			return fmt.Errorf("mcp server %s isolation_options are managed internally with native_strong", serverName)
		}
		return nil
	case "linux_cgroup":
		return validateLinuxCgroupIsolationOptions(serverName, options)
	case "linux_netns":
		return validateLinuxNetworkNamespaceIsolationOptions(serverName, options)
	case "windows_restricted_token":
		if len(options) > 0 {
			return fmt.Errorf("mcp server %s isolation_options are not supported with windows_restricted_token", serverName)
		}
		return nil
	default:
		return fmt.Errorf("mcp server %s isolation_options are only supported with linux_cgroup or linux_netns", serverName)
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

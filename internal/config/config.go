package config

import "time"

// Config is the root runtime configuration.
type Config struct {
	Agent         AgentConfig             `yaml:"agent"`
	LLM           LLMConfig               `yaml:"llm"`
	MCP           []MCPServerRef          `yaml:"mcp_servers"`
	Skill         SkillConfig             `yaml:"skill"`
	Log           LogConfig               `yaml:"log"`
	Providers     map[string]LLMConfig    `yaml:"providers"`
	Agents        map[string]AgentProfile `yaml:"agents"`
	DefaultAgent  string                  `yaml:"default_agent"`
	Audit         AuditConfig             `yaml:"audit"`
	Verifier      VerifierConfig          `yaml:"verifier"`
	Session       SessionConfig           `yaml:"session"`
	RuntimeHome   string                  `yaml:"-"`
	WorkspaceRoot string                  `yaml:"-"`
}

// AgentConfig controls agent loop behaviour.
type AgentConfig struct {
	Name          string        `yaml:"name"`
	MaxIterations int           `yaml:"max_iterations"`
	Timeout       time.Duration `yaml:"timeout"`
}

// AgentProfile configures one named agent runtime.
type AgentProfile struct {
	Name             string       `yaml:"name"`
	Description      string       `yaml:"description"`
	SystemPrompt     string       `yaml:"system_prompt"`
	Provider         string       `yaml:"provider"`
	Model            string       `yaml:"model"`
	Temperature      float64      `yaml:"temperature"`
	MaxTokens        int          `yaml:"max_tokens"`
	MaxIterations    int          `yaml:"max_iterations"`
	AllowedToolKinds []ToolKind   `yaml:"allowed_tool_kinds"`
	AllowedTools     []string     `yaml:"allowed_tools"`
	ToolPolicy       ToolPolicy   `yaml:"tool_policy"`
	Mode             string       `yaml:"mode"`
	SkillOverride    *SkillConfig `yaml:"skill,omitempty"`
}

// LLMConfig configures the model provider.
type LLMConfig struct {
	Provider         string        `yaml:"provider"`
	BaseURL          string        `yaml:"base_url"`
	APIKey           string        `yaml:"api_key"`
	Model            string        `yaml:"model"`
	FallbackProvider string        `yaml:"fallback_provider"`
	Timeout          time.Duration `yaml:"timeout"`
	Temperature      float64       `yaml:"temperature"`
	MaxTokens        int           `yaml:"max_tokens"`
	RetryCount       int           `yaml:"retry_count"`
	RetryBackoff     time.Duration `yaml:"retry_backoff"`
}

// MCPServerRef declares a configured MCP server.
type MCPServerRef struct {
	Name                string            `yaml:"name"`
	Command             string            `yaml:"command"`
	Args                []string          `yaml:"args"`
	Enabled             bool              `yaml:"enabled"`
	Timeout             time.Duration     `yaml:"timeout"`
	WorkDir             string            `yaml:"workdir"`
	EnvAllowlist        []string          `yaml:"env_allowlist"`
	NetworkDisabled     bool              `yaml:"network_disabled"`
	Isolation           string            `yaml:"isolation"`
	IsolationOptions    map[string]string `yaml:"isolation_options"`
	RestartLimit        int               `yaml:"restart_limit"`
	Cooldown            time.Duration     `yaml:"cooldown"`
	AllowedCommandPaths []string          `yaml:"allowed_command_paths"`
	AllowedCommands     []string          `yaml:"allowed_commands"`
	MaxRequestBytes     int               `yaml:"max_request_bytes"`
	MaxResponseBytes    int               `yaml:"max_response_bytes"`
	WorkspaceRoot       string            `yaml:"-"`
}

// SkillConfig controls skill discovery and matching.
type SkillConfig struct {
	Directory      string `yaml:"directory"`
	MatchThreshold int    `yaml:"match_threshold"`
	HotReload      bool   `yaml:"hot_reload"`
}

// LogConfig controls runtime log formatting.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// AuditConfig controls audit trace behavior.
type AuditConfig struct {
	Enabled        bool `yaml:"enabled"`
	RedactContent  bool `yaml:"redact_content"`
	ShowTraceInCLI bool `yaml:"show_trace_in_cli"`
}

// VerifierConfig controls optional post-response verification passes.
type VerifierConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Agent     string   `yaml:"agent"`
	Modes     []string `yaml:"modes"`
	MaxTokens int      `yaml:"max_tokens"`
}

// SessionConfig controls session behavior.
type SessionConfig struct {
	MaxHistory  int    `yaml:"max_history"`
	PersistPath string `yaml:"persist_path"`
}

// ToolPolicy determines how risky tool calls are handled.
type ToolPolicy string

const (
	ToolPolicyAllow   ToolPolicy = "allow"
	ToolPolicyDeny    ToolPolicy = "deny"
	ToolPolicyConfirm ToolPolicy = "confirm"
)

func (p ToolPolicy) String() string {
	return string(p)
}

// ToolKind classifies tool risk level/capability.
type ToolKind string

const (
	ToolKindRead    ToolKind = "read"
	ToolKindWrite   ToolKind = "write"
	ToolKindExec    ToolKind = "exec"
	ToolKindNetwork ToolKind = "network"
	ToolKindUnknown ToolKind = "unknown"
)

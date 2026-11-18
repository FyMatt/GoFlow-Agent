package config

import "time"

// Config is the root runtime configuration.
type Config struct {
	Agent             AgentConfig             `yaml:"agent"`
	LLM               LLMConfig               `yaml:"llm"`
	MCP               []MCPServerRef          `yaml:"mcp_servers"`
	Skill             SkillConfig             `yaml:"skill"`
	Log               LogConfig               `yaml:"log"`
	Providers         map[string]LLMConfig    `yaml:"providers"`
	Agents            map[string]AgentProfile `yaml:"agents"`
	DefaultAgent      string                  `yaml:"default_agent"`
	Audit             AuditConfig             `yaml:"audit"`
	Verifier          VerifierConfig          `yaml:"verifier"`
	CostControl       CostControlConfig       `yaml:"cost_control"`
	ToolRiskPolicy    ToolRiskPolicyConfig    `yaml:"tool_risk_policy"`
	Session           SessionConfig           `yaml:"session"`
	Workspace         WorkspaceConfig         `yaml:"workspace"`
	HTTP              HTTPConfig              `yaml:"http"`
	Notifications     NotificationsConfig     `yaml:"notifications"`
	RuntimeHome       string                  `yaml:"-"`
	DataRoot          string                  `yaml:"-"`
	WorkspaceDataRoot string                  `yaml:"-"`
	WorkspaceRoot     string                  `yaml:"-"`
	ConfigPath        string                  `yaml:"-"`
}

// WorkspaceConfig controls data-egress behavior for the selected workspace.
type WorkspaceConfig struct {
	// LocalOnly blocks non-loopback model providers for this workspace.
	LocalOnly bool `yaml:"local_only"`
}

// HTTPConfig controls optional single-user access protection.
type HTTPConfig struct {
	Token string `yaml:"token"`
}

// AgentConfig controls agent loop behaviour.
type AgentConfig struct {
	Name          string        `yaml:"name"`
	MaxIterations int           `yaml:"max_iterations"`
	Timeout       time.Duration `yaml:"timeout"`
}

// AgentProfile configures one named agent runtime.
type AgentProfile struct {
	Kind             string        `yaml:"kind,omitempty"`
	TargetScope      string        `yaml:"target_scope,omitempty"`
	Name             string        `yaml:"name"`
	Description      string        `yaml:"description"`
	SystemPrompt     string        `yaml:"system_prompt"`
	Provider         string        `yaml:"provider"`
	Model            string        `yaml:"model"`
	Pricing          PricingConfig `yaml:"pricing,omitempty"`
	Temperature      float64       `yaml:"temperature"`
	MaxTokens        int           `yaml:"max_tokens"`
	MaxIterations    int           `yaml:"max_iterations"`
	AllowedToolKinds []ToolKind    `yaml:"allowed_tool_kinds"`
	AllowedTools     []string      `yaml:"allowed_tools"`
	ToolPolicy       ToolPolicy    `yaml:"tool_policy"`
	Mode             string        `yaml:"mode"`
	SkillOverride    *SkillConfig  `yaml:"skill,omitempty"`
}

const (
	AgentKindAgent    = "agent"
	AgentKindSubagent = "subagent"
)

// IsSubagent reports whether the profile is intended for delegated work.
func (p AgentProfile) IsSubagent() bool {
	return p.Kind == AgentKindSubagent
}

// LLMConfig configures the model provider.
type LLMConfig struct {
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	// WireAPI selects the OpenAI-compatible wire protocol. Empty values use
	// Chat Completions for backwards compatibility; "responses" uses the
	// OpenAI Responses API at <base_url>/responses.
	WireAPI string `yaml:"wire_api,omitempty"`
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
	// ReasoningEffort selects how hard a reasoning model should think.
	// Config may use mid; the OpenAI-compatible wire value is medium.
	// high, xhigh, and max are clamped down to medium.
	ReasoningEffort string `yaml:"reasoning_effort,omitempty"`
	// Thinking toggles provider reasoning mode. DeepSeek enables it by
	// default and bills reasoning tokens as output; "disabled" keeps
	// mechanical stages from paying for chain-of-thought.
	Thinking              string        `yaml:"thinking,omitempty"`
	Pricing               PricingConfig `yaml:"pricing,omitempty"`
	FallbackProvider      string        `yaml:"fallback_provider"`
	ProviderMessageFields []string      `yaml:"provider_message_fields"`
	Vision                bool          `yaml:"vision,omitempty"`
	Timeout               time.Duration `yaml:"timeout"`
	Temperature           float64       `yaml:"temperature"`
	MaxTokens             int           `yaml:"max_tokens"`
	RetryCount            int           `yaml:"retry_count"`
	RetryBackoff          time.Duration `yaml:"retry_backoff"`
}

// PricingConfig describes operator-maintained model pricing metadata.
// Values are denominated per one million tokens and are intentionally optional:
// GoFlow only estimates monetary cost when operators configure current prices.
type PricingConfig struct {
	Currency                    string  `yaml:"currency,omitempty"`
	InputPerMillionTokens       float64 `yaml:"input_per_million_tokens,omitempty"`
	OutputPerMillionTokens      float64 `yaml:"output_per_million_tokens,omitempty"`
	CachedInputPerMillionTokens float64 `yaml:"cached_input_per_million_tokens,omitempty"`
	Source                      string  `yaml:"source,omitempty"`
}

// MCPServerRef declares a configured MCP server.
type MCPServerRef struct {
	Name                string            `yaml:"name"`
	TargetScope         string            `yaml:"target_scope,omitempty"`
	Command             string            `yaml:"command"`
	Args                []string          `yaml:"args"`
	Enabled             bool              `yaml:"enabled"`
	Optional            bool              `yaml:"optional"`
	Timeout             time.Duration     `yaml:"timeout"`
	WorkDir             string            `yaml:"workdir"`
	EnvAllowlist        []string          `yaml:"env_allowlist"`
	NetworkDisabled     bool              `yaml:"network_disabled"`
	Isolation           string            `yaml:"isolation"`
	IsolationProfile    string            `yaml:"isolation_profile"`
	IsolationOptions    map[string]string `yaml:"isolation_options"`
	RestartLimit        int               `yaml:"restart_limit"`
	Cooldown            time.Duration     `yaml:"cooldown"`
	MaxConcurrentCalls  int               `yaml:"max_concurrent_calls"`
	AllowedCommandPaths []string          `yaml:"allowed_command_paths"`
	AllowedCommands     []string          `yaml:"allowed_commands"`
	MaxRequestBytes     int               `yaml:"max_request_bytes"`
	MaxResponseBytes    int               `yaml:"max_response_bytes"`
	WorkspaceRoot       string            `yaml:"-"`
	RuntimeHome         string            `yaml:"-"`
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
	Provider  string   `yaml:"provider"`
	Model     string   `yaml:"model"`
	Modes     []string `yaml:"modes"`
	MaxTokens int      `yaml:"max_tokens"`
}

// CostControlConfig controls low-cost auxiliary model routes.
type CostControlConfig struct {
	Router     AuxiliaryModelConfig `yaml:"router"`
	Summarizer AuxiliaryModelConfig `yaml:"summarizer"`
}

// AuxiliaryModelConfig describes an optional cheap model route for helper calls.
type AuxiliaryModelConfig struct {
	Enabled     bool    `yaml:"enabled"`
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

// ToolRiskPolicyConfig adds optional risk-aware approval controls on top of
// agent tool kind/name policy. It is disabled by default for compatibility.
type ToolRiskPolicyConfig struct {
	RequireApprovalForUnsandboxedRiskyTools bool `yaml:"require_approval_for_unsandboxed_risky_tools"`
	DisableRememberForUnsandboxedRiskyTools bool `yaml:"disable_remember_for_unsandboxed_risky_tools"`
	RejectUnsandboxedRiskyTools             bool `yaml:"reject_unsandboxed_risky_tools"`
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

// NotificationsConfig configures real delivery of run notifications.
type NotificationsConfig struct {
	Email EmailNotificationConfig `yaml:"email"`
}

// EmailNotificationConfig delivers run notifications over SMTP.
type EmailNotificationConfig struct {
	Enabled       bool     `yaml:"enabled"`
	SMTPHost      string   `yaml:"smtp_host"`
	SMTPPort      int      `yaml:"smtp_port"`
	Username      string   `yaml:"username"`
	Password      string   `yaml:"password"`
	From          string   `yaml:"from"`
	To            []string `yaml:"to"`
	TLS           string   `yaml:"tls"`
	SubjectPrefix string   `yaml:"subject_prefix"`
	Timeout       string   `yaml:"timeout"`
}

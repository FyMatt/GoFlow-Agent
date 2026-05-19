package schema

import "encoding/json"

// Message represents a normalized chat message exchanged with the LLM.
type Message struct {
	Role           string                     `json:"role"`
	Content        string                     `json:"content,omitempty"`
	Name           string                     `json:"name,omitempty"`
	ToolCallID     string                     `json:"tool_call_id,omitempty"`
	ToolCall       *ToolCall                  `json:"tool_call,omitempty"`
	ToolCalls      []ToolCall                 `json:"tool_calls,omitempty"`
	ProviderFields map[string]json.RawMessage `json:"provider_fields,omitempty"`
}

// Tool describes a callable tool exposed to the LLM.
type Tool struct {
	Name        string          `json:"name" yaml:"name"`
	Description string          `json:"description" yaml:"description"`
	InputSchema json.RawMessage `json:"input_schema" yaml:"input_schema"`
	Server      string          `json:"server,omitempty" yaml:"server,omitempty"`
	Kind        string          `json:"kind,omitempty" yaml:"kind,omitempty"`
}

// ToolRiskProfile describes the operator-facing risk and isolation context for
// one tool. It is informational; actual enforcement stays in policy, approval,
// and MCP isolation layers.
type ToolRiskProfile struct {
	QualifiedName              string                   `json:"qualified_name,omitempty"`
	Server                     string                   `json:"server,omitempty"`
	Kind                       string                   `json:"kind"`
	RiskLevel                  string                   `json:"risk_level"`
	Capabilities               []string                 `json:"capabilities,omitempty"`
	WorkspaceScopedInputs      bool                     `json:"workspace_scoped_inputs,omitempty"`
	WorkspaceScopeEnforced     bool                     `json:"workspace_scope_enforced"`
	Destructive                bool                     `json:"destructive"`
	RequiresApproval           bool                     `json:"requires_approval"`
	ExternalSandboxRecommended bool                     `json:"external_sandbox_recommended"`
	Isolation                  string                   `json:"isolation,omitempty"`
	IsolationLevel             string                   `json:"isolation_level,omitempty"`
	Sandboxed                  bool                     `json:"sandboxed"`
	NetworkDisabled            bool                     `json:"network_disabled,omitempty"`
	NetworkEnforced            bool                     `json:"network_enforced"`
	FilesystemSandboxed        bool                     `json:"filesystem_sandboxed"`
	PrivilegeSandboxed         bool                     `json:"privilege_sandboxed"`
	ResourceLimited            bool                     `json:"resource_limited"`
	SandboxFeatures            []string                 `json:"sandbox_features,omitempty"`
	MissingSandboxFeatures     []string                 `json:"missing_sandbox_features,omitempty"`
	WindowsIsolation           *WindowsIsolationProfile `json:"windows_isolation,omitempty"`
	EnvAllowlistSet            bool                     `json:"env_allowlist_set,omitempty"`
	EnvAllowlist               []string                 `json:"env_allowlist,omitempty"`
	SensitiveEnv               []string                 `json:"sensitive_env,omitempty"`
	CanAccessHostOutside       bool                     `json:"can_access_host_outside_workspace"`
	SecurityBoundary           string                   `json:"security_boundary,omitempty"`
	Warnings                   []string                 `json:"warnings,omitempty"`
	Recommendations            []string                 `json:"recommendations,omitempty"`
}

// WindowsIsolationProfile describes Windows-specific isolation primitives when
// an MCP server uses a Windows adapter.
type WindowsIsolationProfile struct {
	JobObject       bool `json:"job_object"`
	RestrictedToken bool `json:"restricted_token"`
	AppContainer    bool `json:"app_container"`
	LifecycleOnly   bool `json:"lifecycle_only"`
}

// ToolCall represents a model request to invoke a tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResult is the normalized result returned by a tool.
type ToolResult struct {
	CallID    string `json:"call_id"`
	ToolName  string `json:"tool_name"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
	Denied    bool   `json:"denied,omitempty"`
	Suspended bool   `json:"suspended,omitempty"`
}

// AuditEntry records one runtime action.
type AuditEntry struct {
	Type         string `json:"type"`
	AgentID      string `json:"agent_id,omitempty"`
	SkillName    string `json:"skill_name,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	Detail       string `json:"detail,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	Iteration    int    `json:"iteration,omitempty"`
	ErrorClass   string `json:"error_class,omitempty"`
	FallbackUsed bool   `json:"fallback_used,omitempty"`
	PromptTokens int    `json:"prompt_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	CachedTokens int    `json:"cached_tokens,omitempty"`
}

// StructuredSection is a machine-readable summary block.
type StructuredSection struct {
	Title   string   `json:"title"`
	Kind    string   `json:"kind"`
	Items   []string `json:"items,omitempty"`
	Summary string   `json:"summary,omitempty"`
}

// Finding captures a structured audit/fix finding.
type Finding struct {
	Severity    string   `json:"severity,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Files       []string `json:"files,omitempty"`
	Remediation string   `json:"remediation,omitempty"`
	Fixed       bool     `json:"fixed,omitempty"`
}

// Change captures a structured implementation change summary.
type Change struct {
	Summary string   `json:"summary,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// Verification captures a structured verification step or outcome.
type Verification struct {
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// StreamEventType identifies the type of incremental output event.
type StreamEventType string

const (
	StreamEventText           StreamEventType = "text"
	StreamEventStatus         StreamEventType = "status"
	StreamEventToolCall       StreamEventType = "tool_call"
	StreamEventToolResult     StreamEventType = "tool_result"
	StreamEventError          StreamEventType = "error"
	StreamEventDone           StreamEventType = "done"
	StreamEventFinalMessage   StreamEventType = "final_message"
	StreamEventApproval       StreamEventType = "approval"
	StreamEventTokenUsage     StreamEventType = "token_usage"
	StreamEventPromptBudget   StreamEventType = "prompt_budget"
	StreamEventTaskStage      StreamEventType = "task_stage"
	StreamEventWorkflowResult StreamEventType = "workflow_result"
)

// PromptBudget is an approximate pre-call token budget for one LLM request.
type PromptBudget struct {
	EstimatedPromptTokens            int                  `json:"estimated_prompt_tokens,omitempty"`
	SystemTokens                     int                  `json:"system_tokens,omitempty"`
	MessageTokens                    int                  `json:"message_tokens,omitempty"`
	ToolSchemaTokens                 int                  `json:"tool_schema_tokens,omitempty"`
	SkillTokens                      int                  `json:"skill_tokens,omitempty"`
	SkillSourceTokens                int                  `json:"skill_source_tokens,omitempty"`
	SkillInjectedTokens              int                  `json:"skill_injected_tokens,omitempty"`
	SkillOmittedTokens               int                  `json:"skill_omitted_tokens,omitempty"`
	MemoryTokens                     int                  `json:"memory_tokens,omitempty"`
	DynamicContextTokens             int                  `json:"dynamic_context_tokens,omitempty"`
	CacheablePrefixTokens            int                  `json:"cacheable_prefix_tokens,omitempty"`
	SystemBytes                      int                  `json:"system_bytes,omitempty"`
	MessageBytes                     int                  `json:"message_bytes,omitempty"`
	ToolSchemaBytes                  int                  `json:"tool_schema_bytes,omitempty"`
	SkillSourceBytes                 int                  `json:"skill_source_bytes,omitempty"`
	SkillInjectedBytes               int                  `json:"skill_injected_bytes,omitempty"`
	SkillOmittedBytes                int                  `json:"skill_omitted_bytes,omitempty"`
	MessageCount                     int                  `json:"message_count,omitempty"`
	ExposedToolCount                 int                  `json:"exposed_tool_count,omitempty"`
	TotalToolCount                   int                  `json:"total_tool_count,omitempty"`
	FilteredToolCount                int                  `json:"filtered_tool_count,omitempty"`
	ToolSchemaEstimatedSavedTokens   int                  `json:"tool_schema_estimated_saved_tokens,omitempty"`
	ToolSchemaDiagnosticCount        int                  `json:"tool_schema_diagnostic_count,omitempty"`
	ToolSchemaDiagnosticOmitted      int                  `json:"tool_schema_diagnostic_omitted,omitempty"`
	MemoryBlockCount                 int                  `json:"memory_block_count,omitempty"`
	MemoryOmittedCount               int                  `json:"memory_omitted_count,omitempty"`
	MemoryEstimatedSavedTokens       int                  `json:"memory_estimated_saved_tokens,omitempty"`
	ArtifactRefCount                 int                  `json:"artifact_ref_count,omitempty"`
	CompactedToolResultCount         int                  `json:"compacted_tool_result_count,omitempty"`
	ArtifactOmittedTokens            int                  `json:"artifact_omitted_tokens,omitempty"`
	HistoryPromptItems               int                  `json:"history_prompt_items,omitempty"`
	HistoryPromptRetainedItems       int                  `json:"history_prompt_retained_items,omitempty"`
	HistoryPromptDeduplicatedItems   int                  `json:"history_prompt_deduplicated_items,omitempty"`
	HistoryPromptCompactedOlderItems int                  `json:"history_prompt_compacted_older_items,omitempty"`
	HistoryToolItems                 int                  `json:"history_tool_items,omitempty"`
	HistoryToolRetainedItems         int                  `json:"history_tool_retained_items,omitempty"`
	HistoryToolDeduplicatedItems     int                  `json:"history_tool_deduplicated_items,omitempty"`
	HistoryToolCompactedOlderItems   int                  `json:"history_tool_compacted_older_items,omitempty"`
	HistoryEstimatedSavedTokens      int                  `json:"history_estimated_saved_tokens,omitempty"`
	AgentID                          string               `json:"agent_id,omitempty"`
	Mode                             string               `json:"mode,omitempty"`
	WorkflowName                     string               `json:"workflow_name,omitempty"`
	TaskStage                        string               `json:"task_stage,omitempty"`
	SkillName                        string               `json:"skill_name,omitempty"`
	SkillInstructionMode             string               `json:"skill_instruction_mode,omitempty"`
	ToolSchemaSelection              string               `json:"tool_schema_selection,omitempty"`
	SystemHash                       string               `json:"system_hash,omitempty"`
	ToolSchemaHash                   string               `json:"tool_schema_hash,omitempty"`
	SkillHash                        string               `json:"skill_hash,omitempty"`
	SkillSourceHash                  string               `json:"skill_source_hash,omitempty"`
	PromptPrefixHash                 string               `json:"prompt_prefix_hash,omitempty"`
	MemoryBlocks                     []PromptContextBlock `json:"memory_blocks,omitempty"`
	OmittedContext                   []string             `json:"omitted_context,omitempty"`
	ArtifactRefs                     []string             `json:"artifact_refs,omitempty"`
	InjectedToolSchemas              []PromptToolSchema   `json:"injected_tool_schemas,omitempty"`
	FilteredToolSchemas              []PromptToolSchema   `json:"filtered_tool_schemas,omitempty"`
}

// PromptContextBlock identifies a compact context block injected into a model
// request without copying the full source content into diagnostics.
type PromptContextBlock struct {
	Kind                 string `json:"kind,omitempty"`
	Title                string `json:"title,omitempty"`
	Ref                  string `json:"ref,omitempty"`
	Tokens               int    `json:"tokens,omitempty"`
	Score                int    `json:"score,omitempty"`
	Hash                 string `json:"hash,omitempty"`
	Language             string `json:"language,omitempty"`
	Size                 int64  `json:"size,omitempty"`
	MTime                string `json:"mtime,omitempty"`
	ContentMode          string `json:"content_mode,omitempty"`
	EstimatedSavedTokens int    `json:"estimated_saved_tokens,omitempty"`
}

// PromptToolSchema identifies one tool schema that was either injected into or
// omitted from a provider request.
type PromptToolSchema struct {
	Name          string `json:"name,omitempty"`
	QualifiedName string `json:"qualified_name,omitempty"`
	Server        string `json:"server,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Tokens        int    `json:"tokens,omitempty"`
	Bytes         int    `json:"bytes,omitempty"`
	SchemaHash    string `json:"schema_hash,omitempty"`
}

// StreamEvent represents incremental agent or provider output.
type StreamEvent struct {
	Type             StreamEventType  `json:"type"`
	RunID            string           `json:"run_id,omitempty"`
	Content          string           `json:"content,omitempty"`
	ToolName         string           `json:"tool_name,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ArgumentsSummary string           `json:"arguments_summary,omitempty"`
	AgentID          string           `json:"agent_id,omitempty"`
	Mode             string           `json:"mode,omitempty"`
	IsError          bool             `json:"is_error,omitempty"`
	NeedsAction      bool             `json:"needs_action,omitempty"`
	Suspended        bool             `json:"suspended,omitempty"`
	TaskStage        string           `json:"task_stage,omitempty"`
	PromptTokens     int              `json:"prompt_tokens,omitempty"`
	OutputTokens     int              `json:"output_tokens,omitempty"`
	CachedTokens     int              `json:"cached_tokens,omitempty"`
	WorkflowName     string           `json:"workflow_name,omitempty"`
	WorkflowStatus   string           `json:"workflow_status,omitempty"`
	NextStage        string           `json:"next_stage,omitempty"`
	PendingApproval  bool             `json:"pending_approval,omitempty"`
	WorkflowResult   *WorkflowResult  `json:"workflow_result,omitempty"`
	PromptBudget     *PromptBudget    `json:"prompt_budget,omitempty"`
	Risk             *ToolRiskProfile `json:"risk,omitempty"`
}

// ChatRequest is the provider-neutral chat completion request.
type ChatRequest struct {
	Model       string    `json:"model"`
	System      string    `json:"system,omitempty"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// TokenUsage captures token accounting when provided by a backend.
type TokenUsage struct {
	PromptTokens int `json:"prompt_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// TokenUsageSample captures one reported model usage sample with runtime labels.
type TokenUsageSample struct {
	AgentID      string `json:"agent_id,omitempty"`
	Mode         string `json:"mode,omitempty"`
	WorkflowName string `json:"workflow_name,omitempty"`
	TaskStage    string `json:"task_stage,omitempty"`
	PromptTokens int    `json:"prompt_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	CachedTokens int    `json:"cached_tokens,omitempty"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
}

// ChatResponse is the provider-neutral chat completion response.
type ChatResponse struct {
	Message    Message    `json:"message"`
	StopReason string     `json:"stop_reason,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Usage      TokenUsage `json:"usage,omitempty"`
}

// Skill contains a parsed SKILL.md definition.
type Skill struct {
	Name             string            `yaml:"name" json:"name"`
	Description      string            `yaml:"description" json:"description"`
	Version          string            `yaml:"version" json:"version"`
	Author           string            `yaml:"author" json:"author"`
	Format           string            `yaml:"format" json:"format,omitempty"`
	Tools            []SkillTool       `yaml:"tools" json:"tools"`
	Params           []SkillParam      `yaml:"params" json:"params"`
	Scripts          []SkillScript     `yaml:"scripts" json:"scripts,omitempty"`
	Activation       Activation        `yaml:"activation" json:"activation"`
	Instructions     string            `json:"instructions"`
	Path             string            `json:"path"`
	Resources        []SkillResource   `json:"resources,omitempty"`
	Mode             string            `yaml:"mode" json:"mode"`
	PreferredAgent   string            `yaml:"preferred_agent" json:"preferred_agent"`
	AllowedToolKinds []string          `yaml:"allowed_tool_kinds" json:"allowed_tool_kinds"`
	OutputKind       string            `yaml:"output_kind" json:"output_kind"`
	Priority         int               `yaml:"priority" json:"priority"`
	MaxIterations    int               `yaml:"max_iterations" json:"max_iterations"`
	NextSkills       []string          `yaml:"next_skills" json:"next_skills"`
	Metadata         map[string]string `yaml:"metadata" json:"metadata"`
}

// SkillResource describes a bundled file inside a skill directory.
type SkillResource struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// SkillScript declares a deterministic helper script bundled with a skill.
// Runtime execution must go through skill_runner/run_script as a normal exec
// tool, preserving agent permissions, approval, audit, and MCP isolation.
type SkillScript struct {
	Name           string            `yaml:"name" json:"name"`
	Description    string            `yaml:"description,omitempty" json:"description,omitempty"`
	Path           string            `yaml:"path" json:"path"`
	Runtime        string            `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	ArgsSchema     map[string]any    `yaml:"args_schema,omitempty" json:"args_schema,omitempty"`
	Output         string            `yaml:"output,omitempty" json:"output,omitempty"`
	Timeout        string            `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Isolation      string            `yaml:"isolation,omitempty" json:"isolation,omitempty"`
	WorkspaceMount string            `yaml:"workspace_mount,omitempty" json:"workspace_mount,omitempty"`
	Network        string            `yaml:"network,omitempty" json:"network,omitempty"`
	Approval       string            `yaml:"approval,omitempty" json:"approval,omitempty"`
	Metadata       map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// SkillMatchDiagnostic explains why a skill was selected.
type SkillMatchDiagnostic struct {
	SkillName      string   `json:"skill_name,omitempty"`
	Score          int      `json:"score,omitempty"`
	KeywordHits    []string `json:"keyword_hits,omitempty"`
	NameHit        bool     `json:"name_hit,omitempty"`
	DescriptionHit bool     `json:"description_hit,omitempty"`
	Reason         string   `json:"reason,omitempty"`
}

// SkillTool declares a tool dependency for a skill.
type SkillTool struct {
	Name     string `yaml:"name" json:"name"`
	Required bool   `yaml:"required" json:"required"`
}

// SkillParam declares a runtime parameter for a skill.
type SkillParam struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	Description string `yaml:"description" json:"description"`
	Required    bool   `yaml:"required" json:"required"`
}

// Activation declares matching metadata for a skill.
type Activation struct {
	Keywords             []string `yaml:"keywords" json:"keywords"`
	EmbeddingDescription string   `yaml:"embedding_description" json:"embedding_description"`
}

// WorkflowResult captures a staged workflow response.
type WorkflowResult struct {
	RunID                    string                `json:"run_id,omitempty"`
	Name                     string                `json:"name"`
	Status                   string                `json:"status"`
	PendingApproval          bool                  `json:"pending_approval,omitempty"`
	PendingInput             bool                  `json:"pending_input,omitempty"`
	PendingFields            []WorkflowInputField  `json:"pending_input_fields,omitempty"`
	PendingSubWorkflow       bool                  `json:"pending_sub_workflow,omitempty"`
	PendingSubWorkflowName   string                `json:"pending_sub_workflow_name,omitempty"`
	PendingSubWorkflowRunID  string                `json:"pending_sub_workflow_run_id,omitempty"`
	PendingSubWorkflowStatus string                `json:"pending_sub_workflow_status,omitempty"`
	ApprovalPrompt           string                `json:"approval_prompt,omitempty"`
	CompletedStages          []WorkflowStageResult `json:"completed_stages,omitempty"`
	NextStage                string                `json:"next_stage,omitempty"`
	FinalSummary             string                `json:"final_summary,omitempty"`
}

// WorkflowInputField describes a manual workflow input form field.
type WorkflowInputField struct {
	Name        string   `json:"name"`
	Label       string   `json:"label,omitempty"`
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Group       string   `json:"group,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Default     string   `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
	Rows        int      `json:"rows,omitempty"`
	Min         string   `json:"min,omitempty"`
	Max         string   `json:"max,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Multiple    bool     `json:"multiple,omitempty"`
	Advanced    bool     `json:"advanced,omitempty"`
}

// WorkflowStageResult captures one stage in a workflow response.
type WorkflowStageResult struct {
	Stage        string            `json:"stage"`
	AgentID      string            `json:"agent_id"`
	NodeType     string            `json:"node_type,omitempty"`
	Skill        string            `json:"skill,omitempty"`
	Tool         string            `json:"tool,omitempty"`
	Status       string            `json:"status,omitempty"`
	Attempts     int               `json:"attempts,omitempty"`
	Inputs       map[string]string `json:"inputs,omitempty"`
	InputValues  map[string]any    `json:"input_values,omitempty"`
	Outputs      map[string]string `json:"outputs,omitempty"`
	OutputValues map[string]any    `json:"output_values,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Result       AgentResult       `json:"result"`
}

// AgentResult is the final answer returned by the agent.
type AgentResult struct {
	Output          string              `json:"output"`
	MatchedSkill    *Skill              `json:"matched_skill,omitempty"`
	ToolResults     []ToolResult        `json:"tool_results,omitempty"`
	AgentID         string              `json:"agent_id,omitempty"`
	Mode            string              `json:"mode,omitempty"`
	Model           string              `json:"model,omitempty"`
	Structured      []StructuredSection `json:"structured,omitempty"`
	AuditTrail      []AuditEntry        `json:"audit_trail,omitempty"`
	Findings        []Finding           `json:"findings,omitempty"`
	Changes         []Change            `json:"changes,omitempty"`
	Verification    []Verification      `json:"verification,omitempty"`
	ResponseMessage Message             `json:"-"`
}

package schema

import "encoding/json"

// Message represents a normalized chat message exchanged with the LLM.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCall   *ToolCall  `json:"tool_call,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// Tool describes a callable tool exposed to the LLM.
type Tool struct {
	Name        string          `json:"name" yaml:"name"`
	Description string          `json:"description" yaml:"description"`
	InputSchema json.RawMessage `json:"input_schema" yaml:"input_schema"`
	Server      string          `json:"server,omitempty" yaml:"server,omitempty"`
	Kind        string          `json:"kind,omitempty" yaml:"kind,omitempty"`
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
	StreamEventTaskStage      StreamEventType = "task_stage"
	StreamEventWorkflowResult StreamEventType = "workflow_result"
)

// StreamEvent represents incremental agent or provider output.
type StreamEvent struct {
	Type             StreamEventType `json:"type"`
	Content          string          `json:"content,omitempty"`
	ToolName         string          `json:"tool_name,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ArgumentsSummary string          `json:"arguments_summary,omitempty"`
	AgentID          string          `json:"agent_id,omitempty"`
	Mode             string          `json:"mode,omitempty"`
	IsError          bool            `json:"is_error,omitempty"`
	NeedsAction      bool            `json:"needs_action,omitempty"`
	Suspended        bool            `json:"suspended,omitempty"`
	TaskStage        string          `json:"task_stage,omitempty"`
	PromptTokens     int             `json:"prompt_tokens,omitempty"`
	OutputTokens     int             `json:"output_tokens,omitempty"`
	CachedTokens     int             `json:"cached_tokens,omitempty"`
	WorkflowName     string          `json:"workflow_name,omitempty"`
	WorkflowStatus   string          `json:"workflow_status,omitempty"`
	NextStage        string          `json:"next_stage,omitempty"`
	PendingApproval  bool            `json:"pending_approval,omitempty"`
	WorkflowResult   *WorkflowResult `json:"workflow_result,omitempty"`
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
	Tools            []SkillTool       `yaml:"tools" json:"tools"`
	Params           []SkillParam      `yaml:"params" json:"params"`
	Activation       Activation        `yaml:"activation" json:"activation"`
	Instructions     string            `json:"instructions"`
	Path             string            `json:"path"`
	Mode             string            `yaml:"mode" json:"mode"`
	PreferredAgent   string            `yaml:"preferred_agent" json:"preferred_agent"`
	AllowedToolKinds []string          `yaml:"allowed_tool_kinds" json:"allowed_tool_kinds"`
	OutputKind       string            `yaml:"output_kind" json:"output_kind"`
	Priority         int               `yaml:"priority" json:"priority"`
	MaxIterations    int               `yaml:"max_iterations" json:"max_iterations"`
	NextSkills       []string          `yaml:"next_skills" json:"next_skills"`
	Metadata         map[string]string `yaml:"metadata" json:"metadata"`
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
	Name            string                `json:"name"`
	Status          string                `json:"status"`
	PendingApproval bool                  `json:"pending_approval,omitempty"`
	ApprovalPrompt  string                `json:"approval_prompt,omitempty"`
	CompletedStages []WorkflowStageResult `json:"completed_stages,omitempty"`
	NextStage       string                `json:"next_stage,omitempty"`
	FinalSummary    string                `json:"final_summary,omitempty"`
}

// WorkflowStageResult captures one stage in a workflow response.
type WorkflowStageResult struct {
	Stage   string      `json:"stage"`
	AgentID string      `json:"agent_id"`
	Result  AgentResult `json:"result"`
}

// AgentResult is the final answer returned by the agent.
type AgentResult struct {
	Output       string              `json:"output"`
	MatchedSkill *Skill              `json:"matched_skill,omitempty"`
	ToolResults  []ToolResult        `json:"tool_results,omitempty"`
	AgentID      string              `json:"agent_id,omitempty"`
	Mode         string              `json:"mode,omitempty"`
	Structured   []StructuredSection `json:"structured,omitempty"`
	AuditTrail   []AuditEntry        `json:"audit_trail,omitempty"`
	Findings     []Finding           `json:"findings,omitempty"`
	Changes      []Change            `json:"changes,omitempty"`
	Verification []Verification      `json:"verification,omitempty"`
}

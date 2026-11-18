package memory

import (
	"fmt"
	"strings"
)

// ProjectMemory is the durable workspace-level project profile injected into
// prompts as a concise summary.
type ProjectMemory struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Summary   string `json:"summary"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// TaskSummary captures reusable knowledge from one completed task.
type TaskSummary struct {
	ID              string   `json:"id"`
	CreatedAt       string   `json:"created_at,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
	AgentID         string   `json:"agent_id,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	UserGoal        string   `json:"user_goal,omitempty"`
	KeyDecisions    []string `json:"key_decisions,omitempty"`
	ModifiedFiles   []string `json:"modified_files,omitempty"`
	TestResults     []string `json:"test_results,omitempty"`
	FailureReasons  []string `json:"failure_reasons,omitempty"`
	NextTodos       []string `json:"next_todos,omitempty"`
	ReusableLessons []string `json:"reusable_lessons,omitempty"`
}

// SearchSummary returns a compact human-readable task summary.
func (t TaskSummary) SearchSummary() string {
	parts := make([]string, 0, 6)
	if strings.TrimSpace(t.UserGoal) != "" {
		parts = append(parts, "Goal: "+strings.TrimSpace(t.UserGoal))
	}
	if len(t.KeyDecisions) > 0 {
		parts = append(parts, "Decisions: "+strings.Join(t.KeyDecisions, "; "))
	}
	if len(t.ModifiedFiles) > 0 {
		parts = append(parts, "Files: "+strings.Join(t.ModifiedFiles, ", "))
	}
	if len(t.TestResults) > 0 {
		parts = append(parts, "Tests: "+strings.Join(t.TestResults, "; "))
	}
	if len(t.FailureReasons) > 0 {
		parts = append(parts, "Failures: "+strings.Join(t.FailureReasons, "; "))
	}
	if len(t.NextTodos) > 0 {
		parts = append(parts, "Next: "+strings.Join(t.NextTodos, "; "))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ErrorKnowledgeBase stores recurring errors and fixes discovered across tasks.
type ErrorKnowledgeBase struct {
	UpdatedAt string        `json:"updated_at,omitempty"`
	Errors    []ErrorMemory `json:"errors,omitempty"`
}

// SolutionKnowledgeBase stores reusable decisions and correct solutions so
// future runs can avoid asking the user to repeat the same choice.
type SolutionKnowledgeBase struct {
	UpdatedAt string           `json:"updated_at,omitempty"`
	Solutions []SolutionMemory `json:"solutions,omitempty"`
}

// SolutionMemory captures one reusable problem-to-solution record.
type SolutionMemory struct {
	ID                  string   `json:"id"`
	CreatedAt           string   `json:"created_at,omitempty"`
	UpdatedAt           string   `json:"updated_at,omitempty"`
	LastUsedAt          string   `json:"last_used_at,omitempty"`
	ProblemSignature    string   `json:"problem_signature"`
	Problem             string   `json:"problem,omitempty"`
	Decision            string   `json:"decision,omitempty"`
	Solution            string   `json:"solution,omitempty"`
	Applicability       []string `json:"applicability,omitempty"`
	InvalidWhen         []string `json:"invalid_when,omitempty"`
	RelatedFiles        []string `json:"related_files,omitempty"`
	VerificationCommand string   `json:"verification_command,omitempty"`
	Confidence          string   `json:"confidence,omitempty"`
	Resolved            bool     `json:"resolved,omitempty"`
	Retired             bool     `json:"retired,omitempty"`
	RetiredAt           string   `json:"retired_at,omitempty"`
	RetiredReason       string   `json:"retired_reason,omitempty"`
	SupersededBy        string   `json:"superseded_by,omitempty"`
	UseCount            int      `json:"use_count,omitempty"`
}

// SearchSummary returns a compact human-readable solution summary.
func (s SolutionMemory) SearchSummary() string {
	parts := make([]string, 0, 8)
	if strings.TrimSpace(s.ProblemSignature) != "" {
		parts = append(parts, "Problem signature: "+strings.TrimSpace(s.ProblemSignature))
	}
	if strings.TrimSpace(s.Problem) != "" {
		parts = append(parts, "Problem: "+strings.TrimSpace(s.Problem))
	}
	if strings.TrimSpace(s.Decision) != "" {
		parts = append(parts, "Decision: "+strings.TrimSpace(s.Decision))
	}
	if strings.TrimSpace(s.Solution) != "" {
		parts = append(parts, "Solution: "+strings.TrimSpace(s.Solution))
	}
	if len(s.Applicability) > 0 {
		parts = append(parts, "Use when: "+strings.Join(s.Applicability, "; "))
	}
	if len(s.InvalidWhen) > 0 {
		parts = append(parts, "Invalid when: "+strings.Join(s.InvalidWhen, "; "))
	}
	if len(s.RelatedFiles) > 0 {
		parts = append(parts, "Files: "+strings.Join(s.RelatedFiles, ", "))
	}
	if strings.TrimSpace(s.VerificationCommand) != "" {
		parts = append(parts, "Verify: "+strings.TrimSpace(s.VerificationCommand))
	}
	if strings.TrimSpace(s.Confidence) != "" {
		parts = append(parts, "Confidence: "+strings.TrimSpace(s.Confidence))
	}
	if s.Retired {
		parts = append(parts, "Retired: true")
	}
	if strings.TrimSpace(s.RetiredReason) != "" {
		parts = append(parts, "Retired reason: "+strings.TrimSpace(s.RetiredReason))
	}
	if strings.TrimSpace(s.SupersededBy) != "" {
		parts = append(parts, "Superseded by: "+strings.TrimSpace(s.SupersededBy))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ErrorMemory captures one reusable troubleshooting record.
type ErrorMemory struct {
	ID                  string   `json:"id"`
	CreatedAt           string   `json:"created_at,omitempty"`
	UpdatedAt           string   `json:"updated_at,omitempty"`
	Error               string   `json:"error"`
	RootCause           string   `json:"root_cause,omitempty"`
	Fix                 string   `json:"fix,omitempty"`
	RelatedFiles        []string `json:"related_files,omitempty"`
	VerificationCommand string   `json:"verification_command,omitempty"`
	Resolved            bool     `json:"resolved,omitempty"`
}

// SearchSummary returns a compact human-readable error knowledge summary.
func (e ErrorMemory) SearchSummary() string {
	parts := make([]string, 0, 5)
	if strings.TrimSpace(e.Error) != "" {
		parts = append(parts, "Error: "+strings.TrimSpace(e.Error))
	}
	if strings.TrimSpace(e.RootCause) != "" {
		parts = append(parts, "Root cause: "+strings.TrimSpace(e.RootCause))
	}
	if strings.TrimSpace(e.Fix) != "" {
		parts = append(parts, "Fix: "+strings.TrimSpace(e.Fix))
	}
	if len(e.RelatedFiles) > 0 {
		parts = append(parts, "Files: "+strings.Join(e.RelatedFiles, ", "))
	}
	if strings.TrimSpace(e.VerificationCommand) != "" {
		parts = append(parts, "Verify: "+strings.TrimSpace(e.VerificationCommand))
	}
	if e.Resolved {
		parts = append(parts, "Resolved: true")
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// FileIndex is a workspace-wide summary index used to avoid repeatedly loading
// unchanged file contents into model prompts.
type FileIndex struct {
	GeneratedAt  string        `json:"generated_at,omitempty"`
	TotalFiles   int           `json:"total_files,omitempty"`
	IndexedBytes int64         `json:"indexed_bytes,omitempty"`
	Files        []FileSummary `json:"files,omitempty"`
}

// FileSummary captures stable file metadata and a lightweight content summary.
type FileSummary struct {
	Path           string   `json:"path"`
	Size           int64    `json:"size"`
	MTime          string   `json:"mtime,omitempty"`
	Hash           string   `json:"hash,omitempty"`
	Language       string   `json:"language,omitempty"`
	Symbols        []string `json:"symbols,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	LastReadAt     string   `json:"last_read_at,omitempty"`
	LastUsedByTask string   `json:"last_used_by_task,omitempty"`
}

// SearchResponse is returned by keyword memory retrieval.
type SearchResponse struct {
	Query    string         `json:"query"`
	Total    int            `json:"total"`
	Returned int            `json:"returned"`
	Results  []SearchResult `json:"results,omitempty"`
}

// SearchResult describes one matched memory item.
type SearchResult struct {
	Kind     string            `json:"kind"`
	Title    string            `json:"title,omitempty"`
	Path     string            `json:"path,omitempty"`
	Summary  string            `json:"summary,omitempty"`
	Score    int               `json:"score,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// PromptContext is the concise memory payload selected for one model request.
type PromptContext struct {
	Project              ProjectMemory `json:"project,omitempty"`
	Blocks               []PromptBlock `json:"blocks,omitempty"`
	Omitted              []string      `json:"omitted,omitempty"`
	EstimatedSavedTokens int           `json:"estimated_saved_tokens,omitempty"`
}

// PromptBlock is one injected memory summary and ref.
type PromptBlock struct {
	Kind                 string `json:"kind"`
	Title                string `json:"title,omitempty"`
	Ref                  string `json:"ref,omitempty"`
	Summary              string `json:"summary,omitempty"`
	Score                int    `json:"score,omitempty"`
	Hash                 string `json:"hash,omitempty"`
	Language             string `json:"language,omitempty"`
	Size                 int64  `json:"size,omitempty"`
	MTime                string `json:"mtime,omitempty"`
	ContentMode          string `json:"content_mode,omitempty"`
	EstimatedSavedTokens int    `json:"estimated_saved_tokens,omitempty"`
}

// ContextSummary is the durable compact view of the current working context.
// It lets prompts and Web Studio reuse the important state without loading the
// full session history by default.
type ContextSummary struct {
	ID                   string            `json:"id,omitempty"`
	CreatedAt            string            `json:"created_at,omitempty"`
	UpdatedAt            string            `json:"updated_at,omitempty"`
	Summary              string            `json:"summary,omitempty"`
	Reason               string            `json:"reason,omitempty"`
	Auto                 bool              `json:"auto,omitempty"`
	ActiveAgent          string            `json:"active_agent,omitempty"`
	Mode                 string            `json:"mode,omitempty"`
	Workflow             string            `json:"workflow,omitempty"`
	TaskStage            string            `json:"task_stage,omitempty"`
	RecentGoals          []string          `json:"recent_goals,omitempty"`
	Decisions            []string          `json:"decisions,omitempty"`
	PendingActions       []string          `json:"pending_actions,omitempty"`
	RelevantFiles        []string          `json:"relevant_files,omitempty"`
	ArtifactRefs         []string          `json:"artifact_refs,omitempty"`
	PromptBudget         ContextPromptCost `json:"prompt_budget,omitempty"`
	EstimatedSavedTokens int               `json:"estimated_saved_tokens,omitempty"`
	SourceCounts         map[string]int    `json:"source_counts,omitempty"`
}

// SearchSummary returns a compact human-readable context summary.
func (c ContextSummary) SearchSummary() string {
	parts := make([]string, 0, 8)
	if strings.TrimSpace(c.Summary) != "" {
		parts = append(parts, strings.TrimSpace(c.Summary))
	}
	if len(c.RecentGoals) > 0 {
		parts = append(parts, "Goals: "+strings.Join(c.RecentGoals, "; "))
	}
	if len(c.Decisions) > 0 {
		parts = append(parts, "Decisions: "+strings.Join(c.Decisions, "; "))
	}
	if len(c.PendingActions) > 0 {
		parts = append(parts, "Pending: "+strings.Join(c.PendingActions, "; "))
	}
	if len(c.RelevantFiles) > 0 {
		parts = append(parts, "Files: "+strings.Join(c.RelevantFiles, ", "))
	}
	if len(c.ArtifactRefs) > 0 {
		parts = append(parts, "Artifacts: "+strings.Join(c.ArtifactRefs, ", "))
	}
	if strings.TrimSpace(c.Workflow) != "" {
		parts = append(parts, "Workflow: "+strings.TrimSpace(c.Workflow))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ContextPromptCost captures the latest prompt-budget fields relevant to
// compaction and token-savings diagnostics.
type ContextPromptCost struct {
	EstimatedPromptTokens       int    `json:"estimated_prompt_tokens,omitempty"`
	MemoryBlocks                int    `json:"memory_blocks,omitempty"`
	MemoryEstimatedSavedTokens  int    `json:"memory_estimated_saved_tokens,omitempty"`
	ArtifactRefs                int    `json:"artifact_refs,omitempty"`
	ArtifactOmittedTokens       int    `json:"artifact_omitted_tokens,omitempty"`
	HistoryEstimatedSavedTokens int    `json:"history_estimated_saved_tokens,omitempty"`
	CacheablePrefixTokens       int    `json:"cacheable_prefix_tokens,omitempty"`
	OmittedContextCount         int    `json:"omitted_context_count,omitempty"`
	PromptPrefixHash            string `json:"prompt_prefix_hash,omitempty"`
}

// Dashboard is a lightweight aggregate view for Web Studio.
type Dashboard struct {
	Project   ProjectMemory         `json:"project"`
	Tasks     []TaskSummary         `json:"tasks,omitempty"`
	Errors    ErrorKnowledgeBase    `json:"errors"`
	Solutions SolutionKnowledgeBase `json:"solutions"`
	FileIndex FileIndex             `json:"file_index"`
	Context   ContextSummary        `json:"context,omitempty"`
}

func pluralizeMemoryCount(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

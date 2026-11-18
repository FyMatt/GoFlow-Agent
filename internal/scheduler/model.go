package scheduler

import "time"

const SchemaVersion = 1

const (
	TriggerOnce = "once"
	TriggerCron = "cron"

	TaskAgent    = "agent"
	TaskWorkflow = "workflow"

	JobPending          = "pending"
	JobClaimed          = "claimed"
	JobRunning          = "running"
	JobAwaitingApproval = "awaiting_approval"
	JobRetryWaiting     = "retry_waiting"
	JobSucceeded        = "succeeded"
	JobFailed           = "failed"
	JobTimedOut         = "timed_out"
	JobApprovalExpired  = "approval_expired"
	JobCancelled        = "cancelled"
	JobSkipped          = "skipped"

	AttemptClaimed          = "claimed"
	AttemptRunning          = "running"
	AttemptAwaitingApproval = "awaiting_approval"
	AttemptSucceeded        = "succeeded"
	AttemptFailed           = "failed"
	AttemptTimedOut         = "timed_out"
	AttemptApprovalExpired  = "approval_expired"
	AttemptCancelled        = "cancelled"
)

type State struct {
	SchemaVersion int        `json:"schema_version"`
	Revision      uint64     `json:"revision"`
	Schedules     []Schedule `json:"schedules,omitempty"`
	Jobs          []Job      `json:"jobs,omitempty"`
}

type Schedule struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Trigger   Trigger         `json:"trigger"`
	Task      TaskSpec        `json:"task"`
	Policy    ExecutionPolicy `json:"policy"`
	Notify    Notification    `json:"notify,omitempty"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	NextRunAt string          `json:"next_run_at,omitempty"`
	LastJobID string          `json:"last_job_id,omitempty"`
	Archived  bool            `json:"archived,omitempty"`
	DeletedAt string          `json:"deleted_at,omitempty"`
}

type Trigger struct {
	Kind       string `json:"kind"`
	RunAt      string `json:"run_at,omitempty"`
	Expression string `json:"expression,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

type TaskSpec struct {
	Kind         string `json:"kind"`
	Request      string `json:"request"`
	AgentID      string `json:"agent_id,omitempty"`
	WorkflowName string `json:"workflow_name,omitempty"`
	SkillName    string `json:"skill_name,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
	TargetID     string `json:"target_id,omitempty"`
	ReadOnly     bool   `json:"read_only"`
}

type ExecutionPolicy struct {
	TimeoutSeconds        int  `json:"timeout_seconds,omitempty"`
	MaxAttempts           int  `json:"max_attempts,omitempty"`
	RetryDelaySeconds     int  `json:"retry_delay_seconds,omitempty"`
	MisfireGraceSeconds   int  `json:"misfire_grace_seconds,omitempty"`
	AllowOverlap          bool `json:"allow_overlap,omitempty"`
	ApprovalExpirySeconds int  `json:"approval_expiry_seconds,omitempty"`
}

type Notification struct {
	Kind         string `json:"kind,omitempty"`
	URL          string `json:"url,omitempty"`
	Topic        string `json:"topic,omitempty"`
	AllowPrivate bool   `json:"allow_private,omitempty"`
	OnSuccess    bool   `json:"on_success,omitempty"`
	OnFailure    bool   `json:"on_failure,omitempty"`
}

type Job struct {
	ID             string    `json:"id"`
	ScheduleID     string    `json:"schedule_id"`
	ExecutionKey   string    `json:"execution_key"`
	ScheduledFor   string    `json:"scheduled_for"`
	Status         string    `json:"status"`
	Attempt        int       `json:"attempt"`
	Attempts       []Attempt `json:"attempts,omitempty"`
	RunID          string    `json:"run_id,omitempty"`
	ClaimedAt      string    `json:"claimed_at,omitempty"`
	StartedAt      string    `json:"started_at,omitempty"`
	CompletedAt    string    `json:"completed_at,omitempty"`
	NextAttemptAt  string    `json:"next_attempt_at,omitempty"`
	ApprovalExpiry string    `json:"approval_expiry,omitempty"`
	Error          string    `json:"error,omitempty"`
	NotifyStatus   string    `json:"notify_status,omitempty"`
	NotifyError    string    `json:"notify_error,omitempty"`
	NotifyAttempts int       `json:"notify_attempts,omitempty"`
	NextNotifyAt   string    `json:"next_notify_at,omitempty"`
	NotifiedAt     string    `json:"notified_at,omitempty"`
	CreatedAt      string    `json:"created_at"`
	UpdatedAt      string    `json:"updated_at"`
}

type Attempt struct {
	Number      int    `json:"number"`
	Status      string `json:"status"`
	RunID       string `json:"run_id,omitempty"`
	ClaimedAt   string `json:"claimed_at,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	Error       string `json:"error,omitempty"`
}

func parseTimestamp(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

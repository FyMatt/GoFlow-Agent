package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	maxPromptBudgetHistory = 60
	maxTokenUsageHistory   = 60
	snapshotSchemaVersion  = 1
)

// State keeps lightweight in-memory session context.
type State struct {
	mu                sync.RWMutex
	saveMu            sync.Mutex
	persistPathMu     sync.RWMutex
	workflowPersistMu sync.Mutex
	persistPath       string
	activeAgent       string
	mode              string
	maxHistory        int
	prompts           []string
	toolSummaries     []string
	lastSkill         string
	lastSkillMatch    *schema.SkillMatchDiagnostic
	agentRuns         []AgentRunSnapshot
	workflow          WorkflowSnapshot
	workflowRuns      []WorkflowRunSnapshot
	workflowSchemas   map[string]WorkflowSchemaSnapshot
	messages          []CollaborationMessageSnapshot
	blackboard        []BlackboardEntrySnapshot
	artifacts         []SessionArtifactSnapshot
	artifactStore     *ArtifactObjectStore
	attachmentStore   *AttachmentObjectStore
	pendingApprovals  []PendingApprovalSnapshot
	pendingHandoff    PendingHandoffSnapshot
	lastRouting       RoutingSnapshot
	taskStage         TaskStageSnapshot
	promptBudget      *schema.PromptBudget
	promptBudgets     []schema.PromptBudget
	tokenUsages       []schema.TokenUsageSample
	usageRecords      []UsageRecord
	approvedTools     map[string]map[string]struct{}
	approvalScopes    []ApprovalScope
}

type ApprovedToolScope struct {
	Kind string
	Name string
}

// New creates a session state store.
func New(maxHistory int) *State {
	if maxHistory <= 0 {
		maxHistory = 12
	}
	return &State{maxHistory: maxHistory, mode: "chat"}
}

// SetPersistPath configures the runtime path used for durable task transitions.
// Explicit Save calls remain supported for CLI and shutdown boundaries.
func (s *State) SetPersistPath(path string) {
	if s == nil {
		return
	}
	s.persistPathMu.Lock()
	s.persistPath = strings.TrimSpace(path)
	s.persistPathMu.Unlock()
}

// PersistConfigured saves to the configured runtime path, if any.
func (s *State) PersistConfigured() error {
	if s == nil {
		return nil
	}
	s.persistPathMu.RLock()
	path := s.persistPath
	s.persistPathMu.RUnlock()
	if path == "" {
		return nil
	}
	return s.Save(path)
}

func (s *State) SetActiveAgent(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeAgent = agentID
}

func (s *State) ActiveAgent() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeAgent
}

func (s *State) SetMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode == "" {
		mode = "chat"
	}
	s.mode = mode
}

func (s *State) Mode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.mode == "" {
		return "chat"
	}
	return s.mode
}

func (s *State) AddPrompt(prompt string) {
	if prompt == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = appendBounded(s.prompts, prompt, s.maxHistory)
}

func (s *State) AddToolSummary(summary string) {
	if summary == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolSummaries = appendBounded(s.toolSummaries, summary, s.maxHistory)
}

func (s *State) SetLastSkill(skill *schema.Skill) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if skill == nil {
		s.lastSkill = ""
		s.lastSkillMatch = nil
		return
	}
	s.lastSkill = skill.Name
	s.lastSkillMatch = nil
}

func (s *State) SetLastSkillMatch(skill *schema.Skill, diagnostic schema.SkillMatchDiagnostic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if skill == nil {
		s.lastSkill = ""
		s.lastSkillMatch = nil
		return
	}
	s.lastSkill = skill.Name
	if strings.TrimSpace(diagnostic.SkillName) == "" {
		diagnostic.SkillName = skill.Name
	}
	copied := diagnostic
	s.lastSkillMatch = &copied
}

func (s *State) SetWorkflow(snapshot WorkflowSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(snapshot.ResourcePins) == 0 && strings.TrimSpace(snapshot.RunID) != "" && snapshot.RunID == s.workflow.RunID {
		snapshot.ResourcePins = CopyResourcePins(s.workflow.ResourcePins)
	}
	snapshot = s.normalizeWorkflowSnapshotLocked(snapshot)
	s.workflow = copyWorkflowSnapshot(snapshot)
}

func (s *State) SetPendingApprovals(approvals []PendingApprovalSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingApprovals = s.normalizePendingApprovalsLocked(approvals)
}

func (s *State) SetPendingHandoff(handoff PendingHandoffSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingHandoff = handoff
}

func (s *State) SetLastRouting(route RoutingSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRouting = route
}

func (s *State) SetTaskStage(stage TaskStageSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskStage = stage
}

func (s *State) SetPromptBudget(budget schema.PromptBudget) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := budget
	s.promptBudget = &copied
	s.promptBudgets = appendPromptBudgetHistory(s.promptBudgets, budget)
}

// SetArtifactObjectStore attaches a content-addressed artifact store used to
// keep large artifact bodies out of session.json.
func (s *State) SetArtifactObjectStore(store *ArtifactObjectStore) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifactStore = store
}

// ArtifactObjectStore returns the attached content-addressed artifact store.
func (s *State) ArtifactObjectStore() *ArtifactObjectStore {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.artifactStore
}

// SetAttachmentObjectStore attaches the binary input store used by task
// attachments and multimodal messages.
func (s *State) SetAttachmentObjectStore(store *AttachmentObjectStore) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachmentStore = store
}

func (s *State) AttachmentObjectStore() *AttachmentObjectStore {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attachmentStore
}

func (s *State) AddTokenUsage(sample schema.TokenUsageSample) {
	if s == nil {
		return
	}
	if sample.PromptTokens+sample.OutputTokens+sample.CachedTokens <= 0 && strings.TrimSpace(sample.OutputKind) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sample.TotalTokens <= 0 {
		sample.TotalTokens = sample.PromptTokens + sample.OutputTokens
	}
	s.tokenUsages = appendTokenUsageHistory(s.tokenUsages, sample)
}

func (s *State) ClearLastRouting() {
	s.SetLastRouting(RoutingSnapshot{})
}

func (s *State) ClearPendingHandoff() {
	s.SetPendingHandoff(PendingHandoffSnapshot{})
}

func (s *State) PendingHandoff() PendingHandoffSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pendingHandoff
}

func (s *State) LastRouting() RoutingSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRouting
}

func (s *State) RememberApprovedTool(workspaceRoot, toolName string) {
	if s == nil || workspaceRoot == "" || toolName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvedTools == nil {
		s.approvedTools = make(map[string]map[string]struct{})
	}
	if s.approvedTools[workspaceRoot] == nil {
		s.approvedTools[workspaceRoot] = make(map[string]struct{})
	}
	s.approvedTools[workspaceRoot][toolName] = struct{}{}
}

func (s *State) RememberApprovedToolScope(workspaceRoot, kind, toolName string) {
	if s == nil || workspaceRoot == "" || kind == "" || toolName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvedTools == nil {
		s.approvedTools = make(map[string]map[string]struct{})
	}
	if s.approvedTools[workspaceRoot] == nil {
		s.approvedTools[workspaceRoot] = make(map[string]struct{})
	}
	s.approvedTools[workspaceRoot][approvedToolScopeKey(kind, toolName)] = struct{}{}
}

func (s *State) HasApprovedTool(workspaceRoot, toolName string) bool {
	if s == nil || workspaceRoot == "" || toolName == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := s.approvedTools[workspaceRoot]
	_, ok := tools[toolName]
	return ok
}

func (s *State) HasApprovedToolScope(workspaceRoot, kind, toolName string) bool {
	if s == nil || workspaceRoot == "" || kind == "" || toolName == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := s.approvedTools[workspaceRoot]
	_, ok := tools[approvedToolScopeKey(kind, toolName)]
	return ok
}

func (s *State) ApprovedToolsForWorkspace(workspaceRoot string) []string {
	if s == nil || workspaceRoot == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := s.approvedTools[workspaceRoot]
	if len(tools) == 0 {
		return nil
	}
	result := make([]string, 0, len(tools))
	for toolName := range tools {
		result = append(result, toolName)
	}
	return result
}

func (s *State) ApprovedToolScopesForWorkspace(workspaceRoot string) []ApprovedToolScope {
	if s == nil || workspaceRoot == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := s.approvedTools[workspaceRoot]
	if len(tools) == 0 {
		return nil
	}
	result := make([]ApprovedToolScope, 0, len(tools))
	for raw := range tools {
		kind, name, ok := parseApprovedToolScope(raw)
		if !ok {
			continue
		}
		result = append(result, ApprovedToolScope{Kind: kind, Name: name})
	}
	return result
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prompts := append([]string(nil), s.prompts...)
	tools := append([]string(nil), s.toolSummaries...)
	pendingApprovals := copyPendingApprovalSnapshots(s.pendingApprovals)
	agentRuns := copyAgentRunSnapshots(s.agentRuns)
	workflowRuns := copyWorkflowRunSnapshots(s.workflowRuns)
	workflowSchemas := copyWorkflowSchemaCatalog(s.workflowSchemas)
	messages := copyCollaborationMessages(s.messages)
	blackboard := copyBlackboardEntries(s.blackboard)
	artifacts := copySessionArtifacts(s.artifacts)
	approvedTools := make(map[string][]string, len(s.approvedTools))
	for workspace, toolSet := range s.approvedTools {
		list := make([]string, 0, len(toolSet))
		for toolName := range toolSet {
			list = append(list, toolName)
		}
		approvedTools[workspace] = list
	}
	var lastSkillMatch *schema.SkillMatchDiagnostic
	if s.lastSkillMatch != nil {
		copied := *s.lastSkillMatch
		lastSkillMatch = &copied
	}
	var promptBudget *schema.PromptBudget
	if s.promptBudget != nil {
		copied := *s.promptBudget
		promptBudget = &copied
	}
	promptBudgets := append([]schema.PromptBudget(nil), s.promptBudgets...)
	if len(promptBudgets) == 0 && promptBudget != nil {
		promptBudgets = []schema.PromptBudget{*promptBudget}
	}
	tokenUsages := append([]schema.TokenUsageSample(nil), s.tokenUsages...)
	return Snapshot{
		SchemaVersion:    snapshotSchemaVersion,
		ActiveAgent:      s.activeAgent,
		Mode:             s.mode,
		RecentPrompts:    prompts,
		RecentTools:      tools,
		LastSkill:        s.lastSkill,
		LastSkillMatch:   lastSkillMatch,
		AgentRuns:        agentRuns,
		Workflow:         copyWorkflowSnapshot(s.workflow),
		WorkflowRuns:     workflowRuns,
		WorkflowSchemas:  workflowSchemas,
		Messages:         messages,
		Blackboard:       blackboard,
		Artifacts:        artifacts,
		PendingApprovals: pendingApprovals,
		PendingHandoff:   s.pendingHandoff,
		LastRouting:      s.lastRouting,
		TaskStage:        s.taskStage,
		PromptBudget:     promptBudget,
		PromptBudgets:    promptBudgets,
		TokenUsages:      tokenUsages,
		UsageRecords:     append([]UsageRecord(nil), s.usageRecords...),
		ApprovedTools:    approvedTools,
		ApprovalScopes:   copyApprovalScopes(normalizeApprovalScopes(s.approvalScopes, time.Now().UTC())),
	}
}

// DurableSnapshot waits for an in-flight Workflow transition to finish
// persisting before exposing the corresponding in-memory state.
func (s *State) DurableSnapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.workflowPersistMu.Lock()
	defer s.workflowPersistMu.Unlock()
	return s.Snapshot()
}

func (s *State) Save(path string) error {
	if s == nil || path == "" {
		return nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	snapshot := s.Snapshot()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeFullSessionArchive(path, snapshot); err != nil {
		return err
	}
	data, err := json.MarshalIndent(compactSnapshotForPersist(snapshot), "", "  ")
	if err != nil {
		return err
	}
	return persistence.WriteFileAtomic(path, data, 0o644)
}

func (s *State) Load(path string) error {
	if s == nil || path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	snapshot, fromArchive, archiveErr := loadFullSessionArchive(path)
	if archiveErr != nil {
		return archiveErr
	}
	if !fromArchive {
		if err := json.Unmarshal(data, &snapshot); err != nil {
			return err
		}
	}
	migrated, err := migrateSnapshot(&snapshot)
	if err != nil {
		return err
	}
	if !fromArchive || migrated {
		if err := writeFullSessionArchive(path, snapshot); err != nil {
			return err
		}
	}
	if migrated {
		compacted, marshalErr := json.MarshalIndent(compactSnapshotForPersist(snapshot), "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		if writeErr := persistence.WriteFileAtomic(path, compacted, 0o644); writeErr != nil {
			return writeErr
		}
	} else {
		_ = rewriteCompactedSessionFile(path, compactSnapshotForPersist(snapshot), len(data))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeAgent = snapshot.ActiveAgent
	if snapshot.Mode == "" {
		s.mode = "chat"
	} else {
		s.mode = snapshot.Mode
	}
	s.prompts = append([]string(nil), snapshot.RecentPrompts...)
	if len(s.prompts) > s.maxHistory {
		s.prompts = append([]string(nil), s.prompts[len(s.prompts)-s.maxHistory:]...)
	}
	s.toolSummaries = append([]string(nil), snapshot.RecentTools...)
	if len(s.toolSummaries) > s.maxHistory {
		s.toolSummaries = append([]string(nil), s.toolSummaries[len(s.toolSummaries)-s.maxHistory:]...)
	}
	s.lastSkill = snapshot.LastSkill
	if snapshot.LastSkillMatch != nil {
		copied := *snapshot.LastSkillMatch
		s.lastSkillMatch = &copied
	} else {
		s.lastSkillMatch = nil
	}
	s.workflow = copyWorkflowSnapshot(snapshot.Workflow)
	s.agentRuns = copyAgentRunSnapshots(snapshot.AgentRuns)
	if len(s.agentRuns) > maxAgentRuns {
		s.agentRuns = append([]AgentRunSnapshot(nil), s.agentRuns[:maxAgentRuns]...)
	}
	s.workflowRuns = copyWorkflowRunSnapshots(snapshot.WorkflowRuns)
	if len(s.workflowRuns) > maxWorkflowRuns {
		s.workflowRuns = append([]WorkflowRunSnapshot(nil), s.workflowRuns[:maxWorkflowRuns]...)
	}
	s.workflowSchemas = copyWorkflowSchemaCatalog(snapshot.WorkflowSchemas)
	if len(s.workflowSchemas) == 0 && len(s.workflowRuns) > 0 {
		for i := len(s.workflowRuns) - 1; i >= 0; i-- {
			s.mergeWorkflowSchemaFromRunLocked(s.workflowRuns[i])
		}
	}
	s.messages = copyCollaborationMessages(snapshot.Messages)
	if len(s.messages) > maxCollaborationMessages {
		s.messages = append([]CollaborationMessageSnapshot(nil), s.messages[:maxCollaborationMessages]...)
	}
	s.blackboard = copyBlackboardEntries(snapshot.Blackboard)
	if len(s.blackboard) > maxBlackboardEntries {
		s.blackboard = append([]BlackboardEntrySnapshot(nil), s.blackboard[:maxBlackboardEntries]...)
	}
	s.artifacts = copySessionArtifacts(snapshot.Artifacts)
	if len(s.artifacts) > maxSessionArtifacts {
		s.artifacts = append([]SessionArtifactSnapshot(nil), s.artifacts[:maxSessionArtifacts]...)
	}
	s.pendingApprovals = copyPendingApprovalSnapshots(snapshot.PendingApprovals)
	s.pendingHandoff = snapshot.PendingHandoff
	s.lastRouting = snapshot.LastRouting
	s.taskStage = snapshot.TaskStage
	if snapshot.PromptBudget != nil {
		copied := *snapshot.PromptBudget
		s.promptBudget = &copied
	} else {
		s.promptBudget = nil
	}
	s.promptBudgets = append([]schema.PromptBudget(nil), snapshot.PromptBudgets...)
	if len(s.promptBudgets) == 0 && s.promptBudget != nil {
		s.promptBudgets = []schema.PromptBudget{*s.promptBudget}
	}
	if len(s.promptBudgets) > maxPromptBudgetHistory {
		s.promptBudgets = append([]schema.PromptBudget(nil), s.promptBudgets[len(s.promptBudgets)-maxPromptBudgetHistory:]...)
	}
	s.tokenUsages = append([]schema.TokenUsageSample(nil), snapshot.TokenUsages...)
	s.usageRecords = SnapshotUsageRecords(snapshot)
	if len(s.tokenUsages) > maxTokenUsageHistory {
		s.tokenUsages = append([]schema.TokenUsageSample(nil), s.tokenUsages[len(s.tokenUsages)-maxTokenUsageHistory:]...)
	}
	s.approvedTools = make(map[string]map[string]struct{}, len(snapshot.ApprovedTools))
	for workspace, toolNames := range snapshot.ApprovedTools {
		if workspace == "" {
			continue
		}
		toolSet := make(map[string]struct{}, len(toolNames))
		for _, toolName := range toolNames {
			if toolName == "" {
				continue
			}
			toolSet[toolName] = struct{}{}
		}
		if len(toolSet) > 0 {
			s.approvedTools[workspace] = toolSet
		}
	}
	s.approvalScopes = normalizeApprovalScopes(copyApprovalScopes(snapshot.ApprovalScopes), time.Now().UTC())
	return nil
}

func migrateSnapshot(snapshot *Snapshot) (bool, error) {
	if snapshot == nil {
		return false, errors.New("session snapshot is required")
	}
	switch snapshot.SchemaVersion {
	case 0:
		// Pre-V3 session files used the same JSON field names but did not carry
		// an explicit version. Optional fields already have deterministic zero
		// values, so migration only needs to stamp the current schema.
		snapshot.SchemaVersion = snapshotSchemaVersion
		return true, nil
	case snapshotSchemaVersion:
		return false, nil
	default:
		return false, fmt.Errorf("unsupported session schema version %d (current %d)", snapshot.SchemaVersion, snapshotSchemaVersion)
	}
}

func rewriteCompactedSessionFile(path string, snapshot Snapshot, originalBytes int) error {
	if path == "" || originalBytes <= 0 {
		return nil
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+4096 >= originalBytes {
		return nil
	}
	return persistence.WriteFileAtomic(path, data, 0o644)
}

// WorkflowSnapshot is a read-only view of current workflow state.
type WorkflowSnapshot struct {
	RunID                        string                  `json:"run_id,omitempty"`
	Name                         string                  `json:"name,omitempty"`
	Status                       string                  `json:"status,omitempty"`
	NextStage                    string                  `json:"next_stage,omitempty"`
	Request                      string                  `json:"request,omitempty"`
	Summary                      string                  `json:"summary,omitempty"`
	LastApproval                 string                  `json:"last_approval,omitempty"`
	PendingCallID                string                  `json:"pending_call_id,omitempty"`
	PendingToolName              string                  `json:"pending_tool_name,omitempty"`
	PendingToolRisk              *schema.ToolRiskProfile `json:"pending_tool_risk,omitempty"`
	PendingAgentID               string                  `json:"pending_agent_id,omitempty"`
	PendingArguments             string                  `json:"pending_arguments,omitempty"`
	PendingArgumentsSummary      string                  `json:"pending_arguments_summary,omitempty"`
	PendingArgumentsArtifactRef  string                  `json:"pending_arguments_artifact_ref,omitempty"`
	PendingArgumentsHash         string                  `json:"pending_arguments_hash,omitempty"`
	PendingArgumentsBytes        int                     `json:"pending_arguments_bytes,omitempty"`
	PendingArgumentsStoredBytes  int                     `json:"pending_arguments_stored_bytes,omitempty"`
	PendingArgumentsExternalized bool                    `json:"pending_arguments_externalized,omitempty"`
	PendingResponseMessage       schema.Message          `json:"pending_response_message,omitempty"`
	PendingSubWorkflowName       string                  `json:"pending_sub_workflow_name,omitempty"`
	PendingSubWorkflowRunID      string                  `json:"pending_sub_workflow_run_id,omitempty"`
	PendingSubWorkflowStatus     string                  `json:"pending_sub_workflow_status,omitempty"`
	ResourcePins                 []ResourcePin           `json:"resource_pins,omitempty"`
	Attachments                  []schema.AttachmentRef  `json:"attachments,omitempty"`
	TargetID                     string                  `json:"target_id,omitempty"`
}

// WorkflowRunSnapshot is a durable, replayable view of one workflow execution.
type WorkflowRunSnapshot struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name,omitempty"`
	Status        string                 `json:"status,omitempty"`
	Request       string                 `json:"request,omitempty"`
	StartedAt     string                 `json:"started_at,omitempty"`
	UpdatedAt     string                 `json:"updated_at,omitempty"`
	CompletedAt   string                 `json:"completed_at,omitempty"`
	CancelledAt   string                 `json:"cancelled_at,omitempty"`
	RetryOf       string                 `json:"retry_of,omitempty"`
	Attempt       int                    `json:"attempt,omitempty"`
	ScheduleID    string                 `json:"schedule_id,omitempty"`
	ScheduleJobID string                 `json:"schedule_job_id,omitempty"`
	TargetID      string                 `json:"target_id,omitempty"`
	ResourcePins  []ResourcePin          `json:"resource_pins,omitempty"`
	Attachments   []schema.AttachmentRef `json:"attachments,omitempty"`
	// StageCalls counts model calls per stage durably, so budget enforcement
	// does not depend on the bounded event window.
	StageCalls                  map[string]int                  `json:"stage_calls,omitempty"`
	ReasoningEffort             string                          `json:"reasoning_effort,omitempty"`
	PermissionMode              string                          `json:"permission_mode,omitempty"`
	NextStage                   string                          `json:"next_stage,omitempty"`
	Summary                     string                          `json:"summary,omitempty"`
	ApprovalPrompt              string                          `json:"approval_prompt,omitempty"`
	PendingFields               []schema.WorkflowInputField     `json:"pending_input_fields,omitempty"`
	PendingCallID               string                          `json:"pending_call_id,omitempty"`
	PendingToolName             string                          `json:"pending_tool_name,omitempty"`
	PendingToolRisk             *schema.ToolRiskProfile         `json:"pending_tool_risk,omitempty"`
	PendingAgentID              string                          `json:"pending_agent_id,omitempty"`
	PendingArgs                 string                          `json:"pending_arguments,omitempty"`
	PendingArgsSummary          string                          `json:"pending_arguments_summary,omitempty"`
	PendingArgsArtifactRef      string                          `json:"pending_arguments_artifact_ref,omitempty"`
	PendingArgsHash             string                          `json:"pending_arguments_hash,omitempty"`
	PendingArgsBytes            int                             `json:"pending_arguments_bytes,omitempty"`
	PendingArgsStoredBytes      int                             `json:"pending_arguments_stored_bytes,omitempty"`
	PendingArgsExternalized     bool                            `json:"pending_arguments_externalized,omitempty"`
	PendingResponseMessage      schema.Message                  `json:"pending_response_message,omitempty"`
	PendingSubWorkflowName      string                          `json:"pending_sub_workflow_name,omitempty"`
	PendingSubWorkflowRunID     string                          `json:"pending_sub_workflow_run_id,omitempty"`
	PendingSubWorkflowStatus    string                          `json:"pending_sub_workflow_status,omitempty"`
	BudgetScope                 string                          `json:"budget_scope,omitempty"`
	BudgetReason                string                          `json:"budget_reason,omitempty"`
	BudgetMetric                string                          `json:"budget_metric,omitempty"`
	BudgetUsed                  int                             `json:"budget_used,omitempty"`
	BudgetSoftLimit             int                             `json:"budget_soft_limit,omitempty"`
	BudgetHardLimit             int                             `json:"budget_hard_limit,omitempty"`
	BudgetRemaining             int                             `json:"budget_remaining,omitempty"`
	BudgetPromptTokens          int                             `json:"budget_prompt_tokens,omitempty"`
	BudgetEstimatedPromptTokens int                             `json:"budget_estimated_prompt_tokens,omitempty"`
	BudgetNetPromptTokens       int                             `json:"budget_net_prompt_tokens,omitempty"`
	BudgetGrossPromptTokens     int                             `json:"budget_gross_prompt_tokens,omitempty"`
	BudgetSavedTokens           int                             `json:"budget_saved_tokens,omitempty"`
	BudgetMemorySavedTokens     int                             `json:"budget_memory_saved_tokens,omitempty"`
	BudgetHistorySavedTokens    int                             `json:"budget_history_saved_tokens,omitempty"`
	BudgetArtifactSavedTokens   int                             `json:"budget_artifact_saved_tokens,omitempty"`
	BudgetSkillSavedTokens      int                             `json:"budget_skill_saved_tokens,omitempty"`
	BudgetToolSchemaSavedTokens int                             `json:"budget_tool_schema_saved_tokens,omitempty"`
	BudgetReportedPromptTokens  int                             `json:"budget_reported_prompt_tokens,omitempty"`
	BudgetOutputTokens          int                             `json:"budget_output_tokens,omitempty"`
	BudgetCachedTokens          int                             `json:"budget_cached_tokens,omitempty"`
	BudgetTotalTokens           int                             `json:"budget_total_tokens,omitempty"`
	BudgetLLMCalls              int                             `json:"budget_llm_calls,omitempty"`
	BudgetContinuations         int                             `json:"budget_continuations,omitempty"`
	BudgetEstimatedInputCost    float64                         `json:"budget_estimated_input_cost,omitempty"`
	BudgetEstimatedOutputCost   float64                         `json:"budget_estimated_output_cost,omitempty"`
	BudgetEstimatedTotalCost    float64                         `json:"budget_estimated_total_cost,omitempty"`
	BudgetCostCurrency          string                          `json:"budget_cost_currency,omitempty"`
	BudgetPricingSource         string                          `json:"budget_pricing_source,omitempty"`
	CompletedStages             []WorkflowRunStageSnapshot      `json:"completed_stages,omitempty"`
	Artifacts                   []WorkflowRunArtifact           `json:"artifacts,omitempty"`
	Events                      []WorkflowRunEventSnapshot      `json:"events,omitempty"`
	UsageRecords                []UsageRecord                   `json:"usage_records,omitempty"`
	ToolOperations              []WorkflowToolOperationSnapshot `json:"tool_operations,omitempty"`
	ChangeSlices                []WorkflowChangeSliceSnapshot   `json:"change_slices,omitempty"`
	StagesCount                 int                             `json:"stages_count,omitempty"`
	ArtifactsCount              int                             `json:"artifacts_count,omitempty"`
	EventsCount                 int                             `json:"events_count,omitempty"`
	DiffsCount                  int                             `json:"diffs_count,omitempty"`
}

// WorkflowChangeSliceSnapshot is a deterministic, durable implementation unit
// compiled from a workflow plan. Model prose cannot directly mark it complete.
type WorkflowChangeSliceSnapshot struct {
	ID                   string            `json:"id"`
	Title                string            `json:"title"`
	ExecutionMode        string            `json:"execution_mode,omitempty"`
	DependsOn            []string          `json:"depends_on,omitempty"`
	InputFiles           []string          `json:"input_files,omitempty"`
	InputHashes          map[string]string `json:"input_hashes,omitempty"`
	TargetFiles          []string          `json:"target_files"`
	VerificationCommands []string          `json:"verification_commands"`
	ContractHash         string            `json:"contract_hash"`
	Status               string            `json:"status"`
	BlockedReason        string            `json:"blocked_reason,omitempty"`
	Attempt              int               `json:"attempt,omitempty"`
	EvidenceStages       []string          `json:"evidence_stages,omitempty"`
	ChangedFiles         []string          `json:"changed_files,omitempty"`
}

// WorkflowToolOperationSnapshot is a durable idempotency record for one
// side-effecting tool invocation inside a retryable Workflow stage.
type WorkflowToolOperationSnapshot struct {
	Key           string            `json:"key"`
	RunID         string            `json:"run_id"`
	Workflow      string            `json:"workflow,omitempty"`
	Stage         string            `json:"stage"`
	ToolName      string            `json:"tool_name"`
	ArgumentsHash string            `json:"arguments_hash"`
	WorkspaceHash string            `json:"workspace_hash,omitempty"`
	Reusable      bool              `json:"reusable,omitempty"`
	Status        string            `json:"status"`
	StartedAt     string            `json:"started_at"`
	UpdatedAt     string            `json:"updated_at"`
	Result        schema.ToolResult `json:"result,omitempty"`
}

// WorkflowRunStageSnapshot captures the latest persisted output for a stage.
type WorkflowRunStageSnapshot struct {
	Stage                       string                          `json:"stage"`
	AgentID                     string                          `json:"agent_id,omitempty"`
	NodeType                    string                          `json:"node_type,omitempty"`
	Skill                       string                          `json:"skill,omitempty"`
	Tool                        string                          `json:"tool,omitempty"`
	Status                      string                          `json:"status,omitempty"`
	StartedAt                   string                          `json:"started_at,omitempty"`
	CompletedAt                 string                          `json:"completed_at,omitempty"`
	Attempts                    int                             `json:"attempts,omitempty"`
	Summary                     string                          `json:"summary,omitempty"`
	Inputs                      map[string]string               `json:"inputs,omitempty"`
	InputValues                 map[string]any                  `json:"input_values,omitempty"`
	InputValuesArtifactRef      string                          `json:"input_values_artifact_ref,omitempty"`
	InputValuesHash             string                          `json:"input_values_hash,omitempty"`
	InputValueCount             int                             `json:"input_value_count,omitempty"`
	InputValuesBytes            int                             `json:"input_values_bytes,omitempty"`
	InputValuesStoredBytes      int                             `json:"input_values_stored_bytes,omitempty"`
	InputValuesExternalized     bool                            `json:"input_values_externalized,omitempty"`
	Outputs                     map[string]string               `json:"outputs,omitempty"`
	OutputValues                map[string]any                  `json:"output_values,omitempty"`
	OutputValuesArtifactRef     string                          `json:"output_values_artifact_ref,omitempty"`
	OutputValuesHash            string                          `json:"output_values_hash,omitempty"`
	OutputValueCount            int                             `json:"output_value_count,omitempty"`
	OutputValuesBytes           int                             `json:"output_values_bytes,omitempty"`
	OutputValuesStoredBytes     int                             `json:"output_values_stored_bytes,omitempty"`
	OutputValuesExternalized    bool                            `json:"output_values_externalized,omitempty"`
	Result                      schema.AgentResult              `json:"result"`
	Artifacts                   []WorkflowRunArtifact           `json:"artifacts,omitempty"`
	Acceptance                  []WorkflowRunAcceptanceSnapshot `json:"acceptance,omitempty"`
	Metadata                    map[string]string               `json:"metadata,omitempty"`
	ChangeSlices                []WorkflowChangeSliceSnapshot   `json:"change_slices,omitempty"`
	BudgetScope                 string                          `json:"budget_scope,omitempty"`
	BudgetReason                string                          `json:"budget_reason,omitempty"`
	BudgetMetric                string                          `json:"budget_metric,omitempty"`
	BudgetUsed                  int                             `json:"budget_used,omitempty"`
	BudgetSoftLimit             int                             `json:"budget_soft_limit,omitempty"`
	BudgetHardLimit             int                             `json:"budget_hard_limit,omitempty"`
	BudgetRemaining             int                             `json:"budget_remaining,omitempty"`
	BudgetPromptTokens          int                             `json:"budget_prompt_tokens,omitempty"`
	BudgetEstimatedPromptTokens int                             `json:"budget_estimated_prompt_tokens,omitempty"`
	BudgetNetPromptTokens       int                             `json:"budget_net_prompt_tokens,omitempty"`
	BudgetGrossPromptTokens     int                             `json:"budget_gross_prompt_tokens,omitempty"`
	BudgetSavedTokens           int                             `json:"budget_saved_tokens,omitempty"`
	BudgetMemorySavedTokens     int                             `json:"budget_memory_saved_tokens,omitempty"`
	BudgetHistorySavedTokens    int                             `json:"budget_history_saved_tokens,omitempty"`
	BudgetArtifactSavedTokens   int                             `json:"budget_artifact_saved_tokens,omitempty"`
	BudgetSkillSavedTokens      int                             `json:"budget_skill_saved_tokens,omitempty"`
	BudgetToolSchemaSavedTokens int                             `json:"budget_tool_schema_saved_tokens,omitempty"`
	BudgetReportedPromptTokens  int                             `json:"budget_reported_prompt_tokens,omitempty"`
	BudgetOutputTokens          int                             `json:"budget_output_tokens,omitempty"`
	BudgetCachedTokens          int                             `json:"budget_cached_tokens,omitempty"`
	BudgetTotalTokens           int                             `json:"budget_total_tokens,omitempty"`
	BudgetLLMCalls              int                             `json:"budget_llm_calls,omitempty"`
	BudgetContinuations         int                             `json:"budget_continuations,omitempty"`
	BudgetEstimatedInputCost    float64                         `json:"budget_estimated_input_cost,omitempty"`
	BudgetEstimatedOutputCost   float64                         `json:"budget_estimated_output_cost,omitempty"`
	BudgetEstimatedTotalCost    float64                         `json:"budget_estimated_total_cost,omitempty"`
	BudgetCostCurrency          string                          `json:"budget_cost_currency,omitempty"`
	BudgetPricingSource         string                          `json:"budget_pricing_source,omitempty"`
}

// WorkflowRunAcceptanceSnapshot captures pass/fail evidence for a stage criterion.
type WorkflowRunAcceptanceSnapshot struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Expected    string `json:"expected,omitempty"`
	Actual      string `json:"actual,omitempty"`
	Status      string `json:"status,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// WorkflowRunArtifact captures a replayable stage output or evidence item.
type WorkflowRunArtifact struct {
	ID           string            `json:"id"`
	Stage        string            `json:"stage,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	Title        string            `json:"title,omitempty"`
	Summary      string            `json:"summary,omitempty"`
	Content      string            `json:"content,omitempty"`
	ArtifactRef  string            `json:"artifact_ref,omitempty"`
	Hash         string            `json:"hash,omitempty"`
	Mime         string            `json:"mime,omitempty"`
	Size         int               `json:"size,omitempty"`
	ContentBytes int               `json:"content_bytes,omitempty"`
	StoredBytes  int               `json:"stored_bytes,omitempty"`
	Deduplicated bool              `json:"deduplicated,omitempty"`
	Externalized bool              `json:"externalized,omitempty"`
	ToolName     string            `json:"tool_name,omitempty"`
	ToolCallID   string            `json:"tool_call_id,omitempty"`
	IsError      bool              `json:"is_error,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// AgentRunArtifactSnapshot stores a summary-first, optionally externalized
// artifact produced by one ordinary agent run.
type AgentRunArtifactSnapshot = SessionArtifactSnapshot

// WorkflowRunEventSnapshot captures a replayable workflow stream/status event.
type WorkflowRunEventSnapshot struct {
	Seq                         int                     `json:"seq,omitempty"`
	At                          string                  `json:"at,omitempty"`
	Stage                       string                  `json:"stage,omitempty"`
	Type                        string                  `json:"type,omitempty"`
	Content                     string                  `json:"content,omitempty"`
	ContentArtifactRef          string                  `json:"content_artifact_ref,omitempty"`
	ContentHash                 string                  `json:"content_hash,omitempty"`
	ContentBytes                int                     `json:"content_bytes,omitempty"`
	ContentStoredBytes          int                     `json:"content_stored_bytes,omitempty"`
	ContentExternalized         bool                    `json:"content_externalized,omitempty"`
	ToolName                    string                  `json:"tool_name,omitempty"`
	ToolCallID                  string                  `json:"tool_call_id,omitempty"`
	DurationMs                  int64                   `json:"duration_ms,omitempty"`
	ToolResultSummary           string                  `json:"tool_result_summary,omitempty"`
	ArtifactRef                 string                  `json:"artifact_ref,omitempty"`
	ArtifactHash                string                  `json:"artifact_hash,omitempty"`
	ToolResultBytes             int                     `json:"tool_result_content_bytes,omitempty"`
	ToolResultStored            int                     `json:"tool_result_stored_bytes,omitempty"`
	ToolResultTruncated         bool                    `json:"tool_result_truncated,omitempty"`
	ArtifactRetention           string                  `json:"artifact_retention,omitempty"`
	Redaction                   string                  `json:"redaction,omitempty"`
	ArgumentsSummary            string                  `json:"arguments_summary,omitempty"`
	ArgumentsHash               string                  `json:"arguments_hash,omitempty"`
	AgentID                     string                  `json:"agent_id,omitempty"`
	Mode                        string                  `json:"mode,omitempty"`
	IsError                     bool                    `json:"is_error,omitempty"`
	NeedsAction                 bool                    `json:"needs_action,omitempty"`
	Suspended                   bool                    `json:"suspended,omitempty"`
	TaskStage                   string                  `json:"task_stage,omitempty"`
	PromptTokens                int                     `json:"prompt_tokens,omitempty"`
	OutputTokens                int                     `json:"output_tokens,omitempty"`
	CachedTokens                int                     `json:"cached_tokens,omitempty"`
	OutputKind                  string                  `json:"output_kind,omitempty"`
	CacheHitRate                float64                 `json:"cache_hit_rate,omitempty"`
	WorkflowName                string                  `json:"workflow_name,omitempty"`
	WorkflowStatus              string                  `json:"workflow_status,omitempty"`
	NextStage                   string                  `json:"next_stage,omitempty"`
	PendingApproval             bool                    `json:"pending_approval,omitempty"`
	Reason                      string                  `json:"reason,omitempty"`
	Severity                    string                  `json:"severity,omitempty"`
	BudgetScope                 string                  `json:"budget_scope,omitempty"`
	BudgetReason                string                  `json:"budget_reason,omitempty"`
	BudgetMetric                string                  `json:"budget_metric,omitempty"`
	BudgetUsed                  int                     `json:"budget_used,omitempty"`
	BudgetSoftLimit             int                     `json:"budget_soft_limit,omitempty"`
	BudgetHardLimit             int                     `json:"budget_hard_limit,omitempty"`
	BudgetRemaining             int                     `json:"budget_remaining,omitempty"`
	BudgetPromptTokens          int                     `json:"budget_prompt_tokens,omitempty"`
	BudgetEstimatedPromptTokens int                     `json:"budget_estimated_prompt_tokens,omitempty"`
	BudgetNetPromptTokens       int                     `json:"budget_net_prompt_tokens,omitempty"`
	BudgetGrossPromptTokens     int                     `json:"budget_gross_prompt_tokens,omitempty"`
	BudgetSavedTokens           int                     `json:"budget_saved_tokens,omitempty"`
	BudgetMemorySavedTokens     int                     `json:"budget_memory_saved_tokens,omitempty"`
	BudgetHistorySavedTokens    int                     `json:"budget_history_saved_tokens,omitempty"`
	BudgetArtifactSavedTokens   int                     `json:"budget_artifact_saved_tokens,omitempty"`
	BudgetSkillSavedTokens      int                     `json:"budget_skill_saved_tokens,omitempty"`
	BudgetToolSchemaSavedTokens int                     `json:"budget_tool_schema_saved_tokens,omitempty"`
	BudgetReportedPromptTokens  int                     `json:"budget_reported_prompt_tokens,omitempty"`
	BudgetOutputTokens          int                     `json:"budget_output_tokens,omitempty"`
	BudgetCachedTokens          int                     `json:"budget_cached_tokens,omitempty"`
	BudgetTotalTokens           int                     `json:"budget_total_tokens,omitempty"`
	BudgetLLMCalls              int                     `json:"budget_llm_calls,omitempty"`
	BudgetContinuations         int                     `json:"budget_continuations,omitempty"`
	BudgetEstimatedInputCost    float64                 `json:"budget_estimated_input_cost,omitempty"`
	BudgetEstimatedOutputCost   float64                 `json:"budget_estimated_output_cost,omitempty"`
	BudgetEstimatedTotalCost    float64                 `json:"budget_estimated_total_cost,omitempty"`
	BudgetCostCurrency          string                  `json:"budget_cost_currency,omitempty"`
	BudgetPricingSource         string                  `json:"budget_pricing_source,omitempty"`
	ContractCheck               string                  `json:"contract_check,omitempty"`
	SourceRef                   string                  `json:"source_ref,omitempty"`
	StopReason                  string                  `json:"stop_reason,omitempty"`
	ContinuationCount           int                     `json:"continuation_count,omitempty"`
	Incomplete                  bool                    `json:"incomplete,omitempty"`
	PromptBudget                *schema.PromptBudget    `json:"prompt_budget,omitempty"`
	Risk                        *schema.ToolRiskProfile `json:"risk,omitempty"`
}

// PendingApprovalSnapshot is a read-only view of pending tool approval state.
type PendingApprovalSnapshot struct {
	CallID                string                  `json:"call_id,omitempty"`
	ToolName              string                  `json:"tool_name,omitempty"`
	ToolServer            string                  `json:"tool_server,omitempty"`
	ToolKind              string                  `json:"tool_kind,omitempty"`
	AgentID               string                  `json:"agent_id,omitempty"`
	ArgumentsSummary      string                  `json:"arguments_summary,omitempty"`
	Arguments             string                  `json:"arguments,omitempty"`
	ArgumentsArtifactRef  string                  `json:"arguments_artifact_ref,omitempty"`
	ArgumentsHash         string                  `json:"arguments_hash,omitempty"`
	ArgumentsBytes        int                     `json:"arguments_bytes,omitempty"`
	ArgumentsStoredBytes  int                     `json:"arguments_stored_bytes,omitempty"`
	ArgumentsExternalized bool                    `json:"arguments_externalized,omitempty"`
	WorkflowName          string                  `json:"workflow_name,omitempty"`
	Stage                 string                  `json:"stage,omitempty"`
	Request               string                  `json:"request,omitempty"`
	CompletedSummary      string                  `json:"completed_summary,omitempty"`
	AgentRunID            string                  `json:"agent_run_id,omitempty"`
	Risk                  *schema.ToolRiskProfile `json:"risk,omitempty"`
	ApprovalFingerprint   string                  `json:"approval_fingerprint,omitempty"`
	ApprovalWorkspace     string                  `json:"approval_workspace,omitempty"`
	ApprovalTarget        string                  `json:"approval_target,omitempty"`
	ApprovalExpiresAt     string                  `json:"approval_expires_at,omitempty"`
	WorkflowResumeID      string                  `json:"workflow_resume_id,omitempty"`
	WorkflowResponse      schema.Message          `json:"workflow_response,omitempty"`
	WorkflowCalls         []schema.ToolCall       `json:"workflow_calls,omitempty"`
	WorkflowBaseResults   []schema.ToolResult     `json:"workflow_base_results,omitempty"`
	WorkflowResults       []schema.ToolResult     `json:"workflow_results,omitempty"`
	WorkflowSystemPrompt  string                  `json:"workflow_system_prompt,omitempty"`
	WorkflowMessages      []schema.Message        `json:"workflow_messages,omitempty"`
	WorkflowCollected     []schema.ToolResult     `json:"workflow_collected_results,omitempty"`
}

// PendingHandoffSnapshot is a read-only view of an ordinary-chat plan->execution handoff.
type PendingHandoffSnapshot struct {
	Request        string `json:"request,omitempty"`
	SourceAgent    string `json:"source_agent,omitempty"`
	TargetAgent    string `json:"target_agent,omitempty"`
	TargetMode     string `json:"target_mode,omitempty"`
	PlanSummary    string `json:"plan_summary,omitempty"`
	ExpectedAction string `json:"expected_action,omitempty"`
}

// RoutingSnapshot is a read-only view of the latest ordinary-chat routing or waiting decision.
type RoutingSnapshot struct {
	Request     string `json:"request,omitempty"`
	SourceAgent string `json:"source_agent,omitempty"`
	TargetAgent string `json:"target_agent,omitempty"`
	TargetMode  string `json:"target_mode,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// TaskStageSnapshot is a read-only view of the current high-level task stage.
type TaskStageSnapshot struct {
	Stage   string `json:"stage,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Snapshot is a read-only view of current session state.
type Snapshot struct {
	SchemaVersion    int                               `json:"schema_version"`
	ActiveAgent      string                            `json:"active_agent"`
	Mode             string                            `json:"mode"`
	RecentPrompts    []string                          `json:"recent_prompts"`
	RecentTools      []string                          `json:"recent_tools"`
	LastSkill        string                            `json:"last_skill"`
	LastSkillMatch   *schema.SkillMatchDiagnostic      `json:"last_skill_match,omitempty"`
	AgentRuns        []AgentRunSnapshot                `json:"agent_runs,omitempty"`
	Workflow         WorkflowSnapshot                  `json:"workflow,omitempty"`
	WorkflowRuns     []WorkflowRunSnapshot             `json:"workflow_runs,omitempty"`
	WorkflowSchemas  map[string]WorkflowSchemaSnapshot `json:"workflow_schemas,omitempty"`
	Messages         []CollaborationMessageSnapshot    `json:"messages,omitempty"`
	Blackboard       []BlackboardEntrySnapshot         `json:"blackboard,omitempty"`
	Artifacts        []SessionArtifactSnapshot         `json:"artifacts,omitempty"`
	PendingApprovals []PendingApprovalSnapshot         `json:"pending_approvals,omitempty"`
	PendingHandoff   PendingHandoffSnapshot            `json:"pending_handoff,omitempty"`
	LastRouting      RoutingSnapshot                   `json:"last_routing,omitempty"`
	TaskStage        TaskStageSnapshot                 `json:"task_stage,omitempty"`
	PromptBudget     *schema.PromptBudget              `json:"prompt_budget,omitempty"`
	PromptBudgets    []schema.PromptBudget             `json:"prompt_budget_history,omitempty"`
	TokenUsages      []schema.TokenUsageSample         `json:"token_usage_history,omitempty"`
	UsageRecords     []UsageRecord                     `json:"usage_records,omitempty"`
	ApprovedTools    map[string][]string               `json:"approved_tools,omitempty"`
	ApprovalScopes   []ApprovalScope                   `json:"approval_scopes,omitempty"`
}

func copyWorkflowSnapshot(snapshot WorkflowSnapshot) WorkflowSnapshot {
	snapshot.TargetID = normalizeRunTargetID(snapshot.TargetID)
	if snapshot.PendingToolRisk != nil {
		risk := copyToolRiskProfile(*snapshot.PendingToolRisk)
		snapshot.PendingToolRisk = &risk
	}
	snapshot.PendingResponseMessage = schema.CopyMessage(snapshot.PendingResponseMessage)
	snapshot.ResourcePins = CopyResourcePins(snapshot.ResourcePins)
	snapshot.Attachments = append([]schema.AttachmentRef(nil), snapshot.Attachments...)
	return snapshot
}

func appendBounded(values []string, value string, max int) []string {
	values = append(values, value)
	if len(values) <= max {
		return values
	}
	return append([]string(nil), values[len(values)-max:]...)
}

func appendPromptBudgetHistory(values []schema.PromptBudget, value schema.PromptBudget) []schema.PromptBudget {
	values = append(values, value)
	if len(values) <= maxPromptBudgetHistory {
		return values
	}
	return append([]schema.PromptBudget(nil), values[len(values)-maxPromptBudgetHistory:]...)
}

func appendTokenUsageHistory(values []schema.TokenUsageSample, value schema.TokenUsageSample) []schema.TokenUsageSample {
	values = append(values, value)
	if len(values) <= maxTokenUsageHistory {
		return values
	}
	return append([]schema.TokenUsageSample(nil), values[len(values)-maxTokenUsageHistory:]...)
}

func copyPendingApprovalSnapshots(items []PendingApprovalSnapshot) []PendingApprovalSnapshot {
	if len(items) == 0 {
		return nil
	}
	copied := make([]PendingApprovalSnapshot, len(items))
	for i, item := range items {
		copied[i] = item
		copied[i].WorkflowResponse = schema.CopyMessage(item.WorkflowResponse)
		copied[i].WorkflowCalls = schema.CopyToolCalls(item.WorkflowCalls)
		copied[i].WorkflowBaseResults = append([]schema.ToolResult(nil), item.WorkflowBaseResults...)
		copied[i].WorkflowResults = append([]schema.ToolResult(nil), item.WorkflowResults...)
		copied[i].WorkflowMessages = schema.CopyMessages(item.WorkflowMessages)
		copied[i].WorkflowCollected = append([]schema.ToolResult(nil), item.WorkflowCollected...)
		if item.Risk != nil {
			risk := copyToolRiskProfile(*item.Risk)
			copied[i].Risk = &risk
		}
	}
	return copied
}

func copyToolRiskProfile(profile schema.ToolRiskProfile) schema.ToolRiskProfile {
	profile.Capabilities = append([]string(nil), profile.Capabilities...)
	profile.SandboxFeatures = append([]string(nil), profile.SandboxFeatures...)
	profile.MissingSandboxFeatures = append([]string(nil), profile.MissingSandboxFeatures...)
	if profile.WindowsIsolation != nil {
		windowsIsolation := *profile.WindowsIsolation
		profile.WindowsIsolation = &windowsIsolation
	}
	profile.Warnings = append([]string(nil), profile.Warnings...)
	profile.Recommendations = append([]string(nil), profile.Recommendations...)
	return profile
}

func approvedToolScopeKey(kind, toolName string) string {
	return kind + ":" + toolName
}

func parseApprovedToolScope(raw string) (string, string, bool) {
	for i, r := range raw {
		if r != ':' {
			continue
		}
		kind := raw[:i]
		name := raw[i+1:]
		if kind == "" || name == "" {
			return "", "", false
		}
		return kind, name, true
	}
	return "", "", false
}

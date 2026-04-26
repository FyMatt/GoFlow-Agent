package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// State keeps lightweight in-memory session context.
type State struct {
	mu               sync.RWMutex
	activeAgent      string
	mode             string
	maxHistory       int
	prompts          []string
	toolSummaries    []string
	lastSkill        string
	lastSkillMatch   *schema.SkillMatchDiagnostic
	workflow         WorkflowSnapshot
	pendingApprovals []PendingApprovalSnapshot
	pendingHandoff   PendingHandoffSnapshot
	lastRouting      RoutingSnapshot
	taskStage        TaskStageSnapshot
	approvedTools    map[string]map[string]struct{}
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
	s.workflow = snapshot
}

func (s *State) SetPendingApprovals(approvals []PendingApprovalSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingApprovals = append([]PendingApprovalSnapshot(nil), approvals...)
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
	pendingApprovals := append([]PendingApprovalSnapshot(nil), s.pendingApprovals...)
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
	return Snapshot{
		ActiveAgent:      s.activeAgent,
		Mode:             s.mode,
		RecentPrompts:    prompts,
		RecentTools:      tools,
		LastSkill:        s.lastSkill,
		LastSkillMatch:   lastSkillMatch,
		Workflow:         s.workflow,
		PendingApprovals: pendingApprovals,
		PendingHandoff:   s.pendingHandoff,
		LastRouting:      s.lastRouting,
		TaskStage:        s.taskStage,
		ApprovedTools:    approvedTools,
	}
}

func (s *State) Save(path string) error {
	if s == nil || path == "" {
		return nil
	}
	snapshot := s.Snapshot()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
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
	s.workflow = snapshot.Workflow
	s.pendingApprovals = append([]PendingApprovalSnapshot(nil), snapshot.PendingApprovals...)
	s.pendingHandoff = snapshot.PendingHandoff
	s.lastRouting = snapshot.LastRouting
	s.taskStage = snapshot.TaskStage
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
	return nil
}

// WorkflowSnapshot is a read-only view of current workflow state.
type WorkflowSnapshot struct {
	Name             string `json:"name,omitempty"`
	Status           string `json:"status,omitempty"`
	NextStage        string `json:"next_stage,omitempty"`
	Request          string `json:"request,omitempty"`
	Summary          string `json:"summary,omitempty"`
	LastApproval     string `json:"last_approval,omitempty"`
	PendingCallID    string `json:"pending_call_id,omitempty"`
	PendingToolName  string `json:"pending_tool_name,omitempty"`
	PendingAgentID   string `json:"pending_agent_id,omitempty"`
	PendingArguments string `json:"pending_arguments,omitempty"`
}

// PendingApprovalSnapshot is a read-only view of pending tool approval state.
type PendingApprovalSnapshot struct {
	CallID           string `json:"call_id,omitempty"`
	ToolName         string `json:"tool_name,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	ArgumentsSummary string `json:"arguments_summary,omitempty"`
	Arguments        string `json:"arguments,omitempty"`
	WorkflowName     string `json:"workflow_name,omitempty"`
	Stage            string `json:"stage,omitempty"`
	Request          string `json:"request,omitempty"`
	CompletedSummary string `json:"completed_summary,omitempty"`
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
	ActiveAgent      string                       `json:"active_agent"`
	Mode             string                       `json:"mode"`
	RecentPrompts    []string                     `json:"recent_prompts"`
	RecentTools      []string                     `json:"recent_tools"`
	LastSkill        string                       `json:"last_skill"`
	LastSkillMatch   *schema.SkillMatchDiagnostic `json:"last_skill_match,omitempty"`
	Workflow         WorkflowSnapshot             `json:"workflow,omitempty"`
	PendingApprovals []PendingApprovalSnapshot    `json:"pending_approvals,omitempty"`
	PendingHandoff   PendingHandoffSnapshot       `json:"pending_handoff,omitempty"`
	LastRouting      RoutingSnapshot              `json:"last_routing,omitempty"`
	TaskStage        TaskStageSnapshot            `json:"task_stage,omitempty"`
	ApprovedTools    map[string][]string          `json:"approved_tools,omitempty"`
}

func appendBounded(values []string, value string, max int) []string {
	values = append(values, value)
	if len(values) <= max {
		return values
	}
	return append([]string(nil), values[len(values)-max:]...)
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

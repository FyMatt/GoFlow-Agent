package agent

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

// TeamState is a read model for one workflow/team collaboration context.
type TeamState struct {
	RunID            string                                 `json:"run_id,omitempty"`
	Workflow         string                                 `json:"workflow,omitempty"`
	Status           string                                 `json:"status,omitempty"`
	Team             string                                 `json:"team,omitempty"`
	TeamStage        string                                 `json:"team_stage,omitempty"`
	ActiveOwner      string                                 `json:"active_owner,omitempty"`
	NextStage        string                                 `json:"next_stage,omitempty"`
	Template         *TeamTemplateSummary                   `json:"template,omitempty"`
	Messages         []session.CollaborationMessageSnapshot `json:"messages,omitempty"`
	Handoffs         []session.CollaborationMessageSnapshot `json:"handoffs,omitempty"`
	Blackboard       []session.BlackboardEntrySnapshot      `json:"blackboard,omitempty"`
	Decisions        []session.BlackboardEntrySnapshot      `json:"decisions,omitempty"`
	Critiques        []session.BlackboardEntrySnapshot      `json:"critiques,omitempty"`
	Questions        []session.BlackboardEntrySnapshot      `json:"questions,omitempty"`
	Risks            []session.BlackboardEntrySnapshot      `json:"risks,omitempty"`
	Assignments      []session.BlackboardEntrySnapshot      `json:"assignments,omitempty"`
	Escalations      []session.BlackboardEntrySnapshot      `json:"escalations,omitempty"`
	Approvals        []session.BlackboardEntrySnapshot      `json:"approvals,omitempty"`
	Rejections       []session.BlackboardEntrySnapshot      `json:"rejections,omitempty"`
	ApprovalGate     *TeamApprovalGateState                 `json:"approval_gate,omitempty"`
	UnresolvedItems  []session.BlackboardEntrySnapshot      `json:"unresolved_items,omitempty"`
	PendingApprovals []session.PendingApprovalSnapshot      `json:"pending_approvals,omitempty"`
	PendingInput     bool                                   `json:"pending_input,omitempty"`
}

// TeamApprovalGateState summarizes team-emitted approval/rejection packets
// against an optional quorum policy carried by executable team stage metadata.
type TeamApprovalGateState struct {
	Status    string   `json:"status,omitempty"`
	Required  int      `json:"required,omitempty"`
	Approved  int      `json:"approved,omitempty"`
	Rejected  int      `json:"rejected,omitempty"`
	Pending   int      `json:"pending,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	Approvers []string `json:"approvers,omitempty"`
	Rejectors []string `json:"rejectors,omitempty"`
}

// BuildTeamState derives a team-oriented collaboration view from a session snapshot.
func BuildTeamState(snapshot session.Snapshot, runID, team string) TeamState {
	return BuildTeamStateWithTemplateLookup(snapshot, runID, team, LoadTeamTemplate)
}

// BuildTeamStateWithTemplateLookup derives a team-oriented collaboration view using a runtime-aware template catalog.
func BuildTeamStateWithTemplateLookup(snapshot session.Snapshot, runID, team string, loadTeamTemplate func(string) (TeamTemplate, bool)) TeamState {
	if loadTeamTemplate == nil {
		loadTeamTemplate = LoadTeamTemplate
	}
	runID = strings.TrimSpace(runID)
	team = normalizePersistedWorkflowName(team)
	run, hasRun := selectTeamStateWorkflowRun(snapshot, runID)
	if runID == "" && hasRun {
		runID = run.ID
	}

	messages := filterTeamStateMessages(snapshot.Messages, runID, team)
	blackboard := filterTeamStateBlackboard(snapshot.Blackboard, runID, team)
	if team == "" {
		team = firstTeamStateName(run, messages, blackboard, loadTeamTemplate)
		if team != "" {
			messages = filterTeamStateMessages(snapshot.Messages, runID, team)
			blackboard = filterTeamStateBlackboard(snapshot.Blackboard, runID, team)
		}
	}

	state := TeamState{
		RunID:            runID,
		Team:             team,
		Messages:         messages,
		Handoffs:         teamStateHandoffs(messages),
		Blackboard:       blackboard,
		Decisions:        teamStateEntriesByKind(blackboard, "team_decision"),
		Critiques:        teamStateEntriesByKind(blackboard, "team_critique"),
		Questions:        teamStateEntriesByKind(blackboard, "team_unresolved_question"),
		Risks:            teamStateEntriesByKind(blackboard, "team_risk"),
		Assignments:      teamStateAssignedItems(blackboard),
		Escalations:      teamStateEscalatedItems(blackboard),
		Approvals:        teamStateEntriesByKind(blackboard, "team_approval"),
		Rejections:       teamStateEntriesByKind(blackboard, "team_rejection"),
		UnresolvedItems:  teamStateUnresolvedItems(blackboard),
		PendingApprovals: filterTeamStateApprovals(snapshot.PendingApprovals, run.Name),
	}
	state.ApprovalGate = teamStateApprovalGate(blackboard)
	if hasRun {
		state.Workflow = run.Name
		state.Status = run.Status
		state.NextStage = run.NextStage
		state.PendingInput = strings.EqualFold(run.Status, "awaiting_input") || len(run.PendingFields) > 0
		state.TeamStage = teamStateStageName(run, team)
	}
	if strings.TrimSpace(state.Workflow) == "" && strings.TrimSpace(snapshot.Workflow.Name) != "" && (runID == "" || runID == snapshot.Workflow.RunID) {
		state.Workflow = snapshot.Workflow.Name
		state.Status = snapshot.Workflow.Status
		state.NextStage = snapshot.Workflow.NextStage
	}
	if team != "" {
		if template, ok := loadTeamTemplate(team); ok {
			summary := template.TeamTemplateSummary
			summary.Roles = len(template.RoleTemplates)
			state.Template = &summary
		}
	}
	state.ActiveOwner = teamStateActiveOwner(state, run, messages, blackboard)
	return state
}

func selectTeamStateWorkflowRun(snapshot session.Snapshot, runID string) (session.WorkflowRunSnapshot, bool) {
	runID = strings.TrimSpace(runID)
	if runID != "" {
		for _, run := range snapshot.WorkflowRuns {
			if run.ID == runID {
				return run, true
			}
		}
		return session.WorkflowRunSnapshot{}, false
	}
	if strings.TrimSpace(snapshot.Workflow.RunID) != "" {
		for _, run := range snapshot.WorkflowRuns {
			if run.ID == snapshot.Workflow.RunID {
				return run, true
			}
		}
		return session.WorkflowRunSnapshot{ID: snapshot.Workflow.RunID, Name: snapshot.Workflow.Name, Status: snapshot.Workflow.Status, NextStage: snapshot.Workflow.NextStage, Summary: snapshot.Workflow.Summary}, true
	}
	if len(snapshot.WorkflowRuns) > 0 {
		return snapshot.WorkflowRuns[0], true
	}
	return session.WorkflowRunSnapshot{}, false
}

func filterTeamStateMessages(messages []session.CollaborationMessageSnapshot, runID, team string) []session.CollaborationMessageSnapshot {
	out := make([]session.CollaborationMessageSnapshot, 0, len(messages))
	for _, message := range messages {
		if runID != "" && message.RunID != runID {
			continue
		}
		if team != "" && !teamStateHasTeam(message.Metadata, nil, team) {
			continue
		}
		out = append(out, message)
	}
	return out
}

func filterTeamStateBlackboard(entries []session.BlackboardEntrySnapshot, runID, team string) []session.BlackboardEntrySnapshot {
	out := make([]session.BlackboardEntrySnapshot, 0, len(entries))
	for _, entry := range entries {
		if runID != "" && entry.RunID != runID {
			continue
		}
		if team != "" && !teamStateHasTeam(entry.Metadata, entry.Tags, team) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func filterTeamStateApprovals(approvals []session.PendingApprovalSnapshot, workflowName string) []session.PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	workflowName = strings.TrimSpace(workflowName)
	out := make([]session.PendingApprovalSnapshot, 0, len(approvals))
	for _, approval := range approvals {
		if workflowName == "" || strings.EqualFold(strings.TrimSpace(approval.WorkflowName), workflowName) {
			out = append(out, approval)
		}
	}
	return out
}

func firstTeamStateName(run session.WorkflowRunSnapshot, messages []session.CollaborationMessageSnapshot, blackboard []session.BlackboardEntrySnapshot, loadTeamTemplate func(string) (TeamTemplate, bool)) string {
	for _, stage := range run.CompletedStages {
		if team := normalizePersistedWorkflowName(stage.Outputs["team"]); team != "" {
			return team
		}
	}
	for _, entry := range blackboard {
		if team := normalizePersistedWorkflowName(entry.Metadata["team"]); team != "" {
			return team
		}
		for _, tag := range entry.Tags {
			if _, ok := loadTeamTemplate(tag); ok {
				return normalizePersistedWorkflowName(tag)
			}
		}
	}
	for _, message := range messages {
		if team := normalizePersistedWorkflowName(message.Metadata["team"]); team != "" {
			return team
		}
	}
	return ""
}

func teamStateStageName(run session.WorkflowRunSnapshot, team string) string {
	for _, stage := range run.CompletedStages {
		if normalizeWorkflowSkillName(stage.NodeType) != "team" && normalizeWorkflowSkillName(stage.NodeType) != "agent_team" && normalizeWorkflowSkillName(stage.NodeType) != "team_template" {
			continue
		}
		if team == "" || normalizePersistedWorkflowName(stage.Outputs["team"]) == team {
			return stage.Stage
		}
	}
	return ""
}

func teamStateHandoffs(messages []session.CollaborationMessageSnapshot) []session.CollaborationMessageSnapshot {
	out := make([]session.CollaborationMessageSnapshot, 0)
	for _, message := range messages {
		if strings.Contains(strings.ToLower(strings.TrimSpace(message.Kind)), "handoff") {
			out = append(out, message)
		}
	}
	return out
}

func teamStateUnresolvedItems(entries []session.BlackboardEntrySnapshot) []session.BlackboardEntrySnapshot {
	out := make([]session.BlackboardEntrySnapshot, 0)
	for _, entry := range entries {
		if !teamStateOpenStatus(entry.Status) {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(entry.Kind))
		switch kind {
		case "decision", "approval", "input", "issue", "finding", "findings", "handoff", "team_assignment", "team_escalation", "team_critique", "team_unresolved_question", "team_risk", "team_rejection":
			out = append(out, entry)
		}
	}
	return out
}

func teamStateAssignedItems(entries []session.BlackboardEntrySnapshot) []session.BlackboardEntrySnapshot {
	out := make([]session.BlackboardEntrySnapshot, 0)
	for _, entry := range entries {
		if !teamStateOpenStatus(entry.Status) {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(entry.Kind))
		if kind == "team_assignment" ||
			strings.TrimSpace(entry.Metadata["assigned_agent"]) != "" ||
			strings.TrimSpace(entry.Metadata["owner_agent"]) != "" ||
			strings.TrimSpace(entry.Metadata["owner_role"]) != "" {
			out = append(out, entry)
		}
	}
	return out
}

func teamStateEscalatedItems(entries []session.BlackboardEntrySnapshot) []session.BlackboardEntrySnapshot {
	out := make([]session.BlackboardEntrySnapshot, 0)
	for _, entry := range entries {
		kind := strings.ToLower(strings.TrimSpace(entry.Kind))
		status := strings.ToLower(strings.TrimSpace(entry.Status))
		if kind == "team_escalation" ||
			status == "escalated" ||
			strings.EqualFold(strings.TrimSpace(entry.Metadata["lifecycle_action"]), "escalate") ||
			strings.EqualFold(strings.TrimSpace(entry.Metadata["escalated"]), "true") ||
			strings.TrimSpace(entry.Metadata["escalated_to"]) != "" {
			out = append(out, entry)
		}
	}
	return out
}

func teamStateApprovalGate(entries []session.BlackboardEntrySnapshot) *TeamApprovalGateState {
	policy := teamStateApprovalGatePolicy(entries)
	if policy.Required == 0 && len(policy.Roles) == 0 {
		return nil
	}
	roleSet := make(map[string]struct{}, len(policy.Roles))
	for _, role := range policy.Roles {
		roleSet[normalizeWorkflowSkillName(role)] = struct{}{}
	}
	approvers := make(map[string]struct{})
	rejectors := make(map[string]struct{})
	for _, entry := range entries {
		role := teamStateEntryRole(entry)
		if len(roleSet) > 0 {
			if _, ok := roleSet[normalizeWorkflowSkillName(role)]; !ok {
				continue
			}
		}
		switch strings.ToLower(strings.TrimSpace(entry.Kind)) {
		case "team_approval":
			if role != "" {
				approvers[role] = struct{}{}
			}
		case "team_rejection":
			if role != "" {
				rejectors[role] = struct{}{}
			}
		}
	}
	required := policy.Required
	if required <= 0 && len(policy.Roles) > 0 {
		required = len(policy.Roles)
	}
	if required <= 0 {
		required = 1
	}
	state := &TeamApprovalGateState{
		Required:  required,
		Approved:  len(approvers),
		Rejected:  len(rejectors),
		Roles:     policy.Roles,
		Approvers: teamStateSortedKeys(approvers),
		Rejectors: teamStateSortedKeys(rejectors),
	}
	if state.Approved < required {
		state.Pending = required - state.Approved
	}
	if policy.RejectBlocks && state.Rejected > 0 {
		state.Status = "blocked"
	} else if state.Approved >= required {
		state.Status = "passed"
	} else {
		state.Status = "pending"
	}
	return state
}

type teamApprovalGatePolicy struct {
	Required        int
	Roles           []string
	RejectBlocks    bool
	RejectBlocksSet bool
}

func teamStateApprovalGatePolicy(entries []session.BlackboardEntrySnapshot) teamApprovalGatePolicy {
	var policy teamApprovalGatePolicy
	for _, entry := range entries {
		if policy.Required == 0 {
			policy.Required = teamStateMetadataInt(entry.Metadata, "approval_quorum", "review_quorum", "quorum")
		}
		if len(policy.Roles) == 0 {
			policy.Roles = teamStateMetadataList(entry.Metadata, "approval_roles", "review_roles", "quorum_roles")
		}
		if !policy.RejectBlocksSet {
			if value, ok := teamStateMetadataBoolValue(entry.Metadata, "reject_blocks", "rejection_blocks", "review_reject_blocks"); ok {
				policy.RejectBlocks = value
				policy.RejectBlocksSet = true
			}
		}
	}
	return policy
}

func teamStateEntriesByKind(entries []session.BlackboardEntrySnapshot, kind string) []session.BlackboardEntrySnapshot {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	out := make([]session.BlackboardEntrySnapshot, 0)
	for _, entry := range entries {
		if strings.ToLower(strings.TrimSpace(entry.Kind)) == kind {
			out = append(out, entry)
		}
	}
	return out
}

func teamStateOpenStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "open", "running", "pending", "blocked", "failed", "cancelled", "escalated", "awaiting_approval", "awaiting_input":
		return true
	case "resolved", "closed", "completed", "done", "deleted", "accepted", "approved":
		return false
	default:
		return true
	}
}

func teamStateHasTeam(metadata map[string]string, tags []string, team string) bool {
	team = normalizePersistedWorkflowName(team)
	if team == "" {
		return true
	}
	if normalizePersistedWorkflowName(metadata["team"]) == team {
		return true
	}
	for _, tag := range tags {
		if normalizePersistedWorkflowName(tag) == team {
			return true
		}
	}
	return false
}

func teamStateActiveOwner(state TeamState, run session.WorkflowRunSnapshot, messages []session.CollaborationMessageSnapshot, blackboard []session.BlackboardEntrySnapshot) string {
	if len(state.PendingApprovals) > 0 && strings.TrimSpace(state.PendingApprovals[0].AgentID) != "" {
		return state.PendingApprovals[0].AgentID
	}
	for _, entries := range [][]session.BlackboardEntrySnapshot{state.Escalations, state.Assignments, state.UnresolvedItems} {
		for _, entry := range entries {
			if owner := teamStateEntryOwner(entry); owner != "" {
				return owner
			}
		}
	}
	for _, entry := range blackboard {
		if !teamStateOpenStatus(entry.Status) {
			continue
		}
		if owner := teamStateEntryOwner(entry); owner != "" {
			return owner
		}
	}
	if strings.TrimSpace(run.NextStage) != "" {
		for _, stage := range run.CompletedStages {
			if strings.EqualFold(stage.Stage, run.NextStage) && strings.TrimSpace(stage.AgentID) != "" {
				return stage.AgentID
			}
		}
	}
	for _, message := range messages {
		if strings.TrimSpace(message.ToAgent) != "" {
			return message.ToAgent
		}
		if strings.TrimSpace(message.FromAgent) != "" {
			return message.FromAgent
		}
	}
	for _, stage := range run.CompletedStages {
		if strings.TrimSpace(stage.AgentID) != "" {
			return stage.AgentID
		}
	}
	if state.Template != nil {
		return state.Template.RecommendedEntryAgent
	}
	return ""
}

func teamStateEntryRole(entry session.BlackboardEntrySnapshot) string {
	for _, key := range []string{"role", "team_role", "owner_role"} {
		if value := strings.TrimSpace(entry.Metadata[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(entry.Stage)
}

func teamStateMetadataInt(metadata map[string]string, keys ...string) int {
	for _, key := range keys {
		value := strings.TrimSpace(metadata[key])
		if value == "" {
			continue
		}
		n := 0
		for _, r := range value {
			if r < '0' || r > '9' {
				n = 0
				break
			}
			n = n*10 + int(r-'0')
		}
		if n > 0 {
			return n
		}
	}
	return 0
}

func teamStateMetadataList(metadata map[string]string, keys ...string) []string {
	for _, key := range keys {
		value := strings.TrimSpace(metadata[key])
		if value == "" {
			continue
		}
		out := make([]string, 0)
		seen := make(map[string]struct{})
		for _, item := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ';' || r == '|' || r == '\n' || r == '\t' || r == ' '
		}) {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			key := normalizeWorkflowSkillName(item)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func teamStateMetadataBool(metadata map[string]string, keys ...string) bool {
	value, _ := teamStateMetadataBoolValue(metadata, keys...)
	return value
}

func teamStateMetadataBoolValue(metadata map[string]string, keys ...string) (bool, bool) {
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(metadata[key])) {
		case "1", "true", "yes", "y", "on", "required", "block", "blocking":
			return true, true
		case "0", "false", "no", "n", "off", "optional", "allow", "nonblocking", "non_blocking", "not_required":
			return false, true
		}
	}
	return false, false
}

func teamStateSortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && strings.ToLower(out[j]) < strings.ToLower(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func teamStateEntryOwner(entry session.BlackboardEntrySnapshot) string {
	for _, key := range []string{"escalated_to", "assigned_agent", "owner_agent", "entry_agent", "agent_id"} {
		if value := strings.TrimSpace(entry.Metadata[key]); value != "" {
			return value
		}
	}
	if strings.TrimSpace(entry.AgentID) != "" {
		return entry.AgentID
	}
	return ""
}

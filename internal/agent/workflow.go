package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const (
	workflowNamePlanFixAudit = "plan-fix-audit"
	workflowNameSkillChain   = "skill-chain"

	workflowAgentPlanner = "planner"
	workflowAgentFixer   = "fixer"
	workflowAgentAuditor = "auditor"
	workflowAgentChat    = "chat"
)

// WorkflowRunner coordinates built-in staged workflows.
type WorkflowRunner struct {
	runtime  *Runtime
	registry map[string]workflowDefinition
}

type workflowDefinition struct {
	run    func(*WorkflowRunner, context.Context, string, bool, func(event schema.StreamEvent) error) (WorkflowResult, error)
	resume func(*WorkflowRunner, context.Context, pendingApproval, bool, func(event schema.StreamEvent) error) (WorkflowResult, error)
}

// WorkflowStage identifies the stage currently running.
type WorkflowStage string

const (
	WorkflowStagePlan  WorkflowStage = "plan"
	WorkflowStageFix   WorkflowStage = "fix"
	WorkflowStageAudit WorkflowStage = "audit"
)

// WorkflowStageResult captures the outcome of one workflow stage.
type WorkflowStageResult struct {
	Stage  WorkflowStage      `json:"stage"`
	Agent  string             `json:"agent"`
	Result schema.AgentResult `json:"result"`
}

// WorkflowResult represents a workflow run or resume outcome.
type WorkflowResult struct {
	Name            string                `json:"name"`
	Status          string                `json:"status"`
	CompletedStages []WorkflowStageResult `json:"completed_stages,omitempty"`
	PendingApproval bool                  `json:"pending_approval,omitempty"`
	ApprovalPrompt  string                `json:"approval_prompt,omitempty"`
	NextStage       WorkflowStage         `json:"next_stage,omitempty"`
	FinalSummary    string                `json:"final_summary,omitempty"`
}

// NewWorkflowRunner constructs a workflow helper for a runtime.
func NewWorkflowRunner(rt *Runtime) *WorkflowRunner {
	return &WorkflowRunner{runtime: rt, registry: builtInWorkflowRegistry()}
}

// Run dispatches a named workflow.
func (w *WorkflowRunner) Run(ctx context.Context, name, request string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	definition, ok, err := w.workflowDefinition(name)
	if err != nil {
		return WorkflowResult{}, err
	}
	if !ok || definition.run == nil {
		return WorkflowResult{}, fmt.Errorf("unknown workflow: %s", name)
	}
	return definition.run(w, ctx, request, approve, handler)
}

// Resume resumes a suspended workflow.
func (w *WorkflowRunner) Resume(ctx context.Context, name, callID string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	definition, ok, err := w.workflowDefinition(name)
	if err != nil {
		return WorkflowResult{}, err
	}
	if !ok || definition.resume == nil {
		return WorkflowResult{}, fmt.Errorf("unknown workflow: %s", name)
	}
	pending, ok := w.runtime.PendingApproval(callID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("pending approval not found: %s", callID)
	}
	return definition.resume(w, ctx, pending, approve, handler)
}

func builtInWorkflowRegistry() map[string]workflowDefinition {
	return map[string]workflowDefinition{
		workflowNamePlanFixAudit: {
			run: func(w *WorkflowRunner, ctx context.Context, request string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
				return w.RunPlanFixAudit(ctx, request, approve, handler)
			},
			resume: resumePlanFixAuditWorkflow,
		},
		workflowNameSkillChain: {
			run: func(w *WorkflowRunner, ctx context.Context, request string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
				return w.RunSkillChain(ctx, request, approve, handler)
			},
			resume: resumeSkillChainWorkflow,
		},
	}
}

func (w *WorkflowRunner) workflow(name string) (workflowDefinition, bool) {
	definition, ok, _ := w.workflowDefinition(name)
	return definition, ok
}

func (w *WorkflowRunner) workflowDefinition(name string) (workflowDefinition, bool, error) {
	if w == nil {
		return workflowDefinition{}, false, nil
	}
	if w.registry == nil {
		w.registry = builtInWorkflowRegistry()
	}
	definition, ok := w.registry[strings.TrimSpace(strings.ToLower(name))]
	if !ok {
		return w.workflowFromGraph(name)
	}
	return definition, ok, nil
}

// RunSkillChain executes the matched skill and its declared next_skills in order.
func (w *WorkflowRunner) RunSkillChain(ctx context.Context, request string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	_ = approve
	if w == nil || w.runtime == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	if w.runtime.skills == nil {
		return WorkflowResult{}, fmt.Errorf("skill manager not configured")
	}
	first, _, ok := w.runtime.skills.MatchWithDiagnostics(request)
	if !ok || first == nil {
		return WorkflowResult{}, fmt.Errorf("no skill matched request for workflow %s", workflowNameSkillChain)
	}
	chain := w.resolveSkillChain(*first)
	if len(chain) == 0 {
		return WorkflowResult{}, fmt.Errorf("no executable skills found for workflow %s", workflowNameSkillChain)
	}
	w.runtime.DisableWorkflowAutoApproval(workflowNameSkillChain)
	return w.runSkillChainFrom(ctx, request, chain, 0, nil, handler)
}

func (w *WorkflowRunner) runSkillChainFrom(ctx context.Context, request string, chain []schema.Skill, start int, completed []WorkflowStageResult, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	for index := start; index < len(chain); index++ {
		skill := chain[index]
		agentID, err := w.agentForSkill(skill)
		if err != nil {
			return WorkflowResult{}, err
		}
		if err := w.ensureWorkflowAgent(agentID); err != nil {
			return WorkflowResult{}, err
		}
		stage := WorkflowStage(skill.Name)
		stagePrompt := buildSkillChainStagePrompt(request, skill, completed)
		w.persistWorkflowState(workflowNameSkillChain, "running", stage, request, summarizeWorkflow(completed), "")
		if err := w.runtime.SetActiveAgent(agentID); err != nil {
			return WorkflowResult{}, err
		}
		result, err := runSkillStage(ctx, w.runtime, agentID, stagePrompt, skill, handler)
		if err != nil {
			return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", stage, err)
		}
		if hasSuspendedToolResult(result.ToolResults) {
			w.captureSkillChainApprovalContext(stage, request, result.Output, completed, result.ToolResults, chain, index, stagePrompt)
			approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), agentID)
			w.recordWorkflowSummary(fmt.Sprintf("workflow %s awaiting tool approval", workflowNameSkillChain))
			w.persistWorkflowState(workflowNameSkillChain, "awaiting_tool_approval", stage, request, summarizeWorkflow(completed), approvalPrompt)
			return WorkflowResult{
				Name:            workflowNameSkillChain,
				Status:          "awaiting_tool_approval",
				PendingApproval: true,
				ApprovalPrompt:  approvalPrompt,
				CompletedStages: completed,
				NextStage:       stage,
			}, nil
		}
		completed = append(completed, WorkflowStageResult{Stage: stage, Agent: agentID, Result: result})
		chain = w.extendSkillChain(chain, index, request, result.Output, completed)
	}
	finalSummary := summarizeWorkflow(completed)
	w.runtime.DisableWorkflowAutoApproval(workflowNameSkillChain)
	if err := w.runtime.RestoreDefaultAgent(); err != nil {
		return WorkflowResult{}, err
	}
	w.persistWorkflowState(workflowNameSkillChain, "completed", "", request, finalSummary, "")
	return WorkflowResult{Name: workflowNameSkillChain, Status: "completed", CompletedStages: completed, FinalSummary: finalSummary}, nil
}

func (w *WorkflowRunner) resolveSkillChain(first schema.Skill) []schema.Skill {
	key := normalizeWorkflowSkillName(first.Name)
	if key == "" {
		return nil
	}
	return []schema.Skill{first}
}

func (w *WorkflowRunner) extendSkillChain(chain []schema.Skill, currentIndex int, request, stageOutput string, completed []WorkflowStageResult) []schema.Skill {
	if w == nil || w.runtime == nil || w.runtime.skills == nil || currentIndex < 0 || currentIndex >= len(chain) || len(chain) >= 8 {
		return chain
	}
	current := chain[currentIndex]
	if len(current.NextSkills) == 0 {
		return chain
	}
	candidates := w.nextSkillCandidates(current, chain, completed)
	if len(candidates) == 0 {
		return chain
	}
	selected := selectNextSkillCandidates(current, candidates, request, stageOutput)
	for _, skill := range selected {
		if len(chain) >= 8 {
			break
		}
		chain = append(chain, skill)
	}
	return chain
}

func (w *WorkflowRunner) nextSkillCandidates(current schema.Skill, chain []schema.Skill, completed []WorkflowStageResult) []schema.Skill {
	byName := make(map[string]schema.Skill)
	for _, skill := range w.runtime.skills.List() {
		byName[normalizeWorkflowSkillName(skill.Name)] = skill
	}
	seen := make(map[string]struct{})
	for _, skill := range chain {
		if key := normalizeWorkflowSkillName(skill.Name); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, stage := range completed {
		if key := normalizeWorkflowSkillName(string(stage.Stage)); key != "" {
			seen[key] = struct{}{}
		}
	}
	candidates := make([]schema.Skill, 0, len(current.NextSkills))
	for _, nextName := range current.NextSkills {
		key := normalizeWorkflowSkillName(nextName)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		next, ok := byName[key]
		if !ok {
			continue
		}
		candidates = append(candidates, next)
	}
	return candidates
}

func selectNextSkillCandidates(current schema.Skill, candidates []schema.Skill, request, stageOutput string) []schema.Skill {
	switch strings.ToLower(strings.TrimSpace(current.Metadata["next_strategy"])) {
	case "select", "best", "conditional", "planner_select", "planner-select", "dynamic", "graph":
		if explicit := filterExplicitNextSkillCandidates(parseExplicitNextSkillNames(stageOutput), candidates); len(explicit) > 0 {
			return explicit
		}
		bestIndex := -1
		bestScore := 0
		context := strings.TrimSpace(request + "\n" + stageOutput)
		normalizedContext := normalizeWorkflowSkillName(context)
		for i, candidate := range candidates {
			score := scoreWorkflowSkillCandidate(normalizedContext, candidate)
			if score > bestScore {
				bestScore = score
				bestIndex = i
			}
		}
		if bestIndex < 0 {
			return nil
		}
		return []schema.Skill{candidates[bestIndex]}
	default:
		return candidates
	}
}

func parseExplicitNextSkillNames(output string) []string {
	markers := []string{"goflow next skills:", "goflow next skill:", "next skills:", "next skill:", "next_skills:", "next_skill:"}
	names := make([]string, 0)
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(strings.TrimLeft(rawLine, "-*0123456789. "))
		lower := strings.ToLower(line)
		for _, marker := range markers {
			if !strings.HasPrefix(lower, marker) {
				continue
			}
			value := strings.TrimSpace(line[len(marker):])
			names = append(names, splitNextSkillNames(value)...)
		}
	}
	return uniqueWorkflowSkillNames(names)
}

func splitNextSkillNames(value string) []string {
	replacer := strings.NewReplacer(
		"[", "", "]", "",
		"(", "", ")", "",
		"{", "", "}", "",
		"`", "", `"`, "", `'`, "",
		"->", ",", "|", ",",
		" then ", ",", " Then ", ",", " THEN ", ",",
	)
	value = replacer.Replace(value)
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ';', '\u3001', '\uff0c', '\uff1b':
			return true
		default:
			return false
		}
	})
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		name = strings.TrimPrefix(name, "@")
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func uniqueWorkflowSkillNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		key := normalizeWorkflowSkillName(value)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func filterExplicitNextSkillCandidates(names []string, candidates []schema.Skill) []schema.Skill {
	if len(names) == 0 || len(candidates) == 0 {
		return nil
	}
	byName := make(map[string]schema.Skill, len(candidates))
	for _, candidate := range candidates {
		byName[normalizeWorkflowSkillName(candidate.Name)] = candidate
	}
	selected := make([]schema.Skill, 0, len(names))
	for _, name := range names {
		candidate, ok := byName[normalizeWorkflowSkillName(name)]
		if !ok {
			continue
		}
		selected = append(selected, candidate)
	}
	return selected
}

func scoreWorkflowSkillCandidate(normalizedContext string, candidate schema.Skill) int {
	score := 0
	for _, keyword := range candidate.Activation.Keywords {
		if strings.Contains(normalizedContext, normalizeWorkflowSkillName(keyword)) {
			score += 3
		}
	}
	if strings.Contains(normalizedContext, normalizeWorkflowSkillName(candidate.Name)) {
		score += 2
	}
	if strings.Contains(normalizedContext, normalizeWorkflowSkillName(candidate.Description)) {
		score++
	}
	return score
}

func (w *WorkflowRunner) agentForSkill(skill schema.Skill) (string, error) {
	if agentID := strings.TrimSpace(skill.PreferredAgent); agentID != "" {
		if _, ok := w.runtime.Profile(agentID); ok {
			return agentID, nil
		}
	}
	switch strings.ToLower(strings.TrimSpace(skill.Mode)) {
	case "plan":
		if _, ok := w.runtime.Profile(workflowAgentPlanner); ok {
			return workflowAgentPlanner, nil
		}
	case "fix":
		if _, ok := w.runtime.Profile(workflowAgentFixer); ok {
			return workflowAgentFixer, nil
		}
	case "audit":
		if _, ok := w.runtime.Profile(workflowAgentAuditor); ok {
			return workflowAgentAuditor, nil
		}
	case "chat":
		if _, ok := w.runtime.Profile(workflowAgentChat); ok {
			return workflowAgentChat, nil
		}
	}
	if agentID := w.runtime.defaultAgentID(); agentID != "" {
		if _, ok := w.runtime.Profile(agentID); ok {
			return agentID, nil
		}
	}
	return "", fmt.Errorf("no configured agent can run skill %s", skill.Name)
}

func buildSkillChainStagePrompt(request string, skill schema.Skill, completed []WorkflowStageResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Run workflow skill %q for the following request.\n\nOriginal request:\n%s\n", skill.Name, request)
	if len(completed) > 0 {
		builder.WriteString("\nCompleted prior skill stages:\n")
		for _, stage := range completed {
			fmt.Fprintf(&builder, "- %s/%s: %s\n", stage.Stage, stage.Agent, truncateSummary(stage.Result.Output))
		}
	}
	builder.WriteString("\nUse the matched skill instructions for this stage. Produce a concise stage result that the next skill can use.")
	return builder.String()
}

func runSkillStage(ctx context.Context, runtimeRef *Runtime, agentID, input string, skill schema.Skill, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	runner, ok := runtimeRef.runners[agentID]
	if !ok {
		return schema.AgentResult{}, fmt.Errorf("workflow agent not configured: %s", agentID)
	}
	manager := fixedSkillManager{skill: &skill}
	return runner.RunStream(ctx, input, manager, runtimeRef.mcp, runtimeRef.session, runtimeRef.audit, runtimeRef, handler)
}

type fixedSkillManager struct {
	skill *schema.Skill
}

func (m fixedSkillManager) List() []schema.Skill {
	if m.skill == nil {
		return nil
	}
	return []schema.Skill{*m.skill}
}

func (m fixedSkillManager) Match(string) (*schema.Skill, bool) {
	if m.skill == nil {
		return nil, false
	}
	return m.skill, true
}

func (m fixedSkillManager) MatchWithDiagnostics(string) (*schema.Skill, schema.SkillMatchDiagnostic, bool) {
	if m.skill == nil {
		return nil, schema.SkillMatchDiagnostic{}, false
	}
	return m.skill, schema.SkillMatchDiagnostic{SkillName: m.skill.Name, Score: 1, NameHit: true, Reason: "workflow skill-chain stage"}, true
}

func normalizeWorkflowSkillName(input string) string {
	replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", "\\", "_")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(input)))
}

func copySkillChain(chain []schema.Skill) []schema.Skill {
	if len(chain) == 0 {
		return nil
	}
	copied := make([]schema.Skill, len(chain))
	copy(copied, chain)
	return copied
}

// RunPlanFixAudit executes the planner �?fixer �?auditor workflow.
func (w *WorkflowRunner) RunPlanFixAudit(ctx context.Context, request string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	_ = approve
	if w == nil || w.runtime == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	w.runtime.DisableWorkflowAutoApproval(workflowNamePlanFixAudit)
	if err := w.ensureWorkflowAgent(workflowAgentPlanner); err != nil {
		return WorkflowResult{}, err
	}
	if err := w.runtime.SetActiveAgent(workflowAgentPlanner); err != nil {
		return WorkflowResult{}, err
	}
	planPrompt := fmt.Sprintf("Create a concise implementation plan for the following request. Focus on concrete code changes, tests, and risks.\n\nRequest:\n%s", request)
	w.persistWorkflowState(workflowNamePlanFixAudit, "running", WorkflowStagePlan, request, "", "")
	planResult, err := w.runtime.RunStream(ctx, planPrompt, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", WorkflowStagePlan, err)
	}
	completed := []WorkflowStageResult{{Stage: WorkflowStagePlan, Agent: workflowAgentPlanner, Result: planResult}}
	w.persistWorkflowState(workflowNamePlanFixAudit, "awaiting_approval", WorkflowStageFix, request, summarizeWorkflow(completed), "Workflow requires approval before running fixer.")
	if !approve {
		w.recordWorkflowSummary(fmt.Sprintf("workflow %s awaiting stage approval", workflowNamePlanFixAudit))
		return WorkflowResult{
			Name:            workflowNamePlanFixAudit,
			Status:          "awaiting_approval",
			PendingApproval: true,
			ApprovalPrompt:  "Workflow requires approval before running fixer.",
			CompletedStages: completed,
			NextStage:       WorkflowStageFix,
		}, nil
	}
	w.persistWorkflowState(workflowNamePlanFixAudit, "running", WorkflowStageFix, request, summarizeWorkflow(completed), "")

	if err := w.ensureWorkflowAgent(workflowAgentFixer); err != nil {
		return WorkflowResult{}, err
	}
	if err := w.runtime.SetActiveAgent(workflowAgentFixer); err != nil {
		return WorkflowResult{}, err
	}
	fixPrompt := fmt.Sprintf("Implement the approved plan for the following request. Reuse the provided plan, make minimal necessary changes, and summarize what changed.\n\nOriginal request:\n%s\n\nApproved plan:\n%s", request, planResult.Output)
	fixResult, err := continueAgentRun(ctx, w.runtime, workflowAgentFixer, fixPrompt, schema.ToolCall{}, "", schema.ToolResult{}, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", WorkflowStageFix, err)
	}
	if hasSuspendedToolResult(fixResult.ToolResults) {
		w.captureApprovalContext(workflowNamePlanFixAudit, WorkflowStageFix, request, fixResult.Output, completed, fixResult.ToolResults)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(fixResult.ToolResults), workflowAgentFixer)
		w.recordWorkflowSummary(fmt.Sprintf("workflow %s awaiting tool approval", workflowNamePlanFixAudit))
		w.persistWorkflowState(workflowNamePlanFixAudit, "awaiting_tool_approval", WorkflowStageFix, request, summarizeWorkflow(completed), approvalPrompt)
		return WorkflowResult{
			Name:            workflowNamePlanFixAudit,
			Status:          "awaiting_tool_approval",
			PendingApproval: true,
			ApprovalPrompt:  approvalPrompt,
			CompletedStages: completed,
			NextStage:       WorkflowStageFix,
		}, nil
	}
	completed = append(completed, WorkflowStageResult{Stage: WorkflowStageFix, Agent: workflowAgentFixer, Result: fixResult})

	if err := w.ensureWorkflowAgent(workflowAgentAuditor); err != nil {
		return WorkflowResult{}, err
	}
	if err := w.runtime.SetActiveAgent(workflowAgentAuditor); err != nil {
		return WorkflowResult{}, err
	}
	auditPrompt := fmt.Sprintf("Audit the completed implementation against the original request and approved plan. Identify risks, regressions, and any follow-up actions.\n\nOriginal request:\n%s\n\nPlan summary:\n%s\n\nImplementation summary:\n%s", request, planResult.Output, fixResult.Output)
	auditResult, err := w.runtime.RunStream(ctx, auditPrompt, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", WorkflowStageAudit, err)
	}
	completed = append(completed, WorkflowStageResult{Stage: WorkflowStageAudit, Agent: workflowAgentAuditor, Result: auditResult})
	finalSummary := summarizeWorkflow(completed)
	w.runtime.DisableWorkflowAutoApproval(workflowNamePlanFixAudit)
	if err := w.runtime.RestoreDefaultAgent(); err != nil {
		return WorkflowResult{}, err
	}
	w.persistWorkflowState(workflowNamePlanFixAudit, "completed", "", request, finalSummary, fixResult.Output)
	return WorkflowResult{Name: workflowNamePlanFixAudit, Status: "completed", CompletedStages: completed, FinalSummary: finalSummary}, nil
}

func (w *WorkflowRunner) ensureWorkflowAgent(agentID string) error {
	if w == nil || w.runtime == nil {
		return fmt.Errorf("workflow runtime not configured")
	}
	if _, ok := w.runtime.Profile(agentID); !ok {
		return fmt.Errorf("workflow agent not configured: %s", agentID)
	}
	return nil
}

func (w *WorkflowRunner) recordWorkflowSummary(summary string) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return
	}
	w.runtime.session.SetWorkflow(session.WorkflowSnapshot{Summary: summary})
}

func (w *WorkflowRunner) persistWorkflowState(name, status string, nextStage WorkflowStage, request, summary, prompt string) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return
	}
	snapshot := session.WorkflowSnapshot{
		Name:         name,
		Status:       status,
		NextStage:    string(nextStage),
		Request:      request,
		Summary:      summary,
		LastApproval: prompt,
	}
	if status == "awaiting_tool_approval" {
		for _, pending := range w.runtime.SessionSnapshot().PendingApprovals {
			if pending.WorkflowName != name {
				continue
			}
			if string(nextStage) != "" && pending.Stage != string(nextStage) {
				continue
			}
			snapshot.PendingCallID = pending.CallID
			snapshot.PendingToolName = pending.ToolName
			snapshot.PendingAgentID = pending.AgentID
			snapshot.PendingArguments = pending.Arguments
			break
		}
	}
	w.runtime.session.SetWorkflow(snapshot)
}

func (w *WorkflowRunner) captureApprovalContext(workflowName string, stage WorkflowStage, request, responseContent string, completed []WorkflowStageResult, results []schema.ToolResult) {
	if w == nil || w.runtime == nil || w.runtime.approvals == nil {
		return
	}
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	if !w.runtime.AnnotatePendingApproval(callID, workflowName, stage, request, responseContent, completed) {
		return
	}
}

func (w *WorkflowRunner) captureSkillChainApprovalContext(stage WorkflowStage, request, responseContent string, completed []WorkflowStageResult, results []schema.ToolResult, chain []schema.Skill, index int, stagePrompt string) {
	if w == nil || w.runtime == nil || w.runtime.approvals == nil {
		return
	}
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	_ = w.runtime.AnnotateSkillChainPendingApproval(callID, workflowNameSkillChain, stage, request, responseContent, completed, chain, index, stagePrompt)
}

func (w *WorkflowRunner) ResumePlanFixAudit(ctx context.Context, callID string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	return w.Resume(ctx, workflowNamePlanFixAudit, callID, approve, handler)
}

func resumePlanFixAuditWorkflow(w *WorkflowRunner, ctx context.Context, pending pendingApproval, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	completed := append([]WorkflowStageResult(nil), pending.completed...)
	if approve {
		resolved := false
		if _, ok := w.runtime.PendingApproval(pending.call.ID); ok {
			resolved = w.runtime.ResolvePendingApproval(pending.call.ID, true).Found
		}
		toolResult, err := w.runtime.mcp.CallTool(ctx, pending.call.Name, pending.call.Arguments)
		if err != nil {
			return WorkflowResult{}, err
		}
		toolResult.CallID = pending.call.ID
		if toolResult.ToolName == "" {
			toolResult.ToolName = pending.tool.Name
		}
		if strings.TrimSpace(toolResult.Content) == "ok" {
			toolResult.Content = fmt.Sprintf("done: %s", pending.call.Name)
		}
		if resolved && w.runtime.audit != nil {
			w.runtime.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "approved", Detail: toolResult.Content})
		}
		if err := w.ensureWorkflowAgent(workflowAgentFixer); err != nil {
			return WorkflowResult{}, err
		}
		if err := w.runtime.SetActiveAgent(workflowAgentFixer); err != nil {
			return WorkflowResult{}, err
		}
		planSummary := pending.request
		if len(completed) > 0 {
			planSummary = completed[len(completed)-1].Result.Output
		}
		fixPrompt := fmt.Sprintf("Implement the approved plan for the following request. Reuse the provided plan, make minimal necessary changes, and summarize what changed.\n\nOriginal request:\n%s\n\nApproved plan:\n%s", pending.request, planSummary)
		fixResult, err := continueAgentRun(ctx, w.runtime, workflowAgentFixer, fixPrompt, pending.call, pending.responseContent, toolResult, handler)
		if err != nil {
			return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", WorkflowStageFix, err)
		}
		completed = append(completed, WorkflowStageResult{
			Stage:  WorkflowStageFix,
			Agent:  workflowAgentFixer,
			Result: fixResult,
		})

		if hasSuspendedToolResult(fixResult.ToolResults) {
			w.captureApprovalContext(workflowNamePlanFixAudit, WorkflowStageFix, pending.request, fixResult.Output, completed[:len(completed)-1], fixResult.ToolResults)
			approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(fixResult.ToolResults), workflowAgentFixer)
			w.recordWorkflowSummary(fmt.Sprintf("workflow %s awaiting tool approval", workflowNamePlanFixAudit))
			w.persistWorkflowState(workflowNamePlanFixAudit, "awaiting_tool_approval", WorkflowStageFix, pending.request, summarizeWorkflow(completed[:len(completed)-1]), approvalPrompt)
			return WorkflowResult{
				Name:            workflowNamePlanFixAudit,
				Status:          "awaiting_tool_approval",
				PendingApproval: true,
				ApprovalPrompt:  approvalPrompt,
				CompletedStages: completed[:len(completed)-1],
				NextStage:       WorkflowStageFix,
			}, nil
		}

		if err := w.ensureWorkflowAgent(workflowAgentAuditor); err != nil {
			return WorkflowResult{}, err
		}
		if err := w.runtime.SetActiveAgent(workflowAgentAuditor); err != nil {
			return WorkflowResult{}, err
		}
		auditPrompt := fmt.Sprintf("Audit the completed implementation against the original request and approved plan. Identify risks, regressions, and any follow-up actions.\n\nOriginal request:\n%s\n\nPlan summary:\n%s\n\nImplementation summary:\n%s", pending.request, completed[0].Result.Output, completed[len(completed)-1].Result.Output)
		auditResult, err := w.runtime.RunStream(ctx, auditPrompt, handler)
		if err != nil {
			return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", WorkflowStageAudit, err)
		}
		completed = append(completed, WorkflowStageResult{Stage: WorkflowStageAudit, Agent: workflowAgentAuditor, Result: auditResult})
		finalSummary := summarizeWorkflow(completed)
		w.runtime.DisableWorkflowAutoApproval(workflowNamePlanFixAudit)
		if w.runtime.approvedWorkflowResumes != nil {
			delete(w.runtime.approvedWorkflowResumes, pending.call.ID)
		}
		if err := w.runtime.RestoreDefaultAgent(); err != nil {
			return WorkflowResult{}, err
		}
		w.persistWorkflowState(workflowNamePlanFixAudit, "completed", "", pending.request, finalSummary, toolResult.Content)
		return WorkflowResult{Name: workflowNamePlanFixAudit, Status: "completed", CompletedStages: completed, FinalSummary: finalSummary}, nil
	}
	decision := w.runtime.ResolvePendingApproval(pending.call.ID, false)
	if !decision.Found {
		return WorkflowResult{}, fmt.Errorf("pending approval not found: %s", pending.call.ID)
	}
	result := schema.ToolResult{
		CallID:   pending.call.ID,
		ToolName: pending.tool.Name,
		Content:  fmt.Sprintf("tool %s denied by operator", pending.tool.Name),
		IsError:  true,
		Denied:   true,
	}
	if w.runtime.audit != nil {
		w.runtime.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "denied", Detail: result.Content})
	}
	completed = append(completed, WorkflowStageResult{
		Stage: WorkflowStageFix,
		Agent: workflowAgentFixer,
		Result: schema.AgentResult{
			Output:      result.Content,
			ToolResults: []schema.ToolResult{result},
			AgentID:     workflowAgentFixer,
			Mode:        string(WorkflowStageFix),
		},
	})
	w.runtime.DisableWorkflowAutoApproval(workflowNamePlanFixAudit)
	if w.runtime.approvedWorkflowResumes != nil {
		delete(w.runtime.approvedWorkflowResumes, pending.call.ID)
	}
	if err := w.runtime.RestoreDefaultAgent(); err != nil {
		return WorkflowResult{}, err
	}
	w.persistWorkflowState(workflowNamePlanFixAudit, "denied", WorkflowStageFix, pending.request, summarizeWorkflow(completed), result.Content)
	return WorkflowResult{Name: workflowNamePlanFixAudit, Status: "denied", PendingApproval: false, ApprovalPrompt: result.Content, CompletedStages: completed, NextStage: WorkflowStageFix}, nil
}

func resumeSkillChainWorkflow(w *WorkflowRunner, ctx context.Context, pending pendingApproval, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	completed := append([]WorkflowStageResult(nil), pending.completed...)
	if !approve {
		decision := w.runtime.ResolvePendingApproval(pending.call.ID, false)
		if !decision.Found {
			return WorkflowResult{}, fmt.Errorf("pending approval not found: %s", pending.call.ID)
		}
		result := schema.ToolResult{
			CallID:   pending.call.ID,
			ToolName: pending.tool.Name,
			Content:  fmt.Sprintf("tool %s denied by operator", pending.tool.Name),
			IsError:  true,
			Denied:   true,
		}
		completed = append(completed, WorkflowStageResult{
			Stage: pending.stage,
			Agent: pending.agent,
			Result: schema.AgentResult{
				Output:      result.Content,
				ToolResults: []schema.ToolResult{result},
				AgentID:     pending.agent,
				Mode:        string(pending.stage),
			},
		})
		w.runtime.DisableWorkflowAutoApproval(workflowNameSkillChain)
		if err := w.runtime.RestoreDefaultAgent(); err != nil {
			return WorkflowResult{}, err
		}
		w.persistWorkflowState(workflowNameSkillChain, "denied", pending.stage, pending.request, summarizeWorkflow(completed), result.Content)
		return WorkflowResult{Name: workflowNameSkillChain, Status: "denied", CompletedStages: completed, NextStage: pending.stage, ApprovalPrompt: result.Content}, nil
	}

	resolved := false
	if _, ok := w.runtime.PendingApproval(pending.call.ID); ok {
		resolved = w.runtime.ResolvePendingApproval(pending.call.ID, true).Found
	}
	toolResult, err := w.runtime.mcp.CallTool(ctx, pending.call.Name, pending.call.Arguments)
	if err != nil {
		return WorkflowResult{}, err
	}
	toolResult.CallID = pending.call.ID
	if toolResult.ToolName == "" {
		toolResult.ToolName = pending.tool.Name
	}
	if strings.TrimSpace(toolResult.Content) == "ok" {
		toolResult.Content = fmt.Sprintf("done: %s", pending.call.Name)
	}
	if resolved && w.runtime.audit != nil {
		w.runtime.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "approved", Detail: toolResult.Content})
	}
	chain := copySkillChain(pending.skillChain)
	if len(chain) == 0 {
		if skill, ok := findWorkflowSkillByName(w.runtime.skills.List(), string(pending.stage)); ok {
			chain = []schema.Skill{skill}
		}
	}
	if pending.skillIndex < 0 || pending.skillIndex >= len(chain) {
		return WorkflowResult{}, fmt.Errorf("workflow %s resume context is missing skill stage %s", workflowNameSkillChain, pending.stage)
	}
	skill := chain[pending.skillIndex]
	stagePrompt := pending.stagePrompt
	if strings.TrimSpace(stagePrompt) == "" {
		stagePrompt = buildSkillChainStagePrompt(pending.request, skill, completed)
	}
	if err := w.ensureWorkflowAgent(pending.agent); err != nil {
		return WorkflowResult{}, err
	}
	if err := w.runtime.SetActiveAgent(pending.agent); err != nil {
		return WorkflowResult{}, err
	}
	stageResult, err := continueAgentRunWithSkill(ctx, w.runtime, pending.agent, stagePrompt, &skill, pending.call, pending.responseContent, toolResult, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", pending.stage, err)
	}
	if hasSuspendedToolResult(stageResult.ToolResults) {
		w.captureSkillChainApprovalContext(pending.stage, pending.request, stageResult.Output, completed, stageResult.ToolResults, chain, pending.skillIndex, stagePrompt)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(stageResult.ToolResults), pending.agent)
		w.persistWorkflowState(workflowNameSkillChain, "awaiting_tool_approval", pending.stage, pending.request, summarizeWorkflow(completed), approvalPrompt)
		return WorkflowResult{
			Name:            workflowNameSkillChain,
			Status:          "awaiting_tool_approval",
			PendingApproval: true,
			ApprovalPrompt:  approvalPrompt,
			CompletedStages: completed,
			NextStage:       pending.stage,
		}, nil
	}
	completed = append(completed, WorkflowStageResult{Stage: pending.stage, Agent: pending.agent, Result: stageResult})
	chain = w.extendSkillChain(chain, pending.skillIndex, pending.request, stageResult.Output, completed)
	return w.runSkillChainFrom(ctx, pending.request, chain, pending.skillIndex+1, completed, handler)
}

func findWorkflowSkillByName(skills []schema.Skill, name string) (schema.Skill, bool) {
	target := normalizeWorkflowSkillName(name)
	for _, skill := range skills {
		if normalizeWorkflowSkillName(skill.Name) == target {
			return skill, true
		}
	}
	return schema.Skill{}, false
}

func continueAgentRun(ctx context.Context, runtimeRef *Runtime, agentID, input string, approvedCall schema.ToolCall, approvedResponseContent string, approvedResult schema.ToolResult, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	return continueAgentRunWithSkill(ctx, runtimeRef, agentID, input, nil, approvedCall, approvedResponseContent, approvedResult, handler)
}

func continueAgentRunWithSkill(ctx context.Context, runtimeRef *Runtime, agentID, input string, matchedSkill *schema.Skill, approvedCall schema.ToolCall, approvedResponseContent string, approvedResult schema.ToolResult, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	runner, ok := runtimeRef.runners[agentID]
	if !ok {
		return schema.AgentResult{}, fmt.Errorf("workflow agent not configured: %s", agentID)
	}
	tools, err := runtimeRef.mcp.ListTools(ctx)
	if err != nil {
		return schema.AgentResult{}, fmt.Errorf("list tools: %w", err)
	}
	profile := applySkillToProfile(runner.profile, matchedSkill)
	profile = ensureMinimumToolIterations(profile)
	mode := profile.Mode
	systemPrompt := BuildSystemPrompt(profile, matchedSkill, runtimeRef.SessionSnapshot())
	messages := []schema.Message{{Role: "user", Content: input}}
	collectedResults := make([]schema.ToolResult, 0, 1)
	if strings.TrimSpace(approvedCall.ID) != "" {
		messages = append(messages, schema.Message{Role: "assistant", Content: approvedResponseContent, ToolCalls: []schema.ToolCall{approvedCall}})
	}
	if strings.TrimSpace(approvedResult.CallID) != "" || strings.TrimSpace(approvedResult.ToolName) != "" || strings.TrimSpace(approvedResult.Content) != "" {
		messages = append(messages, schema.Message{Role: "tool", Name: approvedResult.ToolName, ToolCallID: approvedResult.CallID, Content: approvedResult.Content})
		collectedResults = append(collectedResults, approvedResult)
	}
	execCtx := newExecutionContext(agentID, profile, tools, runtimeRef.audit, workspaceRoot(runtimeRef))
	runtimeRef.applySessionApprovedTools(&execCtx)
	streamedText := false
	startedAt := time.Now()
	emitTaskStage(handler, runtimeRef.session, agentID, mode, initialTaskStageForMode(mode), "starting workflow stage")
	for i := 0; i <= profile.MaxIterations; i++ {
		finalResponseTurn := i == profile.MaxIterations
		if i > 0 && len(collectedResults) > 0 {
			emitTaskStage(handler, runtimeRef.session, agentID, mode, "summarize", "reasoning over tool observations")
		}
		emitModelWaitStatus(handler, agentID, mode, modelWaitStatusContext{AfterToolResults: i > 0 && len(collectedResults) > 0})
		request := schema.ChatRequest{Model: profile.Model, System: systemPrompt, Messages: messages, Tools: tools, Temperature: profile.Temperature, MaxTokens: profile.MaxTokens}
		resp, err := runAgentChatWithRecovery(ctx, runner.llm, request, agentID, mode, handler, &streamedText, &messages)
		if err != nil {
			if isRecoverableToolArgumentError(err) {
				return runner.finalizeWithInvalidToolArguments(agentConversationState{Profile: profile, MatchedSkill: matchedSkill, SystemPrompt: systemPrompt, Mode: mode, Input: input, Messages: messages, StartedAt: startedAt}, collectedResults, false, i+1, handler, runtimeRef.audit)
			}
			return schema.AgentResult{}, fmt.Errorf("llm chat: %w", err)
		}
		emitTokenUsage(handler, agentID, mode, resp.Usage)
		if len(resp.ToolCalls) == 0 {
			emitTaskStage(handler, runtimeRef.session, agentID, mode, "summarize", "preparing stage response")
			if strings.TrimSpace(runtimeRef.SessionSnapshot().Workflow.Name) != "" && strings.EqualFold(runtimeRef.SessionSnapshot().Workflow.Status, "running") {
				return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: matchedSkill, ToolResults: collectedResults, AgentID: agentID, Mode: mode}, nil
			}
			if !streamedText && handler != nil && resp.Message.Content != "" {
				if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: resp.Message.Content, AgentID: agentID, Mode: mode}); err != nil {
					return schema.AgentResult{}, err
				}
			}
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventDone, Content: resp.Message.Content, AgentID: agentID, Mode: mode})
			}
			return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: matchedSkill, ToolResults: collectedResults, AgentID: agentID, Mode: mode}, nil
		}
		runFinalToolCalls := finalResponseTurn && shouldRunFinalTurnToolCalls(execCtx, resp.ToolCalls)
		if finalResponseTurn && !runFinalToolCalls {
			return runner.finalizeAfterIterationBudget(ctx, agentConversationState{Profile: profile, MatchedSkill: matchedSkill, SystemPrompt: systemPrompt, Mode: mode, Input: input, Messages: messages, StartedAt: startedAt}, messages, collectedResults, false, i+1, handler, runtimeRef.audit)
		}
		messages = append(messages, schema.Message{Role: "assistant", Content: resp.Message.Content, ToolCalls: resp.ToolCalls})
		emitTaskStage(handler, runtimeRef.session, agentID, mode, taskStageForToolCalls(resp.ToolCalls, execCtx), summarizeToolCallStage(resp.ToolCalls, execCtx))
		results, err := runner.executor.RunToolCalls(ctx, execCtx, resp.ToolCalls, runtimeRef, handler)
		if err != nil {
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), AgentID: agentID, Mode: mode, IsError: true})
			}
			return schema.AgentResult{}, err
		}
		for i, result := range results {
			if !result.Suspended {
				continue
			}
			if autoApproved, ok := runtimeRef.AutoApprovedToolResult(ctx, result.CallID); ok {
				results[i] = autoApproved
			}
		}
		collectedResults = append(collectedResults, results...)
		for _, result := range results {
			if handler != nil {
				if err := handler(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, ArgumentsSummary: summarizeToolResultArguments(result, resp.ToolCalls), Content: result.Content, AgentID: agentID, Mode: mode, IsError: result.IsError, Suspended: result.Suspended}); err != nil {
					return schema.AgentResult{}, err
				}
			}
			if result.Suspended {
				return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: matchedSkill, ToolResults: collectedResults, AgentID: agentID, Mode: mode}, nil
			}
			messages = append(messages, schema.Message{Role: "tool", Name: result.ToolName, ToolCallID: result.CallID, Content: result.Content})
		}
		if runFinalToolCalls {
			return runner.finalizeAfterIterationBudget(ctx, agentConversationState{Profile: profile, MatchedSkill: matchedSkill, SystemPrompt: systemPrompt, Mode: mode, Input: input, Messages: messages, StartedAt: startedAt}, messages, collectedResults, false, i+1, handler, runtimeRef.audit)
		}
		streamedText = false
	}
	return runner.finalizeWithLocalBudgetSummary(agentConversationState{Profile: profile, MatchedSkill: matchedSkill, SystemPrompt: systemPrompt, Mode: mode, Input: input, Messages: messages, StartedAt: startedAt}, collectedResults, false, profile.MaxIterations, handler, runtimeRef.audit)
}

func summarizeWorkflow(stages []WorkflowStageResult) string {
	if len(stages) == 0 {
		return ""
	}
	parts := make([]string, 0, len(stages))
	for _, stage := range stages {
		parts = append(parts, fmt.Sprintf("[%s/%s] %s", stage.Stage, stage.Agent, truncateSummary(stage.Result.Output)))
	}
	return strings.Join(parts, "\n\n")
}

func hasSuspendedToolResult(results []schema.ToolResult) bool {
	for _, result := range results {
		if result.Suspended {
			return true
		}
	}
	return false
}

func firstSuspendedToolCallID(results []schema.ToolResult) string {
	for _, result := range results {
		if result.Suspended {
			return result.CallID
		}
	}
	return ""
}

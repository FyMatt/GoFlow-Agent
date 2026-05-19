package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
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
	runID    string
}

type workflowRunStartHookKey struct{}
type workflowRunRetryOfKey struct{}

type workflowStageRunOptions struct {
	AllowedTools []string
}

// WithWorkflowRunStartHook returns a context that is notified after a run id is allocated.
func WithWorkflowRunStartHook(ctx context.Context, hook func(runID string)) context.Context {
	if ctx == nil || hook == nil {
		return ctx
	}
	return context.WithValue(ctx, workflowRunStartHookKey{}, hook)
}

// WithWorkflowRunRetryOf links a new workflow run to the run it is retrying.
func WithWorkflowRunRetryOf(ctx context.Context, retryOf string) context.Context {
	if ctx == nil || strings.TrimSpace(retryOf) == "" {
		return ctx
	}
	return context.WithValue(ctx, workflowRunRetryOfKey{}, strings.TrimSpace(retryOf))
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
	Stage       WorkflowStage                           `json:"stage"`
	Agent       string                                  `json:"agent"`
	NodeType    string                                  `json:"node_type,omitempty"`
	Skill       string                                  `json:"skill,omitempty"`
	Tool        string                                  `json:"tool,omitempty"`
	Status      string                                  `json:"status,omitempty"`
	Attempts    int                                     `json:"attempts,omitempty"`
	Input       map[string]string                       `json:"input,omitempty"`
	InputValues map[string]any                          `json:"input_values,omitempty"`
	Metadata    map[string]string                       `json:"metadata,omitempty"`
	Result      schema.AgentResult                      `json:"result"`
	Output      WorkflowStageOutput                     `json:"output,omitempty"`
	Acceptance  []session.WorkflowRunAcceptanceSnapshot `json:"acceptance,omitempty"`
}

// WorkflowStageOutput is the workflow-data representation of a completed stage.
type WorkflowStageOutput struct {
	Summary      string                        `json:"summary,omitempty"`
	RawOutput    string                        `json:"raw_output,omitempty"`
	Variables    map[string]string             `json:"variables,omitempty"`
	Values       map[string]any                `json:"values,omitempty"`
	Evidence     []session.WorkflowRunArtifact `json:"evidence,omitempty"`
	Decision     string                        `json:"decision,omitempty"`
	NextActions  []string                      `json:"next_actions,omitempty"`
	Artifacts    []session.WorkflowRunArtifact `json:"artifacts,omitempty"`
	ToolResults  []schema.ToolResult           `json:"tool_results,omitempty"`
	Findings     []schema.Finding              `json:"findings,omitempty"`
	Changes      []schema.Change               `json:"changes,omitempty"`
	Verification []schema.Verification         `json:"verification,omitempty"`
}

// WorkflowResult represents a workflow run or resume outcome.
type WorkflowResult struct {
	RunID                    string                      `json:"run_id,omitempty"`
	Name                     string                      `json:"name"`
	Status                   string                      `json:"status"`
	CompletedStages          []WorkflowStageResult       `json:"completed_stages,omitempty"`
	PendingApproval          bool                        `json:"pending_approval,omitempty"`
	PendingInput             bool                        `json:"pending_input,omitempty"`
	PendingFields            []schema.WorkflowInputField `json:"pending_input_fields,omitempty"`
	PendingSubWorkflow       bool                        `json:"pending_sub_workflow,omitempty"`
	PendingSubWorkflowName   string                      `json:"pending_sub_workflow_name,omitempty"`
	PendingSubWorkflowRunID  string                      `json:"pending_sub_workflow_run_id,omitempty"`
	PendingSubWorkflowStatus string                      `json:"pending_sub_workflow_status,omitempty"`
	ApprovalPrompt           string                      `json:"approval_prompt,omitempty"`
	NextStage                WorkflowStage               `json:"next_stage,omitempty"`
	FinalSummary             string                      `json:"final_summary,omitempty"`
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
	runID := w.startWorkflowRun(ctx, name, request)
	previousRunID := w.runID
	w.runID = runID
	defer func() { w.runID = previousRunID }()
	result, err := definition.run(w, ctx, request, approve, w.recordWorkflowRunEvents(runID, handler))
	if err != nil {
		w.failWorkflowRun(runID, name, request, err)
		return WorkflowResult{}, err
	}
	result.RunID = runID
	w.completeWorkflowRun(runID, result)
	return result, nil
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
	runID := w.workflowRunIDForResume(ctx, name, pending)
	previousRunID := w.runID
	w.runID = runID
	defer func() { w.runID = previousRunID }()
	result, err := definition.resume(w, ctx, pending, approve, w.recordWorkflowRunEvents(runID, handler))
	if err != nil {
		w.failWorkflowRun(runID, name, pending.request, err)
		return WorkflowResult{}, err
	}
	result.RunID = runID
	w.completeWorkflowRun(runID, result)
	return result, nil
}

// ResumeToolApproval resumes a workflow run paused at a tool approval gate.
// Unlike Resume, it can rebuild the pending approval context from the
// persisted workflow run snapshot after a process restart.
func (w *WorkflowRunner) ResumeToolApproval(ctx context.Context, runID string, approve, remember bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_tool_approval") {
		return WorkflowResult{}, fmt.Errorf("workflow run %s is not awaiting tool approval", runID)
	}
	definition, ok, err := w.workflowDefinition(run.Name)
	if err != nil {
		return WorkflowResult{}, err
	}
	if !ok || definition.resume == nil {
		return WorkflowResult{}, fmt.Errorf("unknown workflow: %s", run.Name)
	}
	pending, err := w.pendingToolApprovalFromRun(ctx, run)
	if err != nil {
		return WorkflowResult{}, err
	}
	if remember {
		if strings.TrimSpace(pending.workflow) == "" || strings.TrimSpace(pending.tool.Name) == "" {
			return WorkflowResult{}, fmt.Errorf("pending approval is not scoped to a workflow tool call: %s", pending.call.ID)
		}
		if err := w.runtime.EnableWorkflowAutoApprovalForTool(pending.workflow, pending.stage, pending.tool); err != nil {
			return WorkflowResult{}, err
		}
	}
	previousRunID := w.runID
	w.runID = run.ID
	defer func() { w.runID = previousRunID }()
	handler = w.recordWorkflowRunEvents(run.ID, handler)
	w.runtime.session.SetWorkflow(session.WorkflowSnapshot{
		RunID:                        run.ID,
		Name:                         run.Name,
		Status:                       "running",
		NextStage:                    run.NextStage,
		Request:                      run.Request,
		Summary:                      run.Summary,
		LastApproval:                 run.ApprovalPrompt,
		PendingCallID:                run.PendingCallID,
		PendingToolName:              run.PendingToolName,
		PendingAgentID:               run.PendingAgentID,
		PendingArguments:             run.PendingArgs,
		PendingArgumentsSummary:      run.PendingArgsSummary,
		PendingArgumentsArtifactRef:  run.PendingArgsArtifactRef,
		PendingArgumentsHash:         run.PendingArgsHash,
		PendingArgumentsBytes:        run.PendingArgsBytes,
		PendingArgumentsStoredBytes:  run.PendingArgsStoredBytes,
		PendingArgumentsExternalized: run.PendingArgsExternalized,
		PendingResponseMessage:       schema.CopyMessage(run.PendingResponseMessage),
	})
	w.runtime.session.UpdateWorkflowRunState(w.runtime.session.Snapshot().Workflow)
	w.runtime.session.AppendWorkflowRunEvent(run.ID, session.WorkflowRunEventSnapshot{
		Type:           "workflow_tool_approval_submitted",
		Stage:          string(pending.stage),
		Content:        workflowToolApprovalSubmittedContent(pending, approve, remember),
		WorkflowName:   run.Name,
		WorkflowStatus: "running",
		NextStage:      string(pending.stage),
	})
	result, err := definition.resume(w, ctx, pending, approve, handler)
	if err != nil {
		w.failWorkflowRun(run.ID, run.Name, run.Request, err)
		return WorkflowResult{}, err
	}
	result.RunID = run.ID
	w.completeWorkflowRun(run.ID, result)
	w.clearWorkflowPendingApprovalSummary(pending.call.ID)
	return result, nil
}

// CanResumeToolApproval reports whether the workflow run snapshot has enough
// persisted context to resume a paused workflow tool approval in this process.
func (w *WorkflowRunner) CanResumeToolApproval(run session.WorkflowRunSnapshot) bool {
	if w == nil || w.runtime == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_tool_approval") {
		return false
	}
	callID := strings.TrimSpace(run.PendingCallID)
	if callID != "" {
		if _, ok := w.runtime.PendingApproval(callID); ok {
			return true
		}
	}
	return w.canRebuildToolApprovalFromRun(run)
}

// CanRememberToolApproval reports whether the workflow run's pending tool call
// can be auto-approved for later matching calls under the active risk policy.
func (w *WorkflowRunner) CanRememberToolApproval(run session.WorkflowRunSnapshot) (bool, string) {
	if w == nil || w.runtime == nil {
		return false, "workflow runtime not configured"
	}
	pending, err := w.pendingToolApprovalFromRun(context.Background(), run)
	if err != nil {
		return false, err.Error()
	}
	if w.runtime.canRememberToolApproval(pending.tool) {
		return true, ""
	}
	return false, toolRememberRiskPolicyReason(pending.tool)
}

// CanApproveToolApproval reports whether the workflow run's pending tool call
// can be approved under the active risk policy.
func (w *WorkflowRunner) CanApproveToolApproval(run session.WorkflowRunSnapshot) (bool, string) {
	if w == nil || w.runtime == nil {
		return false, "workflow runtime not configured"
	}
	pending, err := w.pendingToolApprovalFromRun(context.Background(), run)
	if err != nil {
		return false, err.Error()
	}
	if err := w.runtime.rejectToolApprovalByRiskPolicy(pending.tool); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func (w *WorkflowRunner) pendingToolApprovalFromRun(ctx context.Context, run session.WorkflowRunSnapshot) (pendingApproval, error) {
	if w != nil && w.runtime != nil && w.runtime.session != nil {
		run = w.runtime.session.HydrateWorkflowRunPendingArguments(run)
	}
	callID := strings.TrimSpace(run.PendingCallID)
	if callID == "" {
		return pendingApproval{}, fmt.Errorf("workflow run %s has no pending tool call id", run.ID)
	}
	if pending, ok := w.runtime.PendingApproval(callID); ok {
		if strings.TrimSpace(pending.workflow) == "" {
			pending.workflow = strings.TrimSpace(run.Name)
		}
		if strings.TrimSpace(string(pending.stage)) == "" {
			pending.stage = WorkflowStage(strings.TrimSpace(run.NextStage))
		}
		if strings.TrimSpace(pending.request) == "" {
			pending.request = strings.TrimSpace(run.Request)
		}
		if len(pending.completed) == 0 {
			pending.completed = workflowStageResultsFromRunSnapshots(run.CompletedStages)
		}
		return pending, nil
	}
	if !w.canRebuildToolApprovalFromRun(run) {
		return pendingApproval{}, fmt.Errorf("workflow tool approval context is not available for run %s; retry or cancel this run", run.ID)
	}
	toolName := strings.TrimSpace(run.PendingToolName)
	tool, err := w.workflowToolByName(ctx, toolName)
	if err != nil {
		return pendingApproval{}, err
	}
	pending := pendingApproval{
		call: schema.ToolCall{
			ID:        callID,
			Name:      toolName,
			Arguments: json.RawMessage(strings.TrimSpace(run.PendingArgs)),
		},
		tool:            tool,
		workflow:        strings.TrimSpace(run.Name),
		stage:           WorkflowStage(strings.TrimSpace(run.NextStage)),
		request:         strings.TrimSpace(run.Request),
		responseContent: strings.TrimSpace(run.PendingResponseMessage.Content),
		responseMessage: schema.CopyMessage(run.PendingResponseMessage),
		completed:       workflowStageResultsFromRunSnapshots(run.CompletedStages),
	}
	pending.responseMessage = responseMessageForApproval(pending.responseContent, pending.call, pending.responseMessage)
	if strings.TrimSpace(run.PendingAgentID) != "" {
		pending.agent = strings.TrimSpace(run.PendingAgentID)
	}
	if err := w.enrichRebuiltToolApproval(run, &pending); err != nil {
		return pendingApproval{}, err
	}
	return pending, nil
}

func (w *WorkflowRunner) canRebuildToolApprovalFromRun(run session.WorkflowRunSnapshot) bool {
	if strings.TrimSpace(run.Name) == "" || strings.TrimSpace(run.PendingCallID) == "" || strings.TrimSpace(run.PendingToolName) == "" || strings.TrimSpace(run.NextStage) == "" {
		return false
	}
	if w.runtime != nil && strings.TrimSpace(w.runtime.RuntimeHome()) != "" {
		if graph, err := w.loadWorkflowGraph(run.Name); err == nil {
			return workflowGraphStageIndexByName(graph, run.NextStage) >= 0
		} else if !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	switch strings.ToLower(strings.TrimSpace(run.Name)) {
	case workflowNamePlanFixAudit:
		return workflowStageEqual(run.NextStage, string(WorkflowStageFix))
	case workflowNameSkillChain:
		if w.runtime == nil || w.runtime.skills == nil {
			return false
		}
		_, ok := findWorkflowSkillByName(w.runtime.skills.List(), run.NextStage)
		return ok
	default:
		return false
	}
}

func (w *WorkflowRunner) enrichRebuiltToolApproval(run session.WorkflowRunSnapshot, pending *pendingApproval) error {
	if pending == nil {
		return fmt.Errorf("workflow tool approval context is empty")
	}
	if w.runtime != nil && strings.TrimSpace(w.runtime.RuntimeHome()) != "" {
		if graph, err := w.loadWorkflowGraph(run.Name); err == nil {
			index := workflowGraphStageIndexByName(graph, run.NextStage)
			if index < 0 {
				return fmt.Errorf("workflow run %s references unknown graph stage %q", run.ID, run.NextStage)
			}
			stage := graph.Stages[index]
			pending.workflow = graph.Name
			pending.stage = WorkflowStage(stage.Name)
			pending.skillIndex = index
			pending.stagePrompt = w.buildWorkflowGraphStagePrompt(context.Background(), graph, stage, pending.request, pending.completed)
			if strings.TrimSpace(pending.agent) == "" {
				pending.agent = strings.TrimSpace(stage.Agent)
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	switch strings.ToLower(strings.TrimSpace(run.Name)) {
	case workflowNamePlanFixAudit:
		pending.workflow = workflowNamePlanFixAudit
		pending.stage = WorkflowStageFix
		if strings.TrimSpace(pending.agent) == "" {
			pending.agent = workflowAgentFixer
		}
	case workflowNameSkillChain:
		skill, ok := findWorkflowSkillByName(w.runtime.skills.List(), string(pending.stage))
		if !ok {
			return fmt.Errorf("workflow %s resume context is missing skill stage %s", workflowNameSkillChain, pending.stage)
		}
		agentID, err := w.agentForSkill(skill)
		if err != nil {
			return err
		}
		if strings.TrimSpace(pending.agent) == "" {
			pending.agent = agentID
		}
		pending.workflow = workflowNameSkillChain
		pending.skillChain = []schema.Skill{skill}
		pending.skillIndex = 0
		pending.stagePrompt = buildSkillChainStagePrompt(pending.request, skill, pending.completed)
	default:
		return fmt.Errorf("workflow %s does not support durable tool approval resume", run.Name)
	}
	return nil
}

func (w *WorkflowRunner) workflowToolByName(ctx context.Context, name string) (schema.Tool, error) {
	if w == nil || w.runtime == nil || w.runtime.mcp == nil {
		return schema.Tool{}, fmt.Errorf("workflow runtime tools are not configured")
	}
	target := strings.TrimSpace(name)
	if target == "" {
		return schema.Tool{}, fmt.Errorf("workflow run has no pending tool name")
	}
	tools, err := w.runtime.mcp.ListTools(ctx)
	if err != nil {
		return schema.Tool{}, fmt.Errorf("list tools: %w", err)
	}
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool.Name), target) || strings.EqualFold(formatQualifiedToolName(tool), target) {
			if strings.TrimSpace(tool.Name) == "" {
				tool.Name = target
			}
			return tool, nil
		}
	}
	return schema.Tool{}, fmt.Errorf("pending workflow tool %q is not available", target)
}

func (w *WorkflowRunner) clearWorkflowPendingApprovalSummary(callID string) {
	if w == nil || w.runtime == nil || w.runtime.session == nil || strings.TrimSpace(callID) == "" {
		return
	}
	snapshot := w.runtime.session.Snapshot()
	if len(snapshot.PendingApprovals) == 0 {
		return
	}
	filtered := make([]session.PendingApprovalSnapshot, 0, len(snapshot.PendingApprovals))
	for _, pending := range snapshot.PendingApprovals {
		if strings.EqualFold(strings.TrimSpace(pending.CallID), strings.TrimSpace(callID)) {
			continue
		}
		filtered = append(filtered, pending)
	}
	w.runtime.session.SetPendingApprovals(filtered)
}

func workflowToolApprovalSubmittedContent(pending pendingApproval, approve, remember bool) string {
	action := "approved"
	if !approve {
		action = "denied"
	} else if remember {
		action = "approved and remembered for matching workflow stage tools"
	}
	toolName := strings.TrimSpace(pending.tool.Name)
	if toolName == "" {
		toolName = strings.TrimSpace(pending.call.Name)
	}
	return fmt.Sprintf("workflow tool %s %s", toolName, action)
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
	if w.runtime != nil && strings.TrimSpace(w.runtime.RuntimeHome()) != "" {
		if definition, ok, err := w.workflowFromGraph(name); err != nil || ok {
			return definition, ok, err
		}
	}
	if w.registry == nil {
		w.registry = builtInWorkflowRegistry()
	}
	definition, ok := w.registry[strings.TrimSpace(strings.ToLower(name))]
	return definition, ok, nil
}

func (w *WorkflowRunner) startWorkflowRun(ctx context.Context, name, request string) string {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return ""
	}
	opts := session.WorkflowRunStartOptions{}
	if retryOf, ok := ctx.Value(workflowRunRetryOfKey{}).(string); ok {
		opts.RetryOf = strings.TrimSpace(retryOf)
	}
	runID := w.runtime.session.StartWorkflowRunWithOptions(name, request, opts)
	w.recordWorkflowStarted(runID, name, request, opts.RetryOf)
	notifyWorkflowRunStarted(ctx, runID)
	return runID
}

func (w *WorkflowRunner) workflowRunIDForResume(ctx context.Context, name string, pending pendingApproval) string {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return ""
	}
	snapshot := w.runtime.session.Snapshot().Workflow
	if strings.EqualFold(strings.TrimSpace(snapshot.Name), strings.TrimSpace(name)) && strings.TrimSpace(snapshot.RunID) != "" {
		notifyWorkflowRunStarted(ctx, snapshot.RunID)
		return snapshot.RunID
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.Name), strings.TrimSpace(name)) {
		runID := w.runtime.session.StartWorkflowRunFromSnapshot(snapshot)
		notifyWorkflowRunStarted(ctx, runID)
		return runID
	}
	runID := w.runtime.session.StartWorkflowRun(name, pending.request)
	w.recordWorkflowStarted(runID, name, pending.request, "")
	notifyWorkflowRunStarted(ctx, runID)
	return runID
}

func notifyWorkflowRunStarted(ctx context.Context, runID string) {
	if ctx == nil || strings.TrimSpace(runID) == "" {
		return
	}
	hook, ok := ctx.Value(workflowRunStartHookKey{}).(func(string))
	if ok && hook != nil {
		hook(runID)
	}
}

func (w *WorkflowRunner) recordWorkflowRunEvents(runID string, handler func(event schema.StreamEvent) error) func(event schema.StreamEvent) error {
	if strings.TrimSpace(runID) == "" {
		return handler
	}
	return func(event schema.StreamEvent) error {
		if strings.TrimSpace(event.RunID) == "" {
			event.RunID = runID
		}
		if w != nil && w.runtime != nil && w.runtime.session != nil {
			snapshot := w.runtime.session.Snapshot().Workflow
			runEvent := workflowRunEventSnapshot(event)
			if strings.EqualFold(strings.TrimSpace(snapshot.RunID), strings.TrimSpace(runID)) {
				if strings.TrimSpace(runEvent.Stage) == "" {
					runEvent.Stage = snapshot.NextStage
				}
				if strings.TrimSpace(runEvent.WorkflowName) == "" {
					runEvent.WorkflowName = snapshot.Name
				}
				if strings.TrimSpace(runEvent.WorkflowStatus) == "" {
					runEvent.WorkflowStatus = snapshot.Status
				}
			}
			w.runtime.session.AppendWorkflowRunEvent(runID, runEvent)
		}
		if handler == nil {
			return nil
		}
		return handler(event)
	}
}

func (w *WorkflowRunner) completeWorkflowRun(runID string, result WorkflowResult) {
	if w == nil || w.runtime == nil || w.runtime.session == nil || strings.TrimSpace(runID) == "" {
		return
	}
	w.runtime.session.CompleteWorkflowRun(
		runID,
		result.Status,
		result.FinalSummary,
		string(result.NextStage),
		result.ApprovalPrompt,
		result.PendingFields,
		workflowRunStageSnapshots(result.CompletedStages),
	)
	w.runtime.session.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{
		Type:            string(schema.StreamEventWorkflowResult),
		Content:         result.FinalSummary,
		WorkflowName:    result.Name,
		WorkflowStatus:  result.Status,
		NextStage:       string(result.NextStage),
		PendingApproval: result.PendingApproval,
	})
	w.recordWorkflowResultCollaboration(runID, result)
	if run, ok := w.runtime.session.WorkflowRun(runID); ok {
		w.runtime.recordWorkflowTaskMemoryFromRun(run, nil)
	}
	_, _, _ = w.runtime.MaybeAutoCompactContext(context.Background(), "workflow run completed")
}

func (w *WorkflowRunner) failWorkflowRun(runID, name, request string, err error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil || strings.TrimSpace(runID) == "" || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		reason := err.Error()
		if strings.TrimSpace(reason) == "" {
			reason = "workflow run cancelled"
		}
		run, ok := w.runtime.session.CancelWorkflowRun(runID, reason)
		if ok {
			w.runtime.session.SetWorkflow(session.WorkflowSnapshot{
				RunID:     runID,
				Name:      run.Name,
				Status:    run.Status,
				NextStage: run.NextStage,
				Request:   run.Request,
				Summary:   run.Summary,
			})
			w.recordWorkflowFailure(runID, run.Name, run.Request, run.Status, run.Summary)
			w.runtime.recordWorkflowTaskMemoryFromRun(run, err)
			_, _, _ = w.runtime.MaybeAutoCompactContext(context.Background(), "workflow run cancelled")
		}
		return
	}
	snapshot := session.WorkflowSnapshot{
		RunID:   runID,
		Name:    name,
		Status:  "failed",
		Request: request,
		Summary: err.Error(),
	}
	w.runtime.session.SetWorkflow(snapshot)
	w.runtime.session.UpdateWorkflowRunState(snapshot)
	w.runtime.session.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{
		Type:           string(schema.StreamEventError),
		Content:        err.Error(),
		Stage:          snapshot.NextStage,
		IsError:        true,
		WorkflowName:   name,
		WorkflowStatus: "failed",
	})
	w.recordWorkflowFailure(runID, name, request, "failed", err.Error())
	if run, ok := w.runtime.session.WorkflowRun(runID); ok {
		w.runtime.recordWorkflowTaskMemoryFromRun(run, err)
	}
	_, _, _ = w.runtime.MaybeAutoCompactContext(context.Background(), "workflow run failed")
}

func (w *WorkflowRunner) recordWorkflowStarted(runID, name, request, retryOf string) {
	if w == nil || w.runtime == nil || strings.TrimSpace(runID) == "" {
		return
	}
	metadata := map[string]string{"workflow": name}
	if strings.TrimSpace(retryOf) != "" {
		metadata["retry_of"] = retryOf
	}
	w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		ID:       workflowCollaborationID("msg", runID, "started"),
		RunID:    runID,
		Kind:     "workflow_started",
		Subject:  fmt.Sprintf("Workflow %s started", name),
		Content:  request,
		Metadata: metadata,
	})
	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:       workflowCollaborationID("bb", runID, "request"),
		Scope:    "workflow",
		RunID:    runID,
		Kind:     "request",
		Title:    fmt.Sprintf("Workflow request: %s", name),
		Content:  request,
		Status:   "running",
		Tags:     []string{"workflow", name},
		Metadata: metadata,
	})
}

func (w *WorkflowRunner) recordWorkflowResultCollaboration(runID string, result WorkflowResult) {
	if w == nil || w.runtime == nil || strings.TrimSpace(runID) == "" {
		return
	}
	for index, stage := range result.CompletedStages {
		nextAgent := ""
		if index+1 < len(result.CompletedStages) {
			nextAgent = result.CompletedStages[index+1].Agent
		}
		w.recordWorkflowStageCollaboration(runID, result.Name, stage, index+1, nextAgent)
	}
	if result.PendingApproval {
		w.recordWorkflowPendingApproval(runID, result)
		return
	}
	if result.PendingInput {
		w.recordWorkflowPendingInput(runID, result)
		return
	}
	if result.PendingSubWorkflow {
		w.recordWorkflowPendingSubWorkflow(runID, result)
		return
	}
	w.recordWorkflowFinalCollaboration(runID, result)
}

func (w *WorkflowRunner) recordWorkflowStageCollaboration(runID, workflowName string, stage WorkflowStageResult, index int, nextAgent string) {
	stageName := string(stage.Stage)
	content := stage.Result.Output
	if strings.TrimSpace(content) == "" {
		content = summarizeWorkflow([]WorkflowStageResult{stage})
	}
	metadata := map[string]string{
		"workflow": workflowName,
		"stage":    stageName,
		"index":    fmt.Sprintf("%d", index),
	}
	for key, value := range stage.Metadata {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		if _, reserved := metadata[key]; reserved {
			metadata["stage_"+key] = value
			continue
		}
		metadata[key] = value
	}
	w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		ID:        workflowCollaborationID("msg", runID, stageName, "completed"),
		RunID:     runID,
		Stage:     stageName,
		FromAgent: stage.Agent,
		ToAgent:   nextAgent,
		Kind:      "stage_completed",
		Subject:   fmt.Sprintf("Stage %s completed", stageName),
		Content:   content,
		Metadata:  metadata,
	})
	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:       workflowCollaborationID("bb", runID, stageName, "output"),
		Scope:    "workflow",
		RunID:    runID,
		Stage:    stageName,
		AgentID:  stage.Agent,
		Kind:     "stage_output",
		Title:    fmt.Sprintf("Stage output: %s", stageName),
		Content:  content,
		Status:   "completed",
		Tags:     []string{"workflow", workflowName, stageName},
		Metadata: metadata,
	})
	if isTeamWorkflowStageResult(stage) {
		w.recordWorkflowTeamCollaboration(runID, workflowName, stage, metadata)
	}
	if stage.Metadata["team_role"] != "" {
		w.recordWorkflowTeamRoleCollaboration(runID, workflowName, stage, metadata)
	}
	if len(stage.Result.Findings) > 0 {
		w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
			ID:       workflowCollaborationID("bb", runID, stageName, "findings"),
			Scope:    "workflow",
			RunID:    runID,
			Stage:    stageName,
			AgentID:  stage.Agent,
			Kind:     "findings",
			Title:    fmt.Sprintf("Findings: %s", stageName),
			Content:  workflowFindingsSummary(stage.Result.Findings),
			Status:   "open",
			Tags:     []string{"workflow", workflowName, stageName, "findings"},
			Metadata: metadata,
		})
	}
}

func (w *WorkflowRunner) recordWorkflowTeamRoleCollaboration(runID, workflowName string, stage WorkflowStageResult, metadata map[string]string) {
	if w == nil || w.runtime == nil {
		return
	}
	stageName := string(stage.Stage)
	role := fallbackWorkflowGraphValue(stage.Metadata["team_role"], stageName)
	team := normalizePersistedWorkflowName(stage.Metadata["team"])
	if team == "" {
		return
	}
	content := strings.TrimSpace(stage.Result.Output)
	if content == "" {
		content = stage.Output.Summary
	}
	kind := "team_observation"
	subject := fmt.Sprintf("Team role completed: %s", role)
	status := "completed"
	if workflowTruthy(stage.Metadata["team_role_final"]) {
		kind = "team_final_handoff"
		subject = fmt.Sprintf("Team final handoff: %s", role)
		status = "ready"
	}
	roleMetadata := copyWorkflowMetadata(metadata)
	roleMetadata["team"] = team
	roleMetadata["role"] = role
	roleMetadata["team_role_stage"] = stageName
	roleAgents := w.workflowTeamRoleAgents(team)
	w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		ID:        workflowCollaborationID("msg", runID, stageName, kind),
		RunID:     runID,
		Stage:     stageName,
		FromAgent: stage.Agent,
		Kind:      kind,
		Subject:   subject,
		Content:   content,
		Metadata:  roleMetadata,
	})
	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:       workflowCollaborationID("bb", runID, stageName, kind),
		Scope:    "workflow",
		RunID:    runID,
		Stage:    stageName,
		AgentID:  stage.Agent,
		Kind:     kind,
		Title:    subject,
		Content:  content,
		Status:   status,
		Tags:     []string{"workflow", workflowName, "team", team, "role", role, kind},
		Metadata: roleMetadata,
	})
	for index, packet := range workflowTeamRolePackets(stage) {
		packet = workflowTeamRolePacketApplyEscalationPolicy(packet, stage.Metadata)
		packetMetadata := copyWorkflowMetadata(roleMetadata)
		packetMetadata["packet_kind"] = packet.Kind
		packetMetadata["packet_index"] = fmt.Sprintf("%d", index+1)
		workflowTeamRolePacketApplyMetadata(packetMetadata, packet)
		targetAgent := workflowTeamRolePacketTargetAgent(packet, roleAgents)
		entryAgent := stage.Agent
		if targetAgent != "" {
			entryAgent = targetAgent
		}
		w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
			ID:        workflowCollaborationID("msg", runID, stageName, packet.Kind, fmt.Sprintf("%d", index+1)),
			RunID:     runID,
			Stage:     stageName,
			FromAgent: stage.Agent,
			ToAgent:   targetAgent,
			Kind:      packet.Kind,
			Subject:   packet.Subject,
			Content:   packet.Content,
			Metadata:  packetMetadata,
		})
		w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
			ID:       workflowCollaborationID("bb", runID, stageName, packet.Kind, fmt.Sprintf("%d", index+1)),
			Scope:    "workflow",
			RunID:    runID,
			Stage:    stageName,
			AgentID:  entryAgent,
			Kind:     packet.Kind,
			Title:    packet.Subject,
			Content:  packet.Content,
			Status:   packet.Status,
			Tags:     []string{"workflow", workflowName, "team", team, "role", role, packet.Kind},
			Metadata: packetMetadata,
		})
	}
}

type workflowTeamRolePacket struct {
	Kind          string
	Subject       string
	Content       string
	Status        string
	ToAgent       string
	ToRole        string
	OwnerAgent    string
	OwnerRole     string
	AssignedAgent string
	EscalateTo    string
	Severity      string
	Priority      string
	SLA           string
	SLAMinutes    string
	DueAt         string
	Deadline      string
	EscalateAfter string
	Metadata      map[string]string
}

type workflowFlexibleString string

func (v *workflowFlexibleString) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "" || text == "null" {
		*v = ""
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*v = workflowFlexibleString(strings.TrimSpace(value))
		return nil
	}
	*v = workflowFlexibleString(strings.Trim(text, `"`))
	return nil
}

func workflowTeamRolePackets(stage WorkflowStageResult) []workflowTeamRolePacket {
	packets := make([]workflowTeamRolePacket, 0)
	packets = append(packets, workflowTeamRolePacketsFromJSON(stage.Result.Output)...)
	packets = append(packets, workflowTeamRolePacketsFromOutput(stage.Result.Output)...)
	packets = append(packets, workflowTeamRolePacketsFromStructured(stage.Result.Structured)...)
	for _, finding := range stage.Result.Findings {
		content := strings.TrimSpace(finding.Summary)
		if content == "" {
			continue
		}
		if strings.TrimSpace(finding.Severity) != "" {
			content = fmt.Sprintf("[%s] %s", finding.Severity, content)
		}
		packets = append(packets, workflowTeamRolePacket{
			Kind:     "team_critique",
			Subject:  "Team critique",
			Content:  content,
			Status:   "open",
			Severity: strings.TrimSpace(finding.Severity),
		})
	}
	return workflowTeamRoleUniquePackets(packets)
}

type workflowTeamRolePacketJSON struct {
	Kind          string                 `json:"kind,omitempty"`
	Type          string                 `json:"type,omitempty"`
	Category      string                 `json:"category,omitempty"`
	Label         string                 `json:"label,omitempty"`
	Subject       string                 `json:"subject,omitempty"`
	Content       string                 `json:"content,omitempty"`
	Summary       string                 `json:"summary,omitempty"`
	Text          string                 `json:"text,omitempty"`
	Value         string                 `json:"value,omitempty"`
	Status        string                 `json:"status,omitempty"`
	ToAgent       string                 `json:"to_agent,omitempty"`
	ToRole        string                 `json:"to_role,omitempty"`
	OwnerAgent    string                 `json:"owner_agent,omitempty"`
	OwnerRole     string                 `json:"owner_role,omitempty"`
	AssignedAgent string                 `json:"assigned_agent,omitempty"`
	AssignedTo    string                 `json:"assigned_to,omitempty"`
	EscalateTo    string                 `json:"escalate_to,omitempty"`
	Severity      string                 `json:"severity,omitempty"`
	Priority      string                 `json:"priority,omitempty"`
	SLA           string                 `json:"sla,omitempty"`
	SLAMinutes    workflowFlexibleString `json:"sla_minutes,omitempty"`
	DueAt         string                 `json:"due_at,omitempty"`
	Deadline      string                 `json:"deadline,omitempty"`
	EscalateAfter string                 `json:"escalate_after,omitempty"`
	Metadata      map[string]string      `json:"metadata,omitempty"`
	Items         []string               `json:"items,omitempty"`
}

func workflowTeamRolePacketsFromJSON(output string) []workflowTeamRolePacket {
	candidates := workflowTeamRoleJSONCandidates(output)
	if len(candidates) == 0 {
		return nil
	}
	packets := make([]workflowTeamRolePacket, 0)
	for _, candidate := range candidates {
		packets = append(packets, workflowTeamRolePacketsFromRawJSON(candidate)...)
	}
	return packets
}

func workflowTeamRoleJSONCandidates(output string) []string {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	candidates := make([]string, 0)
	if strings.HasPrefix(output, "{") || strings.HasPrefix(output, "[") {
		candidates = append(candidates, output)
	}
	lines := strings.Split(output, "\n")
	inFence := false
	fenceJSON := false
	var builder strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				block := strings.TrimSpace(builder.String())
				if block != "" && (fenceJSON || strings.HasPrefix(block, "{") || strings.HasPrefix(block, "[")) {
					candidates = append(candidates, block)
				}
				builder.Reset()
				inFence = false
				fenceJSON = false
				continue
			}
			inFence = true
			info := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
			fenceJSON = info == "json" || strings.HasPrefix(info, "json ")
			continue
		}
		if inFence {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	return candidates
}

func workflowTeamRolePacketsFromRawJSON(raw string) []workflowTeamRolePacket {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var list []workflowTeamRolePacketJSON
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return workflowTeamRolePacketsFromJSONList("", list)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return nil
	}
	packets := make([]workflowTeamRolePacket, 0)
	for _, key := range []string{"team_packets", "packets"} {
		if rawList, ok := object[key]; ok {
			var docs []workflowTeamRolePacketJSON
			if err := json.Unmarshal(rawList, &docs); err == nil {
				packets = append(packets, workflowTeamRolePacketsFromJSONList("", docs)...)
			}
		}
	}
	for key, rawValue := range object {
		switch normalizeWorkflowSkillName(key) {
		case "team_packets", "packets":
			continue
		case "decision", "decisions", "critique", "critiques", "question", "questions", "unresolved", "unresolved_questions", "risk", "risks", "approval", "approvals", "rejection", "rejections":
			packets = append(packets, workflowTeamRolePacketsFromJSONValue(key, rawValue)...)
		}
	}
	return packets
}

func workflowTeamRolePacketsFromJSONList(defaultLabel string, docs []workflowTeamRolePacketJSON) []workflowTeamRolePacket {
	packets := make([]workflowTeamRolePacket, 0, len(docs))
	for _, doc := range docs {
		packets = append(packets, workflowTeamRolePacketsFromJSONDoc(defaultLabel, doc)...)
	}
	return packets
}

func workflowTeamRolePacketsFromJSONValue(label string, raw json.RawMessage) []workflowTeamRolePacket {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if packet, ok := workflowTeamRolePacketFromLabel(label, text); ok {
			return []workflowTeamRolePacket{packet}
		}
		return nil
	}
	var stringsValue []string
	if err := json.Unmarshal(raw, &stringsValue); err == nil {
		packets := make([]workflowTeamRolePacket, 0, len(stringsValue))
		for _, item := range stringsValue {
			if packet, ok := workflowTeamRolePacketFromLabel(label, item); ok {
				packets = append(packets, packet)
			}
		}
		return packets
	}
	var docs []workflowTeamRolePacketJSON
	if err := json.Unmarshal(raw, &docs); err == nil {
		return workflowTeamRolePacketsFromJSONList(label, docs)
	}
	var doc workflowTeamRolePacketJSON
	if err := json.Unmarshal(raw, &doc); err == nil {
		return workflowTeamRolePacketsFromJSONDoc(label, doc)
	}
	return nil
}

func workflowTeamRolePacketsFromJSONDoc(defaultLabel string, doc workflowTeamRolePacketJSON) []workflowTeamRolePacket {
	label := fallbackWorkflowGraphValue(doc.Kind, fallbackWorkflowGraphValue(doc.Type, fallbackWorkflowGraphValue(doc.Category, fallbackWorkflowGraphValue(doc.Label, defaultLabel))))
	content := fallbackWorkflowGraphValue(doc.Content, fallbackWorkflowGraphValue(doc.Summary, fallbackWorkflowGraphValue(doc.Text, doc.Value)))
	packets := make([]workflowTeamRolePacket, 0, 1+len(doc.Items))
	if packet, ok := workflowTeamRolePacketFromLabel(label, content); ok {
		packet = workflowTeamRolePacketWithJSONOverrides(packet, doc)
		packets = append(packets, packet)
	}
	for _, item := range doc.Items {
		if packet, ok := workflowTeamRolePacketFromLabel(label, item); ok {
			packet = workflowTeamRolePacketWithJSONOverrides(packet, doc)
			packets = append(packets, packet)
		}
	}
	return packets
}

func workflowTeamRolePacketWithJSONOverrides(packet workflowTeamRolePacket, doc workflowTeamRolePacketJSON) workflowTeamRolePacket {
	if strings.TrimSpace(doc.Subject) != "" {
		packet.Subject = strings.TrimSpace(doc.Subject)
	}
	if strings.TrimSpace(doc.Status) != "" {
		packet.Status = strings.TrimSpace(doc.Status)
	}
	packet.ToAgent = fallbackWorkflowGraphValue(doc.ToAgent, packet.ToAgent)
	packet.ToRole = fallbackWorkflowGraphValue(doc.ToRole, packet.ToRole)
	packet.OwnerAgent = fallbackWorkflowGraphValue(doc.OwnerAgent, packet.OwnerAgent)
	packet.OwnerRole = fallbackWorkflowGraphValue(doc.OwnerRole, packet.OwnerRole)
	packet.AssignedAgent = fallbackWorkflowGraphValue(doc.AssignedAgent, fallbackWorkflowGraphValue(doc.AssignedTo, packet.AssignedAgent))
	packet.EscalateTo = fallbackWorkflowGraphValue(doc.EscalateTo, packet.EscalateTo)
	packet.Severity = fallbackWorkflowGraphValue(doc.Severity, packet.Severity)
	packet.Priority = fallbackWorkflowGraphValue(doc.Priority, packet.Priority)
	packet.SLA = fallbackWorkflowGraphValue(doc.SLA, packet.SLA)
	packet.SLAMinutes = fallbackWorkflowGraphValue(string(doc.SLAMinutes), packet.SLAMinutes)
	packet.DueAt = fallbackWorkflowGraphValue(doc.DueAt, packet.DueAt)
	packet.Deadline = fallbackWorkflowGraphValue(doc.Deadline, packet.Deadline)
	packet.EscalateAfter = fallbackWorkflowGraphValue(doc.EscalateAfter, packet.EscalateAfter)
	if len(doc.Metadata) > 0 {
		packet.Metadata = doc.Metadata
	}
	return packet
}

func workflowTeamRolePacketsFromOutput(output string) []workflowTeamRolePacket {
	lines := strings.Split(output, "\n")
	packets := make([]workflowTeamRolePacket, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-* \t")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		label, content, ok := workflowTeamRolePacketLine(line)
		if !ok {
			continue
		}
		if packet, ok := workflowTeamRolePacketFromLabel(label, content); ok {
			packets = append(packets, packet)
		}
	}
	return packets
}

func workflowTeamRolePacketLine(line string) (string, string, bool) {
	for _, separator := range []string{":", "："} {
		index := strings.Index(line, separator)
		if index <= 0 {
			continue
		}
		label := strings.TrimSpace(line[:index])
		content := strings.TrimSpace(line[index+len(separator):])
		if label == "" || content == "" {
			return "", "", false
		}
		return strings.Trim(label, "#[]【】 "), content, true
	}
	return "", "", false
}

func workflowTeamRolePacketsFromStructured(sections []schema.StructuredSection) []workflowTeamRolePacket {
	packets := make([]workflowTeamRolePacket, 0)
	for _, section := range sections {
		label := fallbackWorkflowGraphValue(section.Kind, section.Title)
		items := append([]string(nil), section.Items...)
		if strings.TrimSpace(section.Summary) != "" {
			items = append(items, section.Summary)
		}
		for _, item := range items {
			if packet, ok := workflowTeamRolePacketFromLabel(label, item); ok {
				packets = append(packets, packet)
			}
		}
	}
	return packets
}

func workflowTeamRolePacketFromLabel(label, content string) (workflowTeamRolePacket, bool) {
	label = strings.ToLower(normalizeWorkflowSkillName(label))
	content = strings.TrimSpace(content)
	if content == "" {
		return workflowTeamRolePacket{}, false
	}
	switch label {
	case "handoff", "hand_off", "team_handoff", "transfer":
		return workflowTeamRolePacket{Kind: "team_handoff", Subject: "Team handoff", Content: content, Status: "open"}, true
	case "assignment", "assign", "assigned", "owner", "assignee":
		return workflowTeamRolePacket{Kind: "team_assignment", Subject: "Team assignment", Content: content, Status: "open"}, true
	case "approval", "approve", "approved", "signoff", "sign_off", "review_approval", "review_approved":
		return workflowTeamRolePacket{Kind: "team_approval", Subject: "Team approval", Content: content, Status: "approved"}, true
	case "rejection", "reject", "rejected", "review_rejection", "changes_requested", "needs_changes", "veto":
		return workflowTeamRolePacket{Kind: "team_rejection", Subject: "Team rejection", Content: content, Status: "blocked"}, true
	case "escalation", "escalate", "escalated":
		return workflowTeamRolePacket{Kind: "team_escalation", Subject: "Team escalation", Content: content, Status: "escalated"}, true
	}
	switch label {
	case "decision", "decisions", "decided", "conclusion", "recommendation", "决策", "决定", "结论", "建议":
		return workflowTeamRolePacket{Kind: "team_decision", Subject: "Team decision", Content: content, Status: "accepted"}, true
	case "critique", "critiques", "review", "review_note", "concern", "concerns", "复核", "审查", "审查意见", "批评", "问题点":
		return workflowTeamRolePacket{Kind: "team_critique", Subject: "Team critique", Content: content, Status: "open"}, true
	case "question", "questions", "unresolved", "unresolved_question", "open_question", "todo", "疑问", "问题", "待确认", "未解决":
		return workflowTeamRolePacket{Kind: "team_unresolved_question", Subject: "Team unresolved question", Content: content, Status: "open"}, true
	case "risk", "risks", "blocker", "issue", "风险", "阻塞", "隐患":
		return workflowTeamRolePacket{Kind: "team_risk", Subject: "Team risk", Content: content, Status: "open"}, true
	default:
		return workflowTeamRolePacket{}, false
	}
}

func workflowTeamRoleUniquePackets(packets []workflowTeamRolePacket) []workflowTeamRolePacket {
	if len(packets) == 0 {
		return nil
	}
	out := make([]workflowTeamRolePacket, 0, len(packets))
	seen := make(map[string]struct{}, len(packets))
	for _, packet := range packets {
		packet.Content = strings.TrimSpace(packet.Content)
		if packet.Kind == "" || packet.Content == "" {
			continue
		}
		key := packet.Kind + "\x00" + strings.ToLower(packet.Content)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(packet.Subject) == "" {
			packet.Subject = packet.Kind
		}
		if strings.TrimSpace(packet.Status) == "" {
			packet.Status = "open"
		}
		out = append(out, packet)
	}
	return out
}

func (w *WorkflowRunner) workflowTeamRoleAgents(team string) map[string]string {
	template, ok := w.TeamTemplate(team)
	if !ok {
		return nil
	}
	agents := make(map[string]string, len(template.RoleTemplates))
	for _, role := range template.RoleTemplates {
		agents[normalizeWorkflowSkillName(role.Name)] = strings.TrimSpace(role.Agent)
	}
	return agents
}

func workflowTeamRolePacketApplyEscalationPolicy(packet workflowTeamRolePacket, metadata map[string]string) workflowTeamRolePacket {
	if packet.Kind == "team_escalation" || strings.EqualFold(strings.TrimSpace(packet.Status), "escalated") {
		return packet
	}
	if !workflowTeamRolePacketEscalationEligible(packet, metadata) {
		return packet
	}
	if packet.Metadata == nil {
		packet.Metadata = make(map[string]string)
	}
	packet.Metadata["escalation_policy"] = "true"
	if reason := workflowTeamRolePacketEscalationReason(packet, metadata); reason != "" {
		packet.Metadata["escalation_reason"] = reason
	}
	packet.Kind = "team_escalation"
	packet.Subject = "Team escalation"
	if !strings.EqualFold(strings.TrimSpace(packet.Status), "escalated") {
		packet.Status = "escalated"
	}
	if strings.TrimSpace(packet.EscalateTo) == "" {
		packet.EscalateTo = workflowTeamEscalationPolicyValue(metadata, "escalate_to", "escalation_to")
	}
	if strings.TrimSpace(packet.ToRole) == "" && strings.TrimSpace(packet.EscalateTo) == "" {
		packet.ToRole = workflowTeamEscalationPolicyValue(metadata, "escalate_role", "escalation_role", "to_role")
	}
	if strings.TrimSpace(packet.OwnerRole) == "" && strings.TrimSpace(packet.OwnerAgent) == "" {
		packet.OwnerRole = workflowTeamEscalationPolicyValue(metadata, "escalate_owner_role", "escalation_owner_role")
	}
	if strings.TrimSpace(packet.SLA) == "" {
		packet.SLA = workflowTeamEscalationPolicyValue(metadata, "escalation_sla", "escalate_sla")
	}
	if strings.TrimSpace(packet.SLAMinutes) == "" {
		packet.SLAMinutes = workflowTeamEscalationPolicyValue(metadata, "escalation_sla_minutes", "escalate_sla_minutes")
	}
	if strings.TrimSpace(packet.DueAt) == "" {
		packet.DueAt = workflowTeamEscalationPolicyValue(metadata, "escalation_due_at", "escalate_due_at")
	}
	if strings.TrimSpace(packet.Deadline) == "" {
		packet.Deadline = workflowTeamEscalationPolicyValue(metadata, "escalation_deadline", "escalate_deadline")
	}
	if strings.TrimSpace(packet.EscalateAfter) == "" {
		packet.EscalateAfter = workflowTeamEscalationPolicyValue(metadata, "escalation_after", "escalate_after")
	}
	return packet
}

func workflowTeamRolePacketEscalationEligible(packet workflowTeamRolePacket, metadata map[string]string) bool {
	if !workflowTeamEscalationKindAllowed(packet.Kind, workflowTeamEscalationPolicyValue(metadata, "escalate_kinds", "escalation_kinds")) {
		return false
	}
	if workflowRankMeetsThreshold(packet.Severity, workflowTeamEscalationPolicyValue(metadata, "escalate_severity", "escalation_severity"), workflowSeverityRank) {
		return true
	}
	return workflowRankMeetsThreshold(packet.Priority, workflowTeamEscalationPolicyValue(metadata, "escalate_priority", "escalation_priority"), workflowPriorityRank)
}

func workflowTeamRolePacketEscalationReason(packet workflowTeamRolePacket, metadata map[string]string) string {
	if workflowRankMeetsThreshold(packet.Severity, workflowTeamEscalationPolicyValue(metadata, "escalate_severity", "escalation_severity"), workflowSeverityRank) {
		return "severity:" + strings.TrimSpace(packet.Severity)
	}
	if workflowRankMeetsThreshold(packet.Priority, workflowTeamEscalationPolicyValue(metadata, "escalate_priority", "escalation_priority"), workflowPriorityRank) {
		return "priority:" + strings.TrimSpace(packet.Priority)
	}
	return ""
}

func workflowTeamEscalationKindAllowed(kind, allowed string) bool {
	kind = normalizeWorkflowSkillName(kind)
	if strings.TrimSpace(allowed) == "" {
		switch kind {
		case "team_risk", "team_unresolved_question", "team_critique":
			return true
		default:
			return false
		}
	}
	for _, item := range strings.FieldsFunc(allowed, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' '
	}) {
		if normalizeWorkflowSkillName(item) == kind {
			return true
		}
	}
	return false
}

func workflowTeamEscalationPolicyValue(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func workflowTeamEscalationPolicyParamKeys() []string {
	return []string{
		"escalate_severity",
		"escalation_severity",
		"escalate_priority",
		"escalation_priority",
		"escalate_kinds",
		"escalation_kinds",
		"escalate_to",
		"escalation_to",
		"escalate_role",
		"escalation_role",
		"escalate_owner_role",
		"escalation_owner_role",
		"escalation_sla",
		"escalate_sla",
		"escalation_sla_minutes",
		"escalate_sla_minutes",
		"escalation_due_at",
		"escalate_due_at",
		"escalation_deadline",
		"escalate_deadline",
		"escalation_after",
		"escalate_after",
	}
}

func workflowTeamApprovalPolicyParamKeys() []string {
	return []string{
		"approval_preset",
		"review_preset",
		"quorum_preset",
		"preset",
		"approval_quorum",
		"review_quorum",
		"quorum",
		"approval_roles",
		"review_roles",
		"quorum_roles",
		"reject_blocks",
		"rejection_blocks",
		"review_reject_blocks",
	}
}

func workflowRankMeetsThreshold(value, threshold string, rank func(string) int) bool {
	valueRank := rank(value)
	thresholdRank := rank(threshold)
	return valueRank > 0 && thresholdRank > 0 && valueRank >= thresholdRank
}

func workflowSeverityRank(value string) int {
	switch normalizeWorkflowSkillName(value) {
	case "info", "informational", "note":
		return 1
	case "low", "minor":
		return 2
	case "medium", "moderate":
		return 3
	case "high", "major":
		return 4
	case "critical", "blocker", "severe":
		return 5
	default:
		return 0
	}
}

func workflowPriorityRank(value string) int {
	switch normalizeWorkflowSkillName(value) {
	case "low", "p4":
		return 1
	case "medium", "normal", "p3":
		return 2
	case "high", "p2":
		return 3
	case "urgent", "p1":
		return 4
	case "critical", "blocker", "p0":
		return 5
	default:
		return 0
	}
}

func workflowTeamRolePacketTargetAgent(packet workflowTeamRolePacket, roleAgents map[string]string) string {
	for _, value := range []string{packet.EscalateTo, packet.AssignedAgent, packet.OwnerAgent, packet.ToAgent} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	for _, role := range []string{packet.OwnerRole, packet.ToRole} {
		if agent := roleAgents[normalizeWorkflowSkillName(role)]; strings.TrimSpace(agent) != "" {
			return strings.TrimSpace(agent)
		}
	}
	return ""
}

func workflowTeamRolePacketApplyMetadata(metadata map[string]string, packet workflowTeamRolePacket) {
	for key, value := range packet.Metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			metadata[key] = value
		}
	}
	if value := strings.TrimSpace(packet.ToAgent); value != "" {
		metadata["to_agent"] = value
	}
	if value := strings.TrimSpace(packet.ToRole); value != "" {
		metadata["to_role"] = value
	}
	if value := strings.TrimSpace(packet.OwnerAgent); value != "" {
		metadata["owner_agent"] = value
	}
	if value := strings.TrimSpace(packet.OwnerRole); value != "" {
		metadata["owner_role"] = value
	}
	if value := strings.TrimSpace(packet.AssignedAgent); value != "" {
		metadata["assigned_agent"] = value
	}
	if value := strings.TrimSpace(packet.EscalateTo); value != "" {
		metadata["escalated_to"] = value
	}
	if value := strings.TrimSpace(packet.Severity); value != "" {
		metadata["severity"] = value
	}
	if value := strings.TrimSpace(packet.Priority); value != "" {
		metadata["priority"] = value
	}
	if value := strings.TrimSpace(packet.SLA); value != "" {
		metadata["sla"] = value
	}
	if value := strings.TrimSpace(packet.SLAMinutes); value != "" {
		metadata["sla_minutes"] = value
		if _, ok := metadata["due_at"]; !ok && strings.TrimSpace(packet.DueAt) == "" && strings.TrimSpace(packet.Deadline) == "" {
			if minutes, err := strconv.Atoi(value); err == nil && minutes > 0 {
				metadata["due_at"] = time.Now().UTC().Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)
			}
		}
	}
	if value := strings.TrimSpace(packet.DueAt); value != "" {
		metadata["due_at"] = value
	}
	if value := strings.TrimSpace(packet.Deadline); value != "" {
		metadata["deadline"] = value
		if _, ok := metadata["due_at"]; !ok {
			metadata["due_at"] = value
		}
	}
	if value := strings.TrimSpace(packet.EscalateAfter); value != "" {
		metadata["escalate_after"] = value
	}
	if packet.Kind == "team_escalation" || strings.EqualFold(strings.TrimSpace(packet.Status), "escalated") {
		metadata["escalated"] = "true"
	}
}

func isTeamWorkflowStageResult(stage WorkflowStageResult) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "team", "agent_team", "team_template":
		return true
	default:
		return false
	}
}

func (w *WorkflowRunner) recordWorkflowTeamCollaboration(runID, workflowName string, stage WorkflowStageResult, baseMetadata map[string]string) {
	teamName := normalizePersistedWorkflowName(stage.Output.Variables["team"])
	if teamName == "" {
		return
	}
	template, ok := w.TeamTemplate(teamName)
	if !ok {
		return
	}
	stageName := string(stage.Stage)
	entryAgent := strings.TrimSpace(stage.Agent)
	if entryAgent == "" {
		entryAgent = template.RecommendedEntryAgent
	}
	metadata := copyWorkflowMetadata(baseMetadata)
	metadata["team"] = template.Name
	metadata["team_title"] = template.Title
	metadata["entry_agent"] = entryAgent
	metadata["recommended_workflow"] = template.RecommendedWorkflow
	metadata["role_count"] = fmt.Sprintf("%d", len(template.RoleTemplates))
	metadata["handoff_count"] = fmt.Sprintf("%d", len(template.Handoffs))

	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:       workflowCollaborationID("bb", runID, stageName, "team", template.Name),
		Scope:    "workflow",
		RunID:    runID,
		Stage:    stageName,
		AgentID:  entryAgent,
		Kind:     "team_state",
		Title:    fmt.Sprintf("Team context: %s", template.Title),
		Content:  template.Description,
		Status:   "completed",
		Tags:     []string{"workflow", workflowName, "team", template.Name},
		Metadata: metadata,
	})

	roleAgents := make(map[string]string, len(template.RoleTemplates))
	for _, role := range template.RoleTemplates {
		roleKey := normalizeWorkflowSkillName(role.Name)
		roleAgents[roleKey] = role.Agent
		roleMetadata := copyWorkflowMetadata(metadata)
		roleMetadata["role"] = role.Name
		roleMetadata["role_label"] = role.Label
		roleMetadata["skill"] = role.Skill
		roleMetadata["consumes"] = strings.Join(role.Consumes, ",")
		roleMetadata["produces"] = strings.Join(role.Produces, ",")
		w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
			ID:       workflowCollaborationID("bb", runID, stageName, "team_role", template.Name, role.Name),
			Scope:    "workflow",
			RunID:    runID,
			Stage:    stageName,
			AgentID:  role.Agent,
			Kind:     "team_role",
			Title:    fmt.Sprintf("Team role: %s", fallbackWorkflowGraphValue(role.Label, role.Name)),
			Content:  strings.Join(role.Responsibilities, "\n"),
			Status:   "open",
			Tags:     []string{"workflow", workflowName, "team", template.Name, "role", role.Name},
			Metadata: roleMetadata,
		})
	}

	for _, handoff := range template.Handoffs {
		fromRole := normalizeWorkflowSkillName(handoff.From)
		toRole := normalizeWorkflowSkillName(handoff.To)
		fromAgent := roleAgents[fromRole]
		toAgent := roleAgents[toRole]
		handoffMetadata := copyWorkflowMetadata(metadata)
		handoffMetadata["from_role"] = handoff.From
		handoffMetadata["to_role"] = handoff.To
		handoffMetadata["artifacts"] = strings.Join(handoff.Artifacts, ",")
		handoffMetadata["blackboard"] = strings.Join(handoff.Blackboard, ",")
		handoffMetadata["condition"] = handoff.Condition
		handoffKind := fallbackWorkflowGraphValue(handoff.Kind, "team_handoff")
		content := fallbackWorkflowGraphValue(handoff.Description, fmt.Sprintf("%s hands off to %s.", handoff.From, handoff.To))
		w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
			ID:        workflowCollaborationID("msg", runID, stageName, "team_handoff", handoff.From, handoff.To),
			RunID:     runID,
			Stage:     stageName,
			FromAgent: fromAgent,
			ToAgent:   toAgent,
			Kind:      "team_handoff",
			Subject:   fallbackWorkflowGraphValue(handoff.Subject, handoffKind),
			Content:   content,
			Metadata:  handoffMetadata,
		})
		w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
			ID:       workflowCollaborationID("bb", runID, stageName, "handoff", handoff.From, handoff.To),
			Scope:    "workflow",
			RunID:    runID,
			Stage:    stageName,
			AgentID:  toAgent,
			Kind:     "handoff",
			Title:    fmt.Sprintf("Handoff: %s -> %s", handoff.From, handoff.To),
			Content:  fallbackWorkflowGraphValue(handoff.Subject, content),
			Status:   "open",
			Tags:     []string{"workflow", workflowName, "team", template.Name, "handoff"},
			Metadata: handoffMetadata,
		})
	}

	for _, item := range template.BlackboardTemplates {
		itemMetadata := copyWorkflowMetadata(metadata)
		itemMetadata["owner_role"] = item.OwnerRole
		itemMetadata["template_kind"] = item.Kind
		ownerAgent := roleAgents[normalizeWorkflowSkillName(item.OwnerRole)]
		status := fallbackWorkflowGraphValue(item.Status, "open")
		w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
			ID:       workflowCollaborationID("bb", runID, stageName, "team_slot", item.Kind, item.OwnerRole),
			Scope:    "workflow",
			RunID:    runID,
			Stage:    stageName,
			AgentID:  ownerAgent,
			Kind:     item.Kind,
			Title:    fallbackWorkflowGraphValue(item.Title, fmt.Sprintf("Team %s", item.Kind)),
			Content:  item.Description,
			Status:   status,
			Tags:     append([]string{"workflow", workflowName, "team", template.Name}, item.Tags...),
			Metadata: itemMetadata,
		})
	}
}

func copyWorkflowMetadata(values map[string]string) map[string]string {
	out := make(map[string]string, len(values)+4)
	for key, value := range values {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func (w *WorkflowRunner) recordWorkflowPendingApproval(runID string, result WorkflowResult) {
	stageName := string(result.NextStage)
	content := result.ApprovalPrompt
	if strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("Workflow %s is awaiting approval.", result.Name)
	}
	w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		ID:      workflowCollaborationID("msg", runID, stageName, "approval_required"),
		RunID:   runID,
		Stage:   stageName,
		Kind:    "approval_required",
		Subject: fmt.Sprintf("Approval required for %s", stageName),
		Content: content,
		Metadata: map[string]string{
			"workflow": result.Name,
			"status":   result.Status,
		},
	})
	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:      workflowCollaborationID("bb", runID, stageName, "approval"),
		Scope:   "workflow",
		RunID:   runID,
		Stage:   stageName,
		Kind:    "approval",
		Title:   fmt.Sprintf("Approval required: %s", stageName),
		Content: content,
		Status:  "open",
		Tags:    []string{"workflow", result.Name, "approval"},
		Metadata: map[string]string{
			"workflow_status": result.Status,
		},
	})
}

func (w *WorkflowRunner) recordWorkflowPendingInput(runID string, result WorkflowResult) {
	stageName := string(result.NextStage)
	content := result.ApprovalPrompt
	if strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("Workflow %s is awaiting input.", result.Name)
	}
	fieldNames := make([]string, 0, len(result.PendingFields))
	for _, field := range result.PendingFields {
		if strings.TrimSpace(field.Name) != "" {
			fieldNames = append(fieldNames, field.Name)
		}
	}
	metadata := map[string]string{
		"workflow": result.Name,
		"status":   result.Status,
	}
	if len(fieldNames) > 0 {
		metadata["fields"] = strings.Join(fieldNames, ",")
	}
	w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		ID:       workflowCollaborationID("msg", runID, stageName, "input_required"),
		RunID:    runID,
		Stage:    stageName,
		Kind:     "input_required",
		Subject:  fmt.Sprintf("Input required for %s", stageName),
		Content:  content,
		Metadata: metadata,
	})
	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:      workflowCollaborationID("bb", runID, stageName, "input"),
		Scope:   "workflow",
		RunID:   runID,
		Stage:   stageName,
		Kind:    "input",
		Title:   fmt.Sprintf("Input required: %s", stageName),
		Content: content,
		Status:  "open",
		Tags:    []string{"workflow", result.Name, "input"},
		Metadata: map[string]string{
			"workflow_status": result.Status,
			"fields":          strings.Join(fieldNames, ","),
		},
	})
}

func (w *WorkflowRunner) recordWorkflowPendingSubWorkflow(runID string, result WorkflowResult) {
	stageName := string(result.NextStage)
	childName := strings.TrimSpace(result.PendingSubWorkflowName)
	childRunID := strings.TrimSpace(result.PendingSubWorkflowRunID)
	childStatus := strings.TrimSpace(result.PendingSubWorkflowStatus)
	content := result.ApprovalPrompt
	if strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("Workflow %s is waiting for nested sub-workflow %s.", result.Name, fallbackWorkflowGraphValue(childName, childRunID))
	}
	metadata := map[string]string{
		"workflow":            result.Name,
		"status":              result.Status,
		"sub_workflow":        childName,
		"sub_workflow_run_id": childRunID,
		"sub_workflow_status": childStatus,
	}
	w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		ID:       workflowCollaborationID("msg", runID, stageName, "sub_workflow_waiting"),
		RunID:    runID,
		Stage:    stageName,
		Kind:     "sub_workflow_waiting",
		Subject:  fmt.Sprintf("Sub-workflow waiting for %s", stageName),
		Content:  content,
		Metadata: metadata,
	})
	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:      workflowCollaborationID("bb", runID, stageName, "sub_workflow"),
		Scope:   "workflow",
		RunID:   runID,
		Stage:   stageName,
		Kind:    "sub_workflow",
		Title:   fmt.Sprintf("Sub-workflow pending: %s", stageName),
		Content: content,
		Status:  "open",
		Tags:    []string{"workflow", result.Name, "sub_workflow"},
		Metadata: map[string]string{
			"workflow_status":     result.Status,
			"sub_workflow":        childName,
			"sub_workflow_run_id": childRunID,
			"sub_workflow_status": childStatus,
		},
	})
}

func (w *WorkflowRunner) recordWorkflowFinalCollaboration(runID string, result WorkflowResult) {
	content := result.FinalSummary
	if strings.TrimSpace(content) == "" {
		content = summarizeWorkflow(result.CompletedStages)
	}
	if strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("Workflow %s finished with status %s.", result.Name, result.Status)
	}
	w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		ID:      workflowCollaborationID("msg", runID, "final", result.Status),
		RunID:   runID,
		Kind:    "workflow_finished",
		Subject: fmt.Sprintf("Workflow %s %s", result.Name, result.Status),
		Content: content,
		Metadata: map[string]string{
			"workflow": result.Name,
			"status":   result.Status,
		},
	})
	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:      workflowCollaborationID("bb", runID, "final_summary"),
		Scope:   "workflow",
		RunID:   runID,
		Kind:    "final_summary",
		Title:   fmt.Sprintf("Workflow summary: %s", result.Name),
		Content: content,
		Status:  workflowBlackboardStatus(result.Status),
		Tags:    []string{"workflow", result.Name, "summary"},
		Metadata: map[string]string{
			"workflow_status": result.Status,
		},
	})
}

func (w *WorkflowRunner) recordWorkflowFailure(runID, name, request, status, reason string) {
	if w == nil || w.runtime == nil || strings.TrimSpace(runID) == "" {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("workflow %s %s", name, status)
	}
	w.runtime.AddCollaborationMessage(session.CollaborationMessageSnapshot{
		ID:      workflowCollaborationID("msg", runID, "failure", status),
		RunID:   runID,
		Kind:    "workflow_" + workflowBlackboardStatus(status),
		Subject: fmt.Sprintf("Workflow %s %s", name, status),
		Content: reason,
		Metadata: map[string]string{
			"workflow": name,
			"request":  request,
			"status":   status,
		},
	})
	w.runtime.UpsertBlackboardEntry(session.BlackboardEntrySnapshot{
		ID:      workflowCollaborationID("bb", runID, "failure"),
		Scope:   "workflow",
		RunID:   runID,
		Kind:    "issue",
		Title:   fmt.Sprintf("Workflow %s: %s", workflowBlackboardStatus(status), name),
		Content: reason,
		Status:  workflowBlackboardStatus(status),
		Tags:    []string{"workflow", name, "issue"},
		Metadata: map[string]string{
			"workflow_status": status,
		},
	})
}

func workflowCollaborationID(prefix string, parts ...string) string {
	values := []string{strings.TrimSpace(prefix)}
	for _, part := range parts {
		part = normalizeWorkflowSkillName(part)
		part = strings.Trim(part, "_")
		if part == "" {
			continue
		}
		values = append(values, part)
	}
	if len(values) == 1 {
		values = append(values, "workflow")
	}
	return strings.Join(values, "-")
}

func workflowBlackboardStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return "completed"
	case "denied":
		return "denied"
	case "blocked":
		return "blocked"
	case "cancelled", "canceled":
		return "cancelled"
	case "failed":
		return "failed"
	default:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(status)), "awaiting_") {
			return "open"
		}
		return "updated"
	}
}

func workflowFindingsSummary(findings []schema.Finding) string {
	if len(findings) == 0 {
		return ""
	}
	lines := make([]string, 0, len(findings))
	for _, finding := range findings {
		summary := strings.TrimSpace(finding.Summary)
		if summary == "" {
			continue
		}
		if strings.TrimSpace(finding.Severity) != "" {
			summary = fmt.Sprintf("[%s] %s", finding.Severity, summary)
		}
		lines = append(lines, summary)
	}
	return strings.Join(lines, "\n")
}

func workflowRunStageSnapshots(stages []WorkflowStageResult) []session.WorkflowRunStageSnapshot {
	if len(stages) == 0 {
		return nil
	}
	snapshots := make([]session.WorkflowRunStageSnapshot, 0, len(stages))
	for _, stage := range stages {
		snapshots = append(snapshots, session.WorkflowRunStageSnapshot{
			Stage:        string(stage.Stage),
			AgentID:      stage.Agent,
			NodeType:     stage.NodeType,
			Skill:        stage.Skill,
			Tool:         stage.Tool,
			Status:       stage.Status,
			Attempts:     stage.Attempts,
			Inputs:       copyStringMap(stage.Input),
			InputValues:  copyWorkflowAnyMap(stage.InputValues),
			Outputs:      copyStringMap(stage.Output.Variables),
			OutputValues: copyWorkflowAnyMap(stage.Output.Values),
			Artifacts:    append([]session.WorkflowRunArtifact(nil), stage.Output.Artifacts...),
			Acceptance:   append([]session.WorkflowRunAcceptanceSnapshot(nil), stage.Acceptance...),
			Metadata:     copyStringMap(stage.Metadata),
			Result:       stage.Result,
		})
	}
	return snapshots
}

func workflowRunEventSnapshot(event schema.StreamEvent) session.WorkflowRunEventSnapshot {
	return session.WorkflowRunEventSnapshot{
		Type:             string(event.Type),
		Content:          event.Content,
		ToolName:         event.ToolName,
		ToolCallID:       event.ToolCallID,
		ArgumentsSummary: event.ArgumentsSummary,
		AgentID:          event.AgentID,
		Mode:             event.Mode,
		IsError:          event.IsError,
		NeedsAction:      event.NeedsAction,
		Suspended:        event.Suspended,
		TaskStage:        event.TaskStage,
		PromptTokens:     event.PromptTokens,
		OutputTokens:     event.OutputTokens,
		CachedTokens:     event.CachedTokens,
		WorkflowName:     event.WorkflowName,
		WorkflowStatus:   event.WorkflowStatus,
		NextStage:        event.NextStage,
		PendingApproval:  event.PendingApproval,
		PromptBudget:     event.PromptBudget,
		Risk:             event.Risk,
	}
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
			w.captureSkillChainApprovalContext(stage, request, result.Output, result.ResponseMessage, completed, result.ToolResults, chain, index, stagePrompt)
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
		builder.WriteString("\nWorkflow context contract:\n")
		builder.WriteString("- include: previous.summary\n")
		builder.WriteString("- exclude: raw_tool_logs, large_file_contents\n")
		builder.WriteString("- max_tokens: 1200\n")
		builder.WriteString("\nSelected prior skill summaries:\n")
		writeWorkflowStageSummaryList(&builder, completed)
	}
	builder.WriteString("\nUse the matched skill instructions for this stage. Produce a concise stage result that the next skill can use.")
	return builder.String()
}

func buildPlanFixAuditPlanPrompt(request string) string {
	return fmt.Sprintf("Create a concise implementation plan for the following request. Focus on concrete code changes, tests, and risks.\n\nRequest:\n%s", request)
}

func buildPlanFixAuditFixPrompt(request, planOutput string) string {
	var builder strings.Builder
	builder.WriteString("Implement the approved plan for the following request. Make minimal necessary changes and summarize what changed.\n\n")
	fmt.Fprintf(&builder, "Original request:\n%s\n\n", request)
	builder.WriteString("Workflow context contract:\n")
	builder.WriteString("- include: previous.summary\n")
	builder.WriteString("- exclude: raw_tool_logs, large_file_contents\n")
	builder.WriteString("- max_tokens: 1200\n\n")
	builder.WriteString("Approved plan summary:\n")
	builder.WriteString(workflowPromptSummary(planOutput))
	builder.WriteString("\n")
	return builder.String()
}

func buildPlanFixAuditAuditPrompt(request, planOutput, fixOutput string) string {
	var builder strings.Builder
	builder.WriteString("Audit the completed implementation against the original request and approved plan. Identify risks, regressions, and any follow-up actions.\n\n")
	fmt.Fprintf(&builder, "Original request:\n%s\n\n", request)
	builder.WriteString("Workflow context contract:\n")
	builder.WriteString("- include: previous.summary\n")
	builder.WriteString("- exclude: raw_tool_logs, large_file_contents\n")
	builder.WriteString("- max_tokens: 1600\n\n")
	builder.WriteString("Plan summary:\n")
	builder.WriteString(workflowPromptSummary(planOutput))
	builder.WriteString("\n\nImplementation summary:\n")
	builder.WriteString(workflowPromptSummary(fixOutput))
	builder.WriteString("\n")
	return builder.String()
}

func writeWorkflowStageSummaryList(builder *strings.Builder, stages []WorkflowStageResult) {
	if builder == nil {
		return
	}
	for _, stage := range stages {
		fmt.Fprintf(builder, "- %s/%s: %s\n", stage.Stage, stage.Agent, workflowPromptSummary(stage.Result.Output))
	}
}

func workflowPromptSummary(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= 600 {
		return text
	}
	return strings.TrimSpace(text[:597]) + "..."
}

func runSkillStage(ctx context.Context, runtimeRef *Runtime, agentID, input string, skill schema.Skill, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	return runSkillStageWithModel(ctx, runtimeRef, agentID, input, skill, workflowGraphStageModel{}, handler)
}

func runSkillStageWithModel(ctx context.Context, runtimeRef *Runtime, agentID, input string, skill schema.Skill, model workflowGraphStageModel, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	return runSkillStageWithModelAndOptions(ctx, runtimeRef, agentID, input, skill, model, workflowStageRunOptions{}, handler)
}

func runSkillStageWithModelAndOptions(ctx context.Context, runtimeRef *Runtime, agentID, input string, skill schema.Skill, model workflowGraphStageModel, options workflowStageRunOptions, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	runner, ok := runtimeRef.runners[agentID]
	if !ok {
		return schema.AgentResult{}, fmt.Errorf("workflow agent not configured: %s", agentID)
	}
	if err := validateWorkflowStageModelRoute(runtimeRef, model); err != nil {
		return schema.AgentResult{}, err
	}
	runner = runtimeRef.workflowStageRunnerWithModel(agentID, runner, model)
	manager := fixedSkillManager{skill: &skill}
	result, err := runner.runStreamWithOptions(ctx, input, manager, runtimeRef.mcp, runtimeRef.session, runtimeRef.audit, runtimeRef, agentRunOptions{AllowedTools: options.AllowedTools}, handler)
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(result.Model) == "" {
		result.Model = strings.TrimSpace(runner.profile.Model)
	}
	return result, nil
}

func validateWorkflowStageModelRoute(runtimeRef *Runtime, model workflowGraphStageModel) error {
	if !workflowGraphStageModelConfigured(model) {
		return nil
	}
	if model.MaxTokens < 0 {
		return fmt.Errorf("workflow stage model.max_tokens must not be negative")
	}
	provider := strings.TrimSpace(model.Provider)
	if provider == "" {
		return nil
	}
	if runtimeRef == nil {
		return fmt.Errorf("workflow stage model provider %s cannot be resolved without runtime", provider)
	}
	if _, ok := runtimeRef.clients[provider]; !ok {
		return fmt.Errorf("workflow stage references unknown model provider: %s", provider)
	}
	return nil
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

func copyAgentResultForWorkflow(result schema.AgentResult) schema.AgentResult {
	result.ToolResults = append([]schema.ToolResult(nil), result.ToolResults...)
	result.Structured = append([]schema.StructuredSection(nil), result.Structured...)
	result.AuditTrail = append([]schema.AuditEntry(nil), result.AuditTrail...)
	result.Findings = append([]schema.Finding(nil), result.Findings...)
	result.Changes = append([]schema.Change(nil), result.Changes...)
	result.Verification = append([]schema.Verification(nil), result.Verification...)
	result.ResponseMessage = schema.CopyMessage(result.ResponseMessage)
	return result
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
	planPrompt := buildPlanFixAuditPlanPrompt(request)
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
	fixPrompt := buildPlanFixAuditFixPrompt(request, planResult.Output)
	fixResult, err := continueAgentRun(ctx, w.runtime, workflowAgentFixer, fixPrompt, schema.ToolCall{}, "", schema.Message{}, schema.ToolResult{}, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", WorkflowStageFix, err)
	}
	if hasSuspendedToolResult(fixResult.ToolResults) {
		w.captureApprovalContext(workflowNamePlanFixAudit, WorkflowStageFix, request, fixResult.Output, fixResult.ResponseMessage, completed, fixResult.ToolResults)
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
	auditPrompt := buildPlanFixAuditAuditPrompt(request, planResult.Output, fixResult.Output)
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
	snapshot := w.runtime.session.Snapshot().Workflow
	snapshot.Summary = summary
	w.runtime.session.SetWorkflow(snapshot)
	w.runtime.session.UpdateWorkflowRunState(snapshot)
}

func (w *WorkflowRunner) persistWorkflowState(name, status string, nextStage WorkflowStage, request, summary, prompt string) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return
	}
	snapshot := session.WorkflowSnapshot{
		RunID:        w.runID,
		Name:         name,
		Status:       status,
		NextStage:    string(nextStage),
		Request:      request,
		Summary:      summary,
		LastApproval: prompt,
	}
	if status == "awaiting_tool_approval" {
		if pending, ok := w.pendingApprovalForWorkflowState(name, nextStage); ok {
			snapshot.PendingResponseMessage = schema.CopyMessage(pending.responseMessage)
		}
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
			snapshot.PendingArgumentsSummary = pending.ArgumentsSummary
			snapshot.PendingArgumentsArtifactRef = pending.ArgumentsArtifactRef
			snapshot.PendingArgumentsHash = pending.ArgumentsHash
			snapshot.PendingArgumentsBytes = pending.ArgumentsBytes
			snapshot.PendingArgumentsStoredBytes = pending.ArgumentsStoredBytes
			snapshot.PendingArgumentsExternalized = pending.ArgumentsExternalized
			break
		}
	}
	w.runtime.session.SetWorkflow(snapshot)
	w.runtime.session.UpdateWorkflowRunState(snapshot)
	if strings.TrimSpace(snapshot.RunID) != "" {
		w.runtime.session.AppendWorkflowRunEvent(snapshot.RunID, session.WorkflowRunEventSnapshot{
			Type:            "workflow_state",
			Stage:           snapshot.NextStage,
			Content:         workflowStateEventContent(snapshot),
			WorkflowName:    snapshot.Name,
			WorkflowStatus:  snapshot.Status,
			NextStage:       snapshot.NextStage,
			PendingApproval: strings.HasPrefix(snapshot.Status, "awaiting_"),
		})
	}
}

func (w *WorkflowRunner) pendingApprovalForWorkflowState(name string, nextStage WorkflowStage) (pendingApproval, bool) {
	if w == nil || w.runtime == nil || w.runtime.approvals == nil {
		return pendingApproval{}, false
	}
	for _, pending := range w.runtime.approvals.List() {
		if !strings.EqualFold(strings.TrimSpace(pending.workflow), strings.TrimSpace(name)) {
			continue
		}
		if strings.TrimSpace(string(nextStage)) != "" && !strings.EqualFold(strings.TrimSpace(string(pending.stage)), strings.TrimSpace(string(nextStage))) {
			continue
		}
		return pending, true
	}
	return pendingApproval{}, false
}

func workflowStateEventContent(snapshot session.WorkflowSnapshot) string {
	if strings.TrimSpace(snapshot.LastApproval) != "" {
		return snapshot.LastApproval
	}
	if strings.TrimSpace(snapshot.Summary) != "" {
		return snapshot.Summary
	}
	if strings.TrimSpace(snapshot.NextStage) != "" {
		return fmt.Sprintf("workflow %s %s at stage %s", snapshot.Name, snapshot.Status, snapshot.NextStage)
	}
	return fmt.Sprintf("workflow %s %s", snapshot.Name, snapshot.Status)
}

func (w *WorkflowRunner) captureApprovalContext(workflowName string, stage WorkflowStage, request, responseContent string, responseMessage schema.Message, completed []WorkflowStageResult, results []schema.ToolResult) {
	if w == nil || w.runtime == nil || w.runtime.approvals == nil {
		return
	}
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	if !w.runtime.AnnotatePendingApproval(callID, workflowName, stage, request, responseContent, responseMessage, completed) {
		return
	}
}

func (w *WorkflowRunner) captureSkillChainApprovalContext(stage WorkflowStage, request, responseContent string, responseMessage schema.Message, completed []WorkflowStageResult, results []schema.ToolResult, chain []schema.Skill, index int, stagePrompt string) {
	if w == nil || w.runtime == nil || w.runtime.approvals == nil {
		return
	}
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	_ = w.runtime.AnnotateSkillChainPendingApproval(callID, workflowNameSkillChain, stage, request, responseContent, responseMessage, completed, chain, index, stagePrompt)
}

func (w *WorkflowRunner) ResumePlanFixAudit(ctx context.Context, callID string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	return w.Resume(ctx, workflowNamePlanFixAudit, callID, approve, handler)
}

func (w *WorkflowRunner) resumeBuiltInWorkflowApproval(ctx context.Context, run session.WorkflowRunSnapshot, handler func(event schema.StreamEvent) error) (WorkflowResult, bool, error) {
	switch strings.ToLower(strings.TrimSpace(run.Name)) {
	case workflowNamePlanFixAudit:
		if !workflowStageEqual(string(WorkflowStageFix), run.NextStage) {
			return WorkflowResult{}, false, nil
		}
		result, err := w.resumePlanFixAuditStageApproval(ctx, run, handler)
		return result, true, err
	default:
		return WorkflowResult{}, false, nil
	}
}

func (w *WorkflowRunner) resumePlanFixAuditStageApproval(ctx context.Context, run session.WorkflowRunSnapshot, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	completed := workflowStageResultsFromRunSnapshots(run.CompletedStages)
	planStage, ok := workflowStageResultForStage(completed, WorkflowStagePlan)
	if !ok || strings.TrimSpace(planStage.Result.Output) == "" {
		return WorkflowResult{}, fmt.Errorf("workflow run %s cannot resume built-in approval without completed plan output", run.ID)
	}
	previousRunID := w.runID
	w.runID = run.ID
	defer func() { w.runID = previousRunID }()
	handler = w.recordWorkflowRunEvents(run.ID, handler)
	w.runtime.session.SetWorkflow(session.WorkflowSnapshot{
		RunID:     run.ID,
		Name:      run.Name,
		Status:    "running",
		NextStage: run.NextStage,
		Request:   run.Request,
		Summary:   run.Summary,
	})
	w.runtime.session.UpdateWorkflowRunState(w.runtime.session.Snapshot().Workflow)
	w.runtime.session.AppendWorkflowRunEvent(run.ID, session.WorkflowRunEventSnapshot{
		Type:           "workflow_approval_submitted",
		Stage:          string(WorkflowStageFix),
		Content:        "built-in workflow stage approval submitted",
		WorkflowName:   run.Name,
		WorkflowStatus: "running",
		NextStage:      string(WorkflowStageFix),
	})
	result, err := w.runPlanFixAuditApprovedStages(ctx, run.Request, completed, planStage.Result.Output, handler)
	if err != nil {
		w.failWorkflowRun(run.ID, run.Name, run.Request, err)
		return WorkflowResult{}, err
	}
	result.RunID = run.ID
	w.completeWorkflowRun(run.ID, result)
	return result, nil
}

func (w *WorkflowRunner) runPlanFixAuditApprovedStages(ctx context.Context, request string, completed []WorkflowStageResult, planOutput string, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	w.persistWorkflowState(workflowNamePlanFixAudit, "running", WorkflowStageFix, request, summarizeWorkflow(completed), "")
	if err := w.ensureWorkflowAgent(workflowAgentFixer); err != nil {
		return WorkflowResult{}, err
	}
	if err := w.runtime.SetActiveAgent(workflowAgentFixer); err != nil {
		return WorkflowResult{}, err
	}
	fixPrompt := buildPlanFixAuditFixPrompt(request, planOutput)
	fixResult, err := continueAgentRun(ctx, w.runtime, workflowAgentFixer, fixPrompt, schema.ToolCall{}, "", schema.Message{}, schema.ToolResult{}, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", WorkflowStageFix, err)
	}
	if hasSuspendedToolResult(fixResult.ToolResults) {
		w.captureApprovalContext(workflowNamePlanFixAudit, WorkflowStageFix, request, fixResult.Output, fixResult.ResponseMessage, completed, fixResult.ToolResults)
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
	auditPrompt := buildPlanFixAuditAuditPrompt(request, planOutput, fixResult.Output)
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

func workflowStageResultForStage(stages []WorkflowStageResult, target WorkflowStage) (WorkflowStageResult, bool) {
	for _, stage := range stages {
		if workflowStageEqual(string(stage.Stage), string(target)) {
			return stage, true
		}
	}
	return WorkflowStageResult{}, false
}

func workflowStageEqual(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func resumePlanFixAuditWorkflow(w *WorkflowRunner, ctx context.Context, pending pendingApproval, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	completed := append([]WorkflowStageResult(nil), pending.completed...)
	if approve {
		if err := w.runtime.rejectToolApprovalByRiskPolicy(pending.tool); err != nil {
			return WorkflowResult{}, err
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
		fixPrompt := buildPlanFixAuditFixPrompt(pending.request, planSummary)
		fixResult, err := continueAgentRun(ctx, w.runtime, workflowAgentFixer, fixPrompt, pending.call, pending.responseContent, pending.responseMessage, toolResult, handler)
		if err != nil {
			return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", WorkflowStageFix, err)
		}
		completed = append(completed, WorkflowStageResult{
			Stage:  WorkflowStageFix,
			Agent:  workflowAgentFixer,
			Result: fixResult,
		})

		if hasSuspendedToolResult(fixResult.ToolResults) {
			w.captureApprovalContext(workflowNamePlanFixAudit, WorkflowStageFix, pending.request, fixResult.Output, fixResult.ResponseMessage, completed[:len(completed)-1], fixResult.ToolResults)
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
		auditPrompt := buildPlanFixAuditAuditPrompt(pending.request, completed[0].Result.Output, completed[len(completed)-1].Result.Output)
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
	_ = decision
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
		_ = decision
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
	if err := w.runtime.rejectToolApprovalByRiskPolicy(pending.tool); err != nil {
		return WorkflowResult{}, err
	}
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
	stageResult, err := continueAgentRunWithSkill(ctx, w.runtime, pending.agent, stagePrompt, &skill, pending.call, pending.responseContent, pending.responseMessage, toolResult, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", pending.stage, err)
	}
	if hasSuspendedToolResult(stageResult.ToolResults) {
		w.captureSkillChainApprovalContext(pending.stage, pending.request, stageResult.Output, stageResult.ResponseMessage, completed, stageResult.ToolResults, chain, pending.skillIndex, stagePrompt)
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

func continueAgentRun(ctx context.Context, runtimeRef *Runtime, agentID, input string, approvedCall schema.ToolCall, approvedResponseContent string, approvedResponseMessage schema.Message, approvedResult schema.ToolResult, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	return continueAgentRunWithModelAndSkill(ctx, runtimeRef, agentID, input, workflowGraphStageModel{}, nil, approvedCall, approvedResponseContent, approvedResponseMessage, approvedResult, handler)
}

func continueAgentRunWithSkill(ctx context.Context, runtimeRef *Runtime, agentID, input string, matchedSkill *schema.Skill, approvedCall schema.ToolCall, approvedResponseContent string, approvedResponseMessage schema.Message, approvedResult schema.ToolResult, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	return continueAgentRunWithModelAndSkill(ctx, runtimeRef, agentID, input, workflowGraphStageModel{}, matchedSkill, approvedCall, approvedResponseContent, approvedResponseMessage, approvedResult, handler)
}

func continueAgentRunWithModelAndSkill(ctx context.Context, runtimeRef *Runtime, agentID, input string, model workflowGraphStageModel, matchedSkill *schema.Skill, approvedCall schema.ToolCall, approvedResponseContent string, approvedResponseMessage schema.Message, approvedResult schema.ToolResult, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	return continueAgentRunWithModelSkillAndOptions(ctx, runtimeRef, agentID, input, model, matchedSkill, workflowStageRunOptions{}, approvedCall, approvedResponseContent, approvedResponseMessage, approvedResult, handler)
}

func continueAgentRunWithModelSkillAndOptions(ctx context.Context, runtimeRef *Runtime, agentID, input string, model workflowGraphStageModel, matchedSkill *schema.Skill, options workflowStageRunOptions, approvedCall schema.ToolCall, approvedResponseContent string, approvedResponseMessage schema.Message, approvedResult schema.ToolResult, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	runner, ok := runtimeRef.runners[agentID]
	if !ok {
		return schema.AgentResult{}, fmt.Errorf("workflow agent not configured: %s", agentID)
	}
	runner = runtimeRef.workflowStageRunnerWithModel(agentID, runner, model)
	tools, err := runtimeRef.mcp.ListTools(ctx)
	if err != nil {
		return schema.AgentResult{}, fmt.Errorf("list tools: %w", err)
	}
	profile := applySkillToProfile(runner.profile, matchedSkill)
	profile = applyWorkflowStageAllowedTools(profile, options.AllowedTools)
	profile = ensureMinimumToolIterations(profile)
	mode := profile.Mode
	memoryText := ""
	promptContext := promptBudgetContext{}
	if runtimeRef != nil && runtimeRef.MemoryStore() != nil {
		if memoryContext, err := runtimeRef.MemoryStore().PromptContextFresh(ctx, input, runtimeRef.currentWorkflowRunID()); err == nil {
			memoryText = memoryContext.PromptText()
			promptContext.Memory = &memoryContext
		}
	}
	snapshot := runtimeRef.SessionSnapshot()
	systemPrompt := BuildSystemPrompt(profile, matchedSkill, snapshot)
	messages := dynamicContextMessages(memoryText, snapshot, input, []schema.Message{{Role: "user", Content: input}})
	collectedResults := make([]schema.ToolResult, 0, 1)
	if strings.TrimSpace(approvedCall.ID) != "" {
		messages = append(messages, responseMessageForApproval(approvedResponseContent, approvedCall, approvedResponseMessage))
		messages = compactMessagesForResumePrompt(messages)
	}
	if strings.TrimSpace(approvedResult.CallID) != "" || strings.TrimSpace(approvedResult.ToolName) != "" || strings.TrimSpace(approvedResult.Content) != "" {
		artifactRef := storeCompactedToolResultArtifact(runtimeRef.session, approvedResult, agentID, mode)
		messages = append(messages, schema.Message{Role: "tool", Name: approvedResult.ToolName, ToolCallID: approvedResult.CallID, Content: compactToolResultForPromptWithArtifact(approvedResult, artifactRef)})
		collectedResults = append(collectedResults, approvedResult)
	}
	execCtx := newExecutionContext(agentID, profile, tools, runtimeRef.audit, workspaceRoot(runtimeRef))
	execCtx.MemoryStore = runtimeRef.MemoryStore()
	execCtx.MemoryTaskID = runtimeRef.currentWorkflowRunID()
	execCtx.RiskPolicy = runtimeRef.toolRiskPolicy()
	execCtx.MCPServers = runtimeRef.MCPServerRefs()
	runtimeRef.applySessionApprovedTools(&execCtx)
	promptTools := filterPromptTools(profile, tools)
	promptToolContext := promptBudgetContextWithTools(promptContext, profile, tools)
	streamedText := false
	startedAt := time.Now()
	actionNudge := newActionNudgeTracker(profile, mode, matchedSkill, input)
	emitTaskStage(handler, runtimeRef.session, agentID, mode, initialTaskStageForMode(mode), "starting workflow stage")
	for i := 0; i <= profile.MaxIterations; i++ {
		finalResponseTurn := i == profile.MaxIterations
		if i > 0 && len(collectedResults) > 0 {
			emitTaskStage(handler, runtimeRef.session, agentID, mode, "summarize", "reasoning over tool observations")
		}
		emitModelWaitStatus(handler, agentID, mode, modelWaitStatusContext{AfterToolResults: i > 0 && len(collectedResults) > 0})
		request := schema.ChatRequest{Model: profile.Model, System: systemPrompt, Messages: messages, Tools: promptTools, Temperature: profile.Temperature, MaxTokens: profile.MaxTokens}
		emitPromptBudget(handler, runtimeRef.session, agentID, mode, request, matchedSkill, len(tools), promptToolContext)
		resp, err := runAgentChatWithRecovery(ctx, runner.llm, request, agentID, mode, handler, &streamedText, &messages)
		if err != nil {
			if isRecoverableToolArgumentError(err) {
				return runner.finalizeWithInvalidToolArguments(agentConversationState{Profile: profile, MatchedSkill: matchedSkill, SystemPrompt: systemPrompt, MemoryText: memoryText, PromptContext: promptContext, Mode: mode, Input: input, Messages: messages, StartedAt: startedAt}, collectedResults, false, i+1, handler, runtimeRef.audit)
			}
			return schema.AgentResult{}, fmt.Errorf("llm chat: %w", err)
		}
		emitTokenUsage(handler, runtimeRef.session, agentID, mode, resp.Usage)
		if len(resp.ToolCalls) == 0 {
			emitTaskStage(handler, runtimeRef.session, agentID, mode, "summarize", "preparing stage response")
			if strings.TrimSpace(runtimeRef.SessionSnapshot().Workflow.Name) != "" && strings.EqualFold(runtimeRef.SessionSnapshot().Workflow.Status, "running") {
				return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: matchedSkill, ToolResults: collectedResults, AgentID: agentID, Mode: mode, Model: profile.Model, ResponseMessage: schema.CopyMessage(resp.Message)}, nil
			}
			if !streamedText && handler != nil && resp.Message.Content != "" {
				if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: resp.Message.Content, AgentID: agentID, Mode: mode}); err != nil {
					return schema.AgentResult{}, err
				}
			}
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventDone, Content: resp.Message.Content, AgentID: agentID, Mode: mode})
			}
			return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: matchedSkill, ToolResults: collectedResults, AgentID: agentID, Mode: mode, Model: profile.Model, ResponseMessage: schema.CopyMessage(resp.Message)}, nil
		}
		runFinalToolCalls := finalResponseTurn && shouldRunFinalTurnToolCalls(execCtx, resp.ToolCalls)
		if finalResponseTurn && !runFinalToolCalls {
			return runner.finalizeAfterIterationBudget(ctx, agentConversationState{Profile: profile, MatchedSkill: matchedSkill, SystemPrompt: systemPrompt, MemoryText: memoryText, PromptContext: promptContext, Mode: mode, Input: input, Messages: messages, StartedAt: startedAt}, messages, collectedResults, false, i+1, handler, runtimeRef.audit, runtimeRef)
		}
		assistantMessage := assistantToolMessage(resp)
		messages = append(messages, assistantMessage)
		messages = compactMessagesForConversation(messages)
		batchProfile := classifyToolCallBatch(resp.ToolCalls, execCtx)
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
				return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: matchedSkill, ToolResults: collectedResults, AgentID: agentID, Mode: mode, Model: profile.Model, ResponseMessage: assistantMessage}, nil
			}
			artifactRef := storeCompactedToolResultArtifact(runtimeRef.session, result, agentID, mode)
			messages = append(messages, schema.Message{Role: "tool", Name: result.ToolName, ToolCallID: result.CallID, Content: compactToolResultForPromptWithArtifact(result, artifactRef)})
		}
		if runFinalToolCalls {
			return runner.finalizeAfterIterationBudget(ctx, agentConversationState{Profile: profile, MatchedSkill: matchedSkill, SystemPrompt: systemPrompt, MemoryText: memoryText, PromptContext: promptContext, Mode: mode, Input: input, Messages: messages, StartedAt: startedAt}, messages, collectedResults, false, i+1, handler, runtimeRef.audit, runtimeRef)
		}
		if nudge, ok := actionNudge.Next(batchProfile, len(results)); ok {
			if summary := toolObservationDigestMessage(collectedResults); strings.TrimSpace(summary) != "" {
				messages = append(messages, schema.Message{Role: "user", Content: summary})
			}
			messages = append(messages, schema.Message{Role: "user", Content: nudge})
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "enough context gathered; nudging model to act", AgentID: agentID, Mode: mode, NeedsAction: true})
			}
		}
		streamedText = false
	}
	return runner.finalizeWithLocalBudgetSummary(agentConversationState{Profile: profile, MatchedSkill: matchedSkill, SystemPrompt: systemPrompt, MemoryText: memoryText, PromptContext: promptContext, Mode: mode, Input: input, Messages: messages, StartedAt: startedAt}, collectedResults, false, profile.MaxIterations, handler, runtimeRef.audit)
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

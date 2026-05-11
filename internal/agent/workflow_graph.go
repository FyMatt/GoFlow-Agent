package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

type workflowGraph struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	Stages      []workflowGraphStage `yaml:"stages"`
}

type workflowGraphStage struct {
	Name               string                             `yaml:"name"`
	NodeType           string                             `yaml:"node_type"`
	Agent              string                             `yaml:"agent"`
	Skill              string                             `yaml:"skill"`
	Tool               string                             `yaml:"tool"`
	Params             map[string]string                  `yaml:"params"`
	Input              map[string]string                  `yaml:"input"`
	Outputs            map[string]string                  `yaml:"outputs"`
	Artifacts          []workflowGraphArtifact            `yaml:"artifacts"`
	AcceptanceCriteria []workflowGraphAcceptanceCriterion `yaml:"acceptance_criteria"`
	Acceptance         []workflowGraphAcceptanceCriterion `yaml:"acceptance"`
	Policy             string                             `yaml:"policy"`
	Condition          string                             `yaml:"condition"`
	Routes             map[string]string                  `yaml:"routes"`
	SwitchOn           string                             `yaml:"switch_on"`
	Cases              map[string]string                  `yaml:"cases"`
	Retry              workflowGraphRetry                 `yaml:"retry"`
	OnError            []string                           `yaml:"on_error"`
	Approval           bool                               `yaml:"approval"`
	NextStrategy       string                             `yaml:"next_strategy"`
	Next               []string                           `yaml:"next"`
}

type workflowGraphRetry struct {
	MaxAttempts int `yaml:"max_attempts"`
}

type workflowGraphArtifact struct {
	Name     string            `yaml:"name"`
	Kind     string            `yaml:"kind"`
	Title    string            `yaml:"title"`
	Ref      string            `yaml:"ref"`
	Summary  string            `yaml:"summary"`
	Content  string            `yaml:"content"`
	Metadata map[string]string `yaml:"metadata"`
}

type workflowGraphAcceptanceCriterion struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Ref         string `yaml:"ref"`
	Equals      string `yaml:"equals"`
	Contains    string `yaml:"contains"`
	Expected    string `yaml:"expected"`
	Exists      *bool  `yaml:"exists"`
}

func (w *WorkflowRunner) workflowFromGraph(name string) (workflowDefinition, bool, error) {
	graph, err := w.loadWorkflowGraph(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return workflowDefinition{}, false, nil
		}
		return workflowDefinition{}, false, fmt.Errorf("invalid workflow graph %q: %w", name, err)
	}
	return workflowDefinition{
		run: func(w *WorkflowRunner, ctx context.Context, request string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
			return w.runWorkflowGraph(ctx, graph, request, approve, handler)
		},
		resume: func(w *WorkflowRunner, ctx context.Context, pending pendingApproval, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
			return w.resumeWorkflowGraph(ctx, graph, pending, approve, handler)
		},
	}, true, nil
}

func (w *WorkflowRunner) loadWorkflowGraph(name string) (workflowGraph, error) {
	path, err := w.workflowGraphPath(name)
	if err != nil {
		return workflowGraph{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return workflowGraph{}, err
	}
	var graph workflowGraph
	if err := yaml.Unmarshal(data, &graph); err != nil {
		return workflowGraph{}, fmt.Errorf("parse workflow graph: %w", err)
	}
	graph = w.expandWorkflowGraphExecutableTeams(graph)
	if err := w.validateWorkflowGraph(normalizePersistedWorkflowName(name), graph); err != nil {
		return workflowGraph{}, err
	}
	return graph, nil
}

func (w *WorkflowRunner) expandWorkflowGraphExecutableTeams(graph workflowGraph) workflowGraph {
	if len(graph.Stages) == 0 {
		return graph
	}
	expanded := make([]workflowGraphStage, 0, len(graph.Stages))
	for _, stage := range graph.Stages {
		if !isTeamWorkflowNode(stage) || !workflowGraphTeamExecutionEnabled(stage) {
			expanded = append(expanded, stage)
			continue
		}
		template, ok := w.TeamTemplate(workflowGraphTeamTemplateName(stage))
		if !ok {
			expanded = append(expanded, stage)
			continue
		}
		roleStages := w.workflowGraphTeamRoleStages(stage, template)
		if len(roleStages) == 0 {
			expanded = append(expanded, stage)
			continue
		}
		control := stage
		control.Next = []string{roleStages[0].Name}
		expanded = append(expanded, control)
		expanded = append(expanded, roleStages...)
	}
	graph.Stages = expanded
	return graph
}

func workflowGraphTeamExecutionEnabled(stage workflowGraphStage) bool {
	for _, key := range []string{"execute", "run", "execute_roles", "run_roles"} {
		if workflowGraphParamBool(stage, key) {
			return true
		}
	}
	return false
}

func (w *WorkflowRunner) workflowGraphTeamRoleStages(parent workflowGraphStage, template TeamTemplate) []workflowGraphStage {
	roles := make([]TeamRoleTemplate, 0, len(template.RoleTemplates))
	for _, role := range template.RoleTemplates {
		if strings.TrimSpace(role.Agent) == "" || strings.TrimSpace(role.Skill) == "" {
			continue
		}
		roles = append(roles, role)
	}
	if len(roles) == 0 {
		return nil
	}
	stages := make([]workflowGraphStage, 0, len(roles))
	for i, role := range roles {
		name := workflowGraphTeamRoleStageName(parent.Name, role.Name)
		input := map[string]string{
			"team":         "stages." + parent.Name + ".outputs.team",
			"team_context": "stages." + parent.Name + ".outputs.summary",
			"role":         quoteWorkflowGraphLiteral(role.Name),
		}
		if i > 0 {
			input["previous_role_output"] = "stages." + workflowGraphTeamRoleStageName(parent.Name, roles[i-1].Name) + ".outputs.summary"
		}
		next := workflowGraphTeamRoleNextCandidates(parent, roles, i)
		params := workflowStageMetadata(map[string]string{
			"team":                  template.Name,
			"team_title":            template.Title,
			"team_stage":            parent.Name,
			"team_role":             role.Name,
			"team_role_label":       role.Label,
			"team_role_final":       strconv.FormatBool(i == len(roles)-1),
			"team_recommended_flow": template.RecommendedWorkflow,
			"responsibilities":      strings.Join(role.Responsibilities, "\n"),
			"consumes":              strings.Join(role.Consumes, ","),
			"produces":              strings.Join(role.Produces, ","),
			"tools":                 strings.Join(role.Tools, ","),
			"notes":                 role.Notes,
		})
		w.workflowGraphApplyTeamApprovalPreset(params, template.Name, parent.Params, false)
		policyKeys := append(workflowTeamEscalationPolicyParamKeys(), workflowTeamApprovalPolicyParamKeys()...)
		for _, key := range policyKeys {
			if value := strings.TrimSpace(parent.Params[key]); value != "" {
				if params == nil {
					params = make(map[string]string)
				}
				params[key] = value
			}
		}
		stages = append(stages, workflowGraphStage{
			Name:     name,
			NodeType: "team_role",
			Agent:    role.Agent,
			Skill:    role.Skill,
			Tool:     strings.Join(role.Tools, ","),
			Params:   params,
			Input:    input,
			Outputs: map[string]string{
				"summary":    "result.summary",
				"raw_output": "result.output",
			},
			Retry:        parent.Retry,
			Next:         next,
			NextStrategy: "team_handoff",
		})
	}
	return stages
}

func workflowGraphTeamRoleNextCandidates(parent workflowGraphStage, roles []TeamRoleTemplate, index int) []string {
	next := make([]string, 0, len(roles)+len(parent.Next))
	for i := index + 1; i < len(roles); i++ {
		next = append(next, workflowGraphTeamRoleStageName(parent.Name, roles[i].Name))
	}
	next = append(next, parent.Next...)
	return next
}

func workflowGraphTeamRoleStageName(parent, role string) string {
	return normalizePersistedWorkflowName(parent) + "__" + normalizePersistedWorkflowName(role)
}

func quoteWorkflowGraphLiteral(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return string(data)
}

func validateWorkflowGraph(invokedName string, graph workflowGraph) error {
	return validateWorkflowGraphWithTeamLookup(invokedName, graph, LoadTeamTemplate)
}

func (w *WorkflowRunner) validateWorkflowGraph(invokedName string, graph workflowGraph) error {
	return validateWorkflowGraphWithTeamLookup(invokedName, graph, w.TeamTemplate)
}

func validateWorkflowGraphWithTeamLookup(invokedName string, graph workflowGraph, loadTeamTemplate func(string) (TeamTemplate, bool)) error {
	if loadTeamTemplate == nil {
		loadTeamTemplate = LoadTeamTemplate
	}
	if strings.TrimSpace(graph.Name) == "" {
		return fmt.Errorf("workflow graph missing name")
	}
	if normalizeWorkflowSkillName(graph.Name) != normalizeWorkflowSkillName(invokedName) {
		return fmt.Errorf("workflow graph name %q does not match %q", graph.Name, invokedName)
	}
	if len(graph.Stages) == 0 {
		return fmt.Errorf("workflow graph %s has no stages", graph.Name)
	}
	seen := make(map[string]struct{}, len(graph.Stages))
	for i, stage := range graph.Stages {
		if strings.TrimSpace(stage.Name) == "" {
			return fmt.Errorf("workflow graph %s stage %d missing name", graph.Name, i)
		}
		if err := validateWorkflowGraphArtifacts(graph.Name, stage); err != nil {
			return err
		}
		key := normalizeWorkflowSkillName(stage.Name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("workflow graph %s has duplicate stage %q", graph.Name, stage.Name)
		}
		seen[key] = struct{}{}
		if isTeamWorkflowNode(stage) {
			teamName := workflowGraphTeamTemplateName(stage)
			if _, ok := loadTeamTemplate(teamName); !ok {
				return fmt.Errorf("workflow graph %s stage %s references unknown team template %s", graph.Name, stage.Name, teamName)
			}
			continue
		}
		if isRepeatWorkflowNode(stage) {
			if strings.TrimSpace(workflowGraphRepeatBodyName(stage)) == "" {
				return fmt.Errorf("workflow graph %s stage %s missing repeat body params.stage/body", graph.Name, stage.Name)
			}
			continue
		}
		if isSubWorkflowNode(stage) {
			subWorkflow := workflowGraphSubWorkflowName(stage)
			if strings.TrimSpace(subWorkflow) == "" {
				return fmt.Errorf("workflow graph %s stage %s missing sub workflow params.workflow", graph.Name, stage.Name)
			}
			if normalizePersistedWorkflowName(subWorkflow) == normalizePersistedWorkflowName(graph.Name) {
				return fmt.Errorf("workflow graph %s stage %s cannot call itself as a sub workflow", graph.Name, stage.Name)
			}
			if err := validatePersistedWorkflowName(normalizePersistedWorkflowName(subWorkflow)); err != nil {
				return fmt.Errorf("workflow graph %s stage %s references invalid sub workflow %q: %w", graph.Name, stage.Name, subWorkflow, err)
			}
			continue
		}
		if isVisualOnlyWorkflowNode(stage) || isControlWorkflowNode(stage) {
			continue
		}
		if strings.TrimSpace(stage.Agent) == "" {
			return fmt.Errorf("workflow graph %s stage %s missing agent", graph.Name, stage.Name)
		}
		if strings.TrimSpace(stage.Skill) == "" {
			return fmt.Errorf("workflow graph %s stage %s missing skill", graph.Name, stage.Name)
		}
		if stage.Retry.MaxAttempts < 0 {
			return fmt.Errorf("workflow graph %s stage %s retry.max_attempts must not be negative", graph.Name, stage.Name)
		}
	}
	for _, stage := range graph.Stages {
		if isRepeatWorkflowNode(stage) {
			bodyName := workflowGraphRepeatBodyName(stage)
			if _, ok := seen[normalizeWorkflowSkillName(bodyName)]; !ok {
				return fmt.Errorf("workflow graph %s stage %s references unknown repeat body stage %q", graph.Name, stage.Name, bodyName)
			}
		}
		for _, next := range workflowGraphStageNextReferences(stage) {
			if _, ok := seen[normalizeWorkflowSkillName(next)]; !ok {
				return fmt.Errorf("workflow graph %s stage %s references unknown next stage %q", graph.Name, stage.Name, next)
			}
		}
	}
	return nil
}

func validateWorkflowGraphArtifacts(graphName string, stage workflowGraphStage) error {
	seen := make(map[string]struct{}, len(stage.Artifacts))
	for i, artifact := range stage.Artifacts {
		label := fallbackWorkflowGraphValue(artifact.Name, fmt.Sprintf("#%d", i+1))
		name := normalizeWorkflowArtifactName(artifact.Name)
		if strings.TrimSpace(artifact.Name) != "" {
			if name == "" {
				return fmt.Errorf("workflow graph %s stage %s artifact %s has invalid name", graphName, stage.Name, label)
			}
			if _, ok := seen[name]; ok {
				return fmt.Errorf("workflow graph %s stage %s has duplicate artifact %q", graphName, stage.Name, artifact.Name)
			}
			seen[name] = struct{}{}
		}
		if strings.TrimSpace(artifact.Ref) == "" && strings.TrimSpace(artifact.Content) == "" {
			return fmt.Errorf("workflow graph %s stage %s artifact %s must declare ref or content", graphName, stage.Name, label)
		}
		if strings.TrimSpace(artifact.Kind) != "" && normalizeWorkflowArtifactKind(artifact.Kind) == "" {
			return fmt.Errorf("workflow graph %s stage %s artifact %s has invalid kind", graphName, stage.Name, label)
		}
	}
	return nil
}

func workflowGraphStageNextReferences(stage workflowGraphStage) []string {
	refs := append([]string(nil), stage.Next...)
	refs = append(refs, stage.OnError...)
	for _, target := range stage.Routes {
		refs = append(refs, target)
	}
	for _, target := range stage.Cases {
		refs = append(refs, target)
	}
	return refs
}

func (w *WorkflowRunner) runWorkflowGraph(ctx context.Context, graph workflowGraph, request string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	if len(graph.Stages) == 0 {
		return WorkflowResult{}, fmt.Errorf("workflow graph %s has no stages", graph.Name)
	}
	w.runtime.DisableWorkflowAutoApproval(graph.Name)
	return w.runWorkflowGraphQueue(ctx, graph, request, approve, []int{entryWorkflowGraphStageIndex(graph)}, nil, nil, nil, handler)
}

func (w *WorkflowRunner) runWorkflowGraphQueue(ctx context.Context, graph workflowGraph, request string, approve bool, queue []int, completed []WorkflowStageResult, manualInputs map[string]map[string]string, manualInputValues map[string]map[string]any, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	seen := completedStageNames(completed)
	for len(queue) > 0 {
		index := queue[0]
		queue = queue[1:]
		if index < 0 || index >= len(graph.Stages) {
			continue
		}
		stage := graph.Stages[index]
		stageKey := normalizeWorkflowSkillName(stage.Name)
		if _, ok := seen[stageKey]; ok {
			continue
		}
		if isVisualOnlyWorkflowNode(stage) {
			seen[stageKey] = struct{}{}
			queue = append(w.nextWorkflowGraphStageIndices(graph, index, request, "", completed), queue...)
			continue
		}
		if requiresWorkflowGraphCheckpointApproval(stage) && !approve {
			prompt := workflowGraphCheckpointPrompt(stage)
			w.persistWorkflowState(graph.Name, "awaiting_approval", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), prompt)
			return WorkflowResult{Name: graph.Name, Status: "awaiting_approval", PendingApproval: true, ApprovalPrompt: prompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
		}
		if w.requiresWorkflowGraphManualInput(stage, request, completed, manualInputs) {
			prompt := w.workflowGraphManualInputPrompt(stage, request, completed)
			w.persistWorkflowState(graph.Name, "awaiting_input", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), prompt)
			return WorkflowResult{Name: graph.Name, Status: "awaiting_input", PendingInput: true, PendingFields: w.workflowGraphManualInputFields(stage, completed), ApprovalPrompt: prompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
		}
		if isJoinWorkflowNode(stage) {
			missing := workflowGraphJoinMissingStages(graph, stage, completed)
			if len(missing) > 0 {
				if len(queue) == 0 || !workflowGraphQueueContainsAnyStage(graph, queue, missing) {
					return WorkflowResult{}, fmt.Errorf("workflow join stage %s is waiting for incomplete stages: %s", stage.Name, strings.Join(missing, ", "))
				}
				queue = append(queue, index)
				continue
			}
		}
		if isRepeatWorkflowNode(stage) {
			outcome, err := w.runWorkflowGraphRepeatStage(ctx, graph, index, request, completed, handler)
			if err != nil {
				return WorkflowResult{}, err
			}
			if outcome.Result != nil {
				return *outcome.Result, nil
			}
			completed = outcome.Completed
			seen = completedStageNames(completed)
			queue = append(outcome.Queue, queue...)
			continue
		}
		if isSubWorkflowNode(stage) {
			stageResult, pendingResult, err := w.runWorkflowGraphSubWorkflowStage(ctx, graph, stage, request, completed, approve, handler)
			if err != nil {
				return WorkflowResult{}, err
			}
			if pendingResult != nil {
				return *pendingResult, nil
			}
			completed = append(completed, stageResult)
			seen[stageKey] = struct{}{}
			queue = append(w.nextWorkflowGraphStageIndices(graph, index, request, stageResult.Result.Output, completed), queue...)
			continue
		}
		if isControlWorkflowNode(stage) {
			decision := w.controlWorkflowGraphStageDecision(graph, index, request, completed, manualInputs, manualInputValues)
			if len(decision.ExtraCompleted) > 0 {
				completed = append(completed, decision.ExtraCompleted...)
				seen = completedStageNames(completed)
			}
			controlResult := workflowGraphControlStageResult(stage, graph, decision)
			completed = append(completed, controlResult)
			seen[stageKey] = struct{}{}
			if decision.Halt {
				finalSummary := summarizeWorkflow(completed)
				status := fallbackWorkflowGraphValue(decision.Status, "blocked")
				w.runtime.DisableWorkflowAutoApproval(graph.Name)
				if err := w.runtime.RestoreDefaultAgent(); err != nil {
					return WorkflowResult{}, err
				}
				w.persistWorkflowState(graph.Name, status, WorkflowStage(stage.Name), request, finalSummary, decision.Reason)
				return WorkflowResult{Name: graph.Name, Status: status, CompletedStages: completed, FinalSummary: finalSummary, NextStage: WorkflowStage(stage.Name), ApprovalPrompt: decision.Reason}, nil
			}
			if workflowGraphConcurrentParallelEnabled(stage) && decision.Kind == "parallel" {
				outcome, handled, err := w.runWorkflowGraphConcurrentParallelBranches(ctx, graph, stage, request, approve, decision.Targets, completed, handler)
				if err != nil {
					return WorkflowResult{}, err
				}
				if handled {
					if outcome.Result != nil {
						return *outcome.Result, nil
					}
					completed = outcome.Completed
					seen = completedStageNames(completed)
					queue = append(outcome.Queue, queue...)
					continue
				}
			}
			queue = append(decision.Targets, queue...)
			continue
		}
		if stage.Approval && !approve {
			w.persistWorkflowState(graph.Name, "awaiting_approval", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), fmt.Sprintf("Workflow requires approval before running stage %s.", stage.Name))
			return WorkflowResult{Name: graph.Name, Status: "awaiting_approval", PendingApproval: true, ApprovalPrompt: fmt.Sprintf("Workflow requires approval before running stage %s.", stage.Name), CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
		}
		skill, err := w.graphStageSkill(stage)
		if err != nil {
			return WorkflowResult{}, err
		}
		if err := w.ensureWorkflowAgent(stage.Agent); err != nil {
			return WorkflowResult{}, err
		}
		inputs := resolveWorkflowGraphStageInputs(stage, request, completed)
		inputValues := resolveWorkflowGraphStageInputValues(stage, request, completed)
		prompt := buildWorkflowGraphStagePrompt(graph, stage, request, completed)
		w.persistWorkflowState(graph.Name, "running", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), "")
		if err := w.runtime.SetActiveAgent(stage.Agent); err != nil {
			return WorkflowResult{}, err
		}
		result, attempts, err := w.runWorkflowGraphExecutableStage(ctx, stage, prompt, skill, handler)
		if err != nil {
			errorTargets := w.workflowGraphNamedStageIndices(graph, stage.OnError, completed)
			if len(errorTargets) > 0 {
				errorResult := workflowGraphErrorStageResult(stage, err, inputs, inputValues, attempts)
				completed = append(completed, errorResult)
				seen[stageKey] = struct{}{}
				queue = append(errorTargets, queue...)
				continue
			}
			return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", stage.Name, err)
		}
		if hasSuspendedToolResult(result.ToolResults) {
			w.captureWorkflowGraphApprovalContext(graph, index, request, result.Output, completed, result.ToolResults, prompt)
			approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), stage.Agent)
			w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), approvalPrompt)
			return WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
		}
		completed = append(completed, workflowGraphStageResult(stage, result, inputs, inputValues, attempts))
		seen[stageKey] = struct{}{}
		queue = append(w.nextWorkflowGraphStageIndices(graph, index, request, result.Output, completed), queue...)
	}
	finalSummary := summarizeWorkflow(completed)
	w.runtime.DisableWorkflowAutoApproval(graph.Name)
	if err := w.runtime.RestoreDefaultAgent(); err != nil {
		return WorkflowResult{}, err
	}
	w.persistWorkflowState(graph.Name, "completed", "", request, finalSummary, "")
	return WorkflowResult{Name: graph.Name, Status: "completed", CompletedStages: completed, FinalSummary: finalSummary}, nil
}

func (w *WorkflowRunner) resumeWorkflowGraph(ctx context.Context, graph workflowGraph, pending pendingApproval, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	completed := append([]WorkflowStageResult(nil), pending.completed...)
	if pending.skillIndex < 0 || pending.skillIndex >= len(graph.Stages) {
		return WorkflowResult{}, fmt.Errorf("workflow %s resume context is missing graph stage %s", graph.Name, pending.stage)
	}
	if strings.TrimSpace(pending.graphRepeat.Kind) != "" {
		return w.resumeWorkflowGraphRepeatStage(ctx, graph, pending, approve, handler)
	}
	stage := graph.Stages[pending.skillIndex]
	if !approve {
		decision := w.runtime.ResolvePendingApproval(pending.call.ID, false)
		_ = decision
		result := schema.ToolResult{CallID: pending.call.ID, ToolName: pending.tool.Name, Content: fmt.Sprintf("tool %s denied by operator", pending.tool.Name), IsError: true, Denied: true}
		inputs := resolveWorkflowGraphStageInputs(stage, pending.request, completed)
		inputValues := resolveWorkflowGraphStageInputValues(stage, pending.request, completed)
		completed = append(completed, workflowGraphDeniedStageResult(stage, result, inputs, inputValues))
		w.runtime.DisableWorkflowAutoApproval(graph.Name)
		if err := w.runtime.RestoreDefaultAgent(); err != nil {
			return WorkflowResult{}, err
		}
		w.persistWorkflowState(graph.Name, "denied", WorkflowStage(stage.Name), pending.request, summarizeWorkflow(completed), result.Content)
		return WorkflowResult{Name: graph.Name, Status: "denied", CompletedStages: completed, NextStage: WorkflowStage(stage.Name), ApprovalPrompt: result.Content}, nil
	}
	toolResult, err := w.approveWorkflowGraphTool(ctx, pending)
	if err != nil {
		return WorkflowResult{}, err
	}
	skill, err := w.graphStageSkill(stage)
	if err != nil {
		return WorkflowResult{}, err
	}
	prompt := pending.stagePrompt
	if strings.TrimSpace(prompt) == "" {
		prompt = buildWorkflowGraphStagePrompt(graph, stage, pending.request, completed)
	}
	if err := w.ensureWorkflowAgent(stage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	if err := w.runtime.SetActiveAgent(stage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	result, err := continueAgentRunWithSkill(ctx, w.runtime, stage.Agent, prompt, &skill, pending.call, pending.responseContent, toolResult, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", stage.Name, err)
	}
	if hasSuspendedToolResult(result.ToolResults) {
		w.captureWorkflowGraphApprovalContext(graph, pending.skillIndex, pending.request, result.Output, completed, result.ToolResults, prompt)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), stage.Agent)
		w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(stage.Name), pending.request, summarizeWorkflow(completed), approvalPrompt)
		return WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
	}
	inputs := resolveWorkflowGraphStageInputs(stage, pending.request, completed)
	inputValues := resolveWorkflowGraphStageInputValues(stage, pending.request, completed)
	completed = append(completed, workflowGraphStageResult(stage, result, inputs, inputValues, 1))
	queue := w.nextWorkflowGraphStageIndices(graph, pending.skillIndex, pending.request, result.Output, completed)
	return w.runWorkflowGraphQueue(ctx, graph, pending.request, true, queue, completed, nil, nil, handler)
}

// ResumeInput resumes a graph workflow paused at an input_gate/manual_input node.
func (w *WorkflowRunner) ResumeInput(ctx context.Context, runID string, inputs map[string]string, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	return w.resumeInput(ctx, runID, inputs, nil, handler)
}

// ResumeInputValues resumes a graph workflow with typed manual input values.
func (w *WorkflowRunner) ResumeInputValues(ctx context.Context, runID string, inputValues map[string]any, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	return w.resumeInput(ctx, runID, workflowGraphStringInputsFromValues(inputValues), inputValues, handler)
}

func (w *WorkflowRunner) resumeInput(ctx context.Context, runID string, inputs map[string]string, inputValues map[string]any, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_input") {
		return WorkflowResult{}, fmt.Errorf("workflow run %s is not awaiting input", runID)
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		return WorkflowResult{}, err
	}
	index := workflowGraphStageIndexByName(graph, run.NextStage)
	if index < 0 {
		return WorkflowResult{}, fmt.Errorf("workflow run %s references unknown next stage %q", runID, run.NextStage)
	}
	stage := graph.Stages[index]
	completed := workflowStageResultsFromRunSnapshots(run.CompletedStages)
	if !w.isWorkflowGraphManualInputStage(stage, completed) {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s is not awaiting manual input", stage.Name)
	}
	validatedInputs, err := w.workflowGraphValidateManualInputs(stage, inputs, completed)
	if err != nil {
		return WorkflowResult{}, err
	}
	validatedInputValues := w.workflowGraphManualInputTypedValues(stage, validatedInputs, inputValues, completed)
	manualInputs := map[string]map[string]string{
		normalizeWorkflowSkillName(stage.Name): validatedInputs,
	}
	manualInputValues := map[string]map[string]any{
		normalizeWorkflowSkillName(stage.Name): validatedInputValues,
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
		Type:           "workflow_input_submitted",
		Stage:          stage.Name,
		Content:        workflowGraphJSON(inputs),
		WorkflowName:   run.Name,
		WorkflowStatus: "running",
		NextStage:      stage.Name,
	})
	result, err := w.runWorkflowGraphQueue(ctx, graph, run.Request, true, []int{index}, completed, manualInputs, manualInputValues, handler)
	if err != nil {
		w.failWorkflowRun(run.ID, run.Name, run.Request, err)
		return WorkflowResult{}, err
	}
	result.RunID = run.ID
	w.completeWorkflowRun(run.ID, result)
	return result, nil
}

// ResumeApproval resumes a graph workflow paused at a checkpoint or stage approval gate.
func (w *WorkflowRunner) ResumeApproval(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_approval") {
		return WorkflowResult{}, fmt.Errorf("workflow run %s is not awaiting approval", runID)
	}
	if result, handled, err := w.resumeBuiltInWorkflowApproval(ctx, run, handler); handled || err != nil {
		return result, err
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		return WorkflowResult{}, err
	}
	index := workflowGraphStageIndexByName(graph, run.NextStage)
	if index < 0 {
		return WorkflowResult{}, fmt.Errorf("workflow run %s references unknown next stage %q", runID, run.NextStage)
	}
	stage := graph.Stages[index]
	if !requiresWorkflowGraphCheckpointApproval(stage) && !stage.Approval {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s is not awaiting approval", stage.Name)
	}
	completed := workflowStageResultsFromRunSnapshots(run.CompletedStages)
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
		Stage:          stage.Name,
		Content:        "workflow approval submitted",
		WorkflowName:   run.Name,
		WorkflowStatus: "running",
		NextStage:      stage.Name,
	})
	result, err := w.runWorkflowGraphQueue(ctx, graph, run.Request, true, []int{index}, completed, nil, nil, handler)
	if err != nil {
		w.failWorkflowRun(run.ID, run.Name, run.Request, err)
		return WorkflowResult{}, err
	}
	result.RunID = run.ID
	w.completeWorkflowRun(run.ID, result)
	return result, nil
}

// ResumeSubWorkflow resumes a parent graph workflow after its nested sub-workflow has completed.
func (w *WorkflowRunner) ResumeSubWorkflow(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_sub_workflow") {
		return WorkflowResult{}, fmt.Errorf("workflow run %s is not awaiting a sub-workflow", runID)
	}
	childRunID := strings.TrimSpace(run.PendingSubWorkflowRunID)
	if childRunID == "" {
		return WorkflowResult{}, fmt.Errorf("workflow run %s has no pending sub-workflow run id", runID)
	}
	childRun, ok := w.runtime.session.WorkflowRun(childRunID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("pending sub-workflow run not found: %s", childRunID)
	}
	if !strings.EqualFold(strings.TrimSpace(childRun.Status), "completed") {
		return WorkflowResult{}, fmt.Errorf("pending sub-workflow run %s is %s, not completed", childRunID, childRun.Status)
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		return WorkflowResult{}, err
	}
	index := workflowGraphStageIndexByName(graph, run.NextStage)
	if index < 0 {
		return WorkflowResult{}, fmt.Errorf("workflow run %s references unknown sub-workflow stage %q", runID, run.NextStage)
	}
	stage := graph.Stages[index]
	if !isSubWorkflowNode(stage) {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s is not a sub-workflow", stage.Name)
	}
	completed := workflowStageResultsFromRunSnapshots(run.CompletedStages)
	childCompleted := workflowStageResultsFromRunSnapshots(childRun.CompletedStages)
	childName := fallbackWorkflowGraphValue(run.PendingSubWorkflowName, childRun.Name)
	stageResult := workflowGraphSubWorkflowStageResult(stage, childName, childRun.ID, childRun.Status, childRun.Summary, childCompleted, resolveWorkflowGraphStageInputs(stage, run.Request, completed))
	completed = append(completed, stageResult)
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
		Type:           "workflow_sub_workflow_resumed",
		Stage:          stage.Name,
		Content:        fmt.Sprintf("sub-workflow %s completed in run %s", childName, childRun.ID),
		WorkflowName:   run.Name,
		WorkflowStatus: "running",
		NextStage:      stage.Name,
	})
	queue := w.nextWorkflowGraphStageIndices(graph, index, run.Request, stageResult.Result.Output, completed)
	result, err := w.runWorkflowGraphQueue(ctx, graph, run.Request, true, queue, completed, nil, nil, handler)
	if err != nil {
		w.failWorkflowRun(run.ID, run.Name, run.Request, err)
		return WorkflowResult{}, err
	}
	result.RunID = run.ID
	w.completeWorkflowRun(run.ID, result)
	return result, nil
}

func (w *WorkflowRunner) graphStageSkill(stage workflowGraphStage) (schema.Skill, error) {
	skill, ok := findWorkflowSkillByName(w.runtime.skills.List(), stage.Skill)
	if !ok {
		return schema.Skill{}, fmt.Errorf("workflow stage %s references unknown skill %s", stage.Name, stage.Skill)
	}
	return skill, nil
}

func isVisualOnlyWorkflowNode(stage workflowGraphStage) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "start", "end":
		return true
	default:
		return false
	}
}

func isControlWorkflowNode(stage workflowGraphStage) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "condition", "switch", "router", "policy_guard", "guard", "quality_gate", "quality-guard", "quality_guard", "checkpoint", "manual_approval", "approval_gate", "input_gate", "manual_input", "parallel", "fan_out", "fork", "join", "merge", "barrier", "team", "agent_team", "team_template", "for_each", "foreach", "map", "loop", "until", "while", "sub_workflow", "subworkflow", "workflow":
		return true
	default:
		return false
	}
}

func isRepeatWorkflowNode(stage workflowGraphStage) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "for_each", "foreach", "map", "loop", "until", "while":
		return true
	default:
		return false
	}
}

func isSubWorkflowNode(stage workflowGraphStage) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "sub_workflow", "subworkflow", "workflow":
		return true
	default:
		return false
	}
}

func isTeamWorkflowNode(stage workflowGraphStage) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "team", "agent_team", "team_template":
		return true
	default:
		return false
	}
}

func entryWorkflowGraphStageIndex(graph workflowGraph) int {
	for index, stage := range graph.Stages {
		if normalizeWorkflowSkillName(stage.NodeType) == "start" {
			return index
		}
	}
	for index, stage := range graph.Stages {
		if !isVisualOnlyWorkflowNode(stage) {
			return index
		}
	}
	return 0
}

func (w *WorkflowRunner) nextWorkflowGraphStageIndices(graph workflowGraph, currentIndex int, request, stageOutput string, completed []WorkflowStageResult) []int {
	stage := graph.Stages[currentIndex]
	candidates := w.nextWorkflowGraphStageCandidates(graph, stage, completed)
	if len(candidates) == 0 && len(stage.Next) == 0 && currentIndex+1 < len(graph.Stages) && !workflowGraphImplicitSequentialBlocked(graph, currentIndex, completed) {
		candidates = []int{currentIndex + 1}
	}
	if len(candidates) == 0 {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(stage.NextStrategy)) {
	case "team_handoff", "team-handoff", "role_handoff", "role-handoff", "handoff":
		if explicit := filterExplicitWorkflowGraphStages(workflowGraphTeamHandoffStageNames(stage, stageOutput), graph, candidates); len(explicit) > 0 {
			return explicit
		}
		if explicit := filterExplicitWorkflowGraphStages(parseExplicitNextSkillNames(stageOutput), graph, candidates); len(explicit) > 0 {
			return explicit
		}
		return candidates[:1]
	case "select", "best", "conditional", "planner_select", "planner-select", "dynamic", "graph":
		if explicit := filterExplicitWorkflowGraphStages(parseExplicitNextSkillNames(stageOutput), graph, candidates); len(explicit) > 0 {
			return explicit
		}
		return bestWorkflowGraphStageCandidates(graph, candidates, request+"\n"+stageOutput)
	default:
		return candidates
	}
}

func workflowGraphImplicitSequentialBlocked(graph workflowGraph, currentIndex int, completed []WorkflowStageResult) bool {
	if currentIndex < 0 || currentIndex >= len(graph.Stages) || len(completed) < 2 {
		return false
	}
	current := graph.Stages[currentIndex]
	last := completed[len(completed)-1]
	if normalizeWorkflowSkillName(string(last.Stage)) != normalizeWorkflowSkillName(current.Name) {
		return false
	}
	previous := completed[len(completed)-2]
	if !workflowGraphExclusiveControlResult(previous) {
		return false
	}
	target := ""
	if previous.Metadata != nil {
		target = previous.Metadata["target"]
	}
	if strings.TrimSpace(target) == "" && previous.Output.Variables != nil {
		target = previous.Output.Variables["target"]
	}
	for _, name := range workflowGraphCSVStageNames(target) {
		if normalizeWorkflowSkillName(name) == normalizeWorkflowSkillName(current.Name) {
			return true
		}
	}
	return false
}

func workflowGraphExclusiveControlResult(result WorkflowStageResult) bool {
	if result.Metadata == nil || result.Metadata["control"] != "true" {
		return false
	}
	kind := result.Output.Variables["kind"]
	if strings.TrimSpace(kind) == "" {
		kind = result.NodeType
	}
	switch normalizeWorkflowSkillName(kind) {
	case "condition", "switch", "router", "policy_guard", "guard", "quality_gate", "quality_guard", "quality-guard":
		return true
	default:
		return false
	}
}

type workflowGraphControlDecision struct {
	Targets        []int
	Kind           string
	Route          string
	Value          string
	Target         string
	Passed         bool
	Status         string
	Reason         string
	Halt           bool
	Variables      map[string]string
	ValueVariables map[string]any
	ExtraCompleted []WorkflowStageResult
}

type workflowGraphRepeatContext struct {
	Kind          string
	ControlIndex  int
	BodyIndex     int
	NextIteration int
	MaxIterations int
	Until         string
	Items         []string
}

type workflowGraphRepeatOutcome struct {
	Completed []WorkflowStageResult
	Queue     []int
	Result    *WorkflowResult
}

type workflowGraphParallelOutcome struct {
	Completed []WorkflowStageResult
	Queue     []int
	Result    *WorkflowResult
}

type workflowGraphParallelBranchPlan struct {
	StartIndex   int
	StageIndices []int
	JoinIndex    int
	Agents       []string
}

type workflowGraphParallelBranchResult struct {
	Index       int
	Stage       workflowGraphStage
	Result      schema.AgentResult
	Inputs      map[string]string
	InputValues map[string]any
	Prompt      string
	Attempts    int
	Completed   []WorkflowStageResult
	Queue       []int
	Suspended   bool
	Err         error
}

func (w *WorkflowRunner) runWorkflowGraphConcurrentParallelBranches(ctx context.Context, graph workflowGraph, control workflowGraphStage, request string, approve bool, targets []int, completed []WorkflowStageResult, handler func(event schema.StreamEvent) error) (workflowGraphParallelOutcome, bool, error) {
	if len(targets) < 2 {
		return workflowGraphParallelOutcome{}, false, nil
	}
	plans := make([]workflowGraphParallelBranchPlan, 0, len(targets))
	usedStages := make(map[int]struct{})
	usedAgents := make(map[string]struct{})
	joinIndex := -1
	for _, index := range targets {
		plan, ok := w.workflowGraphConcurrentBranchPlan(graph, index, completed)
		if !ok {
			return workflowGraphParallelOutcome{}, false, nil
		}
		if joinIndex >= 0 && joinIndex != plan.JoinIndex {
			return workflowGraphParallelOutcome{}, false, nil
		}
		joinIndex = plan.JoinIndex
		for _, stageIndex := range plan.StageIndices {
			if _, exists := usedStages[stageIndex]; exists {
				return workflowGraphParallelOutcome{}, false, nil
			}
			usedStages[stageIndex] = struct{}{}
		}
		planAgents := make(map[string]struct{}, len(plan.Agents))
		for _, agentID := range plan.Agents {
			key := normalizeWorkflowSkillName(agentID)
			if key == "" {
				continue
			}
			planAgents[key] = struct{}{}
		}
		for key := range planAgents {
			if _, exists := usedAgents[key]; exists {
				return workflowGraphParallelOutcome{}, false, nil
			}
			usedAgents[key] = struct{}{}
		}
		plans = append(plans, plan)
	}
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: fmt.Sprintf("running %d workflow branches concurrently from %s", len(plans), control.Name), AgentID: "workflow", Mode: "workflow", NeedsAction: true})
	}
	safeHandler := synchronizedWorkflowGraphHandler(handler)
	results := make([]workflowGraphParallelBranchResult, len(plans))
	var wg sync.WaitGroup
	for i := range plans {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = w.runWorkflowGraphConcurrentBranchPlan(ctx, graph, plans[i], control.Name, request, completed, safeHandler)
		}()
	}
	wg.Wait()

	nextQueue := make([]int, 0, len(results))
	successful := make([]WorkflowStageResult, 0, len(results))
	var suspended *workflowGraphParallelBranchResult
	for i := range results {
		branch := results[i]
		if branch.Err != nil {
			return workflowGraphParallelOutcome{}, true, fmt.Errorf("workflow stage %s: %w", branch.Stage.Name, branch.Err)
		}
		successful = append(successful, branch.Completed...)
		if branch.Suspended || hasSuspendedToolResult(branch.Result.ToolResults) {
			if suspended == nil {
				copied := branch
				suspended = &copied
			}
			continue
		}
		nextQueue = append(nextQueue, branch.Queue...)
	}
	completed = append(completed, successful...)
	if suspended != nil {
		w.captureWorkflowGraphApprovalContext(graph, suspended.Index, request, suspended.Result.Output, completed, suspended.Result.ToolResults, suspended.Prompt)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(suspended.Result.ToolResults), suspended.Stage.Agent)
		w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(suspended.Stage.Name), request, summarizeWorkflow(completed), approvalPrompt)
		return workflowGraphParallelOutcome{Result: &WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(suspended.Stage.Name)}}, true, nil
	}
	return workflowGraphParallelOutcome{Completed: completed, Queue: uniqueWorkflowGraphStageIndices(nextQueue)}, true, nil
}

func (w *WorkflowRunner) runWorkflowGraphConcurrentBranchPlan(ctx context.Context, graph workflowGraph, plan workflowGraphParallelBranchPlan, parent string, request string, completed []WorkflowStageResult, handler func(event schema.StreamEvent) error) workflowGraphParallelBranchResult {
	result := workflowGraphParallelBranchResult{Index: plan.StartIndex, Stage: graph.Stages[plan.StartIndex]}
	branchCompleted := append([]WorkflowStageResult(nil), completed...)
	baseLen := len(branchCompleted)
	for _, index := range plan.StageIndices {
		stage := graph.Stages[index]
		result.Index = index
		result.Stage = stage
		skill, err := w.graphStageSkill(stage)
		if err != nil {
			result.Err = err
			return result
		}
		inputs := resolveWorkflowGraphStageInputs(stage, request, branchCompleted)
		inputValues := resolveWorkflowGraphStageInputValues(stage, request, branchCompleted)
		prompt := buildWorkflowGraphStagePrompt(graph, stage, request, branchCompleted)
		w.persistWorkflowState(graph.Name, "running", WorkflowStage(stage.Name), request, summarizeWorkflow(branchCompleted), "")
		stageResult, attempts, err := w.runWorkflowGraphExecutableStage(ctx, stage, prompt, skill, handler)
		result.Result = stageResult
		result.Inputs = inputs
		result.InputValues = inputValues
		result.Prompt = prompt
		result.Attempts = attempts
		if err != nil {
			errorTargets := w.workflowGraphNamedStageIndices(graph, stage.OnError, branchCompleted)
			if len(errorTargets) > 0 {
				failed := workflowGraphErrorStageResult(stage, err, inputs, inputValues, attempts)
				workflowGraphMarkParallelBranchResult(&failed, parent)
				branchCompleted = append(branchCompleted, failed)
				result.Completed = append([]WorkflowStageResult(nil), branchCompleted[baseLen:]...)
				result.Queue = errorTargets
				return result
			}
			result.Err = err
			return result
		}
		if hasSuspendedToolResult(stageResult.ToolResults) {
			result.Suspended = true
			result.Completed = append([]WorkflowStageResult(nil), branchCompleted[baseLen:]...)
			return result
		}
		completedStage := workflowGraphStageResult(stage, stageResult, inputs, inputValues, attempts)
		workflowGraphMarkParallelBranchResult(&completedStage, parent)
		branchCompleted = append(branchCompleted, completedStage)
	}
	result.Completed = append([]WorkflowStageResult(nil), branchCompleted[baseLen:]...)
	result.Queue = []int{plan.JoinIndex}
	return result
}

func workflowGraphMarkParallelBranchResult(result *WorkflowStageResult, parent string) {
	if result == nil {
		return
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	result.Metadata["parallel_branch"] = "true"
	result.Metadata["parallel_parent"] = parent
}

func (w *WorkflowRunner) workflowGraphConcurrentBranchEligible(graph workflowGraph, index int, completed []WorkflowStageResult) bool {
	_, ok := w.workflowGraphConcurrentBranchPlan(graph, index, completed)
	return ok
}

func (w *WorkflowRunner) workflowGraphParallelValidations(graph workflowGraph) []WorkflowGraphParallelValidation {
	if len(graph.Stages) == 0 {
		return nil
	}
	out := make([]WorkflowGraphParallelValidation, 0)
	for index, stage := range graph.Stages {
		switch normalizeWorkflowSkillName(stage.NodeType) {
		case "parallel", "fan_out", "fork":
		default:
			continue
		}
		validation := w.workflowGraphParallelValidation(graph, index)
		out = append(out, validation)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (w *WorkflowRunner) workflowGraphParallelValidation(graph workflowGraph, index int) WorkflowGraphParallelValidation {
	stage := graph.Stages[index]
	enabled := workflowGraphConcurrentParallelEnabled(stage)
	targets := w.nextWorkflowGraphStageIndices(graph, index, "", "", nil)
	validation := WorkflowGraphParallelValidation{
		Stage:       stage.Name,
		Enabled:     enabled,
		BranchCount: len(targets),
	}
	addIssue := func(level, field, message string) {
		validation.Issues = append(validation.Issues, WorkflowGraphValidationIssue{Level: level, Stage: stage.Name, Field: field, Message: message})
	}
	if !enabled {
		addIssue("info", "params.concurrent", "concurrent execution is not requested; set params.concurrent=true to enable runtime parallel branch execution")
	}
	if len(targets) < 2 {
		addIssue("warning", "next", "concurrent execution requires at least two branch targets")
	}
	plans := make([]workflowGraphParallelBranchPlan, 0, len(targets))
	usedStages := make(map[int]string)
	usedAgents := make(map[string]string)
	joinIndex := -1
	for _, target := range targets {
		branch, plan, ok := w.workflowGraphConcurrentBranchValidation(graph, target, nil)
		validation.Branches = append(validation.Branches, branch)
		if !ok {
			continue
		}
		if joinIndex >= 0 && joinIndex != plan.JoinIndex {
			addIssue("warning", "join", fmt.Sprintf("branch %s joins %s, but previous branches join %s", branch.Start, branch.Join, graph.Stages[joinIndex].Name))
		}
		joinIndex = plan.JoinIndex
		for _, stageIndex := range plan.StageIndices {
			if owner, exists := usedStages[stageIndex]; exists {
				addIssue("warning", "branch", fmt.Sprintf("branch %s overlaps stage %s already used by branch %s", branch.Start, graph.Stages[stageIndex].Name, owner))
				continue
			}
			usedStages[stageIndex] = branch.Start
		}
		branchAgents := make(map[string]struct{}, len(plan.Agents))
		for _, agentID := range plan.Agents {
			key := normalizeWorkflowSkillName(agentID)
			if key != "" {
				branchAgents[key] = struct{}{}
			}
		}
		for key := range branchAgents {
			if owner, exists := usedAgents[key]; exists {
				addIssue("warning", "agent", fmt.Sprintf("agent %s is used by both branch %s and branch %s", key, owner, branch.Start))
				continue
			}
			usedAgents[key] = branch.Start
		}
		plans = append(plans, plan)
	}
	if joinIndex >= 0 && joinIndex < len(graph.Stages) {
		validation.Join = graph.Stages[joinIndex].Name
	}
	validation.Eligible = enabled && len(targets) >= 2 && len(plans) == len(targets) && !workflowGraphParallelValidationHasWarnings(validation.Issues)
	return validation
}

func (w *WorkflowRunner) workflowGraphConcurrentBranchValidation(graph workflowGraph, index int, completed []WorkflowStageResult) (WorkflowGraphParallelBranchValidation, workflowGraphParallelBranchPlan, bool) {
	branch := WorkflowGraphParallelBranchValidation{}
	if index < 0 || index >= len(graph.Stages) {
		branch.Reason = "branch target index is out of range"
		branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Field: "next", Message: branch.Reason})
		return branch, workflowGraphParallelBranchPlan{}, false
	}
	branch.Start = graph.Stages[index].Name
	plan := workflowGraphParallelBranchPlan{StartIndex: index}
	seen := make(map[int]struct{})
	current := index
	for {
		if current < 0 || current >= len(graph.Stages) {
			branch.Reason = "branch path leaves the workflow stage list"
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: branch.Start, Field: "next", Message: branch.Reason})
			return branch, plan, false
		}
		if _, exists := seen[current]; exists {
			branch.Reason = "branch path loops before reaching a join node"
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: graph.Stages[current].Name, Field: "next", Message: branch.Reason})
			return branch, plan, false
		}
		seen[current] = struct{}{}
		stage := graph.Stages[current]
		if reason := workflowGraphConcurrentStageIneligibleReason(stage); reason != "" {
			branch.Reason = reason
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: stage.Name, Field: "node_type", Message: reason})
			return branch, plan, false
		}
		if strings.TrimSpace(stage.NextStrategy) != "" {
			branch.Reason = "branch stage uses next_strategy, so runtime cannot determine a deterministic join path before execution"
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: stage.Name, Field: "next_strategy", Message: branch.Reason})
			return branch, plan, false
		}
		if workflowGraphImplicitSequentialBlocked(graph, current, completed) {
			branch.Reason = "branch is blocked by a prior exclusive control decision"
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: stage.Name, Field: "next", Message: branch.Reason})
			return branch, plan, false
		}
		if _, err := w.graphStageSkill(stage); err != nil {
			branch.Reason = err.Error()
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: stage.Name, Field: "skill", Message: branch.Reason})
			return branch, plan, false
		}
		if err := w.ensureWorkflowAgent(stage.Agent); err != nil {
			branch.Reason = err.Error()
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: stage.Name, Field: "agent", Message: branch.Reason})
			return branch, plan, false
		}
		plan.StageIndices = append(plan.StageIndices, current)
		plan.Agents = append(plan.Agents, stage.Agent)
		branch.Stages = append(branch.Stages, stage.Name)
		branch.Agents = append(branch.Agents, stage.Agent)
		next := w.nextWorkflowGraphStageCandidates(graph, stage, completed)
		if len(next) != 1 {
			branch.Reason = fmt.Sprintf("branch stage must have exactly one deterministic next target before the join; found %d", len(next))
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: stage.Name, Field: "next", Message: branch.Reason})
			return branch, plan, false
		}
		nextIndex := next[0]
		if nextIndex < 0 || nextIndex >= len(graph.Stages) {
			branch.Reason = "branch next target index is out of range"
			branch.Issues = append(branch.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: stage.Name, Field: "next", Message: branch.Reason})
			return branch, plan, false
		}
		if isJoinWorkflowNode(graph.Stages[nextIndex]) {
			plan.JoinIndex = nextIndex
			branch.Join = graph.Stages[nextIndex].Name
			branch.Eligible = true
			return branch, plan, true
		}
		current = nextIndex
	}
}

func workflowGraphConcurrentStageIneligibleReason(stage workflowGraphStage) string {
	switch {
	case isVisualOnlyWorkflowNode(stage):
		return "visual-only start/end nodes cannot execute as concurrent branch stages"
	case isRepeatWorkflowNode(stage):
		return "repeat nodes cannot execute inside concurrent branches"
	case isSubWorkflowNode(stage):
		return "sub-workflow nodes cannot execute inside concurrent branches"
	case isJoinWorkflowNode(stage):
		return "join nodes terminate branches and cannot be branch work stages"
	case isControlWorkflowNode(stage):
		return "control nodes cannot execute inside concurrent branches"
	case stage.Approval:
		return "approval-gated branch stages cannot run concurrently"
	default:
		return ""
	}
}

func workflowGraphParallelValidationHasWarnings(issues []WorkflowGraphValidationIssue) bool {
	for _, issue := range issues {
		switch strings.ToLower(strings.TrimSpace(issue.Level)) {
		case "warning", "error":
			return true
		}
	}
	return false
}

func (w *WorkflowRunner) workflowGraphConcurrentBranchPlan(graph workflowGraph, index int, completed []WorkflowStageResult) (workflowGraphParallelBranchPlan, bool) {
	if index < 0 || index >= len(graph.Stages) {
		return workflowGraphParallelBranchPlan{}, false
	}
	plan := workflowGraphParallelBranchPlan{StartIndex: index}
	seen := make(map[int]struct{})
	current := index
	for {
		if current < 0 || current >= len(graph.Stages) {
			return workflowGraphParallelBranchPlan{}, false
		}
		if _, exists := seen[current]; exists {
			return workflowGraphParallelBranchPlan{}, false
		}
		seen[current] = struct{}{}
		stage := graph.Stages[current]
		if !workflowGraphConcurrentExecutableStageEligible(stage) {
			return workflowGraphParallelBranchPlan{}, false
		}
		if strings.TrimSpace(stage.NextStrategy) != "" {
			return workflowGraphParallelBranchPlan{}, false
		}
		if workflowGraphImplicitSequentialBlocked(graph, current, completed) {
			return workflowGraphParallelBranchPlan{}, false
		}
		if _, err := w.graphStageSkill(stage); err != nil {
			return workflowGraphParallelBranchPlan{}, false
		}
		if err := w.ensureWorkflowAgent(stage.Agent); err != nil {
			return workflowGraphParallelBranchPlan{}, false
		}
		plan.StageIndices = append(plan.StageIndices, current)
		plan.Agents = append(plan.Agents, stage.Agent)
		next := w.nextWorkflowGraphStageCandidates(graph, stage, completed)
		if len(next) != 1 {
			return workflowGraphParallelBranchPlan{}, false
		}
		nextIndex := next[0]
		if nextIndex < 0 || nextIndex >= len(graph.Stages) {
			return workflowGraphParallelBranchPlan{}, false
		}
		if isJoinWorkflowNode(graph.Stages[nextIndex]) {
			plan.JoinIndex = nextIndex
			return plan, true
		}
		current = nextIndex
	}
}

func workflowGraphConcurrentExecutableStageEligible(stage workflowGraphStage) bool {
	return !isVisualOnlyWorkflowNode(stage) &&
		!isControlWorkflowNode(stage) &&
		!isRepeatWorkflowNode(stage) &&
		!isSubWorkflowNode(stage) &&
		!isJoinWorkflowNode(stage) &&
		!stage.Approval
}

func workflowGraphConcurrentParallelEnabled(stage workflowGraphStage) bool {
	return workflowGraphParamBool(stage, "concurrent") || workflowGraphParamBool(stage, "parallel")
}

func workflowGraphParamBool(stage workflowGraphStage, name string) bool {
	value := strings.ToLower(strings.TrimSpace(stage.Params[name]))
	switch value {
	case "1", "true", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func synchronizedWorkflowGraphHandler(handler func(event schema.StreamEvent) error) func(event schema.StreamEvent) error {
	if handler == nil {
		return nil
	}
	var mu sync.Mutex
	return func(event schema.StreamEvent) error {
		mu.Lock()
		defer mu.Unlock()
		return handler(event)
	}
}

func uniqueWorkflowGraphStageIndices(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (w *WorkflowRunner) controlWorkflowGraphStageDecision(graph workflowGraph, currentIndex int, request string, completed []WorkflowStageResult, manualInputs map[string]map[string]string, manualInputValues map[string]map[string]any) workflowGraphControlDecision {
	stage := graph.Stages[currentIndex]
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "parallel", "fan_out", "fork":
		targets := w.nextWorkflowGraphStageIndices(graph, currentIndex, request, "", completed)
		return workflowGraphControlDecision{
			Kind:    "parallel",
			Route:   "fan_out",
			Value:   workflowGraphStageNamesForIndices(graph, targets),
			Targets: targets,
			Target:  workflowGraphStageNamesForIndices(graph, targets),
			Status:  "completed",
			Variables: workflowStageMetadata(map[string]string{
				"branch_count": strconv.Itoa(len(targets)),
			}),
		}
	case "join", "merge", "barrier":
		required := workflowGraphJoinRequiredStageNames(graph, stage)
		targets := w.nextWorkflowGraphStageIndices(graph, currentIndex, request, strings.Join(required, ","), completed)
		return workflowGraphControlDecision{
			Kind:    "join",
			Route:   "joined",
			Value:   strings.Join(required, ","),
			Passed:  true,
			Targets: targets,
			Target:  workflowGraphStageNamesForIndices(graph, targets),
			Status:  "completed",
			Variables: workflowStageMetadata(map[string]string{
				"wait_for":        strings.Join(required, ","),
				"completed_count": strconv.Itoa(len(required)),
			}),
		}
	case "condition":
		passed := evaluateWorkflowGraphCondition(stage.Condition, request, completed)
		routeKey := "false"
		if passed {
			routeKey = "true"
		}
		decision := workflowGraphControlDecision{Kind: "condition", Route: routeKey, Passed: passed, Value: strconv.FormatBool(passed)}
		if target := strings.TrimSpace(stage.Routes[routeKey]); target != "" {
			decision.Target = target
			decision.Targets = w.workflowGraphNamedStageIndices(graph, []string{target}, completed)
			return decision
		}
		if passed && len(stage.Next) > 0 {
			decision.Target = stage.Next[0]
			decision.Targets = w.workflowGraphNamedStageIndices(graph, []string{stage.Next[0]}, completed)
			return decision
		}
		if !passed && len(stage.Next) > 1 {
			decision.Target = stage.Next[1]
			decision.Targets = w.workflowGraphNamedStageIndices(graph, []string{stage.Next[1]}, completed)
			return decision
		}
		return decision
	case "switch", "router":
		value := normalizeWorkflowSwitchValue(workflowGraphReferenceString(stage.SwitchOn, request, completed))
		if value == "" && len(completed) > 0 {
			value = normalizeWorkflowSwitchValue(completed[len(completed)-1].Output.Summary)
		}
		decision := workflowGraphControlDecision{Kind: normalizeWorkflowSkillName(stage.NodeType), Value: value, Route: value}
		if target := workflowGraphSwitchTarget(stage.Cases, value); target != "" {
			decision.Target = target
			decision.Targets = w.workflowGraphNamedStageIndices(graph, []string{target}, completed)
			return decision
		}
		decision.Targets = w.nextWorkflowGraphStageIndices(graph, currentIndex, request, value, completed)
		decision.Target = workflowGraphStageNamesForIndices(graph, decision.Targets)
		return decision
	case "policy_guard", "guard":
		extraCompleted := workflowGraphTeamApprovalGateInputResults(stage, completed, manualInputs[normalizeWorkflowSkillName(stage.Name)])
		completedForEvaluation := completed
		if len(extraCompleted) > 0 {
			completedForEvaluation = append(append([]WorkflowStageResult(nil), completed...), extraCompleted...)
		}
		evaluation := w.evaluateWorkflowGraphPolicyGuard(stage, request, completedForEvaluation)
		passed := evaluation.Passed
		routeKey := "deny"
		if passed {
			routeKey = "allow"
		}
		variables := workflowStageMetadata(map[string]string{
			"policy":    evaluation.Expression,
			"rule":      evaluation.Rule,
			"ref":       evaluation.Ref,
			"value":     evaluation.Value,
			"threshold": evaluation.Threshold,
		})
		for key, value := range evaluation.Details {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				continue
			}
			if variables == nil {
				variables = make(map[string]string)
			}
			variables[key] = value
		}
		decision := workflowGraphControlDecision{
			Kind:      "policy_guard",
			Route:     routeKey,
			Value:     strconv.FormatBool(passed),
			Passed:    passed,
			Status:    "completed",
			Variables: variables,
		}
		if len(extraCompleted) > 0 {
			decision.ExtraCompleted = extraCompleted
		}
		if target := workflowGraphPolicyTarget(stage, routeKey); target != "" {
			decision.Target = target
			decision.Targets = w.workflowGraphNamedStageIndices(graph, []string{target}, completedForEvaluation)
			return decision
		}
		if passed {
			decision.Targets = w.nextWorkflowGraphStageIndices(graph, currentIndex, request, decision.Value, completedForEvaluation)
			decision.Target = workflowGraphStageNamesForIndices(graph, decision.Targets)
			return decision
		}
		decision.Status = "blocked"
		decision.Halt = true
		decision.Reason = fallbackWorkflowGraphValue(stage.Params["reason"], fallbackWorkflowGraphValue(evaluation.Reason, "workflow blocked by policy guard"))
		return decision
	case "quality_gate", "quality_guard", "quality-guard":
		evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
		routeKey := "fail"
		if evaluation.Passed && evaluation.Status == "warning" {
			routeKey = "warning"
		} else if evaluation.Passed {
			routeKey = "pass"
		}
		decision := workflowGraphControlDecision{
			Kind:   "quality_gate",
			Route:  routeKey,
			Value:  evaluation.Status,
			Passed: evaluation.Passed,
			Status: "completed",
			Reason: evaluation.Reason,
			Variables: workflowStageMetadata(map[string]string{
				"quality_status":       evaluation.Status,
				"score":                strconv.Itoa(evaluation.Score),
				"acceptance_total":     strconv.Itoa(evaluation.AcceptanceTotal),
				"acceptance_passed":    strconv.Itoa(evaluation.AcceptancePassed),
				"acceptance_failed":    strconv.Itoa(evaluation.AcceptanceFailed),
				"verification_total":   strconv.Itoa(evaluation.VerificationTotal),
				"verification_passed":  strconv.Itoa(evaluation.VerificationPassed),
				"verification_failed":  strconv.Itoa(evaluation.VerificationFailed),
				"verification_unknown": strconv.Itoa(evaluation.VerificationUnknown),
				"evidence_artifacts":   strconv.Itoa(evaluation.EvidenceArtifacts),
				"tool_errors":          strconv.Itoa(evaluation.ToolErrors),
				"stage_errors":         strconv.Itoa(evaluation.StageErrors),
				"warnings":             strings.Join(evaluation.Warnings, "; "),
				"failures":             strings.Join(evaluation.Failures, "; "),
			}),
			ValueVariables: map[string]any{
				"quality_status":       evaluation.Status,
				"score":                evaluation.Score,
				"acceptance_total":     evaluation.AcceptanceTotal,
				"acceptance_passed":    evaluation.AcceptancePassed,
				"acceptance_failed":    evaluation.AcceptanceFailed,
				"verification_total":   evaluation.VerificationTotal,
				"verification_passed":  evaluation.VerificationPassed,
				"verification_failed":  evaluation.VerificationFailed,
				"verification_unknown": evaluation.VerificationUnknown,
				"evidence_artifacts":   evaluation.EvidenceArtifacts,
				"tool_errors":          evaluation.ToolErrors,
				"stage_errors":         evaluation.StageErrors,
				"failures":             append([]string(nil), evaluation.Failures...),
				"warnings":             append([]string(nil), evaluation.Warnings...),
			},
		}
		if target := workflowGraphQualityGateTarget(stage, routeKey); target != "" {
			decision.Target = target
			decision.Targets = w.workflowGraphNamedStageIndices(graph, []string{target}, completed)
			return decision
		}
		if evaluation.Passed {
			decision.Targets = w.nextWorkflowGraphStageIndices(graph, currentIndex, request, evaluation.Status, completed)
			decision.Target = workflowGraphStageNamesForIndices(graph, decision.Targets)
			return decision
		}
		decision.Status = "blocked"
		decision.Halt = true
		decision.Reason = fallbackWorkflowGraphValue(stage.Params["reason"], fallbackWorkflowGraphValue(evaluation.Reason, "workflow blocked by quality gate"))
		return decision
	case "checkpoint", "manual_approval", "approval_gate":
		decision := workflowGraphControlDecision{
			Kind:   normalizeWorkflowSkillName(stage.NodeType),
			Route:  "approved",
			Value:  "approved",
			Passed: true,
			Status: "completed",
			Reason: workflowGraphCheckpointPrompt(stage),
		}
		decision.Targets = w.nextWorkflowGraphStageIndices(graph, currentIndex, request, decision.Value, completed)
		decision.Target = workflowGraphStageNamesForIndices(graph, decision.Targets)
		return decision
	case "input_gate", "manual_input":
		variables := workflowGraphInputGateVariables(stage, request, completed, manualInputs)
		valueVariables := workflowGraphInputGateValueVariables(stage, request, completed, manualInputValues, variables)
		decision := workflowGraphControlDecision{
			Kind:           normalizeWorkflowSkillName(stage.NodeType),
			Route:          workflowGraphInputGateRoute(variables),
			Value:          workflowGraphJSON(variables),
			Passed:         len(variables) > 0,
			Status:         "completed",
			Variables:      variables,
			ValueVariables: valueVariables,
		}
		decision.Targets = w.nextWorkflowGraphStageIndices(graph, currentIndex, request, decision.Value, completed)
		decision.Target = workflowGraphStageNamesForIndices(graph, decision.Targets)
		return decision
	case "team", "agent_team", "team_template":
		decision := w.workflowGraphTeamDecision(stage)
		decision.Targets = w.nextWorkflowGraphStageIndices(graph, currentIndex, request, decision.Value, completed)
		decision.Target = workflowGraphStageNamesForIndices(graph, decision.Targets)
		return decision
	default:
		targets := w.nextWorkflowGraphStageIndices(graph, currentIndex, request, "", completed)
		return workflowGraphControlDecision{Kind: normalizeWorkflowSkillName(stage.NodeType), Targets: targets, Target: workflowGraphStageNamesForIndices(graph, targets)}
	}
}

func (w *WorkflowRunner) runWorkflowGraphRepeatStage(ctx context.Context, graph workflowGraph, controlIndex int, request string, completed []WorkflowStageResult, handler func(event schema.StreamEvent) error) (workflowGraphRepeatOutcome, error) {
	context, err := w.workflowGraphRepeatContext(graph, controlIndex, request, completed)
	if err != nil {
		return workflowGraphRepeatOutcome{}, err
	}
	return w.runWorkflowGraphRepeatIterations(ctx, graph, request, completed, context, handler)
}

func (w *WorkflowRunner) resumeWorkflowGraphRepeatStage(ctx context.Context, graph workflowGraph, pending pendingApproval, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	context := pending.graphRepeat
	if context.ControlIndex < 0 || context.ControlIndex >= len(graph.Stages) || context.BodyIndex < 0 || context.BodyIndex >= len(graph.Stages) {
		return WorkflowResult{}, fmt.Errorf("workflow %s resume context is missing repeat stage %s", graph.Name, pending.stage)
	}
	completed := append([]WorkflowStageResult(nil), pending.completed...)
	body := graph.Stages[context.BodyIndex]
	if !approve {
		decision := w.runtime.ResolvePendingApproval(pending.call.ID, false)
		_ = decision
		result := schema.ToolResult{CallID: pending.call.ID, ToolName: pending.tool.Name, Content: fmt.Sprintf("tool %s denied by operator", pending.tool.Name), IsError: true, Denied: true}
		iterationStage := workflowGraphRepeatIterationStage(body, graph.Stages[context.ControlIndex], context, context.NextIteration-1)
		inputs := workflowGraphRepeatIterationInputs(iterationStage, context, context.NextIteration-1, pending.request, completed)
		inputValues := workflowGraphRepeatIterationInputValues(iterationStage, context, context.NextIteration-1, pending.request, completed)
		completed = append(completed, workflowGraphDeniedStageResult(iterationStage, result, inputs, inputValues))
		w.runtime.DisableWorkflowAutoApproval(graph.Name)
		if err := w.runtime.RestoreDefaultAgent(); err != nil {
			return WorkflowResult{}, err
		}
		w.persistWorkflowState(graph.Name, "denied", WorkflowStage(iterationStage.Name), pending.request, summarizeWorkflow(completed), result.Content)
		return WorkflowResult{Name: graph.Name, Status: "denied", CompletedStages: completed, NextStage: WorkflowStage(iterationStage.Name), ApprovalPrompt: result.Content}, nil
	}
	toolResult, err := w.approveWorkflowGraphTool(ctx, pending)
	if err != nil {
		return WorkflowResult{}, err
	}
	iterationIndex := context.NextIteration - 1
	if iterationIndex < 0 {
		iterationIndex = 0
	}
	iterationStage := workflowGraphRepeatIterationStage(body, graph.Stages[context.ControlIndex], context, iterationIndex)
	skill, err := w.graphStageSkill(iterationStage)
	if err != nil {
		return WorkflowResult{}, err
	}
	prompt := pending.stagePrompt
	if strings.TrimSpace(prompt) == "" {
		prompt = buildWorkflowGraphStagePrompt(graph, iterationStage, pending.request, completed)
	}
	if err := w.ensureWorkflowAgent(iterationStage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	if err := w.runtime.SetActiveAgent(iterationStage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	result, err := continueAgentRunWithSkill(ctx, w.runtime, iterationStage.Agent, prompt, &skill, pending.call, pending.responseContent, toolResult, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", iterationStage.Name, err)
	}
	if hasSuspendedToolResult(result.ToolResults) {
		w.captureWorkflowGraphRepeatApprovalContext(graph, context, iterationIndex, pending.request, result.Output, completed, result.ToolResults, prompt)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), iterationStage.Agent)
		w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(iterationStage.Name), pending.request, summarizeWorkflow(completed), approvalPrompt)
		return WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(iterationStage.Name)}, nil
	}
	inputs := workflowGraphRepeatIterationInputs(iterationStage, context, iterationIndex, pending.request, completed)
	inputValues := workflowGraphRepeatIterationInputValues(iterationStage, context, iterationIndex, pending.request, completed)
	completed = append(completed, workflowGraphRepeatIterationResult(iterationStage, graph.Stages[context.ControlIndex], context, iterationIndex, result, inputs, inputValues, 1))
	if workflowGraphRepeatKind(context.Kind) == "loop" && strings.TrimSpace(context.Until) != "" && evaluateWorkflowGraphCondition(context.Until, pending.request, completed) {
		controlResult := workflowGraphRepeatControlResult(graph.Stages[context.ControlIndex], graph, context, completed, true)
		completed = append(completed, controlResult)
		queue := w.nextWorkflowGraphStageIndices(graph, context.ControlIndex, pending.request, controlResult.Result.Output, completed)
		return w.runWorkflowGraphQueue(ctx, graph, pending.request, true, queue, completed, nil, nil, handler)
	}
	outcome, err := w.runWorkflowGraphRepeatIterations(ctx, graph, pending.request, completed, context, handler)
	if err != nil {
		return WorkflowResult{}, err
	}
	if outcome.Result != nil {
		return *outcome.Result, nil
	}
	return w.runWorkflowGraphQueue(ctx, graph, pending.request, true, outcome.Queue, outcome.Completed, nil, nil, handler)
}

func (w *WorkflowRunner) runWorkflowGraphRepeatIterations(ctx context.Context, graph workflowGraph, request string, completed []WorkflowStageResult, context workflowGraphRepeatContext, handler func(event schema.StreamEvent) error) (workflowGraphRepeatOutcome, error) {
	control := graph.Stages[context.ControlIndex]
	body := graph.Stages[context.BodyIndex]
	start := context.NextIteration
	if start < 0 {
		start = 0
	}
	total := context.MaxIterations
	if workflowGraphRepeatKind(context.Kind) == "for_each" {
		total = len(context.Items)
	}
	passed := false
	for iteration := start; iteration < total; iteration++ {
		if err := ctx.Err(); err != nil {
			return workflowGraphRepeatOutcome{}, err
		}
		iterationStage := workflowGraphRepeatIterationStage(body, control, context, iteration)
		skill, err := w.graphStageSkill(iterationStage)
		if err != nil {
			return workflowGraphRepeatOutcome{}, err
		}
		if err := w.ensureWorkflowAgent(iterationStage.Agent); err != nil {
			return workflowGraphRepeatOutcome{}, err
		}
		inputs := workflowGraphRepeatIterationInputs(iterationStage, context, iteration, request, completed)
		inputValues := workflowGraphRepeatIterationInputValues(iterationStage, context, iteration, request, completed)
		prompt := buildWorkflowGraphStagePrompt(graph, iterationStage, request, completed)
		w.persistWorkflowState(graph.Name, "running", WorkflowStage(iterationStage.Name), request, summarizeWorkflow(completed), "")
		if err := w.runtime.SetActiveAgent(iterationStage.Agent); err != nil {
			return workflowGraphRepeatOutcome{}, err
		}
		result, attempts, err := w.runWorkflowGraphExecutableStage(ctx, iterationStage, prompt, skill, handler)
		if err != nil {
			return workflowGraphRepeatOutcome{}, fmt.Errorf("workflow stage %s: %w", iterationStage.Name, err)
		}
		if hasSuspendedToolResult(result.ToolResults) {
			w.captureWorkflowGraphRepeatApprovalContext(graph, context, iteration, request, result.Output, completed, result.ToolResults, prompt)
			approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), iterationStage.Agent)
			w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(iterationStage.Name), request, summarizeWorkflow(completed), approvalPrompt)
			return workflowGraphRepeatOutcome{Result: &WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(iterationStage.Name)}}, nil
		}
		completed = append(completed, workflowGraphRepeatIterationResult(iterationStage, control, context, iteration, result, inputs, inputValues, attempts))
		if workflowGraphRepeatKind(context.Kind) == "loop" && strings.TrimSpace(context.Until) != "" && evaluateWorkflowGraphCondition(context.Until, request, completed) {
			passed = true
			break
		}
	}
	controlResult := workflowGraphRepeatControlResult(control, graph, context, completed, passed)
	completed = append(completed, controlResult)
	queue := w.nextWorkflowGraphStageIndices(graph, context.ControlIndex, request, controlResult.Result.Output, completed)
	return workflowGraphRepeatOutcome{Completed: completed, Queue: queue}, nil
}

func (w *WorkflowRunner) workflowGraphRepeatContext(graph workflowGraph, controlIndex int, request string, completed []WorkflowStageResult) (workflowGraphRepeatContext, error) {
	stage := graph.Stages[controlIndex]
	bodyName := workflowGraphRepeatBodyName(stage)
	if bodyName == "" {
		return workflowGraphRepeatContext{}, fmt.Errorf("workflow repeat stage %s missing params.stage/body", stage.Name)
	}
	bodyIndex := workflowGraphStageIndexByName(graph, bodyName)
	if bodyIndex < 0 {
		return workflowGraphRepeatContext{}, fmt.Errorf("workflow repeat stage %s references unknown body stage %q", stage.Name, bodyName)
	}
	if bodyIndex == controlIndex {
		return workflowGraphRepeatContext{}, fmt.Errorf("workflow repeat stage %s cannot use itself as body", stage.Name)
	}
	body := graph.Stages[bodyIndex]
	if isVisualOnlyWorkflowNode(body) || isControlWorkflowNode(body) {
		return workflowGraphRepeatContext{}, fmt.Errorf("workflow repeat stage %s body %s must be an executable agent/skill/tool stage", stage.Name, body.Name)
	}
	kind := workflowGraphRepeatKind(stage.NodeType)
	items := []string(nil)
	maxIterations := workflowGraphLoopMaxIterations(stage)
	if kind == "for_each" {
		items = workflowGraphForEachItems(stage, request, completed)
		maxIterations = len(items)
	}
	return workflowGraphRepeatContext{
		Kind:          kind,
		ControlIndex:  controlIndex,
		BodyIndex:     bodyIndex,
		NextIteration: 0,
		MaxIterations: maxIterations,
		Until:         workflowGraphLoopUntil(stage),
		Items:         items,
	}, nil
}

func workflowGraphRepeatKind(value string) string {
	switch normalizeWorkflowSkillName(value) {
	case "for_each", "foreach", "map":
		return "for_each"
	default:
		return "loop"
	}
}

func workflowGraphRepeatBodyName(stage workflowGraphStage) string {
	for _, key := range []string{"stage", "body", "do", "each_stage", "loop_stage", "target"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	return ""
}

func workflowGraphForEachItems(stage workflowGraphStage, request string, completed []WorkflowStageResult) []string {
	for _, key := range []string{"items_ref", "ref", "source", "in"} {
		if ref := strings.TrimSpace(stage.Params[key]); ref != "" {
			if items := parseWorkflowGraphItems(resolveWorkflowGraphReference(ref, request, completed)); len(items) > 0 {
				return items
			}
		}
	}
	if ref := strings.TrimSpace(stage.Input["items"]); ref != "" {
		if items := parseWorkflowGraphItems(resolveWorkflowGraphReference(ref, request, completed)); len(items) > 0 {
			return items
		}
	}
	value := strings.TrimSpace(stage.Params["items"])
	if value == "" {
		return nil
	}
	if strings.Contains(value, ".") || strings.EqualFold(value, "input") || strings.EqualFold(value, "request") {
		if resolved := resolveWorkflowGraphReference(value, request, completed); strings.TrimSpace(resolved) != "" && resolved != value {
			value = resolved
		}
	}
	return parseWorkflowGraphItems(value)
}

func parseWorkflowGraphItems(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var raw []any
	if err := json.Unmarshal([]byte(value), &raw); err == nil {
		items := make([]string, 0, len(raw))
		for _, item := range raw {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				items = append(items, text)
			}
		}
		return items
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.Trim(strings.TrimSpace(part), `"'`)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func workflowGraphLoopMaxIterations(stage workflowGraphStage) int {
	for _, key := range []string{"max_iterations", "max", "limit", "count"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			n, err := strconv.Atoi(value)
			if err == nil {
				if n < 1 {
					return 1
				}
				if n > 20 {
					return 20
				}
				return n
			}
		}
	}
	return 3
}

func workflowGraphLoopUntil(stage workflowGraphStage) string {
	for _, key := range []string{"until", "stop_when", "condition"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(stage.Condition)
}

func workflowGraphRepeatIterationStage(body, control workflowGraphStage, context workflowGraphRepeatContext, iteration int) workflowGraphStage {
	stage := body
	stage.Name = workflowGraphRepeatIterationStageName(body.Name, iteration)
	stage.Params = copyStringMap(body.Params)
	if stage.Params == nil {
		stage.Params = make(map[string]string)
	}
	stage.Params["repeat.control"] = control.Name
	stage.Params["repeat.kind"] = workflowGraphRepeatKind(context.Kind)
	stage.Params["iteration.index"] = strconv.Itoa(iteration)
	stage.Params["iteration.number"] = strconv.Itoa(iteration + 1)
	if item := workflowGraphRepeatItem(context, iteration); item != "" {
		stage.Params["iteration.item"] = item
	}
	return stage
}

func workflowGraphRepeatIterationStageName(name string, iteration int) string {
	return fmt.Sprintf("%s[%d]", strings.TrimSpace(name), iteration+1)
}

func workflowGraphRepeatItem(context workflowGraphRepeatContext, iteration int) string {
	if iteration < 0 || iteration >= len(context.Items) {
		return ""
	}
	return context.Items[iteration]
}

func workflowGraphRepeatIterationInputs(stage workflowGraphStage, context workflowGraphRepeatContext, iteration int, request string, completed []WorkflowStageResult) map[string]string {
	inputs := resolveWorkflowGraphStageInputs(stage, request, completed)
	if inputs == nil {
		inputs = make(map[string]string)
	}
	inputs["iteration_index"] = strconv.Itoa(iteration)
	inputs["iteration_number"] = strconv.Itoa(iteration + 1)
	if item := workflowGraphRepeatItem(context, iteration); item != "" {
		inputs["item"] = item
	}
	return inputs
}

func workflowGraphRepeatIterationInputValues(stage workflowGraphStage, context workflowGraphRepeatContext, iteration int, request string, completed []WorkflowStageResult) map[string]any {
	inputValues := resolveWorkflowGraphStageInputValues(stage, request, completed)
	if inputValues == nil {
		inputValues = make(map[string]any)
	}
	inputValues["iteration_index"] = iteration
	inputValues["iteration_number"] = iteration + 1
	if item := workflowGraphRepeatItem(context, iteration); item != "" {
		inputValues["item"] = item
	}
	return inputValues
}

func workflowGraphRepeatIterationResult(stage, control workflowGraphStage, context workflowGraphRepeatContext, iteration int, result schema.AgentResult, inputs map[string]string, inputValues map[string]any, attempts int) WorkflowStageResult {
	inputValues = copyWorkflowAnyMap(inputValues)
	if inputValues == nil {
		inputValues = make(map[string]any)
	}
	for key, value := range inputs {
		if _, ok := inputValues[key]; !ok {
			inputValues[key] = value
		}
	}
	stageResult := workflowGraphStageResult(stage, result, inputs, inputValues, attempts)
	metadata := copyStringMap(stageResult.Metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["repeat"] = "true"
	metadata["repeat_kind"] = workflowGraphRepeatKind(context.Kind)
	metadata["repeat_control"] = control.Name
	metadata["original_stage"] = graphStageOriginalName(stage.Name)
	metadata["iteration_index"] = strconv.Itoa(iteration)
	metadata["iteration_number"] = strconv.Itoa(iteration + 1)
	if item := workflowGraphRepeatItem(context, iteration); item != "" {
		metadata["item"] = item
	}
	stageResult.Metadata = workflowStageMetadata(metadata)
	return stageResult
}

func graphStageOriginalName(stageName string) string {
	if index := strings.LastIndex(stageName, "["); index > 0 && strings.HasSuffix(stageName, "]") {
		return stageName[:index]
	}
	return stageName
}

func workflowGraphRepeatControlResult(stage workflowGraphStage, graph workflowGraph, context workflowGraphRepeatContext, completed []WorkflowStageResult, passed bool) WorkflowStageResult {
	iterations := workflowGraphRepeatCompletedIterations(completed, stage.Name)
	outputs := make([]string, 0, len(iterations))
	summaries := make([]string, 0, len(iterations))
	for _, iteration := range iterations {
		if strings.TrimSpace(iteration.Output.RawOutput) != "" {
			outputs = append(outputs, iteration.Output.RawOutput)
		} else {
			outputs = append(outputs, iteration.Result.Output)
		}
		summaries = append(summaries, iteration.Output.Summary)
	}
	variables := workflowStageMetadata(map[string]string{
		"body_stage":      graph.Stages[context.BodyIndex].Name,
		"iteration_count": strconv.Itoa(len(iterations)),
		"max_iterations":  strconv.Itoa(context.MaxIterations),
		"items":           workflowGraphJSON(context.Items),
		"item_count":      strconv.Itoa(len(context.Items)),
		"outputs":         workflowGraphJSON(outputs),
		"summaries":       workflowGraphJSON(summaries),
		"until":           context.Until,
		"passed":          strconv.FormatBool(passed),
	})
	decision := workflowGraphControlDecision{
		Kind:      workflowGraphRepeatKind(context.Kind),
		Route:     "completed",
		Value:     workflowGraphJSON(outputs),
		Passed:    passed || workflowGraphRepeatKind(context.Kind) == "for_each",
		Status:    "completed",
		Variables: variables,
	}
	decision.Targets = nil
	decision.Target = workflowGraphStageNamesForIndices(graph, decision.Targets)
	return workflowGraphControlStageResult(stage, graph, decision)
}

func workflowGraphRepeatCompletedIterations(completed []WorkflowStageResult, controlName string) []WorkflowStageResult {
	controlKey := normalizeWorkflowSkillName(controlName)
	iterations := make([]WorkflowStageResult, 0)
	for _, stage := range completed {
		if normalizeWorkflowSkillName(stage.Metadata["repeat_control"]) == controlKey {
			iterations = append(iterations, stage)
		}
	}
	return iterations
}

func (w *WorkflowRunner) captureWorkflowGraphRepeatApprovalContext(graph workflowGraph, context workflowGraphRepeatContext, iteration int, request, responseContent string, completed []WorkflowStageResult, results []schema.ToolResult, stagePrompt string) {
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	body := graph.Stages[context.BodyIndex]
	iterationStage := workflowGraphRepeatIterationStage(body, graph.Stages[context.ControlIndex], context, iteration)
	context.NextIteration = iteration + 1
	_ = w.runtime.AnnotateWorkflowGraphRepeatPendingApproval(callID, graph.Name, WorkflowStage(iterationStage.Name), request, responseContent, completed, context.BodyIndex, stagePrompt, context)
}

func (w *WorkflowRunner) runWorkflowGraphSubWorkflowStage(ctx context.Context, graph workflowGraph, stage workflowGraphStage, request string, completed []WorkflowStageResult, approve bool, handler func(event schema.StreamEvent) error) (WorkflowStageResult, *WorkflowResult, error) {
	name := workflowGraphSubWorkflowName(stage)
	if name == "" {
		return WorkflowStageResult{}, nil, fmt.Errorf("workflow sub_workflow stage %s missing params.workflow", stage.Name)
	}
	if normalizePersistedWorkflowName(name) == normalizePersistedWorkflowName(graph.Name) {
		return WorkflowStageResult{}, nil, fmt.Errorf("workflow sub_workflow stage %s cannot call its own workflow %s", stage.Name, graph.Name)
	}
	subRequest := workflowGraphSubWorkflowRequest(stage, request, completed)
	result, err := w.Run(ctx, name, subRequest, approve, handler)
	if err != nil {
		return WorkflowStageResult{}, nil, fmt.Errorf("workflow sub_workflow stage %s: %w", stage.Name, err)
	}
	if result.PendingApproval || result.PendingInput || result.PendingSubWorkflow {
		prompt := fmt.Sprintf("Sub-workflow %s paused with status %s. Resume or complete child run %s, then resume this parent workflow.", name, result.Status, result.RunID)
		w.persistWorkflowSubWorkflowState(graph.Name, stage, request, completed, name, result.RunID, result.Status, prompt)
		return WorkflowStageResult{}, &WorkflowResult{
			Name:                     graph.Name,
			Status:                   "awaiting_sub_workflow",
			CompletedStages:          completed,
			PendingSubWorkflow:       true,
			PendingSubWorkflowName:   normalizePersistedWorkflowName(name),
			PendingSubWorkflowRunID:  result.RunID,
			PendingSubWorkflowStatus: result.Status,
			ApprovalPrompt:           prompt,
			NextStage:                WorkflowStage(stage.Name),
		}, nil
	}
	stageResult := workflowGraphSubWorkflowStageResult(stage, name, result.RunID, result.Status, result.FinalSummary, result.CompletedStages, resolveWorkflowGraphStageInputs(stage, request, completed))
	return stageResult, nil, nil
}

func workflowGraphSubWorkflowStageResult(stage workflowGraphStage, name, runID, status, finalSummary string, completedStages []WorkflowStageResult, inputs map[string]string) WorkflowStageResult {
	summary := finalSummary
	if strings.TrimSpace(summary) == "" {
		summary = summarizeWorkflow(completedStages)
	}
	variables := workflowStageMetadata(map[string]string{
		"sub_workflow":     normalizePersistedWorkflowName(name),
		"sub_run_id":       runID,
		"status":           status,
		"summary":          summary,
		"completed_stages": strconv.Itoa(len(completedStages)),
	})
	agentID := strings.TrimSpace(stage.Agent)
	if agentID == "" {
		agentID = "workflow"
	}
	agentResult := schema.AgentResult{Output: summary, AgentID: agentID, Mode: "workflow"}
	return WorkflowStageResult{
		Stage:    WorkflowStage(stage.Name),
		Agent:    agentID,
		NodeType: stage.NodeType,
		Status:   "completed",
		Input:    copyStringMap(inputs),
		Metadata: workflowStageMetadata(map[string]string{
			"sub_workflow": normalizePersistedWorkflowName(name),
			"sub_run_id":   runID,
		}),
		Result: agentResult,
		Output: WorkflowStageOutput{
			Summary:   truncateSummary(summary),
			RawOutput: limitWorkflowGraphText(summary),
			Variables: variables,
		},
	}
}

func (w *WorkflowRunner) persistWorkflowSubWorkflowState(graphName string, stage workflowGraphStage, request string, completed []WorkflowStageResult, childName, childRunID, childStatus, prompt string) {
	w.persistWorkflowState(graphName, "awaiting_sub_workflow", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), prompt)
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return
	}
	snapshot := w.runtime.session.Snapshot().Workflow
	snapshot.PendingSubWorkflowName = normalizePersistedWorkflowName(childName)
	snapshot.PendingSubWorkflowRunID = strings.TrimSpace(childRunID)
	snapshot.PendingSubWorkflowStatus = strings.TrimSpace(childStatus)
	w.runtime.session.SetWorkflow(snapshot)
	w.runtime.session.UpdateWorkflowRunState(snapshot)
	if strings.TrimSpace(snapshot.RunID) != "" {
		w.runtime.session.AppendWorkflowRunEvent(snapshot.RunID, session.WorkflowRunEventSnapshot{
			Type:           "workflow_sub_workflow_paused",
			Stage:          stage.Name,
			Content:        prompt,
			WorkflowName:   graphName,
			WorkflowStatus: "awaiting_sub_workflow",
			NextStage:      stage.Name,
			NeedsAction:    true,
		})
	}
}

func workflowGraphSubWorkflowName(stage workflowGraphStage) string {
	for _, key := range []string{"workflow", "sub_workflow", "name", "target"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	return ""
}

func workflowGraphSubWorkflowRequest(stage workflowGraphStage, request string, completed []WorkflowStageResult) string {
	for _, key := range []string{"request", "input", "prompt"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			resolved := resolveWorkflowGraphReference(value, request, completed)
			if strings.TrimSpace(resolved) != "" {
				return resolved
			}
		}
	}
	if inputs := resolveWorkflowGraphStageInputs(stage, request, completed); len(inputs) > 0 {
		payload := map[string]any{
			"original_request": request,
			"inputs":           inputs,
		}
		if data, err := json.Marshal(payload); err == nil {
			return string(data)
		}
	}
	return request
}

func (w *WorkflowRunner) workflowGraphTeamDecision(stage workflowGraphStage) workflowGraphControlDecision {
	teamName := workflowGraphTeamTemplateName(stage)
	template, ok := w.TeamTemplate(teamName)
	if !ok {
		return workflowGraphControlDecision{
			Kind:   "team",
			Route:  "missing",
			Value:  teamName,
			Status: "failed",
			Reason: fmt.Sprintf("unknown team template: %s", teamName),
			Halt:   true,
		}
	}
	entryAgent := strings.TrimSpace(stage.Agent)
	if entryAgent == "" {
		entryAgent = template.RecommendedEntryAgent
	}
	roleNames := make([]string, 0, len(template.RoleTemplates))
	for _, role := range template.RoleTemplates {
		roleNames = append(roleNames, role.Name)
	}
	blackboardKinds := make([]string, 0, len(template.BlackboardTemplates))
	for _, item := range template.BlackboardTemplates {
		blackboardKinds = append(blackboardKinds, item.Kind)
	}
	variables := workflowStageMetadata(map[string]string{
		"team":                 template.Name,
		"title":                template.Title,
		"category":             template.Category,
		"entry_agent":          entryAgent,
		"recommended_workflow": template.RecommendedWorkflow,
		"role_count":           strconv.Itoa(len(template.RoleTemplates)),
		"handoff_count":        strconv.Itoa(len(template.Handoffs)),
		"roles":                workflowGraphJSON(template.RoleTemplates),
		"role_names":           strings.Join(roleNames, ","),
		"handoffs":             workflowGraphJSON(template.Handoffs),
		"blackboard":           workflowGraphJSON(template.BlackboardTemplates),
		"blackboard_kinds":     strings.Join(blackboardKinds, ","),
		"output_contract":      workflowGraphJSON(template.OutputContract),
	})
	return workflowGraphControlDecision{
		Kind:      "team",
		Route:     "team_ready",
		Value:     template.Name,
		Passed:    true,
		Status:    "completed",
		Variables: variables,
	}
}

func workflowGraphStageIndexByName(graph workflowGraph, name string) int {
	target := normalizeWorkflowSkillName(name)
	for index, stage := range graph.Stages {
		if normalizeWorkflowSkillName(stage.Name) == target {
			return index
		}
	}
	return -1
}

func requiresWorkflowGraphCheckpointApproval(stage workflowGraphStage) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "checkpoint", "manual_approval", "approval_gate":
		return true
	default:
		return false
	}
}

func isManualInputWorkflowNode(stage workflowGraphStage) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "input_gate", "manual_input":
		return true
	default:
		return false
	}
}

func isJoinWorkflowNode(stage workflowGraphStage) bool {
	switch normalizeWorkflowSkillName(stage.NodeType) {
	case "join", "merge", "barrier":
		return true
	default:
		return false
	}
}

func workflowGraphJoinMissingStages(graph workflowGraph, stage workflowGraphStage, completed []WorkflowStageResult) []string {
	required := workflowGraphJoinRequiredStageNames(graph, stage)
	if len(required) == 0 {
		return nil
	}
	completedNames := completedStageNames(completed)
	missing := make([]string, 0, len(required))
	for _, name := range required {
		if _, ok := completedNames[normalizeWorkflowSkillName(name)]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func workflowGraphJoinRequiredStageNames(graph workflowGraph, stage workflowGraphStage) []string {
	for _, key := range []string{"wait_for", "requires", "required", "branches", "stages"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return workflowGraphCSVStageNames(value)
		}
	}
	joinKey := normalizeWorkflowSkillName(stage.Name)
	required := make([]string, 0)
	seen := make(map[string]struct{})
	for _, candidate := range graph.Stages {
		if normalizeWorkflowSkillName(candidate.Name) == joinKey || isVisualOnlyWorkflowNode(candidate) {
			continue
		}
		for _, next := range workflowGraphStageNextReferences(candidate) {
			if normalizeWorkflowSkillName(next) != joinKey {
				continue
			}
			key := normalizeWorkflowSkillName(candidate.Name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			required = append(required, candidate.Name)
		}
	}
	sort.Slice(required, func(i, j int) bool {
		return normalizeWorkflowSkillName(required[i]) < normalizeWorkflowSkillName(required[j])
	})
	return required
}

func workflowGraphCSVStageNames(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	names := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.Trim(strings.TrimSpace(part), `"'`)
		if name == "" {
			continue
		}
		key := normalizeWorkflowSkillName(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return names
}

func workflowGraphQueueContainsAnyStage(graph workflowGraph, queue []int, names []string) bool {
	if len(queue) == 0 || len(names) == 0 {
		return false
	}
	targets := make(map[string]struct{}, len(names))
	for _, name := range names {
		targets[normalizeWorkflowSkillName(name)] = struct{}{}
	}
	for _, index := range queue {
		if index < 0 || index >= len(graph.Stages) {
			continue
		}
		if _, ok := targets[normalizeWorkflowSkillName(graph.Stages[index].Name)]; ok {
			return true
		}
	}
	return false
}

func (w *WorkflowRunner) requiresWorkflowGraphManualInput(stage workflowGraphStage, request string, completed []WorkflowStageResult, manualInputs map[string]map[string]string) bool {
	if !w.isWorkflowGraphManualInputStage(stage, completed) {
		return false
	}
	if len(manualInputs[normalizeWorkflowSkillName(stage.Name)]) > 0 {
		return false
	}
	if isManualInputWorkflowNode(stage) && workflowGraphInputGateManual(stage) {
		return true
	}
	return w.workflowGraphTeamApprovalGateNeedsInput(stage, completed)
}

func (w *WorkflowRunner) isWorkflowGraphManualInputStage(stage workflowGraphStage, completed []WorkflowStageResult) bool {
	if isManualInputWorkflowNode(stage) {
		return true
	}
	return workflowGraphTeamApprovalGateInputEnabled(stage) && w.workflowGraphTeamApprovalGateNeedsInput(stage, completed)
}

func (w *WorkflowRunner) workflowGraphManualInputPrompt(stage workflowGraphStage, request string, completed []WorkflowStageResult) string {
	if w.workflowGraphTeamApprovalGateNeedsInput(stage, completed) {
		return w.workflowGraphTeamApprovalGatePrompt(stage, completed)
	}
	return workflowGraphInputGatePrompt(stage)
}

func (w *WorkflowRunner) workflowGraphManualInputFields(stage workflowGraphStage, completed []WorkflowStageResult) []schema.WorkflowInputField {
	if workflowGraphTeamApprovalGateInputEnabled(stage) {
		return w.workflowGraphTeamApprovalGateInputFields(stage, completed)
	}
	return workflowGraphInputGateFields(stage)
}

func workflowGraphInputGateManual(stage workflowGraphStage) bool {
	for _, key := range []string{"manual", "wait_for_input", "requires_input"} {
		if workflowTruthy(stage.Params[key]) {
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(stage.Params["mode"])) {
	case "manual", "interactive", "operator":
		return true
	default:
		return false
	}
}

func workflowGraphTeamTemplateName(stage workflowGraphStage) string {
	for _, key := range []string{"team", "team_template", "template"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return normalizePersistedWorkflowName(value)
		}
	}
	if value := strings.TrimSpace(stage.Skill); value != "" {
		return normalizePersistedWorkflowName(value)
	}
	return ""
}

func workflowGraphCheckpointPrompt(stage workflowGraphStage) string {
	for _, key := range []string{"prompt", "message", "reason", "label"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	return fmt.Sprintf("Workflow requires manual approval at checkpoint %s.", stage.Name)
}

func workflowGraphInputGatePrompt(stage workflowGraphStage) string {
	for _, key := range []string{"prompt", "message", "reason", "label"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	if fields := strings.TrimSpace(stage.Params["fields"]); fields != "" {
		return fmt.Sprintf("Workflow requires manual input for %s: %s.", stage.Name, fields)
	}
	return fmt.Sprintf("Workflow requires manual input at %s.", stage.Name)
}

func workflowGraphTeamApprovalGateInputEnabled(stage workflowGraphStage) bool {
	if normalizeWorkflowPolicyOperator(workflowGraphPolicyRule(stage)) != "team_approval_gate" {
		return false
	}
	for _, key := range []string{"pause_on_pending", "wait_for_quorum", "manual_quorum", "manual_review", "manual_approval", "requires_input"} {
		if workflowTruthy(stage.Params[key]) {
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(stage.Params["mode"])) {
	case "manual", "interactive", "operator":
		return true
	default:
		return false
	}
}

func (w *WorkflowRunner) workflowGraphTeamApprovalGateNeedsInput(stage workflowGraphStage, completed []WorkflowStageResult) bool {
	if !workflowGraphTeamApprovalGateInputEnabled(stage) {
		return false
	}
	gate := w.workflowGraphTeamApprovalGate(stage, completed)
	return gate != nil && strings.EqualFold(strings.TrimSpace(gate.Status), "pending")
}

func (w *WorkflowRunner) workflowGraphTeamApprovalGatePrompt(stage workflowGraphStage, completed []WorkflowStageResult) string {
	for _, key := range []string{"prompt", "message", "approval_prompt", "quorum_prompt"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	gate := w.workflowGraphTeamApprovalGate(stage, completed)
	if gate == nil {
		return fmt.Sprintf("Workflow requires team approval input at %s.", stage.Name)
	}
	roles := workflowGraphTeamApprovalGatePendingRoles(gate)
	if len(roles) == 0 {
		roles = gate.Roles
	}
	if len(roles) > 0 {
		return fmt.Sprintf("Team approval gate %s needs %d more approval(s). Pending roles: %s.", stage.Name, gate.Pending, strings.Join(roles, ", "))
	}
	return fmt.Sprintf("Team approval gate %s needs %d more approval(s).", stage.Name, gate.Pending)
}

func (w *WorkflowRunner) workflowGraphTeamApprovalGateInputFields(stage workflowGraphStage, completed []WorkflowStageResult) []schema.WorkflowInputField {
	gate := w.workflowGraphTeamApprovalGate(stage, completed)
	roleOptions := []string(nil)
	if gate != nil {
		roleOptions = workflowGraphTeamApprovalGatePendingRoles(gate)
		if len(roleOptions) == 0 {
			roleOptions = gate.Roles
		}
	}
	return []schema.WorkflowInputField{
		normalizeWorkflowGraphInputField(schema.WorkflowInputField{
			Name:        "roles",
			Label:       "Approving roles",
			Type:        "select",
			Description: "One or more team roles to record as manual approval or rejection.",
			Required:    true,
			Options:     roleOptions,
			Multiple:    true,
		}),
		normalizeWorkflowGraphInputField(schema.WorkflowInputField{
			Name:        "decision",
			Label:       "Decision",
			Type:        "select",
			Description: "Record manual team approval or rejection.",
			Required:    true,
			Default:     "approve",
			Options:     []string{"approve", "reject"},
		}),
		normalizeWorkflowGraphInputField(schema.WorkflowInputField{
			Name:        "comment",
			Label:       "Comment",
			Type:        "textarea",
			Description: "Optional approval evidence or rejection reason.",
			Rows:        3,
		}),
	}
}

func workflowGraphTeamApprovalGatePendingRoles(gate *TeamApprovalGateState) []string {
	if gate == nil {
		return nil
	}
	approved := make(map[string]struct{}, len(gate.Approvers))
	for _, role := range gate.Approvers {
		approved[normalizeWorkflowSkillName(role)] = struct{}{}
	}
	roles := make([]string, 0, len(gate.Roles))
	for _, role := range gate.Roles {
		if _, ok := approved[normalizeWorkflowSkillName(role)]; ok {
			continue
		}
		roles = append(roles, role)
	}
	return roles
}

type workflowGraphInputFieldDocument struct {
	Name        string                            `json:"name"`
	Label       string                            `json:"label,omitempty"`
	Type        string                            `json:"type,omitempty"`
	Description string                            `json:"description,omitempty"`
	Placeholder string                            `json:"placeholder,omitempty"`
	Group       string                            `json:"group,omitempty"`
	Required    bool                              `json:"required,omitempty"`
	Default     string                            `json:"default,omitempty"`
	Options     []string                          `json:"options,omitempty"`
	Rows        int                               `json:"rows,omitempty"`
	Min         string                            `json:"min,omitempty"`
	Max         string                            `json:"max,omitempty"`
	Pattern     string                            `json:"pattern,omitempty"`
	Multiple    bool                              `json:"multiple,omitempty"`
	Advanced    bool                              `json:"advanced,omitempty"`
	Children    []workflowGraphInputFieldDocument `json:"children,omitempty"`
	Fields      []workflowGraphInputFieldDocument `json:"fields,omitempty"`
}

type workflowGraphInputFieldsDocument struct {
	Fields     []workflowGraphInputFieldDocument           `json:"fields,omitempty"`
	Required   []string                                    `json:"required,omitempty"`
	Properties map[string]workflowGraphInputSchemaProperty `json:"properties,omitempty"`
}

type workflowGraphInputSchemaProperty struct {
	Type        string                                      `json:"type,omitempty"`
	Title       string                                      `json:"title,omitempty"`
	Label       string                                      `json:"label,omitempty"`
	Description string                                      `json:"description,omitempty"`
	Placeholder string                                      `json:"placeholder,omitempty"`
	Default     any                                         `json:"default,omitempty"`
	Required    []string                                    `json:"required,omitempty"`
	Enum        []any                                       `json:"enum,omitempty"`
	Options     []any                                       `json:"options,omitempty"`
	Format      string                                      `json:"format,omitempty"`
	Pattern     string                                      `json:"pattern,omitempty"`
	Minimum     any                                         `json:"minimum,omitempty"`
	Maximum     any                                         `json:"maximum,omitempty"`
	MinLength   any                                         `json:"minLength,omitempty"`
	MaxLength   any                                         `json:"maxLength,omitempty"`
	Properties  map[string]workflowGraphInputSchemaProperty `json:"properties,omitempty"`
}

func workflowGraphInputGateFields(stage workflowGraphStage) []schema.WorkflowInputField {
	fields := make([]schema.WorkflowInputField, 0)
	seen := make(map[string]int)
	required := workflowGraphStringSet(stage.Params["required"])
	addField := func(field schema.WorkflowInputField) {
		if strings.TrimSpace(field.Name) == "" {
			return
		}
		field = applyWorkflowGraphInputFieldParams(field, stage.Params, required)
		key := normalizeWorkflowInputFieldName(field.Name)
		if existing, ok := seen[key]; ok {
			fields[existing] = field
			return
		}
		seen[key] = len(fields)
		fields = append(fields, field)
	}
	for _, field := range parseWorkflowGraphInputFieldsFromParams(stage.Params) {
		addField(field)
	}
	for _, item := range workflowGraphCSVItems(stage.Params["fields"]) {
		addField(parseWorkflowGraphInputField(item))
	}
	for name := range required {
		if _, ok := seen[normalizeWorkflowInputFieldName(name)]; ok {
			continue
		}
		addField(schema.WorkflowInputField{Name: name, Required: true})
	}
	return fields
}

func parseWorkflowGraphInputFieldsFromParams(params map[string]string) []schema.WorkflowInputField {
	for _, key := range []string{"fields_json", "input_fields", "input_fields_json", "input_schema", "schema"} {
		raw := strings.TrimSpace(params[key])
		if raw == "" {
			continue
		}
		if fields := parseWorkflowGraphInputFieldsJSON(raw); len(fields) > 0 {
			return fields
		}
	}
	return nil
}

func parseWorkflowGraphInputFieldsJSON(raw string) []schema.WorkflowInputField {
	var docs []workflowGraphInputFieldDocument
	if err := json.Unmarshal([]byte(raw), &docs); err == nil {
		return workflowGraphFlattenInputFieldDocuments(docs, "", "")
	}
	var doc workflowGraphInputFieldsDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil
	}
	required := workflowGraphStringSet(strings.Join(doc.Required, ","))
	if len(doc.Fields) > 0 {
		fields := workflowGraphFlattenInputFieldDocuments(doc.Fields, "", "")
		for i := range fields {
			if _, ok := required[normalizeWorkflowInputFieldName(fields[i].Name)]; ok {
				fields[i].Required = true
			}
		}
		return fields
	}
	if len(doc.Properties) > 0 {
		fields := make([]schema.WorkflowInputField, 0, len(doc.Properties))
		names := make([]string, 0, len(doc.Properties))
		for name := range doc.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fields = append(fields, workflowGraphFlattenInputSchemaProperty(name, doc.Properties[name], required, "", "")...)
		}
		return fields
	}
	return nil
}

func workflowGraphFlattenInputFieldDocuments(docs []workflowGraphInputFieldDocument, parent, group string) []schema.WorkflowInputField {
	fields := make([]schema.WorkflowInputField, 0, len(docs))
	for _, doc := range docs {
		name := strings.TrimSpace(doc.Name)
		if parent != "" && name != "" && !strings.Contains(name, ".") {
			name = parent + "." + name
		}
		fieldGroup := fallbackWorkflowGraphValue(doc.Group, group)
		children := append([]workflowGraphInputFieldDocument(nil), doc.Children...)
		children = append(children, doc.Fields...)
		if len(children) > 0 {
			nextGroup := fallbackWorkflowGraphValue(doc.Label, name)
			if fieldGroup != "" {
				nextGroup = fieldGroup + " / " + nextGroup
			}
			fields = append(fields, workflowGraphFlattenInputFieldDocuments(children, name, nextGroup)...)
			continue
		}
		fields = append(fields, normalizeWorkflowGraphInputField(schema.WorkflowInputField{
			Name:        name,
			Label:       doc.Label,
			Type:        doc.Type,
			Description: doc.Description,
			Placeholder: doc.Placeholder,
			Group:       fieldGroup,
			Required:    doc.Required,
			Default:     doc.Default,
			Options:     doc.Options,
			Rows:        doc.Rows,
			Min:         doc.Min,
			Max:         doc.Max,
			Pattern:     doc.Pattern,
			Multiple:    doc.Multiple,
			Advanced:    doc.Advanced,
		}))
	}
	return fields
}

func workflowGraphFlattenInputSchemaProperty(name string, prop workflowGraphInputSchemaProperty, required map[string]struct{}, parent, group string) []schema.WorkflowInputField {
	fullName := strings.TrimSpace(name)
	if parent != "" && fullName != "" && !strings.Contains(fullName, ".") {
		fullName = parent + "." + fullName
	}
	fieldGroup := group
	if len(prop.Properties) > 0 {
		nextGroup := fallbackWorkflowGraphValue(prop.Title, fallbackWorkflowGraphValue(prop.Label, fullName))
		if fieldGroup != "" {
			nextGroup = fieldGroup + " / " + nextGroup
		}
		childRequired := workflowGraphMergeStringSets(required, workflowGraphStringSet(strings.Join(prop.Required, ",")))
		names := make([]string, 0, len(prop.Properties))
		for child := range prop.Properties {
			names = append(names, child)
		}
		sort.Strings(names)
		fields := make([]schema.WorkflowInputField, 0, len(names))
		for _, child := range names {
			fields = append(fields, workflowGraphFlattenInputSchemaProperty(child, prop.Properties[child], childRequired, fullName, nextGroup)...)
		}
		return fields
	}
	options := make([]string, 0, len(prop.Enum)+len(prop.Options))
	for _, value := range prop.Enum {
		options = append(options, workflowGraphInputAnyString(value))
	}
	for _, value := range prop.Options {
		options = append(options, workflowGraphInputAnyString(value))
	}
	fieldType := prop.Type
	if prop.Format != "" && (fieldType == "" || fieldType == "string") {
		fieldType = prop.Format
	}
	field := schema.WorkflowInputField{
		Name:        fullName,
		Label:       fallbackWorkflowGraphValue(prop.Label, prop.Title),
		Type:        fieldType,
		Description: prop.Description,
		Placeholder: prop.Placeholder,
		Group:       fieldGroup,
		Required:    workflowGraphInputFieldRequiredByName(fullName, required),
		Default:     workflowGraphInputAnyString(prop.Default),
		Options:     options,
		Min:         fallbackWorkflowGraphValue(workflowGraphInputAnyString(prop.Minimum), workflowGraphInputAnyString(prop.MinLength)),
		Max:         fallbackWorkflowGraphValue(workflowGraphInputAnyString(prop.Maximum), workflowGraphInputAnyString(prop.MaxLength)),
		Pattern:     prop.Pattern,
	}
	return []schema.WorkflowInputField{normalizeWorkflowGraphInputField(field)}
}

func workflowGraphInputAnyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func workflowGraphInputFieldRequiredByName(name string, required map[string]struct{}) bool {
	if len(required) == 0 {
		return false
	}
	if _, ok := required[normalizeWorkflowInputFieldName(name)]; ok {
		return true
	}
	parts := strings.Split(name, ".")
	if len(parts) > 0 {
		_, ok := required[normalizeWorkflowInputFieldName(parts[len(parts)-1])]
		return ok
	}
	return false
}

func parseWorkflowGraphInputField(raw string) schema.WorkflowInputField {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return schema.WorkflowInputField{}
	}
	parts := strings.Split(raw, ":")
	field := schema.WorkflowInputField{Name: strings.TrimSpace(parts[0])}
	labelParts := make([]string, 0)
	for _, part := range parts[1:] {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if workflowGraphInputFieldRequired(value) {
			field.Required = true
			continue
		}
		if field.Type == "" && workflowGraphInputFieldType(value) != "" {
			field.Type = workflowGraphInputFieldType(value)
			continue
		}
		labelParts = append(labelParts, value)
	}
	if len(labelParts) > 0 {
		field.Label = strings.Join(labelParts, ":")
	}
	return normalizeWorkflowGraphInputField(field)
}

func applyWorkflowGraphInputFieldParams(field schema.WorkflowInputField, params map[string]string, required map[string]struct{}) schema.WorkflowInputField {
	name := strings.TrimSpace(field.Name)
	key := normalizeWorkflowInputFieldName(name)
	if _, ok := required[key]; ok {
		field.Required = true
	}
	prefixes := []string{"field." + name + ".", name + ".", "field." + key + ".", key + "."}
	for _, prefix := range prefixes {
		if value := strings.TrimSpace(params[prefix+"label"]); value != "" {
			field.Label = value
		}
		if value := strings.TrimSpace(params[prefix+"type"]); value != "" {
			field.Type = value
		}
		if value := strings.TrimSpace(params[prefix+"description"]); value != "" {
			field.Description = value
		}
		if value := strings.TrimSpace(params[prefix+"placeholder"]); value != "" {
			field.Placeholder = value
		}
		if value := strings.TrimSpace(params[prefix+"group"]); value != "" {
			field.Group = value
		}
		if value := strings.TrimSpace(params[prefix+"default"]); value != "" {
			field.Default = value
		}
		if value := strings.TrimSpace(params[prefix+"options"]); value != "" {
			field.Options = workflowGraphCSVItems(value)
		}
		if value := strings.TrimSpace(params[prefix+"rows"]); value != "" {
			if rows, err := strconv.Atoi(value); err == nil {
				field.Rows = rows
			}
		}
		if value := strings.TrimSpace(params[prefix+"min"]); value != "" {
			field.Min = value
		}
		if value := strings.TrimSpace(params[prefix+"max"]); value != "" {
			field.Max = value
		}
		if value := strings.TrimSpace(params[prefix+"pattern"]); value != "" {
			field.Pattern = value
		}
		if value := strings.TrimSpace(params[prefix+"multiple"]); value != "" {
			field.Multiple = workflowTruthy(value)
		}
		if value := strings.TrimSpace(params[prefix+"advanced"]); value != "" {
			field.Advanced = workflowTruthy(value)
		}
		if value := strings.TrimSpace(params[prefix+"required"]); value != "" {
			field.Required = workflowTruthy(value)
		}
	}
	for _, optionKey := range []string{name + "_options", key + "_options"} {
		if value := strings.TrimSpace(params[optionKey]); value != "" {
			field.Options = workflowGraphCSVItems(value)
		}
	}
	return normalizeWorkflowGraphInputField(field)
}

func normalizeWorkflowGraphInputField(field schema.WorkflowInputField) schema.WorkflowInputField {
	field.Name = strings.TrimSpace(field.Name)
	field.Label = strings.TrimSpace(field.Label)
	field.Type = workflowGraphInputFieldType(field.Type)
	if field.Type == "" {
		field.Type = "string"
	}
	field.Description = strings.TrimSpace(field.Description)
	field.Placeholder = strings.TrimSpace(field.Placeholder)
	field.Group = strings.TrimSpace(field.Group)
	field.Default = strings.TrimSpace(field.Default)
	field.Min = strings.TrimSpace(field.Min)
	field.Max = strings.TrimSpace(field.Max)
	field.Pattern = strings.TrimSpace(field.Pattern)
	field.Options = workflowGraphUniqueStrings(field.Options)
	if len(field.Options) > 0 && (field.Type == "" || field.Type == "string") {
		field.Type = "select"
	}
	if field.Rows < 0 {
		field.Rows = 0
	}
	if field.Label == "" {
		field.Label = workflowGraphHumanLabel(field.Name)
	}
	return field
}

func (w *WorkflowRunner) workflowGraphValidateManualInputs(stage workflowGraphStage, inputs map[string]string, completed []WorkflowStageResult) (map[string]string, error) {
	fields := w.workflowGraphManualInputFields(stage, completed)
	out := copyStringMap(inputs)
	if out == nil {
		out = make(map[string]string)
	}
	if len(fields) == 0 {
		return out, nil
	}
	known := make(map[string]string, len(fields))
	for _, field := range fields {
		key := normalizeWorkflowInputFieldName(field.Name)
		known[key] = field.Name
		value := strings.TrimSpace(out[field.Name])
		if value == "" && field.Default != "" {
			value = field.Default
			out[field.Name] = field.Default
		}
		if field.Required && value == "" {
			return nil, fmt.Errorf("workflow input %s is required", field.Name)
		}
		if value == "" {
			continue
		}
		if err := validateWorkflowGraphInputFieldValue(field, value); err != nil {
			return nil, err
		}
	}
	if workflowTruthy(stage.Params["strict"]) || workflowTruthy(stage.Params["strict_fields"]) {
		for key := range out {
			if _, ok := known[normalizeWorkflowInputFieldName(key)]; !ok {
				return nil, fmt.Errorf("workflow input %s is not declared by input gate %s", key, stage.Name)
			}
		}
	}
	return out, nil
}

func (w *WorkflowRunner) workflowGraphManualInputTypedValues(stage workflowGraphStage, inputs map[string]string, inputValues map[string]any, completed []WorkflowStageResult) map[string]any {
	out := make(map[string]any)
	fields := w.workflowGraphManualInputFields(stage, completed)
	fieldsByName := make(map[string]schema.WorkflowInputField, len(fields))
	for _, field := range fields {
		fieldsByName[normalizeWorkflowInputFieldName(field.Name)] = field
	}
	for key, value := range inputs {
		if strings.TrimSpace(key) == "" {
			continue
		}
		original, hasOriginal := workflowGraphInputValueByName(inputValues, key)
		if field, ok := fieldsByName[normalizeWorkflowInputFieldName(key)]; ok {
			out[key] = workflowGraphTypedInputValue(field, value, original, hasOriginal)
			continue
		}
		if hasOriginal {
			out[key] = copyWorkflowAnyValue(original)
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func workflowGraphInputValueByName(values map[string]any, name string) (any, bool) {
	if len(values) == 0 {
		return nil, false
	}
	if value, ok := values[name]; ok {
		return value, true
	}
	want := normalizeWorkflowInputFieldName(name)
	for key, value := range values {
		if normalizeWorkflowInputFieldName(key) == want {
			return value, true
		}
	}
	return nil, false
}

func workflowGraphTypedInputValue(field schema.WorkflowInputField, value string, original any, hasOriginal bool) any {
	value = strings.TrimSpace(value)
	if field.Multiple {
		if hasOriginal {
			switch typed := original.(type) {
			case []any:
				return copyWorkflowAnyValue(typed)
			case []string:
				values := make([]any, 0, len(typed))
				for _, item := range typed {
					values = append(values, item)
				}
				return values
			}
		}
		items, err := workflowGraphInputMultipleValues(value)
		if err != nil {
			return value
		}
		values := make([]any, 0, len(items))
		for _, item := range items {
			itemField := field
			itemField.Multiple = false
			if itemField.Type == "array" {
				itemField.Type = "string"
			}
			values = append(values, workflowGraphTypedInputValue(itemField, item, nil, false))
		}
		return values
	}
	switch field.Type {
	case "number":
		number, err := strconv.ParseFloat(value, 64)
		if err == nil {
			return number
		}
	case "integer":
		number, err := strconv.Atoi(value)
		if err == nil {
			return number
		}
	case "boolean":
		boolean, err := strconv.ParseBool(value)
		if err == nil {
			return boolean
		}
	case "json", "object", "array":
		var decoded any
		if err := json.Unmarshal([]byte(value), &decoded); err == nil {
			return decoded
		}
	}
	if hasOriginal {
		switch original.(type) {
		case string, nil:
		default:
			return copyWorkflowAnyValue(original)
		}
	}
	return value
}

func workflowGraphStringInputsFromValues(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = workflowGraphInputAnyString(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateWorkflowGraphInputFieldValue(field schema.WorkflowInputField, value string) error {
	value = strings.TrimSpace(value)
	if field.Multiple {
		values, err := workflowGraphInputMultipleValues(value)
		if err != nil {
			return fmt.Errorf("workflow input %s must be a JSON array or comma-separated list", field.Name)
		}
		if field.Required && len(values) == 0 {
			return fmt.Errorf("workflow input %s is required", field.Name)
		}
		itemField := field
		itemField.Multiple = false
		if itemField.Type == "array" {
			itemField.Type = "string"
		}
		for _, item := range values {
			if strings.TrimSpace(item) == "" {
				continue
			}
			if err := validateWorkflowGraphInputFieldScalarValue(itemField, item); err != nil {
				return err
			}
		}
		return nil
	}
	return validateWorkflowGraphInputFieldScalarValue(field, value)
}

func validateWorkflowGraphInputFieldScalarValue(field schema.WorkflowInputField, value string) error {
	switch field.Type {
	case "number":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("workflow input %s must be a number", field.Name)
		}
		if err := validateWorkflowGraphNumericRange(field, number); err != nil {
			return err
		}
	case "integer":
		number, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("workflow input %s must be an integer", field.Name)
		}
		if err := validateWorkflowGraphNumericRange(field, float64(number)); err != nil {
			return err
		}
	case "boolean":
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("workflow input %s must be a boolean", field.Name)
		}
	case "json":
		if !json.Valid([]byte(value)) {
			return fmt.Errorf("workflow input %s must be valid JSON", field.Name)
		}
	case "array":
		values, err := workflowGraphInputJSONArrayValues(value)
		if err != nil {
			return fmt.Errorf("workflow input %s must be a JSON array", field.Name)
		}
		if field.Required && len(values) == 0 {
			return fmt.Errorf("workflow input %s is required", field.Name)
		}
		if len(field.Options) > 0 {
			for _, item := range values {
				if !workflowGraphStringInOptions(item, field.Options) {
					return fmt.Errorf("workflow input %s must contain only: %s", field.Name, strings.Join(field.Options, ", "))
				}
			}
		}
	case "object":
		var object map[string]any
		if err := json.Unmarshal([]byte(value), &object); err != nil || object == nil {
			return fmt.Errorf("workflow input %s must be a JSON object", field.Name)
		}
	case "url":
		parsed, err := url.ParseRequestURI(value)
		if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
			return fmt.Errorf("workflow input %s must be a URL", field.Name)
		}
	case "string", "text", "textarea", "password", "email", "path", "file", "date", "time", "datetime", "hidden", "select":
		if err := validateWorkflowGraphStringRange(field, value); err != nil {
			return err
		}
	}
	if len(field.Options) > 0 && !workflowGraphStringInOptions(value, field.Options) {
		return fmt.Errorf("workflow input %s must be one of: %s", field.Name, strings.Join(field.Options, ", "))
	}
	if field.Pattern != "" {
		matched, err := regexp.MatchString(field.Pattern, value)
		if err != nil {
			return fmt.Errorf("workflow input %s has invalid pattern: %w", field.Name, err)
		}
		if !matched {
			return fmt.Errorf("workflow input %s does not match required pattern", field.Name)
		}
	}
	return nil
}

func workflowGraphInputMultipleValues(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if strings.HasPrefix(value, "[") {
		return workflowGraphInputJSONArrayValues(value)
	}
	return workflowGraphCSVItems(value), nil
}

func workflowGraphInputJSONArrayValues(value string) ([]string, error) {
	var raw []any
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return nil, err
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		text := strings.TrimSpace(workflowGraphInputAnyString(item))
		if text != "" {
			values = append(values, text)
		}
	}
	return values, nil
}

func validateWorkflowGraphNumericRange(field schema.WorkflowInputField, value float64) error {
	if field.Min != "" {
		min, err := strconv.ParseFloat(field.Min, 64)
		if err != nil {
			return fmt.Errorf("workflow input %s has invalid min value", field.Name)
		}
		if value < min {
			return fmt.Errorf("workflow input %s must be at least %s", field.Name, field.Min)
		}
	}
	if field.Max != "" {
		max, err := strconv.ParseFloat(field.Max, 64)
		if err != nil {
			return fmt.Errorf("workflow input %s has invalid max value", field.Name)
		}
		if value > max {
			return fmt.Errorf("workflow input %s must be at most %s", field.Name, field.Max)
		}
	}
	return nil
}

func validateWorkflowGraphStringRange(field schema.WorkflowInputField, value string) error {
	if field.Min != "" {
		min, err := strconv.Atoi(field.Min)
		if err != nil {
			return fmt.Errorf("workflow input %s has invalid min length", field.Name)
		}
		if len([]rune(value)) < min {
			return fmt.Errorf("workflow input %s must be at least %s characters", field.Name, field.Min)
		}
	}
	if field.Max != "" {
		max, err := strconv.Atoi(field.Max)
		if err != nil {
			return fmt.Errorf("workflow input %s has invalid max length", field.Name)
		}
		if len([]rune(value)) > max {
			return fmt.Errorf("workflow input %s must be at most %s characters", field.Name, field.Max)
		}
	}
	return nil
}

func workflowGraphInputFieldType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "string", "text", "textarea", "multiline", "number", "integer", "int", "select", "enum", "url", "path", "file", "json", "object", "array", "password", "email", "date", "time", "datetime", "datetime-local", "hidden":
		if strings.EqualFold(value, "enum") {
			return "select"
		}
		normalized := strings.ToLower(strings.TrimSpace(value))
		switch normalized {
		case "multiline":
			return "textarea"
		case "int":
			return "integer"
		case "datetime-local":
			return "datetime"
		default:
			return normalized
		}
	case "bool", "boolean":
		return "boolean"
	default:
		return ""
	}
}

func workflowGraphInputFieldRequired(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "required", "require", "mandatory", "must", "true":
		return true
	default:
		return false
	}
}

func workflowGraphStringSet(value string) map[string]struct{} {
	items := workflowGraphCSVItems(value)
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[normalizeWorkflowInputFieldName(item)] = struct{}{}
	}
	return out
}

func workflowGraphMergeStringSets(sets ...map[string]struct{}) map[string]struct{} {
	size := 0
	for _, set := range sets {
		size += len(set)
	}
	if size == 0 {
		return nil
	}
	out := make(map[string]struct{}, size)
	for _, set := range sets {
		for key := range set {
			if strings.TrimSpace(key) == "" {
				continue
			}
			out[normalizeWorkflowInputFieldName(key)] = struct{}{}
		}
	}
	return out
}

func workflowGraphCSVItems(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.Trim(strings.TrimSpace(part), `"'`)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func workflowGraphUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func workflowGraphStringInOptions(value string, options []string) bool {
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(option)) {
			return true
		}
	}
	return false
}

func normalizeWorkflowInputFieldName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func workflowGraphHumanLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func workflowGraphPolicyTarget(stage workflowGraphStage, routeKey string) string {
	for _, key := range workflowGraphPolicyRouteKeys(routeKey) {
		if target := strings.TrimSpace(stage.Routes[key]); target != "" {
			return target
		}
	}
	if routeKey == "allow" && len(stage.Next) > 0 {
		return stage.Next[0]
	}
	if routeKey == "deny" && len(stage.Next) > 1 {
		return stage.Next[1]
	}
	return ""
}

func workflowGraphPolicyRouteKeys(routeKey string) []string {
	if routeKey == "allow" {
		return []string{"allow", "allowed", "pass", "passed", "true", "yes"}
	}
	return []string{"deny", "denied", "block", "blocked", "fail", "failed", "false", "no"}
}

func workflowGraphQualityGateTarget(stage workflowGraphStage, routeKey string) string {
	for _, key := range workflowGraphQualityGateRouteKeys(routeKey) {
		if target := strings.TrimSpace(stage.Routes[key]); target != "" {
			return target
		}
	}
	switch routeKey {
	case "pass":
		if len(stage.Next) > 0 {
			return stage.Next[0]
		}
	case "fail":
		if len(stage.Next) > 1 {
			return stage.Next[1]
		}
	case "warning":
		if len(stage.Next) > 2 {
			return stage.Next[2]
		}
	}
	return ""
}

func workflowGraphQualityGateRouteKeys(routeKey string) []string {
	switch routeKey {
	case "pass":
		return []string{"pass", "passed", "allow", "allowed", "ok", "success", "true", "yes"}
	case "warning":
		return []string{"warning", "warn", "partial", "unknown"}
	default:
		return []string{"fail", "failed", "deny", "denied", "block", "blocked", "false", "no"}
	}
}

func firstNonEmptyWorkflowGraphParam(params map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(params[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstWorkflowGraphValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type workflowGraphPolicyEvaluation struct {
	Rule       string
	Expression string
	Ref        string
	Value      string
	Threshold  string
	Passed     bool
	Reason     string
	Details    map[string]string
}

type workflowGraphQualityEvaluation struct {
	Status              string
	Score               int
	Passed              bool
	Reason              string
	AcceptanceTotal     int
	AcceptancePassed    int
	AcceptanceFailed    int
	VerificationTotal   int
	VerificationPassed  int
	VerificationFailed  int
	VerificationUnknown int
	EvidenceArtifacts   int
	ToolErrors          int
	StageErrors         int
	Failures            []string
	Warnings            []string
}

func evaluateWorkflowGraphQualityGate(stage workflowGraphStage, completed []WorkflowStageResult) workflowGraphQualityEvaluation {
	evaluation := workflowGraphQualityEvaluation{Status: "passed", Passed: true}
	stages := workflowGraphQualityGateStages(stage, completed)
	if len(stages) == 0 {
		evaluation.Failures = append(evaluation.Failures, "quality gate has no completed stages to evaluate")
		return workflowGraphFinalizeQualityEvaluation(stage, evaluation)
	}
	for _, result := range stages {
		stageName := strings.TrimSpace(string(result.Stage))
		if workflowGraphQualityStatusFailed(result.Status) {
			evaluation.StageErrors++
			evaluation.Failures = append(evaluation.Failures, fmt.Sprintf("%s status is %s", fallbackWorkflowGraphValue(stageName, "stage"), result.Status))
		}
		for _, toolResult := range result.Result.ToolResults {
			if toolResult.IsError {
				evaluation.ToolErrors++
				evaluation.Failures = append(evaluation.Failures, fmt.Sprintf("%s tool %s returned an error", fallbackWorkflowGraphValue(stageName, "stage"), fallbackWorkflowGraphValue(toolResult.ToolName, "tool")))
			}
		}
		for _, acceptance := range result.Acceptance {
			evaluation.AcceptanceTotal++
			status := workflowGraphQualityStatus(acceptance.Status)
			switch {
			case workflowGraphQualityStatusPassed(status):
				evaluation.AcceptancePassed++
			case workflowGraphQualityStatusFailed(status):
				evaluation.AcceptanceFailed++
				evaluation.Failures = append(evaluation.Failures, fmt.Sprintf("%s acceptance %s failed", fallbackWorkflowGraphValue(stageName, "stage"), fallbackWorkflowGraphValue(acceptance.Name, acceptance.Ref)))
			default:
				evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("%s acceptance %s has unknown status %s", fallbackWorkflowGraphValue(stageName, "stage"), fallbackWorkflowGraphValue(acceptance.Name, acceptance.Ref), fallbackWorkflowGraphValue(status, "empty")))
			}
		}
		for _, verification := range result.Result.Verification {
			evaluation.VerificationTotal++
			status := workflowGraphQualityStatus(verification.Status)
			switch {
			case workflowGraphQualityStatusPassed(status):
				evaluation.VerificationPassed++
			case workflowGraphQualityStatusFailed(status):
				evaluation.VerificationFailed++
				evaluation.Failures = append(evaluation.Failures, fmt.Sprintf("%s verification %s failed", fallbackWorkflowGraphValue(stageName, "stage"), fallbackWorkflowGraphValue(verification.Kind, "verification")))
			default:
				evaluation.VerificationUnknown++
				evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("%s verification %s has unknown status %s", fallbackWorkflowGraphValue(stageName, "stage"), fallbackWorkflowGraphValue(verification.Kind, "verification"), fallbackWorkflowGraphValue(status, "empty")))
			}
		}
		for _, artifact := range result.Output.Artifacts {
			if workflowGraphQualityArtifactEvidence(artifact) {
				evaluation.EvidenceArtifacts++
			}
		}
	}
	return workflowGraphFinalizeQualityEvaluation(stage, evaluation)
}

func workflowGraphFinalizeQualityEvaluation(stage workflowGraphStage, evaluation workflowGraphQualityEvaluation) workflowGraphQualityEvaluation {
	if workflowGraphQualityParamBool(stage, []string{"require_acceptance", "acceptance_required"}, false) && evaluation.AcceptanceTotal == 0 {
		evaluation.Failures = append(evaluation.Failures, "quality gate requires acceptance criteria but none were recorded")
	}
	if workflowGraphQualityParamBool(stage, []string{"require_verification", "verification_required"}, false) && evaluation.VerificationTotal == 0 {
		evaluation.Failures = append(evaluation.Failures, "quality gate requires verification results but none were recorded")
	}
	if workflowGraphQualityParamBool(stage, []string{"require_evidence", "evidence_required", "require_artifacts"}, false) && evaluation.EvidenceArtifacts == 0 {
		evaluation.Failures = append(evaluation.Failures, "quality gate requires evidence artifacts but none were recorded")
	}
	if !workflowGraphQualityParamBool(stage, []string{"allow_empty", "allow_no_checks"}, false) &&
		evaluation.AcceptanceTotal == 0 &&
		evaluation.VerificationTotal == 0 &&
		evaluation.EvidenceArtifacts == 0 {
		evaluation.Failures = append(evaluation.Failures, "quality gate has no acceptance, verification, or artifact evidence")
	}
	if !workflowGraphQualityParamBool(stage, []string{"allow_unknown", "allow_warnings"}, true) && len(evaluation.Warnings) > 0 {
		evaluation.Failures = append(evaluation.Failures, "quality gate has unknown verification or acceptance statuses")
	}
	evaluation.Score = workflowGraphQualityScore(evaluation)
	minScore := workflowGraphQualityParamInt(stage, []string{"min_score", "minimum_score", "score"}, 0)
	if minScore > 0 && evaluation.Score < minScore {
		evaluation.Failures = append(evaluation.Failures, fmt.Sprintf("quality score %d is below required minimum %d", evaluation.Score, minScore))
	}
	evaluation.Passed = len(evaluation.Failures) == 0
	switch {
	case !evaluation.Passed:
		evaluation.Status = "failed"
		evaluation.Reason = strings.Join(evaluation.Failures, "; ")
	case len(evaluation.Warnings) > 0:
		evaluation.Status = "warning"
		evaluation.Reason = strings.Join(evaluation.Warnings, "; ")
	default:
		evaluation.Status = "passed"
		evaluation.Reason = "quality gate passed"
	}
	return evaluation
}

func workflowGraphQualityGateStages(stage workflowGraphStage, completed []WorkflowStageResult) []WorkflowStageResult {
	names := workflowGraphQualityStageFilter(stage)
	if len(names) == 0 {
		return append([]WorkflowStageResult(nil), completed...)
	}
	out := make([]WorkflowStageResult, 0, len(completed))
	for _, result := range completed {
		if _, ok := names[normalizeWorkflowSkillName(string(result.Stage))]; ok {
			out = append(out, result)
		}
	}
	return out
}

func workflowGraphQualityStageFilter(stage workflowGraphStage) map[string]struct{} {
	raw := firstNonEmptyWorkflowGraphParam(stage.Params, "stages", "stage", "source_stage", "source", "ref_stage")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	names := map[string]struct{}{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		name := normalizeWorkflowSkillName(part)
		if name != "" {
			names[name] = struct{}{}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func workflowGraphQualityScore(evaluation workflowGraphQualityEvaluation) int {
	totalChecks := evaluation.AcceptanceTotal + evaluation.VerificationTotal
	passedChecks := evaluation.AcceptancePassed + evaluation.VerificationPassed
	score := 0
	if totalChecks > 0 {
		score = (passedChecks*100 + totalChecks/2) / totalChecks
	} else if evaluation.EvidenceArtifacts > 0 {
		score = 70
	}
	score -= workflowGraphQualityPenalty(evaluation.AcceptanceFailed+evaluation.VerificationFailed, 10, 40)
	score -= workflowGraphQualityPenalty(evaluation.StageErrors+evaluation.ToolErrors, 15, 60)
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func workflowGraphQualityPenalty(count, each, max int) int {
	if count <= 0 {
		return 0
	}
	penalty := count * each
	if penalty > max {
		return max
	}
	return penalty
}

func workflowGraphQualityArtifactEvidence(artifact session.WorkflowRunArtifact) bool {
	if artifact.IsError {
		return false
	}
	if strings.TrimSpace(firstWorkflowGraphValue(artifact.Content, artifact.Summary, artifact.Title)) == "" {
		return false
	}
	switch normalizeWorkflowSkillName(artifact.Kind) {
	case "artifact", "output", "report", "evidence", "verification", "acceptance", "test", "tests", "diff", "patch":
		return true
	default:
		return false
	}
}

func workflowGraphQualityParamBool(stage workflowGraphStage, keys []string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params, keys...)))
	switch value {
	case "1", "true", "yes", "y", "on", "enabled", "required":
		return true
	case "0", "false", "no", "n", "off", "disabled":
		return false
	default:
		return fallback
	}
}

func workflowGraphQualityParamInt(stage workflowGraphStage, keys []string, fallback int) int {
	value := strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params, keys...))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func workflowGraphQualityStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func workflowGraphQualityStatusPassed(status string) bool {
	switch workflowGraphQualityStatus(status) {
	case "passed", "pass", "success", "ok", "completed", "complete", "verified", "valid":
		return true
	default:
		return false
	}
}

func workflowGraphQualityStatusFailed(status string) bool {
	switch workflowGraphQualityStatus(status) {
	case "failed", "fail", "failure", "error", "blocked", "denied", "invalid", "rejected", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func (w *WorkflowRunner) evaluateWorkflowGraphPolicyGuard(stage workflowGraphStage, request string, completed []WorkflowStageResult) workflowGraphPolicyEvaluation {
	rule := workflowGraphPolicyRule(stage)
	if definition, ok := w.loadWorkflowPolicyRule(rule); ok {
		return w.evaluateCustomWorkflowGraphPolicyGuard(definition, stage, request, completed)
	}
	return w.evaluateBuiltInWorkflowGraphPolicyGuard(stage, request, completed, rule)
}

func (w *WorkflowRunner) evaluateCustomWorkflowGraphPolicyGuard(definition WorkflowPolicyRuleDefinition, stage workflowGraphStage, request string, completed []WorkflowStageResult) workflowGraphPolicyEvaluation {
	effective := stage
	effective.Params = copyStringMap(stage.Params)
	if effective.Params == nil {
		effective.Params = make(map[string]string)
	}
	for key, value := range definition.Defaults {
		if strings.TrimSpace(effective.Params[key]) == "" {
			effective.Params[key] = value
		}
	}
	operator := normalizeWorkflowPolicyOperator(definition.Operator)
	if operator == "" {
		operator = "expression"
	}
	if operator == "expression" {
		expr := renderWorkflowPolicyTemplate(definition.Expression, effective.Params)
		effective.Policy = expr
		evaluation := w.evaluateBuiltInWorkflowGraphPolicyGuard(effective, request, completed, "expression")
		evaluation.Rule = definition.Name
		evaluation.Expression = expr
		if !evaluation.Passed && strings.TrimSpace(definition.Reason) != "" {
			evaluation.Reason = renderWorkflowPolicyTemplate(definition.Reason, effective.Params)
		}
		return evaluation
	}
	effective.Params["rule"] = operator
	evaluation := w.evaluateBuiltInWorkflowGraphPolicyGuard(effective, request, completed, operator)
	evaluation.Rule = definition.Name
	evaluation.Expression = operator
	if !evaluation.Passed && strings.TrimSpace(definition.Reason) != "" {
		evaluation.Reason = renderWorkflowPolicyTemplate(definition.Reason, effective.Params)
	}
	return evaluation
}

func renderWorkflowPolicyTemplate(template string, params map[string]string) string {
	out := strings.TrimSpace(template)
	if out == "" || len(params) == 0 {
		return out
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := strings.TrimSpace(params[key])
		out = strings.ReplaceAll(out, "{{"+key+"}}", value)
		out = strings.ReplaceAll(out, "{{params."+key+"}}", value)
	}
	return out
}

func (w *WorkflowRunner) evaluateBuiltInWorkflowGraphPolicyGuard(stage workflowGraphStage, request string, completed []WorkflowStageResult, rule string) workflowGraphPolicyEvaluation {
	rule = normalizeWorkflowPolicyOperator(rule)
	if rule == "" {
		rule = "expression"
	}
	evaluation := workflowGraphPolicyEvaluation{Rule: rule}
	switch rule {
	case "ref_truthy":
		ref := workflowGraphPolicyRef(stage)
		valueAny := workflowGraphExpressionValue(ref, request, completed)
		value := workflowGraphValueString(valueAny)
		evaluation.Ref = ref
		evaluation.Value = truncateSummary(value)
		evaluation.Passed = workflowTruthyAny(valueAny)
	case "contains":
		ref := workflowGraphPolicyRef(stage)
		needle := fallbackWorkflowGraphValue(stage.Params["needle"], stage.Params["contains"])
		valueAny := workflowGraphExpressionValue(ref, request, completed)
		value := workflowGraphValueString(valueAny)
		evaluation.Ref = ref
		evaluation.Value = truncateSummary(value)
		evaluation.Threshold = needle
		evaluation.Passed = strings.TrimSpace(needle) != "" && workflowGraphContainsValue(valueAny, workflowGraphExpressionValue(needle, request, completed))
	case "min_count":
		ref := workflowGraphPolicyRef(stage)
		valueAny := workflowGraphExpressionValue(ref, request, completed)
		minimum := workflowGraphPolicyInt(stage.Params["minimum"], workflowGraphPolicyInt(stage.Params["min"], 1))
		evaluation.Ref = ref
		evaluation.Value = strconv.Itoa(workflowValueLengthAny(valueAny))
		evaluation.Threshold = strconv.Itoa(minimum)
		evaluation.Passed = workflowValueLengthAny(valueAny) >= minimum
	case "risk_at_least":
		ref := workflowGraphPolicyRef(stage)
		value := resolveWorkflowGraphReference(ref, request, completed)
		minimum := fallbackWorkflowGraphValue(stage.Params["minimum"], fallbackWorkflowGraphValue(stage.Params["severity"], "high"))
		evaluation.Ref = ref
		evaluation.Value = workflowGraphHighestSeverity(value)
		evaluation.Threshold = normalizeWorkflowSwitchValue(minimum)
		evaluation.Passed = workflowGraphSeverityRank(evaluation.Value) >= workflowGraphSeverityRank(minimum)
	case "team_approval_gate":
		gate := w.workflowGraphTeamApprovalGate(stage, completed)
		wantStatus := normalizeWorkflowSwitchValue(fallbackWorkflowGraphValue(stage.Params["status"], "passed"))
		evaluation.Ref = "team_approval_gate"
		evaluation.Threshold = wantStatus
		if gate == nil {
			evaluation.Value = "missing"
			evaluation.Passed = false
			evaluation.Reason = "team approval gate policy is missing"
			break
		}
		evaluation.Value = gate.Status
		evaluation.Details = map[string]string{
			"required":  strconv.Itoa(gate.Required),
			"approved":  strconv.Itoa(gate.Approved),
			"rejected":  strconv.Itoa(gate.Rejected),
			"pending":   strconv.Itoa(gate.Pending),
			"approvers": strings.Join(gate.Approvers, ","),
			"rejectors": strings.Join(gate.Rejectors, ","),
			"roles":     strings.Join(gate.Roles, ","),
		}
		switch wantStatus {
		case "not_blocked":
			evaluation.Passed = gate.Status != "blocked"
		default:
			evaluation.Passed = gate.Status == wantStatus
		}
	default:
		expr := strings.TrimSpace(stage.Policy)
		if expr == "" {
			expr = strings.TrimSpace(stage.Condition)
		}
		evaluation.Rule = "expression"
		evaluation.Expression = expr
		evaluation.Value = strconv.FormatBool(evaluateWorkflowGraphCondition(expr, request, completed))
		evaluation.Passed = strings.TrimSpace(expr) != "" && evaluateWorkflowGraphCondition(expr, request, completed)
	}
	if !evaluation.Passed && strings.TrimSpace(evaluation.Reason) == "" {
		evaluation.Reason = fmt.Sprintf("policy guard %s did not pass", evaluation.Rule)
	}
	return evaluation
}

func workflowGraphPolicyRule(stage workflowGraphStage) string {
	for _, key := range []string{"rule", "policy_rule", "guard_rule"} {
		if value := normalizeWorkflowPolicyOperator(stage.Params[key]); value != "" {
			return value
		}
	}
	policy := normalizeWorkflowPolicyOperator(stage.Policy)
	switch policy {
	case "ref_truthy", "contains", "min_count", "risk_at_least", "team_approval_gate":
		return policy
	default:
		return "expression"
	}
}

func workflowGraphPolicyRef(stage workflowGraphStage) string {
	for _, key := range []string{"ref", "reference", "input", "source"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	if strings.TrimSpace(stage.SwitchOn) != "" {
		return stage.SwitchOn
	}
	return "previous.raw_output"
}

func workflowGraphPolicyInt(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func (w *WorkflowRunner) workflowGraphTeamApprovalGate(stage workflowGraphStage, completed []WorkflowStageResult) *TeamApprovalGateState {
	var policy teamApprovalGatePolicy
	team := workflowGraphTeamTemplateName(stage)
	for _, result := range completed {
		if strings.TrimSpace(result.Metadata["team_role"]) == "" {
			continue
		}
		if team != "" && normalizePersistedWorkflowName(result.Metadata["team"]) != team {
			continue
		}
		workflowGraphMergeTeamApprovalGatePolicy(&policy, result.Metadata, false)
	}
	presetTeam := team
	if presetTeam == "" {
		presetTeam = workflowGraphTeamApprovalGateCompletedTeam(completed)
	}
	w.workflowGraphMergeTeamApprovalGatePresetPolicy(&policy, presetTeam, stage.Params, false)
	workflowGraphMergeTeamApprovalGatePolicy(&policy, stage.Params, true)
	if policy.Required == 0 && len(policy.Roles) == 0 {
		return nil
	}
	roleSet := make(map[string]struct{}, len(policy.Roles))
	for _, role := range policy.Roles {
		roleSet[normalizeWorkflowSkillName(role)] = struct{}{}
	}
	approvers := make(map[string]struct{})
	rejectors := make(map[string]struct{})
	for _, result := range completed {
		role := workflowGraphTeamStageRole(result)
		if role == "" {
			continue
		}
		if team != "" && normalizePersistedWorkflowName(result.Metadata["team"]) != team {
			continue
		}
		if len(roleSet) > 0 {
			if _, ok := roleSet[normalizeWorkflowSkillName(role)]; !ok {
				continue
			}
		}
		for _, packet := range workflowTeamRolePackets(result) {
			switch packet.Kind {
			case "team_approval":
				approvers[role] = struct{}{}
			case "team_rejection":
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
	gate := &TeamApprovalGateState{
		Required:  required,
		Approved:  len(approvers),
		Rejected:  len(rejectors),
		Roles:     policy.Roles,
		Approvers: teamStateSortedKeys(approvers),
		Rejectors: teamStateSortedKeys(rejectors),
	}
	if gate.Approved < required {
		gate.Pending = required - gate.Approved
	}
	if policy.RejectBlocks && gate.Rejected > 0 {
		gate.Status = "blocked"
	} else if gate.Approved >= required {
		gate.Status = "passed"
	} else {
		gate.Status = "pending"
	}
	return gate
}

func workflowGraphTeamApprovalGateCompletedTeam(completed []WorkflowStageResult) string {
	team := ""
	for _, result := range completed {
		if strings.TrimSpace(result.Metadata["team_role"]) == "" {
			continue
		}
		current := normalizePersistedWorkflowName(result.Metadata["team"])
		if current == "" {
			continue
		}
		if team == "" {
			team = current
			continue
		}
		if team != current {
			return ""
		}
	}
	return team
}

func workflowGraphTeamApprovalPresetName(params map[string]string) string {
	for _, key := range []string{"approval_preset", "review_preset", "quorum_preset", "preset"} {
		if value := strings.TrimSpace(params[key]); value != "" {
			return normalizePersistedWorkflowName(value)
		}
	}
	return ""
}

func (w *WorkflowRunner) workflowGraphTeamApprovalPreset(teamName, presetName string) (TeamQuorumPreset, bool) {
	template, ok := w.TeamTemplate(teamName)
	if !ok {
		return TeamQuorumPreset{}, false
	}
	presetName = normalizePersistedWorkflowName(presetName)
	for _, preset := range template.QuorumPresets {
		name := normalizePersistedWorkflowName(preset.Name)
		if presetName != "" {
			if name == presetName {
				preset.Name = name
				return preset, true
			}
			continue
		}
		if preset.Default {
			preset.Name = name
			return preset, true
		}
	}
	return TeamQuorumPreset{}, false
}

func (w *WorkflowRunner) workflowGraphApplyTeamApprovalPreset(target map[string]string, teamName string, params map[string]string, override bool) {
	if target == nil {
		return
	}
	preset, ok := w.workflowGraphTeamApprovalPreset(teamName, workflowGraphTeamApprovalPresetName(params))
	if !ok {
		return
	}
	workflowGraphSetPresetParam(target, "approval_preset", preset.Name, override)
	if preset.Required > 0 {
		workflowGraphSetPresetParam(target, "approval_quorum", strconv.Itoa(preset.Required), override)
	}
	if len(preset.Roles) > 0 {
		workflowGraphSetPresetParam(target, "approval_roles", strings.Join(preset.Roles, ","), override)
	}
	workflowGraphSetPresetParam(target, "reject_blocks", strconv.FormatBool(preset.RejectBlocks), override)
}

func (w *WorkflowRunner) workflowGraphMergeTeamApprovalGatePresetPolicy(policy *teamApprovalGatePolicy, teamName string, params map[string]string, override bool) {
	if policy == nil {
		return
	}
	preset, ok := w.workflowGraphTeamApprovalPreset(teamName, workflowGraphTeamApprovalPresetName(params))
	if !ok {
		return
	}
	if preset.Required > 0 && (override || policy.Required == 0) {
		policy.Required = preset.Required
	}
	if len(preset.Roles) > 0 && (override || len(policy.Roles) == 0) {
		policy.Roles = append([]string(nil), preset.Roles...)
	}
	if override || !policy.RejectBlocksSet {
		policy.RejectBlocks = preset.RejectBlocks
		policy.RejectBlocksSet = true
	}
}

func workflowGraphSetPresetParam(target map[string]string, key, value string, override bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if override || strings.TrimSpace(target[key]) == "" {
		target[key] = value
	}
}

func workflowGraphTeamApprovalGateInputResults(stage workflowGraphStage, completed []WorkflowStageResult, inputs map[string]string) []WorkflowStageResult {
	if len(inputs) == 0 || !workflowGraphTeamApprovalGateInputEnabled(stage) {
		return nil
	}
	roles := workflowGraphTeamApprovalGateInputRoles(inputs)
	if len(roles) == 0 {
		return nil
	}
	decision := normalizeWorkflowSwitchValue(fallbackWorkflowGraphValue(inputs["decision"], "approve"))
	rejected := decision == "reject" || decision == "rejected" || decision == "deny" || decision == "denied" || decision == "block" || decision == "blocked"
	comment := strings.TrimSpace(inputs["comment"])
	if comment == "" {
		comment = strings.TrimSpace(inputs["reason"])
	}
	team := workflowGraphTeamTemplateName(stage)
	if team == "" {
		team = workflowGraphTeamApprovalGateCompletedTeam(completed)
	}
	results := make([]WorkflowStageResult, 0, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		kind := "team_approval"
		status := "approved"
		label := "Approval"
		content := comment
		if content == "" {
			content = fmt.Sprintf("manual approval recorded for %s", role)
		}
		if rejected {
			kind = "team_rejection"
			status = "blocked"
			label = "Rejection"
			if comment == "" {
				content = fmt.Sprintf("manual rejection recorded for %s", role)
			}
		}
		output := fmt.Sprintf("%s: %s", label, content)
		stageName := fmt.Sprintf("%s__%s_%s", stage.Name, strings.TrimPrefix(kind, "team_"), normalizeWorkflowSkillName(role))
		results = append(results, WorkflowStageResult{
			Stage:    WorkflowStage(stageName),
			Agent:    "operator",
			NodeType: "team_manual_approval",
			Status:   status,
			Metadata: workflowStageMetadata(map[string]string{
				"control":           "true",
				"manual_input":      "true",
				"team":              team,
				"team_role":         role,
				"team_manual_gate":  stage.Name,
				"team_packet_kind":  kind,
				"team_packet_input": "true",
			}),
			Result: schema.AgentResult{
				Output:  output,
				AgentID: "operator",
				Mode:    "team_manual_approval",
			},
			Output: WorkflowStageOutput{
				Summary:   output,
				RawOutput: output,
				Variables: workflowStageMetadata(map[string]string{
					"kind":     kind,
					"role":     role,
					"team":     team,
					"decision": decision,
					"status":   status,
					"comment":  comment,
				}),
			},
		})
	}
	return results
}

func workflowGraphTeamApprovalGateInputRoles(inputs map[string]string) []string {
	for _, key := range []string{"roles", "role", "approver_roles", "approval_roles", "approver"} {
		value := strings.TrimSpace(inputs[key])
		if value == "" {
			continue
		}
		roles, err := workflowGraphInputMultipleValues(value)
		if err == nil && len(roles) > 0 {
			return workflowGraphUniqueStrings(roles)
		}
	}
	return nil
}

func workflowGraphMergeTeamApprovalGatePolicy(policy *teamApprovalGatePolicy, metadata map[string]string, override bool) {
	if policy == nil || len(metadata) == 0 {
		return
	}
	if value := teamStateMetadataInt(metadata, "approval_quorum", "review_quorum", "quorum"); value > 0 && (override || policy.Required == 0) {
		policy.Required = value
	}
	if roles := teamStateMetadataList(metadata, "approval_roles", "review_roles", "quorum_roles"); len(roles) > 0 && (override || len(policy.Roles) == 0) {
		policy.Roles = roles
	}
	if value, ok := teamStateMetadataBoolValue(metadata, "reject_blocks", "rejection_blocks", "review_reject_blocks"); ok && (override || !policy.RejectBlocksSet) {
		policy.RejectBlocks = value
		policy.RejectBlocksSet = true
	}
}

func workflowGraphTeamStageRole(stage WorkflowStageResult) string {
	if value := strings.TrimSpace(stage.Metadata["team_role"]); value != "" {
		return value
	}
	return strings.TrimSpace(string(stage.Stage))
}

func workflowGraphHighestSeverity(value string) string {
	lower := strings.ToLower(value)
	best := ""
	for _, severity := range []string{"critical", "high", "medium", "low", "info"} {
		if strings.Contains(lower, severity) && workflowGraphSeverityRank(severity) > workflowGraphSeverityRank(best) {
			best = severity
		}
	}
	if best != "" {
		return best
	}
	normalized := normalizeWorkflowSwitchValue(value)
	if workflowGraphSeverityRank(normalized) > 0 {
		return normalized
	}
	return "none"
}

func workflowGraphSeverityRank(value string) int {
	switch normalizeWorkflowSwitchValue(value) {
	case "critical", "crit":
		return 5
	case "high":
		return 4
	case "medium", "med", "moderate":
		return 3
	case "low":
		return 2
	case "info", "informational":
		return 1
	default:
		return 0
	}
}

func workflowGraphInputGateVariables(stage workflowGraphStage, request string, completed []WorkflowStageResult, manualInputs map[string]map[string]string) map[string]string {
	variables := make(map[string]string)
	if len(stage.Input) > 0 {
		keys := make([]string, 0, len(stage.Input))
		for key := range stage.Input {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if value := strings.TrimSpace(resolveWorkflowGraphReference(stage.Input[key], request, completed)); value != "" {
				variables[key] = limitWorkflowGraphText(value)
			}
		}
	}
	if len(stage.Params) > 0 {
		keys := make([]string, 0, len(stage.Params))
		for key := range stage.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if isWorkflowGraphInputGateControlParam(key) {
				continue
			}
			if value := strings.TrimSpace(resolveWorkflowGraphReference(stage.Params[key], request, completed)); value != "" {
				variables[key] = limitWorkflowGraphText(value)
			}
		}
	}
	for key, value := range manualInputs[normalizeWorkflowSkillName(stage.Name)] {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		variables[key] = limitWorkflowGraphText(value)
	}
	if len(variables) == 0 {
		return nil
	}
	return variables
}

func workflowGraphInputGateValueVariables(stage workflowGraphStage, request string, completed []WorkflowStageResult, manualInputValues map[string]map[string]any, variables map[string]string) map[string]any {
	values := make(map[string]any)
	if len(stage.Input) > 0 {
		keys := make([]string, 0, len(stage.Input))
		for key := range stage.Input {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if value, ok := resolveWorkflowGraphReferenceValue(stage.Input[key], request, completed); ok {
				values[key] = copyWorkflowAnyValue(value)
				continue
			}
			if value := strings.TrimSpace(resolveWorkflowGraphReference(stage.Input[key], request, completed)); value != "" {
				values[key] = limitWorkflowGraphText(value)
			}
		}
	}
	if len(stage.Params) > 0 {
		keys := make([]string, 0, len(stage.Params))
		for key := range stage.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if isWorkflowGraphInputGateControlParam(key) {
				continue
			}
			if _, exists := values[key]; exists {
				continue
			}
			if value := strings.TrimSpace(resolveWorkflowGraphReference(stage.Params[key], request, completed)); value != "" {
				values[key] = limitWorkflowGraphText(value)
			}
		}
	}
	for key, value := range manualInputValues[normalizeWorkflowSkillName(stage.Name)] {
		if strings.TrimSpace(key) == "" {
			continue
		}
		values[key] = copyWorkflowAnyValue(value)
	}
	for key, value := range variables {
		if _, ok := values[key]; !ok {
			values[key] = value
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func isWorkflowGraphInputGateControlParam(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "manual", "wait_for_input", "requires_input", "mode", "prompt", "message", "reason", "label", "fields", "required":
		return true
	default:
		return false
	}
}

func workflowGraphInputGateRoute(variables map[string]string) string {
	if len(variables) == 0 {
		return "empty"
	}
	return "provided"
}

func workflowStageResultsFromRunSnapshots(snapshots []session.WorkflowRunStageSnapshot) []WorkflowStageResult {
	if len(snapshots) == 0 {
		return nil
	}
	results := make([]WorkflowStageResult, 0, len(snapshots))
	for _, snapshot := range snapshots {
		output := WorkflowStageOutput{
			Summary:      fallbackWorkflowGraphValue(snapshot.Summary, truncateSummary(snapshot.Result.Output)),
			RawOutput:    limitWorkflowGraphText(snapshot.Result.Output),
			Variables:    copyStringMap(snapshot.Outputs),
			Values:       copyWorkflowAnyMap(snapshot.OutputValues),
			Artifacts:    append([]session.WorkflowRunArtifact(nil), snapshot.Artifacts...),
			ToolResults:  append([]schema.ToolResult(nil), snapshot.Result.ToolResults...),
			Findings:     append([]schema.Finding(nil), snapshot.Result.Findings...),
			Changes:      append([]schema.Change(nil), snapshot.Result.Changes...),
			Verification: append([]schema.Verification(nil), snapshot.Result.Verification...),
		}
		results = append(results, WorkflowStageResult{
			Stage:       WorkflowStage(snapshot.Stage),
			Agent:       snapshot.AgentID,
			NodeType:    snapshot.NodeType,
			Skill:       snapshot.Skill,
			Tool:        snapshot.Tool,
			Status:      snapshot.Status,
			Attempts:    snapshot.Attempts,
			Input:       copyStringMap(snapshot.Inputs),
			InputValues: copyWorkflowAnyMap(snapshot.InputValues),
			Metadata:    copyStringMap(snapshot.Metadata),
			Result:      snapshot.Result,
			Output:      output,
			Acceptance:  append([]session.WorkflowRunAcceptanceSnapshot(nil), snapshot.Acceptance...),
		})
	}
	return results
}

func workflowGraphStageNamesForIndices(graph workflowGraph, indices []int) string {
	if len(indices) == 0 {
		return ""
	}
	names := make([]string, 0, len(indices))
	for _, index := range indices {
		if index >= 0 && index < len(graph.Stages) {
			names = append(names, graph.Stages[index].Name)
		}
	}
	return strings.Join(names, ",")
}

func workflowGraphSwitchTarget(cases map[string]string, value string) string {
	if len(cases) == 0 {
		return ""
	}
	for key, target := range cases {
		if normalizeWorkflowSwitchValue(key) == value {
			return target
		}
	}
	for _, fallback := range []string{"default", "else", "*"} {
		if target := strings.TrimSpace(cases[fallback]); target != "" {
			return target
		}
	}
	return ""
}

func normalizeWorkflowSwitchValue(value string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(value)), `"'`)
}

func (w *WorkflowRunner) workflowGraphNamedStageIndices(graph workflowGraph, names []string, completed []WorkflowStageResult) []int {
	if len(names) == 0 {
		return nil
	}
	seen := completedStageNames(completed)
	indicesByName := make(map[string]int, len(graph.Stages))
	for i, candidate := range graph.Stages {
		indicesByName[normalizeWorkflowSkillName(candidate.Name)] = i
	}
	indices := make([]int, 0, len(names))
	added := make(map[int]struct{})
	for _, name := range names {
		index, ok := indicesByName[normalizeWorkflowSkillName(name)]
		if !ok {
			continue
		}
		if _, done := seen[normalizeWorkflowSkillName(graph.Stages[index].Name)]; done {
			continue
		}
		if _, dup := added[index]; dup {
			continue
		}
		added[index] = struct{}{}
		indices = append(indices, index)
	}
	return indices
}

func (w *WorkflowRunner) nextWorkflowGraphStageCandidates(graph workflowGraph, stage workflowGraphStage, completed []WorkflowStageResult) []int {
	return w.workflowGraphNamedStageIndices(graph, stage.Next, completed)
}

func filterExplicitWorkflowGraphStages(names []string, graph workflowGraph, candidates []int) []int {
	if len(names) == 0 {
		return nil
	}
	allowed := make(map[string]int, len(candidates)*2)
	for _, index := range candidates {
		stage := graph.Stages[index]
		allowed[normalizeWorkflowSkillName(stage.Name)] = index
		allowed[normalizeWorkflowSkillName(stage.Skill)] = index
		allowed[normalizeWorkflowSkillName(stage.Agent)] = index
		if stage.Params != nil {
			allowed[normalizeWorkflowSkillName(stage.Params["team_role"])] = index
		}
	}
	selected := make([]int, 0, len(names))
	seen := make(map[int]struct{})
	for _, name := range names {
		index, ok := allowed[normalizeWorkflowSkillName(name)]
		if !ok {
			continue
		}
		if _, dup := seen[index]; dup {
			continue
		}
		seen[index] = struct{}{}
		selected = append(selected, index)
	}
	return selected
}

func workflowGraphTeamHandoffStageNames(stage workflowGraphStage, output string) []string {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	teamStage := strings.TrimSpace(stage.Params["team_stage"])
	names := make([]string, 0)
	for _, packet := range workflowTeamRolePacketsFromJSON(output) {
		packet = workflowTeamRolePacketApplyEscalationPolicy(packet, stage.Params)
		names = append(names, workflowGraphTeamPacketTargetNames(packet, teamStage)...)
	}
	return uniqueWorkflowSkillNames(names)
}

func workflowGraphTeamPacketTargetNames(packet workflowTeamRolePacket, teamStage string) []string {
	names := make([]string, 0, 3)
	for _, role := range []string{packet.ToRole, packet.OwnerRole} {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		names = append(names, role)
		if strings.TrimSpace(teamStage) != "" {
			names = append(names, workflowGraphTeamRoleStageName(teamStage, role))
		}
	}
	for _, agentID := range []string{packet.ToAgent, packet.AssignedAgent, packet.EscalateTo} {
		if strings.TrimSpace(agentID) != "" {
			names = append(names, agentID)
		}
	}
	return names
}

func bestWorkflowGraphStageCandidates(graph workflowGraph, candidates []int, context string) []int {
	bestIndex := -1
	bestScore := 0
	normalizedContext := normalizeWorkflowSkillName(context)
	for _, index := range candidates {
		stage := graph.Stages[index]
		score := 0
		if strings.Contains(normalizedContext, normalizeWorkflowSkillName(stage.Name)) {
			score += 2
		}
		if strings.Contains(normalizedContext, normalizeWorkflowSkillName(stage.Skill)) {
			score += 2
		}
		if score > bestScore {
			bestScore = score
			bestIndex = index
		}
	}
	if bestIndex < 0 {
		return nil
	}
	return []int{bestIndex}
}

func completedStageNames(completed []WorkflowStageResult) map[string]struct{} {
	seen := make(map[string]struct{}, len(completed))
	for _, stage := range completed {
		if key := normalizeWorkflowSkillName(string(stage.Stage)); key != "" {
			seen[key] = struct{}{}
		}
		if key := normalizeWorkflowSkillName(stage.Metadata["original_stage"]); key != "" {
			seen[key] = struct{}{}
		}
	}
	return seen
}

func resolveWorkflowGraphStageInputs(stage workflowGraphStage, request string, completed []WorkflowStageResult) map[string]string {
	if len(stage.Input) == 0 {
		return nil
	}
	inputs := make(map[string]string, len(stage.Input))
	keys := make([]string, 0, len(stage.Input))
	for key := range stage.Input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := resolveWorkflowGraphReference(stage.Input[key], request, completed)
		if strings.TrimSpace(value) != "" {
			inputs[key] = limitWorkflowGraphText(value)
		}
	}
	if len(inputs) == 0 {
		return nil
	}
	return inputs
}

func resolveWorkflowGraphStageInputValues(stage workflowGraphStage, request string, completed []WorkflowStageResult) map[string]any {
	if len(stage.Input) == 0 {
		return nil
	}
	inputs := make(map[string]any, len(stage.Input))
	keys := make([]string, 0, len(stage.Input))
	for key := range stage.Input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value, ok := resolveWorkflowGraphReferenceValue(stage.Input[key], request, completed); ok {
			inputs[key] = copyWorkflowAnyValue(value)
			continue
		}
		value := resolveWorkflowGraphReference(stage.Input[key], request, completed)
		if strings.TrimSpace(value) != "" {
			inputs[key] = limitWorkflowGraphText(value)
		}
	}
	if len(inputs) == 0 {
		return nil
	}
	return inputs
}

func buildWorkflowGraphStagePrompt(graph workflowGraph, stage workflowGraphStage, request string, completed []WorkflowStageResult) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Run workflow %q stage %q for the following request.\n\nOriginal request:\n%s\n", graph.Name, stage.Name, request)
	if graph.Description != "" {
		fmt.Fprintf(&builder, "\nWorkflow description: %s\n", graph.Description)
	}
	if strings.TrimSpace(stage.NodeType) != "" {
		fmt.Fprintf(&builder, "\nVisual node type: %s\n", stage.NodeType)
	}
	if strings.TrimSpace(stage.Tool) != "" {
		fmt.Fprintf(&builder, "Preferred/related tool for this stage: %s\n", stage.Tool)
	}
	if len(stage.Params) > 0 {
		builder.WriteString("Stage parameters:\n")
		keys := make([]string, 0, len(stage.Params))
		for key := range stage.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&builder, "- %s: %s\n", key, stage.Params[key])
		}
	}
	if inputs := resolveWorkflowGraphStageInputs(stage, request, completed); len(inputs) > 0 {
		builder.WriteString("\nMapped stage inputs:\n")
		keys := make([]string, 0, len(inputs))
		for key := range inputs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&builder, "- %s (%s): %s\n", key, stage.Input[key], truncateSummary(inputs[key]))
		}
	}
	if len(completed) > 0 {
		builder.WriteString("\nCompleted prior stages:\n")
		for _, prior := range completed {
			summary := prior.Output.Summary
			if strings.TrimSpace(summary) == "" {
				summary = prior.Result.Output
			}
			fmt.Fprintf(&builder, "- %s/%s: %s\n", prior.Stage, prior.Agent, truncateSummary(summary))
			if len(prior.Output.Variables) > 0 {
				keys := make([]string, 0, len(prior.Output.Variables))
				for key := range prior.Output.Variables {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					fmt.Fprintf(&builder, "  - output.%s: %s\n", key, truncateSummary(prior.Output.Variables[key]))
				}
			}
		}
	}
	if len(stage.Next) > 0 {
		fmt.Fprintf(&builder, "\nCandidate next stages: %s\n", strings.Join(stage.Next, ", "))
		if isWorkflowGraphSelectStrategy(stage.NextStrategy) {
			builder.WriteString("If you need a specific next stage, end with 'Next skill: <skill-or-stage-name>' or 'Next skills: <name>, <name>'.\n")
		}
	}
	builder.WriteString("\nUse the configured stage skill instructions. Produce a concise stage result.")
	return builder.String()
}

func isWorkflowGraphSelectStrategy(strategy string) bool {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "select", "best", "conditional", "planner_select", "planner-select", "dynamic", "graph":
		return true
	default:
		return false
	}
}

func (w *WorkflowRunner) runWorkflowGraphExecutableStage(ctx context.Context, stage workflowGraphStage, prompt string, skill schema.Skill, handler func(event schema.StreamEvent) error) (schema.AgentResult, int, error) {
	attempts := stage.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := runSkillStage(ctx, w.runtime, stage.Agent, prompt, skill, handler)
		if err == nil {
			return result, attempt, nil
		}
		lastErr = err
	}
	return schema.AgentResult{}, attempts, lastErr
}

func workflowGraphStageResult(stage workflowGraphStage, result schema.AgentResult, inputs map[string]string, inputValues map[string]any, attempts int) WorkflowStageResult {
	metadata := workflowStageMetadata(map[string]string{
		"next_strategy": stage.NextStrategy,
	})
	metadataKeys := []string{"team", "team_title", "team_stage", "team_role", "team_role_label", "team_role_final", "team_recommended_flow", "consumes", "produces", "tools"}
	metadataKeys = append(metadataKeys, workflowTeamEscalationPolicyParamKeys()...)
	metadataKeys = append(metadataKeys, workflowTeamApprovalPolicyParamKeys()...)
	for _, key := range metadataKeys {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			if metadata == nil {
				metadata = make(map[string]string)
			}
			metadata[key] = value
		}
	}
	stageResult := WorkflowStageResult{
		Stage:       WorkflowStage(stage.Name),
		Agent:       stage.Agent,
		NodeType:    stage.NodeType,
		Skill:       stage.Skill,
		Tool:        stage.Tool,
		Status:      "completed",
		Attempts:    attempts,
		Input:       copyStringMap(inputs),
		InputValues: copyWorkflowAnyMap(inputValues),
		Metadata:    metadata,
		Result:      result,
		Output:      buildWorkflowStageOutput(stage, result),
	}
	stageResult.Acceptance = evaluateWorkflowGraphAcceptanceCriteria(stage, stageResult)
	if len(stageResult.Acceptance) > 0 {
		verifications := workflowGraphAcceptanceVerifications(stageResult.Acceptance)
		stageResult.Result.Verification = append(stageResult.Result.Verification, verifications...)
		stageResult.Output.Verification = append(stageResult.Output.Verification, verifications...)
	}
	return stageResult
}

func workflowGraphStageAcceptanceCriteria(stage workflowGraphStage) []workflowGraphAcceptanceCriterion {
	total := len(stage.AcceptanceCriteria) + len(stage.Acceptance)
	if total == 0 {
		return nil
	}
	out := make([]workflowGraphAcceptanceCriterion, 0, total)
	out = append(out, stage.AcceptanceCriteria...)
	out = append(out, stage.Acceptance...)
	return out
}

func evaluateWorkflowGraphAcceptanceCriteria(stage workflowGraphStage, stageResult WorkflowStageResult) []session.WorkflowRunAcceptanceSnapshot {
	criteria := workflowGraphStageAcceptanceCriteria(stage)
	if len(criteria) == 0 {
		return nil
	}
	results := make([]session.WorkflowRunAcceptanceSnapshot, 0, len(criteria))
	for index, criterion := range criteria {
		name := strings.TrimSpace(criterion.Name)
		if name == "" {
			name = fmt.Sprintf("criterion-%d", index+1)
		}
		ref := strings.TrimSpace(criterion.Ref)
		actual, found := workflowGraphAcceptanceActual(stage, stageResult, ref)
		expected := workflowGraphAcceptanceExpected(criterion)
		status := "passed"
		reason := "acceptance criterion passed"
		if criterion.Exists != nil {
			if found != *criterion.Exists {
				status = "failed"
				reason = fmt.Sprintf("expected exists=%t for %s", *criterion.Exists, fallbackWorkflowGraphValue(ref, "stage output"))
			}
		} else if strings.TrimSpace(criterion.Equals) != "" {
			if actual != criterion.Equals {
				status = "failed"
				reason = fmt.Sprintf("expected %q, got %q", criterion.Equals, actual)
			}
		} else if strings.TrimSpace(criterion.Contains) != "" {
			if !strings.Contains(actual, criterion.Contains) {
				status = "failed"
				reason = fmt.Sprintf("expected output to contain %q", criterion.Contains)
			}
		} else if !found {
			status = "failed"
			reason = fmt.Sprintf("expected %s to exist", fallbackWorkflowGraphValue(ref, "stage output"))
		}
		results = append(results, session.WorkflowRunAcceptanceSnapshot{
			Name:        name,
			Description: strings.TrimSpace(criterion.Description),
			Ref:         ref,
			Expected:    expected,
			Actual:      limitWorkflowGraphText(actual),
			Status:      status,
			Reason:      reason,
		})
	}
	return results
}

func workflowGraphAcceptanceExpected(criterion workflowGraphAcceptanceCriterion) string {
	if criterion.Exists != nil {
		return fmt.Sprintf("exists=%t", *criterion.Exists)
	}
	if strings.TrimSpace(criterion.Equals) != "" {
		return criterion.Equals
	}
	if strings.TrimSpace(criterion.Contains) != "" {
		return "contains " + criterion.Contains
	}
	if strings.TrimSpace(criterion.Expected) != "" {
		return criterion.Expected
	}
	return "present"
}

func workflowGraphAcceptanceActual(stage workflowGraphStage, stageResult WorkflowStageResult, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		value := strings.TrimSpace(stageResult.Result.Output)
		return value, value != ""
	}
	lower := strings.ToLower(ref)
	for _, prefix := range []string{"outputs.", "output."} {
		if strings.HasPrefix(lower, prefix) {
			key := strings.TrimSpace(ref[len(prefix):])
			if value, ok := stageResult.Output.Variables[key]; ok {
				return value, strings.TrimSpace(value) != ""
			}
			if value, ok := stageResult.Output.Values[key]; ok {
				return workflowGraphAcceptanceStringValue(value)
			}
			return "", false
		}
	}
	value := resolveWorkflowResultReference(ref, stageResult.Result, stage)
	return value, strings.TrimSpace(value) != ""
}

func workflowGraphAcceptanceStringValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, strings.TrimSpace(typed) != ""
	case nil:
		return "", false
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed), true
		}
		return string(data), true
	}
}

func workflowGraphAcceptanceVerifications(items []session.WorkflowRunAcceptanceSnapshot) []schema.Verification {
	if len(items) == 0 {
		return nil
	}
	verifications := make([]schema.Verification, 0, len(items))
	for _, item := range items {
		verifications = append(verifications, schema.Verification{
			Kind:   "acceptance:" + fallbackWorkflowGraphValue(item.Name, "criterion"),
			Status: item.Status,
			Detail: item.Reason,
		})
	}
	return verifications
}

func workflowGraphErrorStageResult(stage workflowGraphStage, err error, inputs map[string]string, inputValues map[string]any, attempts int) WorkflowStageResult {
	message := ""
	if err != nil {
		message = err.Error()
	}
	result := schema.AgentResult{
		Output:  message,
		AgentID: stage.Agent,
		Mode:    stage.Name,
		ToolResults: []schema.ToolResult{{
			ToolName: "workflow_error",
			Content:  message,
			IsError:  true,
		}},
	}
	stageResult := workflowGraphStageResult(stage, result, inputs, inputValues, attempts)
	stageResult.Status = "failed"
	return stageResult
}

func workflowGraphDeniedStageResult(stage workflowGraphStage, result schema.ToolResult, inputs map[string]string, inputValues map[string]any) WorkflowStageResult {
	stageResult := workflowGraphStageResult(stage, schema.AgentResult{
		Output:      result.Content,
		ToolResults: []schema.ToolResult{result},
		AgentID:     stage.Agent,
		Mode:        stage.Name,
	}, inputs, inputValues, 1)
	stageResult.Status = "denied"
	return stageResult
}

func workflowGraphControlStageResult(stage workflowGraphStage, graph workflowGraph, decision workflowGraphControlDecision) WorkflowStageResult {
	target := strings.TrimSpace(decision.Target)
	if target == "" {
		target = workflowGraphStageNamesForIndices(graph, decision.Targets)
	}
	summaryParts := []string{fmt.Sprintf("%s control", fallbackWorkflowGraphValue(decision.Kind, "control"))}
	if strings.TrimSpace(decision.Route) != "" {
		summaryParts = append(summaryParts, "route="+decision.Route)
	}
	if target != "" {
		summaryParts = append(summaryParts, "target="+target)
	}
	summary := strings.Join(summaryParts, " ")
	output := WorkflowStageOutput{
		Summary:   summary,
		RawOutput: summary,
		Variables: workflowStageMetadata(map[string]string{
			"kind":   decision.Kind,
			"route":  decision.Route,
			"value":  decision.Value,
			"target": target,
			"passed": strconv.FormatBool(decision.Passed),
			"status": decision.Status,
			"reason": decision.Reason,
		}),
		Values: copyWorkflowAnyMap(decision.ValueVariables),
	}
	for key, value := range decision.Variables {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if output.Variables == nil {
			output.Variables = make(map[string]string)
		}
		output.Variables[key] = value
	}
	status := fallbackWorkflowGraphValue(decision.Status, "completed")
	return WorkflowStageResult{
		Stage:    WorkflowStage(stage.Name),
		Agent:    stage.Agent,
		NodeType: stage.NodeType,
		Status:   status,
		Output:   output,
		Metadata: workflowStageMetadata(map[string]string{
			"control": "true",
			"route":   decision.Route,
			"target":  target,
			"reason":  decision.Reason,
		}),
		Result: schema.AgentResult{
			Output: summary,
			Mode:   stage.Name,
		},
	}
}

func workflowStageMetadata(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyWorkflowAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = copyWorkflowAnyValue(value)
	}
	return out
}

func copyWorkflowAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return copyWorkflowAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = copyWorkflowAnyValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	case map[string]string:
		return copyStringMap(typed)
	default:
		return typed
	}
}

func workflowGraphNestedValue(value any, path []string) (any, bool) {
	if len(path) == 0 {
		return value, true
	}
	if len(path) == 1 && strings.TrimSpace(path[0]) == "" {
		return value, true
	}
	head := strings.Trim(strings.TrimSpace(path[0]), "[]")
	if head == "" {
		return nil, false
	}
	switch typed := value.(type) {
	case map[string]any:
		if next, ok := typed[head]; ok {
			return workflowGraphNestedValue(next, path[1:])
		}
		for key, next := range typed {
			if strings.EqualFold(strings.TrimSpace(key), head) {
				return workflowGraphNestedValue(next, path[1:])
			}
		}
	case map[string]string:
		if next, ok := typed[head]; ok {
			return workflowGraphNestedStringValue(next, path[1:])
		}
		for key, next := range typed {
			if strings.EqualFold(strings.TrimSpace(key), head) {
				return workflowGraphNestedStringValue(next, path[1:])
			}
		}
	case []any:
		index, err := strconv.Atoi(head)
		if err != nil || index < 0 || index >= len(typed) {
			return nil, false
		}
		return workflowGraphNestedValue(typed[index], path[1:])
	case []string:
		index, err := strconv.Atoi(head)
		if err != nil || index < 0 || index >= len(typed) {
			return nil, false
		}
		return workflowGraphNestedStringValue(typed[index], path[1:])
	case string:
		return workflowGraphNestedStringValue(typed, path)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil, false
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil, false
		}
		return workflowGraphNestedValue(decoded, path)
	}
	return nil, false
}

func workflowGraphNestedStringValue(value string, path []string) (any, bool) {
	if len(path) == 0 {
		return value, true
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, false
	}
	return workflowGraphNestedValue(decoded, path)
}

func fallbackWorkflowGraphValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func buildWorkflowStageOutput(stage workflowGraphStage, result schema.AgentResult) WorkflowStageOutput {
	output := WorkflowStageOutput{
		Summary:      truncateSummary(result.Output),
		RawOutput:    limitWorkflowGraphText(result.Output),
		Artifacts:    workflowGraphDeclaredArtifacts(stage, result),
		ToolResults:  append([]schema.ToolResult(nil), result.ToolResults...),
		Findings:     append([]schema.Finding(nil), result.Findings...),
		Changes:      append([]schema.Change(nil), result.Changes...),
		Verification: append([]schema.Verification(nil), result.Verification...),
	}
	if len(stage.Outputs) > 0 {
		output.Variables = make(map[string]string, len(stage.Outputs))
		output.Values = make(map[string]any, len(stage.Outputs))
		keys := make([]string, 0, len(stage.Outputs))
		for key := range stage.Outputs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if value, ok := resolveWorkflowResultReferenceValue(stage.Outputs[key], result, stage); ok {
				output.Values[key] = copyWorkflowAnyValue(value)
			}
			value := resolveWorkflowResultReference(stage.Outputs[key], result, stage)
			if strings.TrimSpace(value) != "" {
				output.Variables[key] = limitWorkflowGraphText(value)
			}
		}
		if len(output.Variables) == 0 {
			output.Variables = nil
		}
		if len(output.Values) == 0 {
			output.Values = nil
		}
	}
	return output
}

func workflowGraphDeclaredArtifacts(stage workflowGraphStage, result schema.AgentResult) []session.WorkflowRunArtifact {
	if len(stage.Artifacts) == 0 {
		return nil
	}
	artifacts := make([]session.WorkflowRunArtifact, 0, len(stage.Artifacts))
	stageName := strings.TrimSpace(stage.Name)
	for index, declared := range stage.Artifacts {
		artifact := workflowGraphDeclaredArtifact(stageName, index+1, declared, stage, result)
		if strings.TrimSpace(artifact.Content) == "" && strings.TrimSpace(artifact.Summary) == "" {
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	if len(artifacts) == 0 {
		return nil
	}
	return artifacts
}

func workflowGraphDeclaredArtifact(stageName string, index int, declared workflowGraphArtifact, stage workflowGraphStage, result schema.AgentResult) session.WorkflowRunArtifact {
	kind := normalizeWorkflowArtifactKind(declared.Kind)
	if kind == "" {
		kind = "artifact"
	}
	name := normalizeWorkflowArtifactName(declared.Name)
	if name == "" {
		name = fmt.Sprintf("%s-%d", kind, index)
	}
	content := workflowGraphDeclaredArtifactValue(declared.Content, stage, result)
	if strings.TrimSpace(content) == "" {
		content = workflowGraphDeclaredArtifactValue(declared.Ref, stage, result)
	}
	summary := workflowGraphDeclaredArtifactValue(declared.Summary, stage, result)
	if strings.TrimSpace(summary) == "" {
		summary = truncateSummary(content)
	}
	title := strings.TrimSpace(declared.Title)
	if title == "" {
		title = workflowGraphHumanLabel(name)
	}
	metadata := copyStringMap(declared.Metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["declared"] = "true"
	metadata["name"] = name
	if strings.TrimSpace(declared.Ref) != "" {
		metadata["ref"] = declared.Ref
	}
	return session.WorkflowRunArtifact{
		ID:       workflowDeclaredArtifactID(stageName, name, kind, index),
		Stage:    stageName,
		Kind:     kind,
		Title:    title,
		Summary:  limitWorkflowGraphText(summary),
		Content:  limitWorkflowGraphText(content),
		Metadata: workflowStageMetadata(metadata),
	}
}

func workflowGraphDeclaredArtifactValue(expr string, stage workflowGraphStage, result schema.AgentResult) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if literal, ok := unquoteWorkflowLiteral(expr); ok {
		return literal
	}
	return resolveWorkflowResultReference(expr, result, stage)
}

func workflowDeclaredArtifactID(stageName, name, kind string, index int) string {
	parts := []string{
		normalizeWorkflowArtifactName(stageName),
		normalizeWorkflowArtifactName(name),
		normalizeWorkflowArtifactName(kind),
		strconv.Itoa(index),
	}
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, "-")
}

func normalizeWorkflowArtifactName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.' || r == '/':
			if !lastDash && builder.Len() > 0 {
				builder.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

func normalizeWorkflowArtifactKind(value string) string {
	value = normalizeWorkflowArtifactName(value)
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func resolveWorkflowResultReference(expr string, result schema.AgentResult, stage workflowGraphStage) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if literal, ok := unquoteWorkflowLiteral(expr); ok {
		return literal
	}
	switch strings.ToLower(expr) {
	case "result.output", "result.raw_output", "output", "raw_output":
		return result.Output
	case "result.summary", "summary":
		return truncateSummary(result.Output)
	case "result.tool_results", "tool_results":
		return workflowGraphJSON(result.ToolResults)
	case "result.findings", "findings":
		return workflowGraphJSON(result.Findings)
	case "result.changes", "changes":
		return workflowGraphJSON(result.Changes)
	case "result.verification", "verification":
		return workflowGraphJSON(result.Verification)
	case "result.structured", "structured":
		return workflowGraphJSON(result.Structured)
	case "result.agent_id", "agent_id":
		return result.AgentID
	case "result.mode", "mode":
		return result.Mode
	}
	if strings.HasPrefix(strings.ToLower(expr), "params.") {
		key := strings.TrimSpace(expr[len("params."):])
		return stage.Params[key]
	}
	return expr
}

func resolveWorkflowResultReferenceValue(expr string, result schema.AgentResult, stage workflowGraphStage) (any, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, false
	}
	if literal, ok := unquoteWorkflowLiteral(expr); ok {
		return literal, true
	}
	switch strings.ToLower(expr) {
	case "result.output", "result.raw_output", "output", "raw_output":
		return result.Output, true
	case "result.summary", "summary":
		return truncateSummary(result.Output), true
	case "result.tool_results", "tool_results":
		return append([]schema.ToolResult(nil), result.ToolResults...), true
	case "result.findings", "findings":
		return append([]schema.Finding(nil), result.Findings...), true
	case "result.changes", "changes":
		return append([]schema.Change(nil), result.Changes...), true
	case "result.verification", "verification":
		return append([]schema.Verification(nil), result.Verification...), true
	case "result.structured", "structured":
		return append([]schema.StructuredSection(nil), result.Structured...), true
	case "result.agent_id", "agent_id":
		return result.AgentID, true
	case "result.mode", "mode":
		return result.Mode, true
	}
	if strings.HasPrefix(strings.ToLower(expr), "params.") {
		key := strings.TrimSpace(expr[len("params."):])
		return stage.Params[key], true
	}
	return nil, false
}

func resolveWorkflowGraphReference(expr, request string, completed []WorkflowStageResult) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if literal, ok := unquoteWorkflowLiteral(expr); ok {
		return literal
	}
	switch strings.ToLower(expr) {
	case "workflow.input", "workflow.request", "request", "input":
		return request
	case "previous.output", "previous.summary":
		if len(completed) == 0 {
			return ""
		}
		return completed[len(completed)-1].Output.Summary
	case "previous.raw_output":
		if len(completed) == 0 {
			return ""
		}
		return completed[len(completed)-1].Output.RawOutput
	}
	if strings.HasPrefix(strings.ToLower(expr), "stages.") {
		return resolveWorkflowStageReference(expr, completed)
	}
	return expr
}

func resolveWorkflowGraphReferenceValue(expr, request string, completed []WorkflowStageResult) (any, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, false
	}
	if literal, ok := unquoteWorkflowLiteral(expr); ok {
		return literal, true
	}
	switch strings.ToLower(expr) {
	case "workflow.input", "workflow.request", "request", "input":
		return request, true
	case "previous.output", "previous.summary":
		if len(completed) == 0 {
			return nil, false
		}
		return completed[len(completed)-1].Output.Summary, true
	case "previous.raw_output":
		if len(completed) == 0 {
			return nil, false
		}
		return completed[len(completed)-1].Output.RawOutput, true
	}
	if strings.HasPrefix(strings.ToLower(expr), "stages.") {
		return resolveWorkflowStageReferenceValue(expr, completed)
	}
	return nil, false
}

func resolveWorkflowStageReferenceValue(expr string, completed []WorkflowStageResult) (any, bool) {
	parts := strings.Split(expr, ".")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "stages") {
		return nil, false
	}
	stageName := normalizeWorkflowSkillName(parts[1])
	var stage *WorkflowStageResult
	for i := range completed {
		if normalizeWorkflowSkillName(string(completed[i].Stage)) == stageName {
			stage = &completed[i]
			break
		}
	}
	if stage == nil {
		return nil, false
	}
	if len(parts) >= 4 && strings.EqualFold(parts[2], "outputs") {
		key := strings.Join(parts[3:], ".")
		switch strings.ToLower(key) {
		case "summary":
			return stage.Output.Summary, true
		case "raw_output", "output":
			return stage.Output.RawOutput, true
		case "tool_results":
			return append([]schema.ToolResult(nil), stage.Output.ToolResults...), true
		case "findings":
			return append([]schema.Finding(nil), stage.Output.Findings...), true
		case "changes":
			return append([]schema.Change(nil), stage.Output.Changes...), true
		case "verification":
			return append([]schema.Verification(nil), stage.Output.Verification...), true
		default:
			if value, ok := stage.Output.Values[key]; ok {
				return copyWorkflowAnyValue(value), true
			}
			if value, ok := workflowGraphNestedValue(stage.Output.Values, strings.Split(key, ".")); ok {
				return copyWorkflowAnyValue(value), true
			}
			if value, ok := stage.Output.Variables[key]; ok {
				return value, true
			}
			if value, ok := workflowGraphNestedValue(stage.Output.Variables, strings.Split(key, ".")); ok {
				return copyWorkflowAnyValue(value), true
			}
		}
	}
	if len(parts) >= 4 && strings.EqualFold(parts[2], "result") {
		key := strings.Join(parts[3:], ".")
		switch strings.ToLower(key) {
		case "output":
			return stage.Result.Output, true
		case "tool_results":
			return append([]schema.ToolResult(nil), stage.Result.ToolResults...), true
		case "findings":
			return append([]schema.Finding(nil), stage.Result.Findings...), true
		case "changes":
			return append([]schema.Change(nil), stage.Result.Changes...), true
		case "verification":
			return append([]schema.Verification(nil), stage.Result.Verification...), true
		case "structured":
			return append([]schema.StructuredSection(nil), stage.Result.Structured...), true
		}
	}
	return nil, false
}

func resolveWorkflowStageReference(expr string, completed []WorkflowStageResult) string {
	parts := strings.Split(expr, ".")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "stages") {
		return ""
	}
	stageName := normalizeWorkflowSkillName(parts[1])
	var stage *WorkflowStageResult
	for i := range completed {
		if normalizeWorkflowSkillName(string(completed[i].Stage)) == stageName {
			stage = &completed[i]
			break
		}
	}
	if stage == nil {
		return ""
	}
	if len(parts) >= 4 && strings.EqualFold(parts[2], "outputs") {
		key := strings.Join(parts[3:], ".")
		switch strings.ToLower(key) {
		case "summary":
			return stage.Output.Summary
		case "raw_output", "output":
			return stage.Output.RawOutput
		case "tool_results":
			return workflowGraphJSON(stage.Output.ToolResults)
		case "findings":
			return workflowGraphJSON(stage.Output.Findings)
		case "changes":
			return workflowGraphJSON(stage.Output.Changes)
		case "verification":
			return workflowGraphJSON(stage.Output.Verification)
		default:
			if value, ok := resolveWorkflowStageReferenceValue(expr, completed); ok {
				return workflowGraphValueString(value)
			}
			return stage.Output.Variables[key]
		}
	}
	if len(parts) >= 4 && strings.EqualFold(parts[2], "result") {
		key := strings.Join(parts[3:], ".")
		switch strings.ToLower(key) {
		case "output":
			return stage.Result.Output
		case "tool_results":
			return workflowGraphJSON(stage.Result.ToolResults)
		case "findings":
			return workflowGraphJSON(stage.Result.Findings)
		case "changes":
			return workflowGraphJSON(stage.Result.Changes)
		case "verification":
			return workflowGraphJSON(stage.Result.Verification)
		}
	}
	return ""
}

func evaluateWorkflowGraphCondition(expr, request string, completed []WorkflowStageResult) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		if len(completed) == 0 {
			return false
		}
		return workflowTruthyAny(completed[len(completed)-1].Output.Summary)
	}
	if name, args, ok := workflowGraphFunctionCall(expr); ok {
		return evaluateWorkflowGraphFunctionCondition(name, args, request, completed)
	}
	for _, op := range []string{">=", "<=", "!=", "==", ">", "<"} {
		if left, right, ok := splitWorkflowComparison(expr, op); ok {
			return compareWorkflowAnyValues(workflowGraphExpressionValue(left, request, completed), workflowGraphExpressionValue(right, request, completed), op)
		}
	}
	return workflowTruthyAny(workflowGraphExpressionValue(expr, request, completed))
}

func evaluateWorkflowGraphFunctionCondition(name string, args []string, request string, completed []WorkflowStageResult) bool {
	switch name {
	case "contains":
		if len(args) != 2 {
			return false
		}
		return workflowGraphContainsValue(workflowGraphExpressionValue(args[0], request, completed), workflowGraphExpressionValue(args[1], request, completed))
	case "in":
		if len(args) != 2 {
			return false
		}
		return workflowGraphInValue(workflowGraphExpressionValue(args[0], request, completed), workflowGraphExpressionValue(args[1], request, completed))
	case "matches":
		if len(args) != 2 {
			return false
		}
		return workflowGraphMatchesValue(workflowGraphExpressionValue(args[0], request, completed), workflowGraphExpressionValue(args[1], request, completed))
	case "any":
		if len(args) == 1 {
			return workflowGraphAnyValue(workflowGraphExpressionValue(args[0], request, completed), nil)
		}
		if len(args) == 2 {
			return workflowGraphAnyValue(workflowGraphExpressionValue(args[0], request, completed), workflowGraphExpressionValue(args[1], request, completed))
		}
		return false
	case "all":
		if len(args) == 1 {
			return workflowGraphAllValue(workflowGraphExpressionValue(args[0], request, completed), nil)
		}
		if len(args) == 2 {
			return workflowGraphAllValue(workflowGraphExpressionValue(args[0], request, completed), workflowGraphExpressionValue(args[1], request, completed))
		}
		return false
	case "exists":
		if len(args) != 1 {
			return false
		}
		value, ok := resolveWorkflowGraphReferenceValue(args[0], request, completed)
		return ok && value != nil
	case "has":
		if len(args) != 1 {
			return false
		}
		return workflowTruthyAny(workflowGraphExpressionValue(args[0], request, completed))
	case "len":
		if len(args) != 1 {
			return false
		}
		return workflowValueLengthAny(workflowGraphExpressionValue(args[0], request, completed)) > 0
	case "risk_rank":
		if len(args) != 1 {
			return false
		}
		return workflowGraphRiskRankValue(workflowGraphExpressionValue(args[0], request, completed)) > 0
	default:
		return false
	}
}

func evaluateWorkflowLengthCondition(expr, request string, completed []WorkflowStageResult) bool {
	closeIndex := strings.Index(expr, ")")
	if closeIndex < len("len(") {
		return false
	}
	ref := strings.TrimSpace(expr[len("len("):closeIndex])
	rest := strings.TrimSpace(expr[closeIndex+1:])
	length := workflowValueLengthAny(workflowGraphExpressionValue(ref, request, completed))
	for _, op := range []string{">=", "<=", "!=", "==", ">", "<"} {
		if strings.HasPrefix(rest, op) {
			target, _ := strconv.Atoi(strings.TrimSpace(rest[len(op):]))
			return compareWorkflowNumbers(float64(length), float64(target), op)
		}
	}
	return length > 0
}

func splitWorkflowArgs(value string) (string, string, bool) {
	args, ok := splitWorkflowArgList(value)
	if !ok || len(args) != 2 {
		return "", "", false
	}
	return args[0], args[1], true
}

func splitWorkflowArgList(value string) ([]string, bool) {
	inQuote := rune(0)
	depth := 0
	start := 0
	var args []string
	for i, r := range value {
		switch {
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
			}
		case r == '\'' || r == '"':
			inQuote = r
		case r == '(' || r == '[' || r == '{':
			depth++
		case r == ')' || r == ']' || r == '}':
			if depth > 0 {
				depth--
			}
		case r == ',' && depth == 0:
			args = append(args, strings.TrimSpace(value[start:i]))
			start = i + len(string(r))
		}
	}
	if inQuote != 0 || depth != 0 {
		return nil, false
	}
	args = append(args, strings.TrimSpace(value[start:]))
	if len(args) == 1 && args[0] == "" {
		return nil, true
	}
	return args, true
}

func workflowGraphFunctionCall(expr string) (string, []string, bool) {
	expr = strings.TrimSpace(expr)
	open := strings.Index(expr, "(")
	if open <= 0 || !strings.HasSuffix(expr, ")") {
		return "", nil, false
	}
	name := strings.ToLower(strings.TrimSpace(expr[:open]))
	if name == "" {
		return "", nil, false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return "", nil, false
		}
	}
	inner := strings.TrimSpace(expr[open+1 : len(expr)-1])
	args, ok := splitWorkflowArgList(inner)
	if !ok {
		return "", nil, false
	}
	return name, args, true
}

func splitWorkflowComparison(value, op string) (string, string, bool) {
	index := strings.Index(value, op)
	if index < 0 {
		return "", "", false
	}
	return strings.TrimSpace(value[:index]), strings.TrimSpace(value[index+len(op):]), true
}

func workflowGraphReferenceString(expr, request string, completed []WorkflowStageResult) string {
	if value, ok := resolveWorkflowGraphReferenceValue(expr, request, completed); ok {
		return workflowGraphValueString(value)
	}
	return resolveWorkflowGraphReference(expr, request, completed)
}

func workflowGraphExpressionValue(expr, request string, completed []WorkflowStageResult) any {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if name, args, ok := workflowGraphFunctionCall(expr); ok {
		switch name {
		case "len":
			if len(args) == 1 {
				return float64(workflowValueLengthAny(workflowGraphExpressionValue(args[0], request, completed)))
			}
		case "risk_rank":
			if len(args) == 1 {
				return float64(workflowGraphRiskRankValue(workflowGraphExpressionValue(args[0], request, completed)))
			}
		}
	}
	if value, ok := resolveWorkflowGraphReferenceValue(expr, request, completed); ok {
		return value
	}
	if value, ok := workflowGraphLiteralValue(expr); ok {
		return value
	}
	return resolveWorkflowGraphReference(expr, request, completed)
}

func workflowGraphLiteralValue(expr string) (any, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", true
	}
	if literal, ok := unquoteWorkflowLiteral(expr); ok {
		return literal, true
	}
	switch strings.ToLower(expr) {
	case "true":
		return true, true
	case "false":
		return false, true
	case "null", "nil", "none":
		return nil, true
	}
	if number, err := strconv.ParseFloat(expr, 64); err == nil {
		return number, true
	}
	if strings.HasPrefix(expr, "{") || strings.HasPrefix(expr, "[") {
		var decoded any
		if err := json.Unmarshal([]byte(expr), &decoded); err == nil {
			return decoded, true
		}
	}
	return nil, false
}

func compareWorkflowAnyValues(left, right any, op string) bool {
	if leftNumber, leftOK := workflowGraphNumberValue(left); leftOK {
		if rightNumber, rightOK := workflowGraphNumberValue(right); rightOK {
			return compareWorkflowNumbers(leftNumber, rightNumber, op)
		}
	}
	if leftBool, leftOK := workflowGraphBoolValue(left); leftOK {
		if rightBool, rightOK := workflowGraphBoolValue(right); rightOK {
			switch op {
			case "==":
				return leftBool == rightBool
			case "!=":
				return leftBool != rightBool
			default:
				return false
			}
		}
	}
	return compareWorkflowValues(workflowGraphValueString(left), workflowGraphValueString(right), op)
}

func compareWorkflowValues(left, right, op string) bool {
	if leftNumber, leftOK := strconv.ParseFloat(strings.TrimSpace(left), 64); leftOK == nil {
		if rightNumber, rightOK := strconv.ParseFloat(strings.TrimSpace(right), 64); rightOK == nil {
			return compareWorkflowNumbers(leftNumber, rightNumber, op)
		}
	}
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch op {
	case "==":
		return strings.EqualFold(left, right)
	case "!=":
		return !strings.EqualFold(left, right)
	case ">":
		return left > right
	case "<":
		return left < right
	case ">=":
		return left >= right
	case "<=":
		return left <= right
	default:
		return false
	}
}

func workflowGraphContainsValue(value, needle any) bool {
	needleText := strings.ToLower(strings.TrimSpace(workflowGraphValueString(needle)))
	if needleText == "" {
		return false
	}
	switch typed := value.(type) {
	case nil:
		return false
	case []any:
		for _, item := range typed {
			if compareWorkflowAnyValues(item, needle, "==") || strings.Contains(strings.ToLower(workflowGraphValueString(item)), needleText) {
				return true
			}
		}
		return false
	case []string:
		for _, item := range typed {
			if strings.EqualFold(strings.TrimSpace(item), needleText) || strings.Contains(strings.ToLower(item), needleText) {
				return true
			}
		}
		return false
	case map[string]any:
		for key, item := range typed {
			if strings.EqualFold(strings.TrimSpace(key), needleText) || compareWorkflowAnyValues(item, needle, "==") || strings.Contains(strings.ToLower(workflowGraphValueString(item)), needleText) {
				return true
			}
		}
		return false
	case map[string]string:
		for key, item := range typed {
			if strings.EqualFold(strings.TrimSpace(key), needleText) || strings.EqualFold(strings.TrimSpace(item), needleText) || strings.Contains(strings.ToLower(item), needleText) {
				return true
			}
		}
		return false
	default:
		return strings.Contains(strings.ToLower(workflowGraphValueString(value)), needleText)
	}
}

func workflowGraphInValue(needle, collection any) bool {
	for _, item := range workflowGraphCollectionItems(collection, true) {
		if compareWorkflowAnyValues(needle, item, "==") {
			return true
		}
	}
	return false
}

func workflowGraphMatchesValue(value, pattern any) bool {
	patternText := strings.TrimSpace(workflowGraphValueString(pattern))
	if patternText == "" {
		return false
	}
	matched, err := regexp.MatchString(patternText, workflowGraphValueString(value))
	return err == nil && matched
}

func workflowGraphAnyValue(value any, needle any) bool {
	items := workflowGraphCollectionItems(value, false)
	if len(items) == 0 {
		return false
	}
	if needle == nil {
		for _, item := range items {
			if workflowTruthyAny(item) {
				return true
			}
		}
		return false
	}
	for _, item := range items {
		if compareWorkflowAnyValues(item, needle, "==") || workflowGraphContainsValue(item, needle) {
			return true
		}
	}
	return false
}

func workflowGraphAllValue(value any, needle any) bool {
	items := workflowGraphCollectionItems(value, false)
	if len(items) == 0 {
		return false
	}
	if needle == nil {
		for _, item := range items {
			if !workflowTruthyAny(item) {
				return false
			}
		}
		return true
	}
	for _, item := range items {
		if !compareWorkflowAnyValues(item, needle, "==") && !workflowGraphContainsValue(item, needle) {
			return false
		}
	}
	return true
}

func workflowGraphCollectionItems(value any, includeMapKeys bool) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		return append([]any(nil), typed...)
	case []string:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i] = item
		}
		return items
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]any, 0, len(typed)*2)
		for _, key := range keys {
			if includeMapKeys {
				items = append(items, key)
			}
			items = append(items, typed[key])
		}
		return items
	case map[string]string:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]any, 0, len(typed)*2)
		for _, key := range keys {
			if includeMapKeys {
				items = append(items, key)
			}
			items = append(items, typed[key])
		}
		return items
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		var array []any
		if json.Unmarshal([]byte(text), &array) == nil {
			return array
		}
		var object map[string]any
		if json.Unmarshal([]byte(text), &object) == nil {
			return workflowGraphCollectionItems(object, includeMapKeys)
		}
		if strings.Contains(text, ",") {
			parts := strings.Split(text, ",")
			items := make([]any, 0, len(parts))
			for _, part := range parts {
				if part = strings.TrimSpace(part); part != "" {
					items = append(items, part)
				}
			}
			return items
		}
		return []any{text}
	default:
		return []any{value}
	}
}

func workflowGraphRiskRankValue(value any) int {
	if number, ok := workflowGraphNumberValue(value); ok {
		return int(number)
	}
	return workflowGraphSeverityRank(workflowGraphHighestSeverity(workflowGraphValueString(value)))
}

func compareWorkflowNumbers(left, right float64, op string) bool {
	switch op {
	case "==":
		return left == right
	case "!=":
		return left != right
	case ">":
		return left > right
	case "<":
		return left < right
	case ">=":
		return left >= right
	case "<=":
		return left <= right
	default:
		return false
	}
}

func workflowValueLength(value string) int {
	return workflowValueLengthAny(value)
}

func workflowValueLengthAny(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return workflowStringValueLength(typed)
	case []any:
		return len(typed)
	case []string:
		return len(typed)
	case map[string]any:
		return len(typed)
	case map[string]string:
		return len(typed)
	default:
		data, err := json.Marshal(typed)
		if err == nil {
			var decoded any
			if json.Unmarshal(data, &decoded) == nil {
				switch converted := decoded.(type) {
				case []any:
					return len(converted)
				case map[string]any:
					return len(converted)
				}
			}
		}
		text := workflowGraphValueString(value)
		if text == "" {
			return 0
		}
		return 1
	}
}

func workflowStringValueLength(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var items []any
	if err := json.Unmarshal([]byte(value), &items); err == nil {
		return len(items)
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(value), &object); err == nil {
		return len(object)
	}
	return len(value)
}

func workflowTruthy(value string) bool {
	return workflowTruthyAny(value)
}

func workflowTruthyAny(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case float32:
		return typed != 0
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	case map[string]string:
		return len(typed) > 0
	default:
		return workflowTruthyString(workflowGraphValueString(value))
	}
}

func workflowTruthyString(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "", "0", "false", "no", "none", "null", "[]", "{}":
		return false
	default:
		return true
	}
}

func workflowGraphNumberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func workflowGraphBoolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		boolean, err := strconv.ParseBool(strings.TrimSpace(typed))
		return boolean, err == nil
	default:
		return false, false
	}
}

func workflowGraphValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case json.Number:
		return typed.String()
	default:
		return workflowGraphJSON(typed)
	}
}

func unquoteWorkflowLiteral(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return "", false
	}
	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		return strings.Trim(value, `"'`), true
	}
	return "", false
}

func workflowGraphJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func limitWorkflowGraphText(value string) string {
	const limit = 8192
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func (w *WorkflowRunner) captureWorkflowGraphApprovalContext(graph workflowGraph, index int, request, responseContent string, completed []WorkflowStageResult, results []schema.ToolResult, stagePrompt string) {
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	stage := graph.Stages[index]
	_ = w.runtime.AnnotateWorkflowGraphPendingApproval(callID, graph.Name, WorkflowStage(stage.Name), request, responseContent, completed, index, stagePrompt)
}

func (w *WorkflowRunner) approveWorkflowGraphTool(ctx context.Context, pending pendingApproval) (schema.ToolResult, error) {
	if autoApproved, ok := w.runtime.AutoApprovedToolResult(ctx, pending.call.ID); ok {
		return autoApproved, nil
	}
	resolved := false
	if _, ok := w.runtime.PendingApproval(pending.call.ID); ok {
		decision := w.runtime.ResolvePendingApproval(pending.call.ID, true)
		resolved = decision.Found
		if decision.Found {
			pending = decision.Pending
		}
	}
	result, err := w.runtime.mcp.CallTool(ctx, pending.call.Name, pending.call.Arguments)
	if err != nil {
		return schema.ToolResult{}, err
	}
	result.CallID = pending.call.ID
	if result.ToolName == "" {
		result.ToolName = pending.tool.Name
	}
	if strings.TrimSpace(result.Content) == "ok" {
		result.Content = fmt.Sprintf("done: %s", pending.call.Name)
	}
	if resolved && w.runtime.audit != nil {
		w.runtime.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "approved", Detail: result.Content})
	}
	return result, nil
}

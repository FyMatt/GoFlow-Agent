package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

type workflowGraph struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	Stages      []workflowGraphStage `yaml:"stages"`
}

type workflowGraphStage struct {
	Name         string            `yaml:"name"`
	NodeType     string            `yaml:"node_type"`
	Agent        string            `yaml:"agent"`
	Skill        string            `yaml:"skill"`
	Tool         string            `yaml:"tool"`
	Params       map[string]string `yaml:"params"`
	Approval     bool              `yaml:"approval"`
	NextStrategy string            `yaml:"next_strategy"`
	Next         []string          `yaml:"next"`
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
	if err := validateWorkflowGraph(normalizePersistedWorkflowName(name), graph); err != nil {
		return workflowGraph{}, err
	}
	return graph, nil
}

func validateWorkflowGraph(invokedName string, graph workflowGraph) error {
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
		key := normalizeWorkflowSkillName(stage.Name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("workflow graph %s has duplicate stage %q", graph.Name, stage.Name)
		}
		seen[key] = struct{}{}
		if isVisualOnlyWorkflowNode(stage) {
			continue
		}
		if strings.TrimSpace(stage.Agent) == "" {
			return fmt.Errorf("workflow graph %s stage %s missing agent", graph.Name, stage.Name)
		}
		if strings.TrimSpace(stage.Skill) == "" {
			return fmt.Errorf("workflow graph %s stage %s missing skill", graph.Name, stage.Name)
		}
	}
	for _, stage := range graph.Stages {
		for _, next := range stage.Next {
			if _, ok := seen[normalizeWorkflowSkillName(next)]; !ok {
				return fmt.Errorf("workflow graph %s stage %s references unknown next stage %q", graph.Name, stage.Name, next)
			}
		}
	}
	return nil
}

func (w *WorkflowRunner) runWorkflowGraph(ctx context.Context, graph workflowGraph, request string, approve bool, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	if len(graph.Stages) == 0 {
		return WorkflowResult{}, fmt.Errorf("workflow graph %s has no stages", graph.Name)
	}
	w.runtime.DisableWorkflowAutoApproval(graph.Name)
	return w.runWorkflowGraphQueue(ctx, graph, request, approve, []int{entryWorkflowGraphStageIndex(graph)}, nil, handler)
}

func (w *WorkflowRunner) runWorkflowGraphQueue(ctx context.Context, graph workflowGraph, request string, approve bool, queue []int, completed []WorkflowStageResult, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
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
		prompt := buildWorkflowGraphStagePrompt(graph, stage, request, completed)
		w.persistWorkflowState(graph.Name, "running", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), "")
		if err := w.runtime.SetActiveAgent(stage.Agent); err != nil {
			return WorkflowResult{}, err
		}
		result, err := runSkillStage(ctx, w.runtime, stage.Agent, prompt, skill, handler)
		if err != nil {
			return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", stage.Name, err)
		}
		if hasSuspendedToolResult(result.ToolResults) {
			w.captureWorkflowGraphApprovalContext(graph, index, request, result.Output, completed, result.ToolResults, prompt)
			approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), stage.Agent)
			w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), approvalPrompt)
			return WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
		}
		completed = append(completed, WorkflowStageResult{Stage: WorkflowStage(stage.Name), Agent: stage.Agent, Result: result})
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
	stage := graph.Stages[pending.skillIndex]
	if !approve {
		decision := w.runtime.ResolvePendingApproval(pending.call.ID, false)
		if !decision.Found {
			return WorkflowResult{}, fmt.Errorf("pending approval not found: %s", pending.call.ID)
		}
		result := schema.ToolResult{CallID: pending.call.ID, ToolName: pending.tool.Name, Content: fmt.Sprintf("tool %s denied by operator", pending.tool.Name), IsError: true, Denied: true}
		completed = append(completed, WorkflowStageResult{Stage: WorkflowStage(stage.Name), Agent: stage.Agent, Result: schema.AgentResult{Output: result.Content, ToolResults: []schema.ToolResult{result}, AgentID: stage.Agent, Mode: stage.Name}})
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
	completed = append(completed, WorkflowStageResult{Stage: WorkflowStage(stage.Name), Agent: stage.Agent, Result: result})
	queue := w.nextWorkflowGraphStageIndices(graph, pending.skillIndex, pending.request, result.Output, completed)
	return w.runWorkflowGraphQueue(ctx, graph, pending.request, true, queue, completed, handler)
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
	if len(candidates) == 0 && len(stage.Next) == 0 && currentIndex+1 < len(graph.Stages) {
		candidates = []int{currentIndex + 1}
	}
	if len(candidates) == 0 {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(stage.NextStrategy)) {
	case "select", "best", "conditional", "planner_select", "planner-select", "dynamic", "graph":
		if explicit := filterExplicitWorkflowGraphStages(parseExplicitNextSkillNames(stageOutput), graph, candidates); len(explicit) > 0 {
			return explicit
		}
		return bestWorkflowGraphStageCandidates(graph, candidates, request+"\n"+stageOutput)
	default:
		return candidates
	}
}

func (w *WorkflowRunner) nextWorkflowGraphStageCandidates(graph workflowGraph, stage workflowGraphStage, completed []WorkflowStageResult) []int {
	seen := completedStageNames(completed)
	indicesByName := make(map[string]int, len(graph.Stages))
	for i, candidate := range graph.Stages {
		indicesByName[normalizeWorkflowSkillName(candidate.Name)] = i
	}
	indices := make([]int, 0, len(stage.Next))
	for _, next := range stage.Next {
		index, ok := indicesByName[normalizeWorkflowSkillName(next)]
		if !ok {
			continue
		}
		if _, done := seen[normalizeWorkflowSkillName(graph.Stages[index].Name)]; done {
			continue
		}
		indices = append(indices, index)
	}
	return indices
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
	}
	return seen
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
	if len(completed) > 0 {
		builder.WriteString("\nCompleted prior stages:\n")
		for _, prior := range completed {
			fmt.Fprintf(&builder, "- %s/%s: %s\n", prior.Stage, prior.Agent, truncateSummary(prior.Result.Output))
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

func (w *WorkflowRunner) captureWorkflowGraphApprovalContext(graph workflowGraph, index int, request, responseContent string, completed []WorkflowStageResult, results []schema.ToolResult, stagePrompt string) {
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	stage := graph.Stages[index]
	_ = w.runtime.AnnotateWorkflowGraphPendingApproval(callID, graph.Name, WorkflowStage(stage.Name), request, responseContent, completed, index, stagePrompt)
}

func (w *WorkflowRunner) approveWorkflowGraphTool(ctx context.Context, pending pendingApproval) (schema.ToolResult, error) {
	resolved := false
	if _, ok := w.runtime.PendingApproval(pending.call.ID); ok {
		resolved = w.runtime.ResolvePendingApproval(pending.call.ID, true).Found
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

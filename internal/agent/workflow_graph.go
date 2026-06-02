package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/memory"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

type workflowGraph struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	Budget      workflowGraphBudget  `yaml:"budget"`
	Stages      []workflowGraphStage `yaml:"stages"`
}

type workflowGraphBudget struct {
	SoftPromptTokens  int `yaml:"soft_prompt_tokens"`
	HardPromptTokens  int `yaml:"hard_prompt_tokens"`
	SoftOutputTokens  int `yaml:"soft_output_tokens"`
	HardOutputTokens  int `yaml:"hard_output_tokens"`
	SoftTotalTokens   int `yaml:"soft_total_tokens"`
	HardTotalTokens   int `yaml:"hard_total_tokens"`
	SoftLLMCalls      int `yaml:"soft_llm_calls"`
	HardLLMCalls      int `yaml:"hard_llm_calls"`
	SoftContinuations int `yaml:"soft_continuations"`
	HardContinuations int `yaml:"hard_continuations"`
}

type workflowGraphStage struct {
	Name               string                             `yaml:"name"`
	NodeType           string                             `yaml:"node_type"`
	Agent              string                             `yaml:"agent"`
	Skill              string                             `yaml:"skill"`
	Tool               string                             `yaml:"tool"`
	Model              workflowGraphStageModel            `yaml:"model"`
	SoftBudgetRoute    bool                               `yaml:"-"`
	SoftBudgetRouteRef string                             `yaml:"-"`
	ModelEscalation    bool                               `yaml:"-"`
	ModelEscalationRef string                             `yaml:"-"`
	ModelEscalationWhy string                             `yaml:"-"`
	Execution          workflowGraphExecutionContract     `yaml:"execution"`
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
	Context            workflowGraphStageContext          `yaml:"context"`
	NextStrategy       string                             `yaml:"next_strategy"`
	Next               []string                           `yaml:"next"`
	Position           WorkflowGraphPosition              `yaml:"position"`
}

type workflowGraphRetry struct {
	MaxAttempts int `yaml:"max_attempts"`
}

type workflowGraphStageModel struct {
	Provider    string   `yaml:"provider"`
	Model       string   `yaml:"model"`
	MaxTokens   int      `yaml:"max_tokens"`
	Temperature *float64 `yaml:"temperature"`
}

type workflowGraphExecutionContract struct {
	Mode                    string   `yaml:"mode"`
	RiskLevel               string   `yaml:"risk_level"`
	Boundary                string   `yaml:"boundary"`
	RequiresApproval        bool     `yaml:"requires_approval"`
	RequiresAuthorizedScope bool     `yaml:"requires_authorized_scope"`
	RequiresRollback        bool     `yaml:"requires_rollback"`
	RequiresCredentialRef   bool     `yaml:"requires_credential_ref"`
	RequiresAllowlist       bool     `yaml:"requires_allowlist"`
	AllowLiveTools          []string `yaml:"allow_live_tools"`
	RequiredParams          []string `yaml:"required_params"`
}

type workflowGraphStageContext struct {
	Include             []string                      `yaml:"include"`
	Exclude             []string                      `yaml:"exclude"`
	MaxTokens           int                           `yaml:"max_tokens"`
	PromptMaxTokens     int                           `yaml:"prompt_max_tokens"`
	RequestMaxTokens    int                           `yaml:"request_max_tokens"`
	InputsMaxTokens     int                           `yaml:"inputs_max_tokens"`
	ParametersMaxTokens int                           `yaml:"parameters_max_tokens"`
	Retrieval           workflowGraphContextRetrieval `yaml:"retrieval"`
}

type workflowGraphContextRetrieval struct {
	Enabled bool   `yaml:"enabled"`
	Query   string `yaml:"query"`
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
			Name:      name,
			NodeType:  "team_role",
			Agent:     role.Agent,
			Skill:     role.Skill,
			Tool:      strings.Join(role.Tools, ","),
			Execution: parent.Execution,
			Params:    params,
			Input:     input,
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
	if err := validateWorkflowGraphBudget(graph.Name, graph.Budget); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(graph.Stages))
	for i, stage := range graph.Stages {
		if strings.TrimSpace(stage.Name) == "" {
			return fmt.Errorf("workflow graph %s stage %d missing name", graph.Name, i)
		}
		if err := validateWorkflowGraphArtifacts(graph.Name, stage); err != nil {
			return err
		}
		if err := validateWorkflowGraphSoftBudgetParams(graph.Name, stage); err != nil {
			return err
		}
		if err := validateWorkflowGraphExecutionContract(graph.Name, stage); err != nil {
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
		if stage.Model.MaxTokens < 0 {
			return fmt.Errorf("workflow graph %s stage %s model.max_tokens must not be negative", graph.Name, stage.Name)
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

func validateWorkflowGraphBudget(graphName string, budget workflowGraphBudget) error {
	values := map[string]int{
		"budget.soft_prompt_tokens": budget.SoftPromptTokens,
		"budget.hard_prompt_tokens": budget.HardPromptTokens,
		"budget.soft_output_tokens": budget.SoftOutputTokens,
		"budget.hard_output_tokens": budget.HardOutputTokens,
		"budget.soft_total_tokens":  budget.SoftTotalTokens,
		"budget.hard_total_tokens":  budget.HardTotalTokens,
		"budget.soft_llm_calls":     budget.SoftLLMCalls,
		"budget.hard_llm_calls":     budget.HardLLMCalls,
		"budget.soft_continuations": budget.SoftContinuations,
		"budget.hard_continuations": budget.HardContinuations,
	}
	for field, value := range values {
		if value < 0 {
			return fmt.Errorf("workflow graph %s %s must not be negative", graphName, field)
		}
	}
	checkLimitPair := func(field string, soft, hard int) error {
		if soft > 0 && hard > 0 && soft > hard {
			return fmt.Errorf("workflow graph %s budget.soft_%s must not exceed budget.hard_%s", graphName, field, field)
		}
		return nil
	}
	if err := checkLimitPair("prompt_tokens", budget.SoftPromptTokens, budget.HardPromptTokens); err != nil {
		return err
	}
	if err := checkLimitPair("output_tokens", budget.SoftOutputTokens, budget.HardOutputTokens); err != nil {
		return err
	}
	if err := checkLimitPair("total_tokens", budget.SoftTotalTokens, budget.HardTotalTokens); err != nil {
		return err
	}
	if err := checkLimitPair("llm_calls", budget.SoftLLMCalls, budget.HardLLMCalls); err != nil {
		return err
	}
	if err := checkLimitPair("continuations", budget.SoftContinuations, budget.HardContinuations); err != nil {
		return err
	}
	return nil
}

func validateWorkflowGraphSoftBudgetParams(graphName string, stage workflowGraphStage) error {
	for _, keys := range [][]string{
		{"soft_budget_max_tokens", "soft_budget.max_tokens", "budget.soft_max_tokens", "budget_soft_max_tokens"},
		{"soft_budget_max_parallel_branches", "soft_budget_max_branches", "soft_budget.max_parallel_branches", "budget.soft_max_parallel_branches", "budget_soft_max_parallel_branches"},
	} {
		key, value := workflowGraphParamFirstKey(stage.Params, keys...)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed < 0 {
			return fmt.Errorf("workflow graph %s stage %s params.%s must be a non-negative integer", graphName, stage.Name, key)
		}
	}
	if key, value := workflowGraphParamFirstKey(stage.Params, "soft_budget_temperature", "soft_budget.temperature", "budget.soft_temperature", "budget_soft_temperature"); value != "" {
		if _, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err != nil {
			return fmt.Errorf("workflow graph %s stage %s params.%s must be a number", graphName, stage.Name, key)
		}
	}
	return nil
}

func validateWorkflowGraphExecutionContract(graphName string, stage workflowGraphStage) error {
	if mode := workflowGraphExecutionMode(stage); mode != "" && !workflowGraphExecutionModeValid(mode) {
		return fmt.Errorf("workflow graph %s stage %s execution.mode must be one of planning, dry_run, live, manual, or disabled", graphName, stage.Name)
	}
	if risk := workflowGraphExecutionRiskLevel(stage); risk != "" && !workflowGraphExecutionRiskLevelValid(risk) {
		return fmt.Errorf("workflow graph %s stage %s execution.risk_level must be one of low, medium, high, or critical", graphName, stage.Name)
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

type workflowGraphBudgetUsage struct {
	PromptTokens          int
	EstimatedPromptTokens int
	NetPromptTokens       int
	GrossPromptTokens     int
	SavedTokens           int
	MemorySavedTokens     int
	HistorySavedTokens    int
	ArtifactSavedTokens   int
	SkillSavedTokens      int
	ToolSchemaSavedTokens int
	ReportedPromptTokens  int
	OutputTokens          int
	CachedTokens          int
	TotalTokens           int
	LLMCalls              int
	Continuations         int
	EstimatedInputCost    float64
	EstimatedOutputCost   float64
	EstimatedTotalCost    float64
	CostCurrency          string
	PricingSource         string
}

type workflowGraphBudgetLimitHit struct {
	Hit       bool
	Scope     string
	Metric    string
	Used      int
	SoftLimit int
	Limit     int
	Remaining int
	Reason    string
	SourceRef string
	Usage     workflowGraphBudgetUsage
}

type workflowGraphBudgetDiagnostic struct {
	Scope         string
	Metric        string
	Used          int
	SoftLimit     int
	HardLimit     int
	Remaining     int
	HasConfigured bool
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
			w.emitWorkflowGraphQualityGateFailureEvent(graph, stage, controlResult, decision, handler)
			w.emitWorkflowGraphParallelBudgetLimitEvent(graph, stage, decision, handler)
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
			targets := decision.Targets
			if decision.Kind == "parallel" {
				targets = w.workflowGraphQueueTargetsUntilJoin(graph, targets, completed)
			}
			queue = append(targets, queue...)
			continue
		}
		if stage.Approval && !approve {
			w.persistWorkflowState(graph.Name, "awaiting_approval", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), fmt.Sprintf("Workflow requires approval before running stage %s.", stage.Name))
			return WorkflowResult{Name: graph.Name, Status: "awaiting_approval", PendingApproval: true, ApprovalPrompt: fmt.Sprintf("Workflow requires approval before running stage %s.", stage.Name), CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
		}
		if readiness := workflowGraphStageExecutionReadiness(stage, approve); readiness.Blocked {
			return w.blockWorkflowGraphExecutionReadiness(graph, stage, request, completed, readiness, handler), nil
		}
		if hit := w.workflowGraphHardBudgetLimitHit(graph, stage, 1); hit.Hit {
			return w.pauseWorkflowGraphForHardBudget(graph, stage, request, completed, hit, handler), nil
		}
		w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, stage, 1, handler)
		stage = w.workflowGraphStageWithSoftBudgetRoute(graph, stage, 1, handler)
		skill, err := w.graphStageSkill(stage)
		if err != nil {
			return WorkflowResult{}, err
		}
		if err := w.ensureWorkflowAgent(stage.Agent); err != nil {
			return WorkflowResult{}, err
		}
		inputs := resolveWorkflowGraphStageInputs(stage, request, completed)
		inputValues := resolveWorkflowGraphStageInputValues(stage, request, completed)
		prompt := w.buildWorkflowGraphStagePrompt(ctx, graph, stage, request, completed)
		w.persistWorkflowState(graph.Name, "running", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), "")
		if err := w.runtime.SetActiveAgent(stage.Agent); err != nil {
			return WorkflowResult{}, err
		}
		beforeBudgetUsage := w.workflowGraphBudgetUsage()
		result, attempts, err := w.runWorkflowGraphExecutableStage(ctx, graph, stage, prompt, skill, handler)
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
			w.captureWorkflowGraphApprovalContext(graph, index, request, result.Output, result.ResponseMessage, completed, result.ToolResults, prompt)
			approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), stage.Agent)
			w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), approvalPrompt)
			return WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
		}
		stageResult := workflowGraphStageResult(stage, result, inputs, inputValues, attempts)
		w.applyWorkflowGraphStageBudgetDelta(&stageResult, beforeBudgetUsage)
		w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, stage, 0, handler)
		if result.Incomplete {
			paused := w.pauseWorkflowForIncompleteStage(graph.Name, request, completed, stageResult)
			return paused, nil
		}
		if outcome, handled, err := w.handleWorkflowGraphStageContractFailure(ctx, graph, index, request, completed, stageResult, handler); handled || err != nil {
			return outcome, err
		}
		w.applyWorkflowGraphFinalQualityHandoff(graph, stage, completed, &stageResult, handler)
		completed = append(completed, stageResult)
		seen[stageKey] = struct{}{}
		queue = append(w.nextWorkflowGraphStageIndices(graph, index, request, result.Output, completed), queue...)
	}
	finalSummary := summarizeWorkflow(completed)
	w.runtime.DisableWorkflowAutoApproval(graph.Name)
	if err := w.runtime.RestoreDefaultAgent(); err != nil {
		return WorkflowResult{}, err
	}
	w.persistWorkflowState(graph.Name, "completed", "", request, finalSummary, "")
	result := WorkflowResult{Name: graph.Name, Status: "completed", CompletedStages: completed, FinalSummary: finalSummary}
	w.applyWorkflowGraphBudgetSummary(graph, &result)
	return result, nil
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
		prompt = w.buildWorkflowGraphStagePrompt(ctx, graph, stage, pending.request, completed)
	}
	if err := w.ensureWorkflowAgent(stage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	if hit := w.workflowGraphHardBudgetLimitHit(graph, stage, 1); hit.Hit {
		return w.pauseWorkflowGraphForHardBudget(graph, stage, pending.request, completed, hit, handler), nil
	}
	w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, stage, 1, handler)
	if err := w.runtime.SetActiveAgent(stage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	beforeBudgetUsage := w.workflowGraphBudgetUsage()
	result, err := continueAgentRunWithModelSkillAndOptions(ctx, w.runtime, stage.Agent, prompt, stage.Model, &skill, workflowStageRunOptions{AllowedTools: workflowGraphStageAllowedTools(stage)}, pending.call, pending.responseContent, pending.responseMessage, toolResult, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", stage.Name, err)
	}
	if hasSuspendedToolResult(result.ToolResults) {
		w.captureWorkflowGraphApprovalContext(graph, pending.skillIndex, pending.request, result.Output, result.ResponseMessage, completed, result.ToolResults, prompt)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), stage.Agent)
		w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(stage.Name), pending.request, summarizeWorkflow(completed), approvalPrompt)
		return WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
	}
	inputs := resolveWorkflowGraphStageInputs(stage, pending.request, completed)
	inputValues := resolveWorkflowGraphStageInputValues(stage, pending.request, completed)
	stageResult := workflowGraphStageResult(stage, result, inputs, inputValues, 1)
	w.applyWorkflowGraphStageBudgetDelta(&stageResult, beforeBudgetUsage)
	w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, stage, 0, handler)
	if result.Incomplete {
		return w.pauseWorkflowForIncompleteStage(graph.Name, pending.request, completed, stageResult), nil
	}
	if outcome, handled, err := w.handleWorkflowGraphStageContractFailure(ctx, graph, pending.skillIndex, pending.request, completed, stageResult, handler); handled || err != nil {
		return outcome, err
	}
	w.applyWorkflowGraphFinalQualityHandoff(graph, stage, completed, &stageResult, handler)
	completed = append(completed, stageResult)
	queue := w.nextWorkflowGraphStageIndices(graph, pending.skillIndex, pending.request, result.Output, completed)
	return w.runWorkflowGraphQueue(ctx, graph, pending.request, true, queue, completed, nil, nil, handler)
}

func (w *WorkflowRunner) handleWorkflowGraphStageContractFailure(ctx context.Context, graph workflowGraph, index int, request string, completed []WorkflowStageResult, stageResult WorkflowStageResult, handler func(event schema.StreamEvent) error) (WorkflowResult, bool, error) {
	if index < 0 || index >= len(graph.Stages) {
		return WorkflowResult{}, false, nil
	}
	stage := graph.Stages[index]
	failure := workflowGraphStageContractFailure(stage, stageResult)
	if !failure.Failed || !failure.Required {
		return WorkflowResult{}, false, nil
	}
	workflowGraphApplyContractFailureMetadata(&stageResult, failure)
	if handler != nil {
		_ = handler(schema.StreamEvent{
			Type:           schema.StreamEventStatus,
			TaskStage:      string(stageResult.Stage),
			Content:        failure.Reason,
			AgentID:        stageResult.Agent,
			Mode:           stageResult.Result.Mode,
			NeedsAction:    true,
			WorkflowName:   graph.Name,
			WorkflowStatus: "blocked",
			NextStage:      string(stageResult.Stage),
			Reason:         "contract_validation_failed",
			Severity:       "error",
			ContractCheck:  failure.Check,
			SourceRef:      failure.SourceRef,
		})
	}
	if w.workflowGraphContractFailureShouldEscalate(stage, stageResult) {
		result, err := w.runWorkflowGraphEscalatedStageAndContinue(ctx, graph, index, request, completed, stageResult, "contract_validation_failed", handler)
		return result, true, err
	}
	nextCompleted := append([]WorkflowStageResult(nil), completed...)
	nextCompleted = append(nextCompleted, stageResult)
	if target := strings.TrimSpace(failure.Route); target != "" {
		targets := w.workflowGraphNamedStageIndices(graph, []string{target}, nextCompleted)
		if len(targets) > 0 {
			result, err := w.runWorkflowGraphQueue(ctx, graph, request, true, targets, nextCompleted, nil, nil, handler)
			return result, true, err
		}
	}
	if len(stage.OnError) > 0 {
		targets := w.workflowGraphNamedStageIndices(graph, stage.OnError, nextCompleted)
		if len(targets) > 0 {
			result, err := w.runWorkflowGraphQueue(ctx, graph, request, true, targets, nextCompleted, nil, nil, handler)
			return result, true, err
		}
	}
	summary := summarizeWorkflow(nextCompleted)
	w.runtime.DisableWorkflowAutoApproval(graph.Name)
	if err := w.runtime.RestoreDefaultAgent(); err != nil {
		return WorkflowResult{}, true, err
	}
	w.persistWorkflowState(graph.Name, "blocked", stageResult.Stage, request, summary, failure.Reason)
	return WorkflowResult{
		Name:            graph.Name,
		Status:          "blocked",
		CompletedStages: nextCompleted,
		ApprovalPrompt:  failure.Reason,
		NextStage:       stageResult.Stage,
		FinalSummary:    summary,
	}, true, nil
}

func (w *WorkflowRunner) workflowGraphContractFailureShouldEscalate(stage workflowGraphStage, stageResult WorkflowStageResult) bool {
	if workflowGraphStageResultWasModelEscalated(stageResult) {
		return false
	}
	if workflowGraphContractFailPolicy(stage) != "escalate" {
		return false
	}
	_, _, ok := workflowGraphEscalationStageModel(stage)
	return ok
}

func workflowGraphStageResultWasModelEscalated(stageResult WorkflowStageResult) bool {
	if workflowTruthy(stageResult.Metadata["model.escalated"]) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(stageResult.Metadata["model.route"]), "model_escalated")
}

func (w *WorkflowRunner) runWorkflowGraphEscalatedStageAndContinue(ctx context.Context, graph workflowGraph, index int, request string, completed []WorkflowStageResult, failed WorkflowStageResult, reason string, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if index < 0 || index >= len(graph.Stages) {
		return WorkflowResult{}, fmt.Errorf("workflow %s model escalation references unknown stage", graph.Name)
	}
	stage, ok := workflowGraphStageWithModelEscalation(graph.Stages[index], reason)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s has no model escalation provider, model, or budget override configured", graph.Stages[index].Name)
	}
	skill, err := w.graphStageSkill(stage)
	if err != nil {
		return WorkflowResult{}, err
	}
	if err := w.ensureWorkflowAgent(stage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	inputs := resolveWorkflowGraphStageInputs(stage, request, completed)
	if len(inputs) == 0 {
		inputs = copyStringMap(failed.Input)
	}
	inputValues := resolveWorkflowGraphStageInputValues(stage, request, completed)
	if len(inputValues) == 0 {
		inputValues = copyWorkflowAnyMap(failed.InputValues)
	}
	prompt := w.buildWorkflowGraphStagePrompt(ctx, graph, stage, request, completed)
	prompt = workflowGraphModelEscalationPrompt(prompt, failed)
	w.persistWorkflowState(graph.Name, "running", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), "")
	if err := w.runtime.SetActiveAgent(stage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	w.emitWorkflowGraphModelEscalationEvent(graph, stage, handler)
	w.emitWorkflowGraphStageRetryEvent(graph, stage, 2, reason, handler)
	beforeBudgetUsage := w.workflowGraphBudgetUsage()
	result, attempts, err := w.runWorkflowGraphExecutableStage(ctx, graph, stage, prompt, skill, handler)
	if err != nil {
		errorTargets := w.workflowGraphNamedStageIndices(graph, stage.OnError, completed)
		if len(errorTargets) > 0 {
			errorResult := workflowGraphErrorStageResult(stage, err, inputs, inputValues, attempts)
			nextCompleted := append([]WorkflowStageResult(nil), completed...)
			nextCompleted = append(nextCompleted, errorResult)
			return w.runWorkflowGraphQueue(ctx, graph, request, true, errorTargets, nextCompleted, nil, nil, handler)
		}
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", stage.Name, err)
	}
	if hasSuspendedToolResult(result.ToolResults) {
		w.captureWorkflowGraphApprovalContext(graph, index, request, result.Output, result.ResponseMessage, completed, result.ToolResults, prompt)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), stage.Agent)
		w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), approvalPrompt)
		return WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}, nil
	}
	stageResult := workflowGraphStageResult(stage, result, inputs, inputValues, attempts)
	w.applyWorkflowGraphStageBudgetDelta(&stageResult, beforeBudgetUsage)
	w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, stage, 0, handler)
	if result.Incomplete {
		return w.pauseWorkflowForIncompleteStage(graph.Name, request, completed, stageResult), nil
	}
	if outcome, handled, err := w.handleWorkflowGraphStageContractFailure(ctx, graph, index, request, completed, stageResult, handler); handled || err != nil {
		return outcome, err
	}
	w.applyWorkflowGraphFinalQualityHandoff(graph, stage, completed, &stageResult, handler)
	completed = append(completed, stageResult)
	queue := w.nextWorkflowGraphStageIndices(graph, index, request, result.Output, completed)
	return w.runWorkflowGraphQueue(ctx, graph, request, true, queue, completed, nil, nil, handler)
}

func (w *WorkflowRunner) emitWorkflowGraphQualityGateFailureEvent(graph workflowGraph, stage workflowGraphStage, stageResult WorkflowStageResult, decision workflowGraphControlDecision, handler func(event schema.StreamEvent) error) {
	if handler == nil || decision.Kind != "quality_gate" || decision.Passed {
		return
	}
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = strings.TrimSpace(stageResult.Metadata["reason"])
	}
	if reason == "" {
		reason = "quality gate failed"
	}
	sourceRef := workflowGraphQualityGateFailureSourceRef(stageResult)
	_ = handler(schema.StreamEvent{
		Type:           schema.StreamEventStatus,
		TaskStage:      stage.Name,
		Content:        reason,
		AgentID:        fallbackWorkflowGraphValue(stage.Agent, "workflow"),
		Mode:           "workflow",
		NeedsAction:    decision.Halt,
		WorkflowName:   graph.Name,
		WorkflowStatus: fallbackWorkflowGraphValue(decision.Status, "blocked"),
		NextStage:      stage.Name,
		Reason:         "quality_gate_failed",
		Severity:       "error",
		ContractCheck:  "quality_gate",
		SourceRef:      sourceRef,
	})
}

func (w *WorkflowRunner) blockWorkflowGraphExecutionReadiness(graph workflowGraph, stage workflowGraphStage, request string, completed []WorkflowStageResult, readiness workflowGraphExecutionReadiness, handler func(event schema.StreamEvent) error) WorkflowResult {
	stageResult := workflowGraphExecutionBlockedStageResult(stage, readiness)
	if handler != nil {
		_ = handler(schema.StreamEvent{
			Type:           schema.StreamEventStatus,
			TaskStage:      stage.Name,
			Content:        readiness.Reason,
			AgentID:        fallbackWorkflowGraphValue(stage.Agent, "workflow"),
			Mode:           "workflow",
			NeedsAction:    true,
			WorkflowName:   graph.Name,
			WorkflowStatus: "blocked",
			NextStage:      stage.Name,
			Reason:         "execution_readiness_blocked",
			Severity:       "error",
			ContractCheck:  "execution_readiness",
			SourceRef:      readiness.SourceRef,
		})
	}
	nextCompleted := append([]WorkflowStageResult(nil), completed...)
	nextCompleted = append(nextCompleted, stageResult)
	summary := summarizeWorkflow(nextCompleted)
	w.runtime.DisableWorkflowAutoApproval(graph.Name)
	if err := w.runtime.RestoreDefaultAgent(); err != nil {
		return WorkflowResult{
			Name:            graph.Name,
			Status:          "blocked",
			CompletedStages: nextCompleted,
			ApprovalPrompt:  err.Error(),
			NextStage:       WorkflowStage(stage.Name),
			FinalSummary:    summary,
		}
	}
	w.persistWorkflowState(graph.Name, "blocked", WorkflowStage(stage.Name), request, summary, readiness.Reason)
	return WorkflowResult{
		Name:            graph.Name,
		Status:          "blocked",
		CompletedStages: nextCompleted,
		ApprovalPrompt:  readiness.Reason,
		NextStage:       WorkflowStage(stage.Name),
		FinalSummary:    summary,
	}
}

type workflowGraphExecutionReadiness struct {
	Mode           string
	RiskLevel      string
	Boundary       string
	Ready          bool
	Blocked        bool
	Missing        []string
	Reason         string
	SourceRef      string
	AllowLiveTools []string
}

func workflowGraphStageExecutionReadiness(stage workflowGraphStage, approved bool) workflowGraphExecutionReadiness {
	mode := workflowGraphExecutionMode(stage)
	if mode == "" {
		mode = "planning"
	}
	readiness := workflowGraphExecutionReadiness{
		Mode:           mode,
		RiskLevel:      workflowGraphExecutionRiskLevel(stage),
		Boundary:       workflowGraphExecutionBoundary(stage),
		Ready:          true,
		AllowLiveTools: workflowGraphExecutionAllowLiveTools(stage),
	}
	if !workflowGraphExecutionModeRequiresLiveReadiness(mode) {
		return readiness
	}
	missing := make([]string, 0, 8)
	addMissing := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && !containsWorkflowGraphString(missing, name) {
			missing = append(missing, name)
		}
	}
	if workflowGraphExecutionRequiresApproval(stage) && !workflowGraphExecutionApprovalSatisfied(stage, approved) {
		addMissing("approval")
	}
	if workflowGraphExecutionRequiresAuthorizedScope(stage) && !workflowGraphExecutionAuthorizedScopeSatisfied(stage) {
		addMissing("authorized_scope")
	}
	if workflowGraphExecutionRequiresRollback(stage) && !workflowGraphExecutionRollbackSatisfied(stage) {
		addMissing("rollback")
	}
	if workflowGraphExecutionRequiresCredentialRef(stage) && !workflowGraphExecutionCredentialRefSatisfied(stage) {
		addMissing("credential_ref")
	}
	if workflowGraphExecutionRequiresAllowlist(stage) && !workflowGraphExecutionAllowlistSatisfied(stage) {
		addMissing("allowlist")
	}
	if required := workflowGraphExecutionMissingRequiredParams(stage); len(required) > 0 {
		for _, key := range required {
			addMissing("params." + key)
		}
	}
	if missingTools := workflowGraphExecutionMissingLiveTools(stage, readiness.AllowLiveTools); len(missingTools) > 0 {
		addMissing("allow_live_tools:" + strings.Join(missingTools, ","))
	}
	if len(missing) == 0 {
		return readiness
	}
	readiness.Ready = false
	readiness.Blocked = true
	readiness.Missing = missing
	readiness.SourceRef = workflowGraphExecutionMissingSourceRef(missing)
	readiness.Reason = fmt.Sprintf("workflow stage %s is live but missing execution readiness: %s", fallbackWorkflowGraphValue(stage.Name, "stage"), strings.Join(missing, ", "))
	return readiness
}

func workflowGraphExecutionBlockedStageResult(stage workflowGraphStage, readiness workflowGraphExecutionReadiness) WorkflowStageResult {
	metadata := workflowGraphStageExecutionMetadata(stage, readiness)
	metadata = mergeWorkflowStageMetadata(metadata, map[string]string{
		"reason":          readiness.Reason,
		"severity":        "error",
		"contract_check":  "execution_readiness",
		"source_ref":      readiness.SourceRef,
		"contract_failed": "true",
	})
	output := WorkflowStageOutput{
		Summary:   readiness.Reason,
		RawOutput: readiness.Reason,
		Variables: workflowStageMetadata(map[string]string{
			"execution_mode":    readiness.Mode,
			"execution_ready":   strconv.FormatBool(readiness.Ready),
			"execution_missing": strings.Join(readiness.Missing, ","),
			"reason":            readiness.Reason,
			"source_ref":        readiness.SourceRef,
		}),
		Values: map[string]any{
			"execution_ready":   readiness.Ready,
			"execution_missing": append([]string(nil), readiness.Missing...),
		},
		Verification: []schema.Verification{{
			Kind:   "contract:execution_readiness",
			Status: "failed",
			Detail: readiness.Reason,
		}},
	}
	if readiness.RiskLevel != "" {
		output.Variables["execution_risk_level"] = readiness.RiskLevel
	}
	if readiness.Boundary != "" {
		output.Variables["execution_boundary"] = readiness.Boundary
	}
	applyWorkflowStageCanonicalOutputFields(&output)
	return WorkflowStageResult{
		Stage:    WorkflowStage(stage.Name),
		Agent:    stage.Agent,
		NodeType: stage.NodeType,
		Skill:    stage.Skill,
		Tool:     stage.Tool,
		Status:   "blocked",
		Metadata: metadata,
		Output:   output,
		Result: schema.AgentResult{
			Output:       readiness.Reason,
			AgentID:      stage.Agent,
			Mode:         stage.Name,
			Verification: append([]schema.Verification(nil), output.Verification...),
		},
	}
}

func (w *WorkflowRunner) emitWorkflowGraphParallelBudgetLimitEvent(graph workflowGraph, stage workflowGraphStage, decision workflowGraphControlDecision, handler func(event schema.StreamEvent) error) {
	if handler == nil || decision.Kind != "parallel" || !strings.EqualFold(strings.TrimSpace(decision.Variables["branch_budget_limited"]), "true") {
		return
	}
	limit := strings.TrimSpace(decision.Variables["branch_budget_limit"])
	original := strings.TrimSpace(decision.Variables["branch_original_count"])
	branches := strings.TrimSpace(decision.Variables["branches"])
	skipped := strings.TrimSpace(decision.Variables["branch_skipped"])
	content := fmt.Sprintf("soft budget branch limit applied at stage %s", fallbackWorkflowGraphValue(stage.Name, "parallel"))
	if original != "" || limit != "" {
		content += fmt.Sprintf(": selected %d of %s", len(decision.Targets), fallbackWorkflowGraphValue(original, "?"))
		if limit != "" {
			content += " with limit " + limit
		}
	}
	_ = handler(schema.StreamEvent{
		Type:           schema.StreamEventStatus,
		TaskStage:      stage.Name,
		Content:        content,
		AgentID:        "workflow",
		Mode:           "workflow",
		WorkflowName:   graph.Name,
		WorkflowStatus: "running",
		NextStage:      stage.Name,
		Reason:         "budget_branch_limit_applied",
		Severity:       "warning",
		SourceRef:      "stage.params.soft_budget_max_parallel_branches",
		ArgumentsSummary: strings.Join([]string{
			"branch_count=" + strconv.Itoa(len(decision.Targets)),
			"branch_budget_limit=" + limit,
			"branch_original_count=" + original,
			"branches=" + branches,
			"branch_skipped=" + skipped,
		}, " "),
	})
}

func (w *WorkflowRunner) workflowGraphHardBudgetLimitHit(graph workflowGraph, stage workflowGraphStage, nextLLMCalls int) workflowGraphBudgetLimitHit {
	budget := workflowGraphEffectiveBudget(graph)
	if workflowGraphBudgetEmpty(budget) || w == nil || w.runtime == nil || w.runtime.session == nil {
		return workflowGraphBudgetLimitHit{}
	}
	usage := w.workflowGraphBudgetUsage()
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
	}
	candidate := usage
	if nextLLMCalls < 0 {
		nextLLMCalls = 0
	}
	candidate.LLMCalls += nextLLMCalls
	if candidate.TotalTokens == 0 {
		candidate.TotalTokens = candidate.PromptTokens + candidate.OutputTokens
	}
	limit := func(scope, metric string, used, soft, hard int) workflowGraphBudgetLimitHit {
		if hard <= 0 || used <= hard {
			return workflowGraphBudgetLimitHit{}
		}
		reason := fmt.Sprintf("workflow hard budget exceeded before stage %s: %s used %d of %d", fallbackWorkflowGraphValue(stage.Name, "next"), metric, used, hard)
		return workflowGraphBudgetLimitHit{
			Hit:       true,
			Scope:     scope,
			Metric:    metric,
			Used:      used,
			SoftLimit: soft,
			Limit:     hard,
			Remaining: maxInt(hard-used, 0),
			Reason:    reason,
			SourceRef: fmt.Sprintf("workflow.budget.%s", metric),
			Usage:     usage,
		}
	}
	for _, hit := range []workflowGraphBudgetLimitHit{
		limit("prompt", "prompt_tokens", candidate.PromptTokens, budget.SoftPromptTokens, budget.HardPromptTokens),
		limit("output", "output_tokens", candidate.OutputTokens, budget.SoftOutputTokens, budget.HardOutputTokens),
		limit("total", "total_tokens", candidate.TotalTokens, budget.SoftTotalTokens, budget.HardTotalTokens),
		limit("llm_call", "llm_calls", candidate.LLMCalls, budget.SoftLLMCalls, budget.HardLLMCalls),
		limit("continuation", "continuations", candidate.Continuations, budget.SoftContinuations, budget.HardContinuations),
	} {
		if hit.Hit {
			return hit
		}
	}
	return workflowGraphBudgetLimitHit{}
}

func (w *WorkflowRunner) workflowGraphSoftBudgetLimitHit(graph workflowGraph, stage workflowGraphStage, nextLLMCalls int) workflowGraphBudgetLimitHit {
	budget := workflowGraphEffectiveBudget(graph)
	if workflowGraphBudgetEmpty(budget) || w == nil || w.runtime == nil || w.runtime.session == nil {
		return workflowGraphBudgetLimitHit{}
	}
	usage := w.workflowGraphBudgetUsage()
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
	}
	candidate := usage
	if nextLLMCalls < 0 {
		nextLLMCalls = 0
	}
	candidate.LLMCalls += nextLLMCalls
	if candidate.TotalTokens == 0 {
		candidate.TotalTokens = candidate.PromptTokens + candidate.OutputTokens
	}
	limit := func(scope, metric string, used, soft, hard int) workflowGraphBudgetLimitHit {
		if soft <= 0 || used <= soft || w.workflowGraphSoftBudgetWarningEmitted(metric) {
			return workflowGraphBudgetLimitHit{}
		}
		limitForRemaining := hard
		if limitForRemaining <= 0 {
			limitForRemaining = soft
		}
		reason := fmt.Sprintf("workflow soft budget exceeded near stage %s: %s used %d of soft limit %d", fallbackWorkflowGraphValue(stage.Name, "next"), metric, used, soft)
		return workflowGraphBudgetLimitHit{
			Hit:       true,
			Scope:     scope,
			Metric:    metric,
			Used:      used,
			SoftLimit: soft,
			Limit:     hard,
			Remaining: maxInt(limitForRemaining-used, 0),
			Reason:    reason,
			SourceRef: fmt.Sprintf("workflow.budget.%s", metric),
			Usage:     candidate,
		}
	}
	for _, hit := range []workflowGraphBudgetLimitHit{
		limit("prompt", "prompt_tokens", candidate.PromptTokens, budget.SoftPromptTokens, budget.HardPromptTokens),
		limit("output", "output_tokens", candidate.OutputTokens, budget.SoftOutputTokens, budget.HardOutputTokens),
		limit("total", "total_tokens", candidate.TotalTokens, budget.SoftTotalTokens, budget.HardTotalTokens),
		limit("llm_call", "llm_calls", candidate.LLMCalls, budget.SoftLLMCalls, budget.HardLLMCalls),
		limit("continuation", "continuations", candidate.Continuations, budget.SoftContinuations, budget.HardContinuations),
	} {
		if hit.Hit {
			return hit
		}
	}
	return workflowGraphBudgetLimitHit{}
}

func (w *WorkflowRunner) workflowGraphSoftBudgetWarningEmitted(metric string) bool {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return false
	}
	runID := strings.TrimSpace(w.runID)
	if runID == "" {
		runID = w.runtime.currentWorkflowRunID()
	}
	if runID == "" {
		return false
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return false
	}
	metric = strings.TrimSpace(metric)
	for _, event := range run.Events {
		if !strings.EqualFold(strings.TrimSpace(event.BudgetReason), "budget_soft_limit_hit") && !strings.EqualFold(strings.TrimSpace(event.Reason), "budget_soft_limit_hit") {
			continue
		}
		if metric == "" || strings.EqualFold(strings.TrimSpace(event.BudgetMetric), metric) {
			return true
		}
	}
	return false
}

func (w *WorkflowRunner) workflowGraphSoftBudgetActive(graph workflowGraph, stage workflowGraphStage, nextLLMCalls int) bool {
	if w.workflowGraphSoftBudgetWarningEmitted("") {
		return true
	}
	return w.workflowGraphSoftBudgetLimitHit(graph, stage, nextLLMCalls).Hit
}

func (w *WorkflowRunner) workflowGraphStageWithSoftBudgetRoute(graph workflowGraph, stage workflowGraphStage, nextLLMCalls int, handler func(event schema.StreamEvent) error) workflowGraphStage {
	if !w.workflowGraphSoftBudgetActive(graph, stage, nextLLMCalls) {
		return stage
	}
	model, ok := workflowGraphSoftBudgetStageModel(stage)
	if !ok || !workflowGraphStageModelConfigured(model) {
		return stage
	}
	stage.Model = model
	stage.SoftBudgetRoute = true
	stage.SoftBudgetRouteRef = "stage.params.soft_budget_*"
	w.emitWorkflowGraphSoftBudgetRouteEvent(graph, stage, handler)
	return stage
}

func workflowGraphSoftBudgetStageModel(stage workflowGraphStage) (workflowGraphStageModel, bool) {
	model := stage.Model
	configured := false
	if provider := workflowGraphParamFirst(stage.Params, "soft_budget_provider", "soft_budget.provider", "budget.soft_provider", "budget_soft_provider"); provider != "" {
		model.Provider = provider
		configured = true
	}
	if name := workflowGraphParamFirst(stage.Params, "soft_budget_model", "soft_budget.model", "budget.soft_model", "budget_soft_model"); name != "" {
		model.Model = name
		configured = true
	}
	if raw := workflowGraphParamFirst(stage.Params, "soft_budget_max_tokens", "soft_budget.max_tokens", "budget.soft_max_tokens", "budget_soft_max_tokens"); raw != "" {
		if maxTokens, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && maxTokens > 0 {
			model.MaxTokens = maxTokens
			configured = true
		}
	}
	if raw := workflowGraphParamFirst(stage.Params, "soft_budget_temperature", "soft_budget.temperature", "budget.soft_temperature", "budget_soft_temperature"); raw != "" {
		if temperature, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
			model.Temperature = &temperature
			configured = true
		}
	}
	return model, configured
}

func workflowGraphEscalationStageModel(stage workflowGraphStage) (workflowGraphStageModel, string, bool) {
	model := stage.Model
	configured := false
	sourceKeys := make([]string, 0, 4)
	if key, provider := workflowGraphParamFirstKey(stage.Params,
		"escalate_provider",
		"escalation_provider",
		"model_escalation_provider",
		"contract_fail_provider",
		"contract_fail_model_provider",
	); provider != "" {
		model.Provider = provider
		configured = true
		sourceKeys = append(sourceKeys, "stage.params."+key)
	}
	if key, name := workflowGraphParamFirstKey(stage.Params,
		"escalate_model",
		"escalation_model",
		"model_escalation_model",
		"contract_fail_model",
		"contract_fail_model_name",
	); name != "" {
		model.Model = name
		configured = true
		sourceKeys = append(sourceKeys, "stage.params."+key)
	}
	if key, raw := workflowGraphParamFirstKey(stage.Params,
		"escalate_max_tokens",
		"escalation_max_tokens",
		"model_escalation_max_tokens",
		"contract_fail_max_tokens",
	); raw != "" {
		if maxTokens, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && maxTokens > 0 {
			model.MaxTokens = maxTokens
			configured = true
			sourceKeys = append(sourceKeys, "stage.params."+key)
		}
	}
	if key, raw := workflowGraphParamFirstKey(stage.Params,
		"escalate_temperature",
		"escalation_temperature",
		"model_escalation_temperature",
		"contract_fail_temperature",
	); raw != "" {
		if temperature, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
			model.Temperature = &temperature
			configured = true
			sourceKeys = append(sourceKeys, "stage.params."+key)
		}
	}
	sourceRef := "stage.params.model_escalation_*"
	if len(sourceKeys) > 0 {
		sourceRef = strings.Join(workflowGraphUniqueStrings(sourceKeys), ",")
	}
	return model, sourceRef, configured
}

func workflowGraphStageWithModelEscalation(stage workflowGraphStage, reason string) (workflowGraphStage, bool) {
	model, sourceRef, ok := workflowGraphEscalationStageModel(stage)
	if !ok || !workflowGraphStageModelConfigured(model) {
		return stage, false
	}
	stage.Model = model
	stage.ModelEscalation = true
	stage.ModelEscalationRef = sourceRef
	stage.ModelEscalationWhy = fallbackWorkflowGraphValue(reason, "model_escalated")
	return stage, true
}

func workflowGraphParamFirst(params map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(params[key]); value != "" {
			return value
		}
	}
	return ""
}

func workflowGraphParamFirstKey(params map[string]string, keys ...string) (string, string) {
	for _, key := range keys {
		if value := strings.TrimSpace(params[key]); value != "" {
			return key, value
		}
	}
	return "", ""
}

func (w *WorkflowRunner) emitWorkflowGraphSoftBudgetRouteEvent(graph workflowGraph, stage workflowGraphStage, handler func(event schema.StreamEvent) error) {
	if handler == nil {
		return
	}
	parts := make([]string, 0, 4)
	if provider := strings.TrimSpace(stage.Model.Provider); provider != "" {
		parts = append(parts, "provider="+provider)
	}
	if model := strings.TrimSpace(stage.Model.Model); model != "" {
		parts = append(parts, "model="+model)
	}
	if stage.Model.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("max_tokens=%d", stage.Model.MaxTokens))
	}
	if stage.Model.Temperature != nil {
		parts = append(parts, "temperature="+strconv.FormatFloat(*stage.Model.Temperature, 'f', -1, 64))
	}
	detail := strings.Join(parts, " ")
	content := fmt.Sprintf("soft budget model route applied at stage %s", fallbackWorkflowGraphValue(stage.Name, "next"))
	if detail != "" {
		content += ": " + detail
	}
	_ = handler(schema.StreamEvent{
		Type:           schema.StreamEventStatus,
		TaskStage:      stage.Name,
		Content:        content,
		AgentID:        fallbackWorkflowGraphValue(stage.Agent, "workflow"),
		Mode:           "workflow",
		WorkflowName:   graph.Name,
		WorkflowStatus: "running",
		NextStage:      stage.Name,
		Reason:         "budget_model_route_applied",
		Severity:       "info",
		SourceRef:      "stage.params.soft_budget_*",
	})
}

func (w *WorkflowRunner) emitWorkflowGraphModelEscalationEvent(graph workflowGraph, stage workflowGraphStage, handler func(event schema.StreamEvent) error) {
	if handler == nil {
		return
	}
	parts := make([]string, 0, 4)
	if provider := strings.TrimSpace(stage.Model.Provider); provider != "" {
		parts = append(parts, "provider="+provider)
	}
	if model := strings.TrimSpace(stage.Model.Model); model != "" {
		parts = append(parts, "model="+model)
	}
	if stage.Model.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("max_tokens=%d", stage.Model.MaxTokens))
	}
	if stage.Model.Temperature != nil {
		parts = append(parts, "temperature="+strconv.FormatFloat(*stage.Model.Temperature, 'f', -1, 64))
	}
	content := fmt.Sprintf("model escalated for stage %s", fallbackWorkflowGraphValue(stage.Name, "stage"))
	if detail := strings.Join(parts, " "); detail != "" {
		content += ": " + detail
	}
	_ = handler(schema.StreamEvent{
		Type:           schema.StreamEventStatus,
		TaskStage:      stage.Name,
		Content:        content,
		AgentID:        fallbackWorkflowGraphValue(stage.Agent, "workflow"),
		Mode:           "workflow",
		WorkflowName:   graph.Name,
		WorkflowStatus: "running",
		NextStage:      stage.Name,
		Reason:         "model_escalated",
		Severity:       "info",
		SourceRef:      fallbackWorkflowGraphValue(stage.ModelEscalationRef, "stage.params.model_escalation_*"),
	})
}

func (w *WorkflowRunner) emitWorkflowGraphStageRetryEvent(graph workflowGraph, stage workflowGraphStage, attempt int, reason string, handler func(event schema.StreamEvent) error) {
	if handler == nil || attempt <= 1 {
		return
	}
	content := fmt.Sprintf("workflow stage %s retry attempt %d", fallbackWorkflowGraphValue(stage.Name, "stage"), attempt)
	if strings.TrimSpace(reason) != "" {
		content += ": " + strings.TrimSpace(reason)
	}
	_ = handler(schema.StreamEvent{
		Type:           schema.StreamEventStatus,
		TaskStage:      stage.Name,
		Content:        content,
		AgentID:        fallbackWorkflowGraphValue(stage.Agent, "workflow"),
		Mode:           "workflow",
		WorkflowName:   graph.Name,
		WorkflowStatus: "running",
		NextStage:      stage.Name,
		Reason:         "stage_retry",
		Severity:       "warning",
		SourceRef:      fmt.Sprintf("stage.retry.attempt[%d]", attempt),
	})
}

func workflowGraphBudgetDiagnosticFromSoftHit(hit workflowGraphBudgetLimitHit) workflowGraphBudgetDiagnostic {
	if !hit.Hit {
		return workflowGraphBudgetDiagnostic{}
	}
	return workflowGraphBudgetDiagnostic{
		Scope:         hit.Scope,
		Metric:        hit.Metric,
		Used:          hit.Used,
		SoftLimit:     hit.SoftLimit,
		HardLimit:     hit.Limit,
		Remaining:     hit.Remaining,
		HasConfigured: hit.SoftLimit > 0 || hit.Limit > 0,
	}
}

func (w *WorkflowRunner) emitWorkflowGraphSoftBudgetWarning(ctx context.Context, graph workflowGraph, stage workflowGraphStage, nextLLMCalls int, handler func(event schema.StreamEvent) error) {
	hit := w.workflowGraphSoftBudgetLimitHit(graph, stage, nextLLMCalls)
	if !hit.Hit {
		return
	}
	content := hit.Reason
	if strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("workflow soft budget exceeded near stage %s", fallbackWorkflowGraphValue(stage.Name, "next"))
	}
	if compacted, detail := w.workflowGraphCompactContextForSoftBudget(ctx, graph, stage, hit); compacted {
		content += "; compacted context snapshot recorded for downstream stages"
	} else if strings.TrimSpace(detail) != "" {
		content += "; context compaction skipped: " + detail
	}
	diagnostic := workflowGraphBudgetDiagnosticFromSoftHit(hit)
	event := schema.StreamEvent{
		Type:                        schema.StreamEventStatus,
		TaskStage:                   stage.Name,
		Content:                     content,
		AgentID:                     fallbackWorkflowGraphValue(stage.Agent, "workflow"),
		Mode:                        "workflow",
		WorkflowName:                graph.Name,
		WorkflowStatus:              "running",
		NextStage:                   stage.Name,
		Reason:                      "budget_soft_limit_hit",
		Severity:                    "warning",
		BudgetScope:                 hit.Scope,
		BudgetReason:                "budget_soft_limit_hit",
		BudgetMetric:                diagnostic.Metric,
		BudgetUsed:                  diagnostic.Used,
		BudgetSoftLimit:             diagnostic.SoftLimit,
		BudgetHardLimit:             diagnostic.HardLimit,
		BudgetRemaining:             diagnostic.Remaining,
		BudgetPromptTokens:          hit.Usage.PromptTokens,
		BudgetEstimatedPromptTokens: hit.Usage.EstimatedPromptTokens,
		BudgetNetPromptTokens:       hit.Usage.NetPromptTokens,
		BudgetGrossPromptTokens:     hit.Usage.GrossPromptTokens,
		BudgetSavedTokens:           hit.Usage.SavedTokens,
		BudgetMemorySavedTokens:     hit.Usage.MemorySavedTokens,
		BudgetHistorySavedTokens:    hit.Usage.HistorySavedTokens,
		BudgetArtifactSavedTokens:   hit.Usage.ArtifactSavedTokens,
		BudgetSkillSavedTokens:      hit.Usage.SkillSavedTokens,
		BudgetToolSchemaSavedTokens: hit.Usage.ToolSchemaSavedTokens,
		BudgetReportedPromptTokens:  hit.Usage.ReportedPromptTokens,
		BudgetOutputTokens:          hit.Usage.OutputTokens,
		BudgetCachedTokens:          hit.Usage.CachedTokens,
		BudgetTotalTokens:           hit.Usage.TotalTokens,
		BudgetLLMCalls:              hit.Usage.LLMCalls,
		BudgetContinuations:         hit.Usage.Continuations,
		BudgetEstimatedInputCost:    hit.Usage.EstimatedInputCost,
		BudgetEstimatedOutputCost:   hit.Usage.EstimatedOutputCost,
		BudgetEstimatedTotalCost:    hit.Usage.EstimatedTotalCost,
		BudgetCostCurrency:          hit.Usage.CostCurrency,
		BudgetPricingSource:         hit.Usage.PricingSource,
		SourceRef:                   hit.SourceRef,
		PromptTokens:                hit.Usage.ReportedPromptTokens,
		OutputTokens:                hit.Usage.OutputTokens,
		CachedTokens:                hit.Usage.CachedTokens,
	}
	if handler != nil {
		_ = handler(event)
		return
	}
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return
	}
	runID := strings.TrimSpace(w.runID)
	if runID == "" {
		runID = w.runtime.currentWorkflowRunID()
	}
	if runID != "" {
		w.runtime.session.AppendWorkflowRunEvent(runID, workflowRunEventSnapshot(event))
	}
}

func (w *WorkflowRunner) workflowGraphCompactContextForSoftBudget(ctx context.Context, graph workflowGraph, stage workflowGraphStage, hit workflowGraphBudgetLimitHit) (bool, string) {
	if w == nil || w.runtime == nil || w.runtime.memory == nil || w.runtime.session == nil {
		return false, ""
	}
	reason := fmt.Sprintf("workflow soft budget exceeded: workflow=%s stage=%s metric=%s used=%d soft_limit=%d", graph.Name, fallbackWorkflowGraphValue(stage.Name, "next"), hit.Metric, hit.Used, hit.SoftLimit)
	_, err := w.runtime.compactContext(ctx, reason, true)
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

func workflowGraphEffectiveBudget(graph workflowGraph) workflowGraphBudget {
	budget := graph.Budget
	for _, stage := range graph.Stages {
		budget = workflowGraphBudgetFromParams(budget, stage.Params)
	}
	return budget
}

func workflowGraphBudgetFromParams(budget workflowGraphBudget, params map[string]string) workflowGraphBudget {
	if len(params) == 0 {
		return budget
	}
	assign := func(keys []string, target *int) {
		for _, key := range keys {
			value, ok := params[key]
			if !ok {
				continue
			}
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err == nil && n > 0 {
				*target = n
				return
			}
		}
	}
	assign([]string{"workflow_soft_prompt_tokens", "budget.soft_prompt_tokens", "soft_prompt_tokens"}, &budget.SoftPromptTokens)
	assign([]string{"workflow_hard_prompt_tokens", "budget.hard_prompt_tokens", "hard_prompt_tokens"}, &budget.HardPromptTokens)
	assign([]string{"workflow_soft_output_tokens", "budget.soft_output_tokens", "soft_output_tokens"}, &budget.SoftOutputTokens)
	assign([]string{"workflow_hard_output_tokens", "budget.hard_output_tokens", "hard_output_tokens"}, &budget.HardOutputTokens)
	assign([]string{"workflow_soft_total_tokens", "budget.soft_total_tokens", "soft_total_tokens"}, &budget.SoftTotalTokens)
	assign([]string{"workflow_hard_total_tokens", "budget.hard_total_tokens", "hard_total_tokens"}, &budget.HardTotalTokens)
	assign([]string{"workflow_soft_llm_calls", "budget.soft_llm_calls", "soft_llm_calls"}, &budget.SoftLLMCalls)
	assign([]string{"workflow_hard_llm_calls", "budget.hard_llm_calls", "hard_llm_calls"}, &budget.HardLLMCalls)
	assign([]string{"workflow_soft_continuations", "budget.soft_continuations", "soft_continuations"}, &budget.SoftContinuations)
	assign([]string{"workflow_hard_continuations", "budget.hard_continuations", "hard_continuations"}, &budget.HardContinuations)
	return budget
}

func workflowGraphBudgetEmpty(budget workflowGraphBudget) bool {
	return budget.SoftPromptTokens == 0 &&
		budget.HardPromptTokens == 0 &&
		budget.SoftOutputTokens == 0 &&
		budget.HardOutputTokens == 0 &&
		budget.SoftTotalTokens == 0 &&
		budget.HardTotalTokens == 0 &&
		budget.SoftLLMCalls == 0 &&
		budget.HardLLMCalls == 0 &&
		budget.SoftContinuations == 0 &&
		budget.HardContinuations == 0
}

func workflowGraphBudgetDiagnosticForUsage(budget workflowGraphBudget, usage workflowGraphBudgetUsage, preferredMetric string) workflowGraphBudgetDiagnostic {
	if workflowGraphBudgetEmpty(budget) {
		return workflowGraphBudgetDiagnostic{}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
	}
	type candidate struct {
		scope  string
		metric string
		used   int
		soft   int
		hard   int
	}
	candidates := []candidate{
		{scope: "prompt", metric: "prompt_tokens", used: usage.PromptTokens, soft: budget.SoftPromptTokens, hard: budget.HardPromptTokens},
		{scope: "output", metric: "output_tokens", used: usage.OutputTokens, soft: budget.SoftOutputTokens, hard: budget.HardOutputTokens},
		{scope: "total", metric: "total_tokens", used: usage.TotalTokens, soft: budget.SoftTotalTokens, hard: budget.HardTotalTokens},
		{scope: "llm_call", metric: "llm_calls", used: usage.LLMCalls, soft: budget.SoftLLMCalls, hard: budget.HardLLMCalls},
		{scope: "continuation", metric: "continuations", used: usage.Continuations, soft: budget.SoftContinuations, hard: budget.HardContinuations},
	}
	metric := strings.TrimSpace(preferredMetric)
	if metric != "" {
		for _, item := range candidates {
			if item.metric == metric || item.scope == metric {
				return workflowGraphBudgetDiagnosticFromCandidate(item.scope, item.metric, item.used, item.soft, item.hard)
			}
		}
	}
	best := workflowGraphBudgetDiagnostic{}
	bestScore := -1.0
	for _, item := range candidates {
		if item.soft <= 0 && item.hard <= 0 {
			continue
		}
		limit := item.hard
		if limit <= 0 {
			limit = item.soft
		}
		score := 0.0
		if limit > 0 {
			score = float64(item.used) / float64(limit)
		}
		diagnostic := workflowGraphBudgetDiagnosticFromCandidate(item.scope, item.metric, item.used, item.soft, item.hard)
		if score > bestScore || !best.HasConfigured {
			best = diagnostic
			bestScore = score
		}
	}
	return best
}

func workflowGraphBudgetDiagnosticFromCandidate(scope, metric string, used, soft, hard int) workflowGraphBudgetDiagnostic {
	remaining := 0
	if hard > 0 {
		remaining = maxInt(hard-used, 0)
	} else if soft > 0 {
		remaining = maxInt(soft-used, 0)
	}
	return workflowGraphBudgetDiagnostic{
		Scope:         scope,
		Metric:        metric,
		Used:          used,
		SoftLimit:     soft,
		HardLimit:     hard,
		Remaining:     remaining,
		HasConfigured: soft > 0 || hard > 0,
	}
}

func workflowGraphBudgetDiagnosticFromHardHit(hit workflowGraphBudgetLimitHit) workflowGraphBudgetDiagnostic {
	if !hit.Hit {
		return workflowGraphBudgetDiagnostic{}
	}
	return workflowGraphBudgetDiagnostic{
		Scope:         hit.Scope,
		Metric:        hit.Metric,
		Used:          hit.Used,
		SoftLimit:     hit.SoftLimit,
		HardLimit:     hit.Limit,
		Remaining:     hit.Remaining,
		HasConfigured: hit.SoftLimit > 0 || hit.Limit > 0,
	}
}

func (w *WorkflowRunner) workflowGraphBudgetDiagnosticForName(name string, usage workflowGraphBudgetUsage, preferredMetric string) workflowGraphBudgetDiagnostic {
	if w == nil || strings.TrimSpace(name) == "" {
		return workflowGraphBudgetDiagnostic{}
	}
	graph, err := w.loadWorkflowGraph(name)
	if err != nil {
		return workflowGraphBudgetDiagnostic{}
	}
	return workflowGraphBudgetDiagnosticForUsage(workflowGraphEffectiveBudget(graph), usage, preferredMetric)
}

func applyWorkflowGraphBudgetDiagnosticToResult(result *WorkflowResult, diagnostic workflowGraphBudgetDiagnostic) {
	if result == nil || !diagnostic.HasConfigured {
		return
	}
	result.BudgetMetric = diagnostic.Metric
	result.BudgetUsed = diagnostic.Used
	result.BudgetSoftLimit = diagnostic.SoftLimit
	result.BudgetHardLimit = diagnostic.HardLimit
	result.BudgetRemaining = diagnostic.Remaining
	if strings.TrimSpace(result.BudgetScope) == "" {
		result.BudgetScope = diagnostic.Scope
	}
}

func applyWorkflowGraphBudgetDiagnosticToStreamEvent(event *schema.StreamEvent, diagnostic workflowGraphBudgetDiagnostic) {
	if event == nil || !diagnostic.HasConfigured {
		return
	}
	event.BudgetMetric = diagnostic.Metric
	event.BudgetUsed = diagnostic.Used
	event.BudgetSoftLimit = diagnostic.SoftLimit
	event.BudgetHardLimit = diagnostic.HardLimit
	event.BudgetRemaining = diagnostic.Remaining
	if strings.TrimSpace(event.BudgetScope) == "" {
		event.BudgetScope = diagnostic.Scope
	}
}

func (w *WorkflowRunner) applyWorkflowGraphStageBudgetDelta(stageResult *WorkflowStageResult, before workflowGraphBudgetUsage) {
	if w == nil || stageResult == nil {
		return
	}
	usage := workflowGraphBudgetUsageDelta(before, w.workflowGraphBudgetUsage())
	if workflowGraphBudgetUsageIsEmpty(usage) {
		return
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
	}
	stageResult.BudgetPromptTokens = maxInt(stageResult.BudgetPromptTokens, usage.PromptTokens)
	stageResult.BudgetEstimatedPromptTokens = maxInt(stageResult.BudgetEstimatedPromptTokens, usage.EstimatedPromptTokens)
	stageResult.BudgetNetPromptTokens = maxInt(stageResult.BudgetNetPromptTokens, usage.NetPromptTokens)
	stageResult.BudgetGrossPromptTokens = maxInt(stageResult.BudgetGrossPromptTokens, usage.GrossPromptTokens)
	stageResult.BudgetSavedTokens = maxInt(stageResult.BudgetSavedTokens, usage.SavedTokens)
	stageResult.BudgetMemorySavedTokens = maxInt(stageResult.BudgetMemorySavedTokens, usage.MemorySavedTokens)
	stageResult.BudgetHistorySavedTokens = maxInt(stageResult.BudgetHistorySavedTokens, usage.HistorySavedTokens)
	stageResult.BudgetArtifactSavedTokens = maxInt(stageResult.BudgetArtifactSavedTokens, usage.ArtifactSavedTokens)
	stageResult.BudgetSkillSavedTokens = maxInt(stageResult.BudgetSkillSavedTokens, usage.SkillSavedTokens)
	stageResult.BudgetToolSchemaSavedTokens = maxInt(stageResult.BudgetToolSchemaSavedTokens, usage.ToolSchemaSavedTokens)
	stageResult.BudgetReportedPromptTokens = maxInt(stageResult.BudgetReportedPromptTokens, usage.ReportedPromptTokens)
	stageResult.BudgetOutputTokens = maxInt(stageResult.BudgetOutputTokens, usage.OutputTokens)
	stageResult.BudgetCachedTokens = maxInt(stageResult.BudgetCachedTokens, usage.CachedTokens)
	stageResult.BudgetTotalTokens = maxInt(stageResult.BudgetTotalTokens, usage.TotalTokens)
	stageResult.BudgetLLMCalls = maxInt(stageResult.BudgetLLMCalls, usage.LLMCalls)
	stageResult.BudgetContinuations = maxInt(stageResult.BudgetContinuations, usage.Continuations)
	stageResult.BudgetEstimatedInputCost = maxFloat64(stageResult.BudgetEstimatedInputCost, usage.EstimatedInputCost)
	stageResult.BudgetEstimatedOutputCost = maxFloat64(stageResult.BudgetEstimatedOutputCost, usage.EstimatedOutputCost)
	stageResult.BudgetEstimatedTotalCost = maxFloat64(stageResult.BudgetEstimatedTotalCost, usage.EstimatedTotalCost)
	if strings.TrimSpace(stageResult.BudgetCostCurrency) == "" {
		stageResult.BudgetCostCurrency = usage.CostCurrency
	}
	if strings.TrimSpace(stageResult.BudgetPricingSource) == "" {
		stageResult.BudgetPricingSource = usage.PricingSource
	}
}

func workflowGraphBudgetUsageDelta(before, after workflowGraphBudgetUsage) workflowGraphBudgetUsage {
	deltaInt := func(next, previous int) int {
		if next <= previous {
			return 0
		}
		return next - previous
	}
	deltaFloat := func(next, previous float64) float64 {
		if next <= previous {
			return 0
		}
		return roundEstimatedCost(next - previous)
	}
	usage := workflowGraphBudgetUsage{
		PromptTokens:          deltaInt(after.PromptTokens, before.PromptTokens),
		EstimatedPromptTokens: deltaInt(after.EstimatedPromptTokens, before.EstimatedPromptTokens),
		NetPromptTokens:       deltaInt(after.NetPromptTokens, before.NetPromptTokens),
		GrossPromptTokens:     deltaInt(after.GrossPromptTokens, before.GrossPromptTokens),
		SavedTokens:           deltaInt(after.SavedTokens, before.SavedTokens),
		MemorySavedTokens:     deltaInt(after.MemorySavedTokens, before.MemorySavedTokens),
		HistorySavedTokens:    deltaInt(after.HistorySavedTokens, before.HistorySavedTokens),
		ArtifactSavedTokens:   deltaInt(after.ArtifactSavedTokens, before.ArtifactSavedTokens),
		SkillSavedTokens:      deltaInt(after.SkillSavedTokens, before.SkillSavedTokens),
		ToolSchemaSavedTokens: deltaInt(after.ToolSchemaSavedTokens, before.ToolSchemaSavedTokens),
		ReportedPromptTokens:  deltaInt(after.ReportedPromptTokens, before.ReportedPromptTokens),
		OutputTokens:          deltaInt(after.OutputTokens, before.OutputTokens),
		CachedTokens:          deltaInt(after.CachedTokens, before.CachedTokens),
		TotalTokens:           deltaInt(after.TotalTokens, before.TotalTokens),
		LLMCalls:              deltaInt(after.LLMCalls, before.LLMCalls),
		Continuations:         deltaInt(after.Continuations, before.Continuations),
		EstimatedInputCost:    deltaFloat(after.EstimatedInputCost, before.EstimatedInputCost),
		EstimatedOutputCost:   deltaFloat(after.EstimatedOutputCost, before.EstimatedOutputCost),
		EstimatedTotalCost:    deltaFloat(after.EstimatedTotalCost, before.EstimatedTotalCost),
		CostCurrency:          after.CostCurrency,
		PricingSource:         after.PricingSource,
	}
	if usage.NetPromptTokens == 0 {
		usage.NetPromptTokens = usage.EstimatedPromptTokens
	}
	if usage.SavedTokens == 0 {
		usage.SavedTokens = usage.MemorySavedTokens + usage.HistorySavedTokens + usage.ArtifactSavedTokens + usage.SkillSavedTokens + usage.ToolSchemaSavedTokens
	}
	if usage.GrossPromptTokens == 0 && (usage.NetPromptTokens > 0 || usage.SavedTokens > 0) {
		usage.GrossPromptTokens = usage.NetPromptTokens + usage.SavedTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
	}
	if usage.EstimatedTotalCost == 0 && (usage.EstimatedInputCost > 0 || usage.EstimatedOutputCost > 0) {
		usage.EstimatedTotalCost = roundEstimatedCost(usage.EstimatedInputCost + usage.EstimatedOutputCost)
	}
	return usage
}

func workflowGraphBudgetUsageIsEmpty(usage workflowGraphBudgetUsage) bool {
	return usage.PromptTokens == 0 &&
		usage.EstimatedPromptTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.CachedTokens == 0 &&
		usage.TotalTokens == 0 &&
		usage.LLMCalls == 0 &&
		usage.Continuations == 0 &&
		usage.EstimatedInputCost == 0 &&
		usage.EstimatedOutputCost == 0 &&
		usage.EstimatedTotalCost == 0
}

func (w *WorkflowRunner) applyWorkflowGraphBudgetSummary(graph workflowGraph, result *WorkflowResult) {
	if w == nil || result == nil {
		return
	}
	usage := w.workflowGraphBudgetUsage()
	if usage.PromptTokens == 0 && usage.OutputTokens == 0 && usage.LLMCalls == 0 && usage.Continuations == 0 && workflowGraphBudgetEmpty(workflowGraphEffectiveBudget(graph)) {
		return
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
	}
	result.BudgetPromptTokens = maxInt(result.BudgetPromptTokens, usage.PromptTokens)
	result.BudgetEstimatedPromptTokens = maxInt(result.BudgetEstimatedPromptTokens, usage.EstimatedPromptTokens)
	result.BudgetNetPromptTokens = maxInt(result.BudgetNetPromptTokens, usage.NetPromptTokens)
	result.BudgetGrossPromptTokens = maxInt(result.BudgetGrossPromptTokens, usage.GrossPromptTokens)
	result.BudgetSavedTokens = maxInt(result.BudgetSavedTokens, usage.SavedTokens)
	result.BudgetMemorySavedTokens = maxInt(result.BudgetMemorySavedTokens, usage.MemorySavedTokens)
	result.BudgetHistorySavedTokens = maxInt(result.BudgetHistorySavedTokens, usage.HistorySavedTokens)
	result.BudgetArtifactSavedTokens = maxInt(result.BudgetArtifactSavedTokens, usage.ArtifactSavedTokens)
	result.BudgetSkillSavedTokens = maxInt(result.BudgetSkillSavedTokens, usage.SkillSavedTokens)
	result.BudgetToolSchemaSavedTokens = maxInt(result.BudgetToolSchemaSavedTokens, usage.ToolSchemaSavedTokens)
	result.BudgetReportedPromptTokens = maxInt(result.BudgetReportedPromptTokens, usage.ReportedPromptTokens)
	result.BudgetOutputTokens = maxInt(result.BudgetOutputTokens, usage.OutputTokens)
	result.BudgetCachedTokens = maxInt(result.BudgetCachedTokens, usage.CachedTokens)
	result.BudgetTotalTokens = maxInt(result.BudgetTotalTokens, usage.TotalTokens)
	result.BudgetLLMCalls = maxInt(result.BudgetLLMCalls, usage.LLMCalls)
	result.BudgetContinuations = maxInt(result.BudgetContinuations, usage.Continuations)
	result.BudgetEstimatedInputCost = maxFloat64(result.BudgetEstimatedInputCost, usage.EstimatedInputCost)
	result.BudgetEstimatedOutputCost = maxFloat64(result.BudgetEstimatedOutputCost, usage.EstimatedOutputCost)
	result.BudgetEstimatedTotalCost = maxFloat64(result.BudgetEstimatedTotalCost, usage.EstimatedTotalCost)
	if strings.TrimSpace(result.BudgetCostCurrency) == "" {
		result.BudgetCostCurrency = usage.CostCurrency
	}
	if strings.TrimSpace(result.BudgetPricingSource) == "" {
		result.BudgetPricingSource = usage.PricingSource
	}
	diagnostic := workflowGraphBudgetDiagnosticForUsage(workflowGraphEffectiveBudget(graph), usage, "")
	applyWorkflowGraphBudgetDiagnosticToResult(result, diagnostic)
	if strings.TrimSpace(result.BudgetReason) == "" {
		result.BudgetReason = w.workflowGraphLatestBudgetReason()
	}
}

func (w *WorkflowRunner) workflowGraphLatestBudgetReason() string {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return ""
	}
	runID := strings.TrimSpace(w.runID)
	if runID == "" {
		runID = w.runtime.currentWorkflowRunID()
	}
	if runID == "" {
		return ""
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return ""
	}
	for i := len(run.Events) - 1; i >= 0; i-- {
		event := run.Events[i]
		reason := strings.TrimSpace(event.BudgetReason)
		if reason == "" {
			reason = strings.TrimSpace(event.Reason)
		}
		switch strings.ToLower(reason) {
		case "budget_soft_limit_hit", "budget_hard_limit_hit", "budget_approved":
			return reason
		}
	}
	return ""
}

func (w *WorkflowRunner) workflowGraphBudgetUsage() workflowGraphBudgetUsage {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return workflowGraphBudgetUsage{}
	}
	snapshot := w.runtime.session.Snapshot()
	workflowSnapshotRunID := strings.TrimSpace(snapshot.Workflow.RunID)
	runID := strings.TrimSpace(w.runID)
	if runID == "" {
		runID = workflowSnapshotRunID
	}
	if runID == "" {
		return workflowGraphBudgetUsageFromSession(snapshot)
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return workflowGraphBudgetUsageFromSession(snapshot)
	}
	if strings.TrimSpace(runID) != workflowSnapshotRunID {
		usage := workflowGraphBudgetUsage{
			PromptTokens:          run.BudgetPromptTokens,
			EstimatedPromptTokens: run.BudgetEstimatedPromptTokens,
			NetPromptTokens:       run.BudgetNetPromptTokens,
			GrossPromptTokens:     run.BudgetGrossPromptTokens,
			SavedTokens:           run.BudgetSavedTokens,
			MemorySavedTokens:     run.BudgetMemorySavedTokens,
			HistorySavedTokens:    run.BudgetHistorySavedTokens,
			ArtifactSavedTokens:   run.BudgetArtifactSavedTokens,
			SkillSavedTokens:      run.BudgetSkillSavedTokens,
			ToolSchemaSavedTokens: run.BudgetToolSchemaSavedTokens,
			ReportedPromptTokens:  run.BudgetReportedPromptTokens,
			OutputTokens:          run.BudgetOutputTokens,
			CachedTokens:          run.BudgetCachedTokens,
			TotalTokens:           run.BudgetTotalTokens,
			LLMCalls:              run.BudgetLLMCalls,
			Continuations:         run.BudgetContinuations,
			EstimatedInputCost:    run.BudgetEstimatedInputCost,
			EstimatedOutputCost:   run.BudgetEstimatedOutputCost,
			EstimatedTotalCost:    run.BudgetEstimatedTotalCost,
			CostCurrency:          run.BudgetCostCurrency,
			PricingSource:         run.BudgetPricingSource,
		}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
		}
		return usage
	}
	usage := workflowGraphBudgetUsage{
		PromptTokens:          run.BudgetPromptTokens,
		EstimatedPromptTokens: run.BudgetEstimatedPromptTokens,
		NetPromptTokens:       run.BudgetNetPromptTokens,
		GrossPromptTokens:     run.BudgetGrossPromptTokens,
		SavedTokens:           run.BudgetSavedTokens,
		MemorySavedTokens:     run.BudgetMemorySavedTokens,
		HistorySavedTokens:    run.BudgetHistorySavedTokens,
		ArtifactSavedTokens:   run.BudgetArtifactSavedTokens,
		SkillSavedTokens:      run.BudgetSkillSavedTokens,
		ToolSchemaSavedTokens: run.BudgetToolSchemaSavedTokens,
		ReportedPromptTokens:  run.BudgetReportedPromptTokens,
		OutputTokens:          run.BudgetOutputTokens,
		CachedTokens:          run.BudgetCachedTokens,
		TotalTokens:           run.BudgetTotalTokens,
		LLMCalls:              run.BudgetLLMCalls,
		Continuations:         run.BudgetContinuations,
		EstimatedInputCost:    run.BudgetEstimatedInputCost,
		EstimatedOutputCost:   run.BudgetEstimatedOutputCost,
		EstimatedTotalCost:    run.BudgetEstimatedTotalCost,
		CostCurrency:          run.BudgetCostCurrency,
		PricingSource:         run.BudgetPricingSource,
	}
	fallback := workflowGraphBudgetUsageFromSession(snapshot)
	if fallback.EstimatedPromptTokens > usage.EstimatedPromptTokens {
		usage.EstimatedPromptTokens = fallback.EstimatedPromptTokens
	}
	if fallback.NetPromptTokens > usage.NetPromptTokens {
		usage.NetPromptTokens = fallback.NetPromptTokens
	}
	if fallback.GrossPromptTokens > usage.GrossPromptTokens {
		usage.GrossPromptTokens = fallback.GrossPromptTokens
	}
	if fallback.SavedTokens > usage.SavedTokens {
		usage.SavedTokens = fallback.SavedTokens
	}
	if fallback.MemorySavedTokens > usage.MemorySavedTokens {
		usage.MemorySavedTokens = fallback.MemorySavedTokens
	}
	if fallback.HistorySavedTokens > usage.HistorySavedTokens {
		usage.HistorySavedTokens = fallback.HistorySavedTokens
	}
	if fallback.ArtifactSavedTokens > usage.ArtifactSavedTokens {
		usage.ArtifactSavedTokens = fallback.ArtifactSavedTokens
	}
	if fallback.SkillSavedTokens > usage.SkillSavedTokens {
		usage.SkillSavedTokens = fallback.SkillSavedTokens
	}
	if fallback.ToolSchemaSavedTokens > usage.ToolSchemaSavedTokens {
		usage.ToolSchemaSavedTokens = fallback.ToolSchemaSavedTokens
	}
	if fallback.ReportedPromptTokens > usage.ReportedPromptTokens {
		usage.ReportedPromptTokens = fallback.ReportedPromptTokens
	}
	if fallback.OutputTokens > usage.OutputTokens {
		usage.OutputTokens = fallback.OutputTokens
	}
	if fallback.CachedTokens > usage.CachedTokens {
		usage.CachedTokens = fallback.CachedTokens
	}
	if fallback.LLMCalls > usage.LLMCalls {
		usage.LLMCalls = fallback.LLMCalls
	}
	if fallback.Continuations > usage.Continuations {
		usage.Continuations = fallback.Continuations
	}
	if fallback.EstimatedInputCost > usage.EstimatedInputCost {
		usage.EstimatedInputCost = fallback.EstimatedInputCost
	}
	if fallback.EstimatedOutputCost > usage.EstimatedOutputCost {
		usage.EstimatedOutputCost = fallback.EstimatedOutputCost
	}
	if fallback.EstimatedTotalCost > usage.EstimatedTotalCost {
		usage.EstimatedTotalCost = fallback.EstimatedTotalCost
	}
	if strings.TrimSpace(usage.CostCurrency) == "" {
		usage.CostCurrency = fallback.CostCurrency
	}
	if strings.TrimSpace(usage.PricingSource) == "" {
		usage.PricingSource = fallback.PricingSource
	}
	if usage.NetPromptTokens == 0 {
		usage.NetPromptTokens = usage.EstimatedPromptTokens
	}
	if usage.SavedTokens == 0 {
		usage.SavedTokens = usage.MemorySavedTokens + usage.HistorySavedTokens + usage.ArtifactSavedTokens + usage.SkillSavedTokens + usage.ToolSchemaSavedTokens
	}
	if usage.GrossPromptTokens == 0 && (usage.NetPromptTokens > 0 || usage.SavedTokens > 0) {
		usage.GrossPromptTokens = usage.NetPromptTokens + usage.SavedTokens
	}
	if usage.ReportedPromptTokens > usage.EstimatedPromptTokens {
		usage.PromptTokens = usage.ReportedPromptTokens
	} else {
		usage.PromptTokens = usage.EstimatedPromptTokens
	}
	usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
	if usage.EstimatedTotalCost == 0 && (usage.EstimatedInputCost > 0 || usage.EstimatedOutputCost > 0) {
		usage.EstimatedTotalCost = roundEstimatedCost(usage.EstimatedInputCost + usage.EstimatedOutputCost)
	}
	return usage
}

func workflowGraphBudgetUsageFromSession(snapshot session.Snapshot) workflowGraphBudgetUsage {
	var usage workflowGraphBudgetUsage
	promptCostFromBudgets := false
	for _, budget := range snapshot.PromptBudgets {
		usage.EstimatedPromptTokens += budget.EstimatedPromptTokens
		usage.NetPromptTokens += budget.EstimatedPromptTokens
		usage.EstimatedInputCost += budget.EstimatedInputCost
		if budget.EstimatedInputCost > 0 {
			promptCostFromBudgets = true
		}
		if strings.TrimSpace(usage.CostCurrency) == "" {
			usage.CostCurrency = budget.CostCurrency
		}
		if strings.TrimSpace(usage.PricingSource) == "" {
			usage.PricingSource = budget.PricingSource
		}
		memorySaved := budget.MemoryEstimatedSavedTokens
		historySaved := budget.HistoryEstimatedSavedTokens
		artifactSaved := budget.ArtifactOmittedTokens
		skillSaved := budget.SkillOmittedTokens
		toolSchemaSaved := budget.ToolSchemaEstimatedSavedTokens
		usage.MemorySavedTokens += memorySaved
		usage.HistorySavedTokens += historySaved
		usage.ArtifactSavedTokens += artifactSaved
		usage.SkillSavedTokens += skillSaved
		usage.ToolSchemaSavedTokens += toolSchemaSaved
		usage.SavedTokens += memorySaved + historySaved + artifactSaved + skillSaved + toolSchemaSaved
		usage.LLMCalls++
	}
	for _, sample := range snapshot.TokenUsages {
		usage.ReportedPromptTokens += sample.PromptTokens
		usage.OutputTokens += sample.OutputTokens
		usage.CachedTokens += sample.CachedTokens
		if !promptCostFromBudgets {
			usage.EstimatedInputCost += sample.EstimatedInputCost
		}
		usage.EstimatedOutputCost += sample.EstimatedOutputCost
		if strings.TrimSpace(usage.CostCurrency) == "" {
			usage.CostCurrency = sample.CostCurrency
		}
		if strings.TrimSpace(usage.PricingSource) == "" {
			usage.PricingSource = sample.PricingSource
		}
	}
	if usage.ReportedPromptTokens > usage.EstimatedPromptTokens {
		usage.PromptTokens = usage.ReportedPromptTokens
	} else {
		usage.PromptTokens = usage.EstimatedPromptTokens
	}
	if usage.GrossPromptTokens == 0 && (usage.NetPromptTokens > 0 || usage.SavedTokens > 0) {
		usage.GrossPromptTokens = usage.NetPromptTokens + usage.SavedTokens
	}
	usage.TotalTokens = usage.PromptTokens + usage.OutputTokens
	usage.EstimatedInputCost = roundEstimatedCost(usage.EstimatedInputCost)
	usage.EstimatedOutputCost = roundEstimatedCost(usage.EstimatedOutputCost)
	usage.EstimatedTotalCost = roundEstimatedCost(usage.EstimatedInputCost + usage.EstimatedOutputCost)
	if usage.LLMCalls == 0 && len(snapshot.TokenUsages) > 0 {
		usage.LLMCalls = len(snapshot.TokenUsages)
	}
	return usage
}

func (w *WorkflowRunner) pauseWorkflowGraphForHardBudget(graph workflowGraph, stage workflowGraphStage, request string, completed []WorkflowStageResult, hit workflowGraphBudgetLimitHit, handler func(event schema.StreamEvent) error) WorkflowResult {
	summary := summarizeWorkflow(completed)
	reason := hit.Reason
	if strings.TrimSpace(reason) == "" {
		reason = fmt.Sprintf("workflow hard budget exceeded before stage %s", fallbackWorkflowGraphValue(stage.Name, "next"))
	}
	w.runtime.DisableWorkflowAutoApproval(graph.Name)
	w.persistWorkflowState(graph.Name, "awaiting_budget_approval", WorkflowStage(stage.Name), request, summary, reason)
	diagnostic := workflowGraphBudgetDiagnosticFromHardHit(hit)
	event := schema.StreamEvent{
		Type:                        schema.StreamEventStatus,
		TaskStage:                   stage.Name,
		Content:                     reason,
		AgentID:                     fallbackWorkflowGraphValue(stage.Agent, "workflow"),
		Mode:                        "workflow",
		NeedsAction:                 true,
		WorkflowName:                graph.Name,
		WorkflowStatus:              "awaiting_budget_approval",
		NextStage:                   stage.Name,
		Reason:                      "budget_hard_limit_hit",
		Severity:                    "warning",
		BudgetScope:                 hit.Scope,
		BudgetReason:                "budget_hard_limit_hit",
		BudgetMetric:                diagnostic.Metric,
		BudgetUsed:                  diagnostic.Used,
		BudgetSoftLimit:             diagnostic.SoftLimit,
		BudgetHardLimit:             diagnostic.HardLimit,
		BudgetRemaining:             diagnostic.Remaining,
		BudgetPromptTokens:          hit.Usage.PromptTokens,
		BudgetEstimatedPromptTokens: hit.Usage.EstimatedPromptTokens,
		BudgetNetPromptTokens:       hit.Usage.NetPromptTokens,
		BudgetGrossPromptTokens:     hit.Usage.GrossPromptTokens,
		BudgetSavedTokens:           hit.Usage.SavedTokens,
		BudgetMemorySavedTokens:     hit.Usage.MemorySavedTokens,
		BudgetHistorySavedTokens:    hit.Usage.HistorySavedTokens,
		BudgetArtifactSavedTokens:   hit.Usage.ArtifactSavedTokens,
		BudgetSkillSavedTokens:      hit.Usage.SkillSavedTokens,
		BudgetToolSchemaSavedTokens: hit.Usage.ToolSchemaSavedTokens,
		BudgetReportedPromptTokens:  hit.Usage.ReportedPromptTokens,
		BudgetOutputTokens:          hit.Usage.OutputTokens,
		BudgetCachedTokens:          hit.Usage.CachedTokens,
		BudgetTotalTokens:           hit.Usage.TotalTokens,
		BudgetLLMCalls:              hit.Usage.LLMCalls,
		BudgetContinuations:         hit.Usage.Continuations,
		BudgetEstimatedInputCost:    hit.Usage.EstimatedInputCost,
		BudgetEstimatedOutputCost:   hit.Usage.EstimatedOutputCost,
		BudgetEstimatedTotalCost:    hit.Usage.EstimatedTotalCost,
		BudgetCostCurrency:          hit.Usage.CostCurrency,
		BudgetPricingSource:         hit.Usage.PricingSource,
		SourceRef:                   hit.SourceRef,
		PromptTokens:                hit.Usage.ReportedPromptTokens,
		OutputTokens:                hit.Usage.OutputTokens,
		CachedTokens:                hit.Usage.CachedTokens,
	}
	if handler != nil {
		_ = handler(event)
	} else if w != nil && w.runtime != nil && w.runtime.session != nil {
		runID := strings.TrimSpace(w.runID)
		if runID == "" {
			runID = w.runtime.currentWorkflowRunID()
		}
		if runID != "" {
			w.runtime.session.AppendWorkflowRunEvent(runID, workflowRunEventSnapshot(event))
		}
	}
	return WorkflowResult{
		Name:                        graph.Name,
		Status:                      "awaiting_budget_approval",
		PendingApproval:             true,
		ApprovalPrompt:              reason,
		CompletedStages:             append([]WorkflowStageResult(nil), completed...),
		NextStage:                   WorkflowStage(stage.Name),
		FinalSummary:                summary,
		BudgetScope:                 hit.Scope,
		BudgetReason:                "budget_hard_limit_hit",
		BudgetMetric:                diagnostic.Metric,
		BudgetUsed:                  diagnostic.Used,
		BudgetSoftLimit:             diagnostic.SoftLimit,
		BudgetHardLimit:             diagnostic.HardLimit,
		BudgetRemaining:             diagnostic.Remaining,
		BudgetPromptTokens:          hit.Usage.PromptTokens,
		BudgetEstimatedPromptTokens: hit.Usage.EstimatedPromptTokens,
		BudgetNetPromptTokens:       hit.Usage.NetPromptTokens,
		BudgetGrossPromptTokens:     hit.Usage.GrossPromptTokens,
		BudgetSavedTokens:           hit.Usage.SavedTokens,
		BudgetMemorySavedTokens:     hit.Usage.MemorySavedTokens,
		BudgetHistorySavedTokens:    hit.Usage.HistorySavedTokens,
		BudgetArtifactSavedTokens:   hit.Usage.ArtifactSavedTokens,
		BudgetSkillSavedTokens:      hit.Usage.SkillSavedTokens,
		BudgetToolSchemaSavedTokens: hit.Usage.ToolSchemaSavedTokens,
		BudgetReportedPromptTokens:  hit.Usage.ReportedPromptTokens,
		BudgetOutputTokens:          hit.Usage.OutputTokens,
		BudgetCachedTokens:          hit.Usage.CachedTokens,
		BudgetTotalTokens:           hit.Usage.TotalTokens,
		BudgetLLMCalls:              hit.Usage.LLMCalls,
		BudgetContinuations:         hit.Usage.Continuations,
		BudgetEstimatedInputCost:    hit.Usage.EstimatedInputCost,
		BudgetEstimatedOutputCost:   hit.Usage.EstimatedOutputCost,
		BudgetEstimatedTotalCost:    hit.Usage.EstimatedTotalCost,
		BudgetCostCurrency:          hit.Usage.CostCurrency,
		BudgetPricingSource:         hit.Usage.PricingSource,
	}
}

func workflowGraphQualityGateFailureSourceRef(stageResult WorkflowStageResult) string {
	if ref := strings.TrimSpace(stageResult.Metadata["source_ref"]); ref != "" {
		return ref
	}
	if failures := strings.TrimSpace(stageResult.Metadata["failures"]); failures != "" {
		return failures
	}
	if failed := strings.TrimSpace(stageResult.Output.Variables["acceptance_failed"]); failed != "" && failed != "0" {
		return "acceptance_failed=" + failed
	}
	if failed := strings.TrimSpace(stageResult.Output.Variables["verification_failed"]); failed != "" && failed != "0" {
		return "verification_failed=" + failed
	}
	return ""
}

func workflowGraphApplyQualityGateFailureMetadata(stageResult *WorkflowStageResult, decision workflowGraphControlDecision) {
	if stageResult == nil || decision.Kind != "quality_gate" || decision.Passed {
		return
	}
	if stageResult.Metadata == nil {
		stageResult.Metadata = make(map[string]string)
	}
	stageResult.Metadata["quality_failed"] = "true"
	stageResult.Metadata["contract_check"] = "quality_gate"
	stageResult.Metadata["severity"] = "error"
	if sourceRef := workflowGraphQualityGateFailureSourceRef(*stageResult); sourceRef != "" {
		stageResult.Metadata["source_ref"] = sourceRef
	}
	if !decision.Halt && stageResult.Status == "completed" {
		stageResult.Status = "warning"
	}
}

func (w *WorkflowRunner) applyWorkflowGraphFinalQualityHandoff(graph workflowGraph, stage workflowGraphStage, completed []WorkflowStageResult, stageResult *WorkflowStageResult, handler func(event schema.StreamEvent) error) {
	if stageResult == nil || !workflowGraphStageLooksFinal(stage) {
		return
	}
	handoff := buildWorkflowGraphFinalQualityHandoff(completed)
	if !handoff.HasIssues() {
		return
	}
	workflowGraphApplyFinalQualityHandoffMetadata(stageResult, handoff)
	if handler == nil {
		return
	}
	_ = handler(schema.StreamEvent{
		Type:           schema.StreamEventStatus,
		TaskStage:      string(stageResult.Stage),
		Content:        handoff.Reason,
		AgentID:        stageResult.Agent,
		Mode:           stageResult.Result.Mode,
		NeedsAction:    handoff.FailedCount > 0,
		WorkflowName:   graph.Name,
		WorkflowStatus: "completed",
		NextStage:      string(stageResult.Stage),
		Reason:         "final_quality_handoff",
		Severity:       handoff.Severity,
		ContractCheck:  "final_quality_handoff",
		SourceRef:      handoff.SourceRef,
	})
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

// CanContinueOutput reports whether a paused graph workflow run has enough
// persisted state to rerun the incomplete stage and continue downstream.
func (w *WorkflowRunner) CanContinueOutput(run session.WorkflowRunSnapshot) error {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return fmt.Errorf("workflow runtime not configured")
	}
	status := strings.ToLower(strings.TrimSpace(run.Status))
	if status != "paused_need_more_budget" {
		return fmt.Errorf("workflow run %s is not paused for output continuation", run.ID)
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("continue output is currently supported for graph workflows only")
		}
		return err
	}
	completed := workflowStageResultsFromRunSnapshots(run.CompletedStages)
	incomplete, ok := workflowGraphFindIncompleteStageResult(completed, run.NextStage)
	if !ok {
		return fmt.Errorf("workflow run %s has no persisted incomplete result for stage %q", run.ID, run.NextStage)
	}
	index := workflowGraphContinueStageIndex(graph, run.NextStage, incomplete)
	if index < 0 {
		return fmt.Errorf("workflow run %s references unknown next stage %q", run.ID, run.NextStage)
	}
	stage := graph.Stages[index]
	if !workflowGraphContinuableExecutableStage(stage) {
		return fmt.Errorf("workflow stage %s cannot be continued because it is not an executable graph stage", stage.Name)
	}
	return nil
}

// CanApproveBudget reports whether a graph workflow can continue after a hard
// workflow budget pause without requiring a partial model output.
func (w *WorkflowRunner) CanApproveBudget(run session.WorkflowRunSnapshot) error {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return fmt.Errorf("workflow runtime not configured")
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "awaiting_budget_approval") {
		return fmt.Errorf("workflow run %s is not awaiting budget approval", run.ID)
	}
	if strings.TrimSpace(run.Name) == "" || strings.TrimSpace(run.NextStage) == "" {
		return fmt.Errorf("workflow run %s is missing workflow name or next stage", run.ID)
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("budget approval resume is currently supported for graph workflows only")
		}
		return err
	}
	index := workflowGraphStageIndexByName(graph, run.NextStage)
	if index < 0 {
		return fmt.Errorf("workflow run %s references unknown next stage %q", run.ID, run.NextStage)
	}
	stage := graph.Stages[index]
	if isVisualOnlyWorkflowNode(stage) {
		return fmt.Errorf("workflow stage %s cannot consume budget approval because it is visual only", stage.Name)
	}
	return nil
}

// CanEscalateModel reports whether a blocked graph workflow can rerun the
// blocked executable stage with a configured escalation model route.
func (w *WorkflowRunner) CanEscalateModel(run session.WorkflowRunSnapshot) error {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return fmt.Errorf("workflow runtime not configured")
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "blocked") {
		return fmt.Errorf("workflow run %s is not blocked", run.ID)
	}
	if strings.TrimSpace(run.Name) == "" || strings.TrimSpace(run.NextStage) == "" {
		return fmt.Errorf("workflow run %s is missing workflow name or blocked stage", run.ID)
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("model escalation is currently supported for graph workflows only")
		}
		return err
	}
	index := workflowGraphStageIndexByName(graph, run.NextStage)
	if index < 0 {
		return fmt.Errorf("workflow run %s references unknown blocked stage %q", run.ID, run.NextStage)
	}
	stage := graph.Stages[index]
	if isControlWorkflowNode(stage) || isVisualOnlyWorkflowNode(stage) || isSubWorkflowNode(stage) {
		return fmt.Errorf("workflow stage %s cannot use model escalation because it is not an executable model stage", stage.Name)
	}
	if _, _, ok := workflowGraphEscalationStageModel(stage); !ok {
		return fmt.Errorf("workflow stage %s has no model escalation provider, model, or budget override configured", stage.Name)
	}
	completed := workflowStageResultsFromRunSnapshots(run.CompletedStages)
	failed, ok := workflowGraphLatestStageResultByName(completed, run.NextStage)
	if !ok {
		return fmt.Errorf("workflow run %s has no persisted blocked stage result for %q", run.ID, run.NextStage)
	}
	if workflowGraphStageResultWasModelEscalated(failed) {
		return fmt.Errorf("workflow stage %s already used model escalation; retry or route remediation", run.NextStage)
	}
	if !workflowGraphRunHasContractFailure(run, run.NextStage, failed) {
		return fmt.Errorf("workflow stage %s is not blocked by a required contract failure", run.NextStage)
	}
	return nil
}

// ContinueOutput resumes a graph workflow paused because a stage hit output or
// budget limits. The incomplete stage is rerun with the persisted partial output
// as context, then downstream graph execution continues in the same durable run.
func (w *WorkflowRunner) ContinueOutput(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	if err := w.CanContinueOutput(run); err != nil {
		return WorkflowResult{}, err
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		return WorkflowResult{}, err
	}
	completed := workflowStageResultsFromRunSnapshots(run.CompletedStages)
	incomplete, ok := workflowGraphFindIncompleteStageResult(completed, run.NextStage)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run %s has no persisted incomplete result for stage %q", runID, run.NextStage)
	}
	index := workflowGraphContinueStageIndex(graph, run.NextStage, incomplete)
	if index < 0 {
		return WorkflowResult{}, fmt.Errorf("workflow run %s references unknown next stage %q", runID, run.NextStage)
	}
	stage := graph.Stages[index]
	priorCompleted := workflowGraphRemoveStageResult(completed, run.NextStage)
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
	eventStage := strings.TrimSpace(string(incomplete.Stage))
	if eventStage == "" {
		eventStage = stage.Name
	}
	w.runtime.session.AppendWorkflowRunEvent(run.ID, session.WorkflowRunEventSnapshot{
		Type:           "workflow_output_continue_submitted",
		Stage:          eventStage,
		Content:        workflowGraphContinueOutputEventContent(incomplete),
		AgentID:        stage.Agent,
		Mode:           incomplete.Result.Mode,
		WorkflowName:   run.Name,
		WorkflowStatus: "running",
		NextStage:      eventStage,
		Reason:         "continue_output",
		Severity:       "info",
		BudgetScope:    "output",
		StopReason:     incomplete.Result.StopReason,
		Incomplete:     false,
	})
	if result, handled, err := w.continueRepeatStageOutput(ctx, run, graph, priorCompleted, incomplete, handler); handled || err != nil {
		if err != nil {
			w.failWorkflowRun(run.ID, run.Name, run.Request, err)
			return WorkflowResult{}, err
		}
		result.RunID = run.ID
		w.completeWorkflowRun(run.ID, result)
		return result, nil
	}
	result, err := w.continueWorkflowGraphStageOutput(ctx, graph, index, run.Request, priorCompleted, incomplete, nil, handler)
	if err != nil {
		w.failWorkflowRun(run.ID, run.Name, run.Request, err)
		return WorkflowResult{}, err
	}
	result.RunID = run.ID
	w.completeWorkflowRun(run.ID, result)
	return result, nil
}

// EscalateModel reruns a blocked executable stage with its configured
// escalation model route, then continues downstream stages in the same durable
// workflow run.
func (w *WorkflowRunner) EscalateModel(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	if err := w.CanEscalateModel(run); err != nil {
		return WorkflowResult{}, err
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		return WorkflowResult{}, err
	}
	index := workflowGraphStageIndexByName(graph, run.NextStage)
	if index < 0 {
		return WorkflowResult{}, fmt.Errorf("workflow run %s references unknown blocked stage %q", runID, run.NextStage)
	}
	completed := workflowStageResultsFromRunSnapshots(run.CompletedStages)
	failed, ok := workflowGraphLatestStageResultByName(completed, run.NextStage)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run %s has no persisted blocked stage result for %q", runID, run.NextStage)
	}
	priorCompleted := workflowGraphRemoveStageResult(completed, run.NextStage)
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
	result, err := w.runWorkflowGraphEscalatedStageAndContinue(ctx, graph, index, run.Request, priorCompleted, failed, "operator_escalate_model", handler)
	if err != nil {
		w.failWorkflowRun(run.ID, run.Name, run.Request, err)
		return WorkflowResult{}, err
	}
	result.RunID = run.ID
	w.completeWorkflowRun(run.ID, result)
	return result, nil
}

// ApproveBudget resumes a graph workflow paused at a hard budget boundary.
func (w *WorkflowRunner) ApproveBudget(ctx context.Context, runID string, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if w == nil || w.runtime == nil || w.runtime.session == nil {
		return WorkflowResult{}, fmt.Errorf("workflow runtime not configured")
	}
	run, ok := w.runtime.session.WorkflowRun(runID)
	if !ok {
		return WorkflowResult{}, fmt.Errorf("workflow run not found: %s", runID)
	}
	if err := w.CanApproveBudget(run); err != nil {
		return WorkflowResult{}, err
	}
	graph, err := w.loadWorkflowGraph(run.Name)
	if err != nil {
		return WorkflowResult{}, err
	}
	graph = workflowGraphWithApprovedHardBudget(graph)
	index := workflowGraphStageIndexByName(graph, run.NextStage)
	if index < 0 {
		return WorkflowResult{}, fmt.Errorf("workflow run %s references unknown next stage %q", runID, run.NextStage)
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
	stage := graph.Stages[index]
	w.runtime.session.AppendWorkflowRunEvent(run.ID, session.WorkflowRunEventSnapshot{
		Type:                        "workflow_budget_approved",
		Stage:                       stage.Name,
		Content:                     "workflow budget approval submitted",
		AgentID:                     fallbackWorkflowGraphValue(stage.Agent, "workflow"),
		Mode:                        "workflow",
		WorkflowName:                run.Name,
		WorkflowStatus:              "running",
		NextStage:                   stage.Name,
		Reason:                      "budget_approved",
		Severity:                    "info",
		BudgetScope:                 run.BudgetScope,
		BudgetReason:                "budget_approved",
		BudgetMetric:                run.BudgetMetric,
		BudgetUsed:                  run.BudgetUsed,
		BudgetSoftLimit:             run.BudgetSoftLimit,
		BudgetHardLimit:             run.BudgetHardLimit,
		BudgetRemaining:             run.BudgetRemaining,
		BudgetPromptTokens:          run.BudgetPromptTokens,
		BudgetEstimatedPromptTokens: run.BudgetEstimatedPromptTokens,
		BudgetNetPromptTokens:       run.BudgetNetPromptTokens,
		BudgetGrossPromptTokens:     run.BudgetGrossPromptTokens,
		BudgetSavedTokens:           run.BudgetSavedTokens,
		BudgetMemorySavedTokens:     run.BudgetMemorySavedTokens,
		BudgetHistorySavedTokens:    run.BudgetHistorySavedTokens,
		BudgetArtifactSavedTokens:   run.BudgetArtifactSavedTokens,
		BudgetSkillSavedTokens:      run.BudgetSkillSavedTokens,
		BudgetToolSchemaSavedTokens: run.BudgetToolSchemaSavedTokens,
		BudgetReportedPromptTokens:  run.BudgetReportedPromptTokens,
		BudgetOutputTokens:          run.BudgetOutputTokens,
		BudgetCachedTokens:          run.BudgetCachedTokens,
		BudgetTotalTokens:           run.BudgetTotalTokens,
		BudgetLLMCalls:              run.BudgetLLMCalls,
		BudgetContinuations:         run.BudgetContinuations,
		BudgetEstimatedInputCost:    run.BudgetEstimatedInputCost,
		BudgetEstimatedOutputCost:   run.BudgetEstimatedOutputCost,
		BudgetEstimatedTotalCost:    run.BudgetEstimatedTotalCost,
		BudgetCostCurrency:          run.BudgetCostCurrency,
		BudgetPricingSource:         run.BudgetPricingSource,
		PromptTokens:                run.BudgetReportedPromptTokens,
		OutputTokens:                run.BudgetOutputTokens,
		CachedTokens:                run.BudgetCachedTokens,
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

func workflowGraphWithApprovedHardBudget(graph workflowGraph) workflowGraph {
	graph.Budget.HardPromptTokens = 0
	graph.Budget.HardOutputTokens = 0
	graph.Budget.HardTotalTokens = 0
	graph.Budget.HardLLMCalls = 0
	graph.Budget.HardContinuations = 0
	for index := range graph.Stages {
		graph.Stages[index].Params = workflowGraphParamsWithoutHardBudget(graph.Stages[index].Params)
	}
	return graph
}

func workflowGraphParamsWithoutHardBudget(params map[string]string) map[string]string {
	if len(params) == 0 {
		return params
	}
	out := make(map[string]string, len(params))
	for key, value := range params {
		if workflowGraphHardBudgetParamKey(key) {
			continue
		}
		out[key] = value
	}
	if len(out) == len(params) {
		return params
	}
	return out
}

func workflowGraphHardBudgetParamKey(key string) bool {
	switch strings.TrimSpace(key) {
	case "workflow_hard_prompt_tokens", "budget.hard_prompt_tokens", "hard_prompt_tokens",
		"workflow_hard_output_tokens", "budget.hard_output_tokens", "hard_output_tokens",
		"workflow_hard_total_tokens", "budget.hard_total_tokens", "hard_total_tokens",
		"workflow_hard_llm_calls", "budget.hard_llm_calls", "hard_llm_calls",
		"workflow_hard_continuations", "budget.hard_continuations", "hard_continuations":
		return true
	default:
		return false
	}
}

func (w *WorkflowRunner) continueRepeatStageOutput(ctx context.Context, run session.WorkflowRunSnapshot, graph workflowGraph, completed []WorkflowStageResult, incomplete WorkflowStageResult, handler func(event schema.StreamEvent) error) (WorkflowResult, bool, error) {
	controlName := strings.TrimSpace(incomplete.Metadata["repeat_control"])
	if controlName == "" {
		return WorkflowResult{}, false, nil
	}
	controlIndex := workflowGraphStageIndexByName(graph, controlName)
	if controlIndex < 0 {
		return WorkflowResult{}, true, fmt.Errorf("workflow run %s references unknown repeat control stage %q", run.ID, controlName)
	}
	context, err := w.workflowGraphRepeatContext(graph, controlIndex, run.Request, completed)
	if err != nil {
		return WorkflowResult{}, true, err
	}
	iterationIndex, err := strconv.Atoi(strings.TrimSpace(incomplete.Metadata["iteration_index"]))
	if err != nil || iterationIndex < 0 {
		return WorkflowResult{}, true, fmt.Errorf("workflow run %s is missing repeat iteration metadata for stage %q", run.ID, incomplete.Stage)
	}
	body := graph.Stages[context.BodyIndex]
	iterationStage := workflowGraphRepeatIterationStage(body, graph.Stages[context.ControlIndex], context, iterationIndex)
	stageIndex := workflowGraphStageIndexByName(graph, body.Name)
	if stageIndex < 0 {
		return WorkflowResult{}, true, fmt.Errorf("workflow run %s references unknown repeat body stage %q", run.ID, body.Name)
	}
	inputs := workflowGraphRepeatIterationInputs(iterationStage, context, iterationIndex, run.Request, completed)
	inputValues := workflowGraphRepeatIterationInputValues(iterationStage, context, iterationIndex, run.Request, completed)
	stageResult, pending, err := w.continueWorkflowGraphExecutableStage(ctx, graph, stageIndex, iterationStage, run.Request, completed, incomplete, inputs, inputValues, handler)
	if err != nil {
		return WorkflowResult{}, true, err
	}
	if pending != nil {
		return *pending, true, nil
	}
	if outcome, handled, err := w.handleWorkflowGraphStageContractFailure(ctx, graph, context.BodyIndex, run.Request, completed, stageResult, handler); handled || err != nil {
		return outcome, true, err
	}
	w.applyWorkflowGraphFinalQualityHandoff(graph, body, completed, &stageResult, handler)
	completedWithIteration := append(append([]WorkflowStageResult(nil), completed...), stageResult)
	if workflowGraphRepeatKind(context.Kind) == "loop" && strings.TrimSpace(context.Until) != "" && evaluateWorkflowGraphCondition(context.Until, run.Request, completedWithIteration) {
		controlResult := workflowGraphRepeatControlResult(graph.Stages[context.ControlIndex], graph, context, completedWithIteration, true)
		completedWithIteration = append(completedWithIteration, controlResult)
		queue := w.nextWorkflowGraphStageIndices(graph, context.ControlIndex, run.Request, controlResult.Result.Output, completedWithIteration)
		resumed, err := w.runWorkflowGraphQueue(ctx, graph, run.Request, true, queue, completedWithIteration, nil, nil, handler)
		return resumed, true, err
	}
	context.NextIteration = iterationIndex + 1
	outcome, err := w.runWorkflowGraphRepeatIterations(ctx, graph, run.Request, completedWithIteration, context, handler)
	if err != nil {
		return WorkflowResult{}, true, err
	}
	if outcome.Result != nil {
		return *outcome.Result, true, nil
	}
	queue := outcome.Queue
	if len(queue) == 0 {
		controlIndex := context.ControlIndex
		stageOutput := ""
		if len(outcome.Completed) > 0 {
			stageOutput = outcome.Completed[len(outcome.Completed)-1].Result.Output
		}
		queue = w.nextWorkflowGraphStageIndices(graph, controlIndex, run.Request, stageOutput, outcome.Completed)
	}
	resumed, err := w.runWorkflowGraphQueue(ctx, graph, run.Request, true, queue, outcome.Completed, nil, nil, handler)
	return resumed, true, err
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

func (w *WorkflowRunner) continueWorkflowGraphStageOutput(ctx context.Context, graph workflowGraph, index int, request string, completed []WorkflowStageResult, incomplete WorkflowStageResult, manualInputValues map[string]map[string]any, handler func(event schema.StreamEvent) error) (WorkflowResult, error) {
	if index < 0 || index >= len(graph.Stages) {
		return WorkflowResult{}, fmt.Errorf("workflow %s continue context is missing graph stage %s", graph.Name, incomplete.Stage)
	}
	stage := graph.Stages[index]
	inputs := resolveWorkflowGraphStageInputs(stage, request, completed)
	inputValues := resolveWorkflowGraphStageInputValues(stage, request, completed)
	stageResult, pending, err := w.continueWorkflowGraphExecutableStage(ctx, graph, index, stage, request, completed, incomplete, inputs, inputValues, handler)
	if err != nil {
		return WorkflowResult{}, err
	}
	if pending != nil {
		return *pending, nil
	}
	if outcome, handled, err := w.handleWorkflowGraphStageContractFailure(ctx, graph, index, request, completed, stageResult, handler); handled || err != nil {
		return outcome, err
	}
	w.applyWorkflowGraphFinalQualityHandoff(graph, stage, completed, &stageResult, handler)
	completed = append(completed, stageResult)
	queue := w.nextWorkflowGraphStageIndices(graph, index, request, stageResult.Result.Output, completed)
	return w.runWorkflowGraphQueue(ctx, graph, request, true, queue, completed, nil, manualInputValues, handler)
}

func (w *WorkflowRunner) continueWorkflowGraphExecutableStage(ctx context.Context, graph workflowGraph, index int, stage workflowGraphStage, request string, completed []WorkflowStageResult, incomplete WorkflowStageResult, inputs map[string]string, inputValues map[string]any, handler func(event schema.StreamEvent) error) (WorkflowStageResult, *WorkflowResult, error) {
	if index < 0 || index >= len(graph.Stages) {
		return WorkflowStageResult{}, nil, fmt.Errorf("workflow %s continue context is missing graph stage %s", graph.Name, incomplete.Stage)
	}
	skill, err := w.graphStageSkill(stage)
	if err != nil {
		return WorkflowStageResult{}, nil, err
	}
	if err := w.ensureWorkflowAgent(stage.Agent); err != nil {
		return WorkflowStageResult{}, nil, err
	}
	if len(inputs) == 0 {
		inputs = copyStringMap(incomplete.Input)
	}
	if len(inputValues) == 0 {
		inputValues = copyWorkflowAnyMap(incomplete.InputValues)
	}
	prompt := w.buildWorkflowGraphStagePrompt(ctx, graph, stage, request, completed)
	prompt = workflowGraphContinueOutputPrompt(prompt, incomplete)
	w.persistWorkflowState(graph.Name, "running", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), "")
	if err := w.runtime.SetActiveAgent(stage.Agent); err != nil {
		return WorkflowStageResult{}, nil, err
	}
	result, attempts, err := w.runWorkflowGraphExecutableStage(ctx, graph, stage, prompt, skill, handler)
	if err != nil {
		return WorkflowStageResult{}, nil, fmt.Errorf("workflow stage %s: %w", stage.Name, err)
	}
	if hasSuspendedToolResult(result.ToolResults) {
		if strings.TrimSpace(incomplete.Metadata["repeat_control"]) != "" {
			if context, iteration, ok := w.workflowGraphRepeatContextForIncomplete(graph, request, completed, incomplete); ok {
				w.captureWorkflowGraphRepeatApprovalContext(graph, context, iteration, request, result.Output, result.ResponseMessage, completed, result.ToolResults, prompt)
			} else {
				w.captureWorkflowGraphApprovalContext(graph, index, request, result.Output, result.ResponseMessage, completed, result.ToolResults, prompt)
			}
		} else {
			w.captureWorkflowGraphApprovalContext(graph, index, request, result.Output, result.ResponseMessage, completed, result.ToolResults, prompt)
		}
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), stage.Agent)
		w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(stage.Name), request, summarizeWorkflow(completed), approvalPrompt)
		pending := WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(stage.Name)}
		return WorkflowStageResult{}, &pending, nil
	}
	stageResult := workflowGraphStageResult(stage, result, inputs, inputValues, attempts)
	stageResult.Metadata = mergeWorkflowStageMetadata(stageResult.Metadata, workflowGraphContinueOutputMetadata(incomplete))
	if result.Incomplete {
		paused := w.pauseWorkflowForIncompleteStage(graph.Name, request, completed, stageResult)
		if strings.TrimSpace(incomplete.Metadata["repeat_control"]) != "" && normalizeWorkflowSkillName(string(incomplete.Stage)) != normalizeWorkflowSkillName(stage.Name) {
			paused.NextStage = incomplete.Stage
			paused.CompletedStages = workflowGraphRenamePausedStage(paused.CompletedStages, WorkflowStage(stage.Name), incomplete.Stage)
			if w != nil && w.runtime != nil && w.runtime.session != nil && strings.TrimSpace(w.runID) != "" {
				w.runtime.session.CompleteWorkflowRun(w.runID, paused.Status, paused.FinalSummary, string(paused.NextStage), paused.ApprovalPrompt, nil, workflowRunStageSnapshots(paused.CompletedStages))
			}
		}
		return WorkflowStageResult{}, &paused, nil
	}
	return stageResult, nil, nil
}

func workflowGraphRenamePausedStage(stages []WorkflowStageResult, from, to WorkflowStage) []WorkflowStageResult {
	if len(stages) == 0 || strings.TrimSpace(string(to)) == "" {
		return stages
	}
	out := append([]WorkflowStageResult(nil), stages...)
	target := normalizeWorkflowSkillName(string(from))
	for i := len(out) - 1; i >= 0; i-- {
		if normalizeWorkflowSkillName(string(out[i].Stage)) == target {
			out[i].Stage = to
			return out
		}
	}
	return out
}

func (w *WorkflowRunner) workflowGraphRepeatContextForIncomplete(graph workflowGraph, request string, completed []WorkflowStageResult, incomplete WorkflowStageResult) (workflowGraphRepeatContext, int, bool) {
	controlName := strings.TrimSpace(incomplete.Metadata["repeat_control"])
	if controlName == "" {
		return workflowGraphRepeatContext{}, 0, false
	}
	controlIndex := workflowGraphStageIndexByName(graph, controlName)
	if controlIndex < 0 {
		return workflowGraphRepeatContext{}, 0, false
	}
	context, err := w.workflowGraphRepeatContext(graph, controlIndex, request, completed)
	if err != nil {
		return workflowGraphRepeatContext{}, 0, false
	}
	iteration, err := strconv.Atoi(strings.TrimSpace(incomplete.Metadata["iteration_index"]))
	if err != nil || iteration < 0 {
		return workflowGraphRepeatContext{}, 0, false
	}
	return context, iteration, true
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
	target := workflowGraphExclusiveRouteTarget(previous)
	for _, name := range workflowGraphCSVStageNames(target) {
		if normalizeWorkflowSkillName(name) == normalizeWorkflowSkillName(current.Name) {
			return true
		}
	}
	return false
}

func workflowGraphExclusiveRouteTarget(result WorkflowStageResult) string {
	if result.Metadata != nil {
		if route := strings.TrimSpace(result.Metadata["contract_route"]); route != "" {
			return route
		}
	}
	if !workflowGraphExclusiveControlResult(result) {
		return ""
	}
	if result.Metadata != nil {
		if target := strings.TrimSpace(result.Metadata["target"]); target != "" {
			return target
		}
	}
	if result.Output.Variables != nil {
		return result.Output.Variables["target"]
	}
	return ""
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
	Contract    workflowGraphContractFailure
	Blocked     bool
	Err         error
}

type workflowGraphParallelFileConflict struct {
	Path      string
	Owners    []string
	Stages    []string
	SourceRef string
}

type workflowGraphParallelPatchArtifactWarning struct {
	Stage            string
	Owner            string
	DirectWriteTools []string
	Reason           string
	SourceRef        string
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
			return workflowGraphParallelOutcome{Completed: completed, Queue: w.workflowGraphQueueTargetsUntilJoin(graph, targets, completed)}, true, nil
		}
		if joinIndex >= 0 && joinIndex != plan.JoinIndex {
			return workflowGraphParallelOutcome{Completed: completed, Queue: w.workflowGraphQueueTargetsUntilJoin(graph, targets, completed)}, true, nil
		}
		joinIndex = plan.JoinIndex
		for _, stageIndex := range plan.StageIndices {
			if _, exists := usedStages[stageIndex]; exists {
				return workflowGraphParallelOutcome{Completed: completed, Queue: w.workflowGraphQueueTargetsUntilJoin(graph, targets, completed)}, true, nil
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
				return workflowGraphParallelOutcome{Completed: completed, Queue: w.workflowGraphQueueTargetsUntilJoin(graph, targets, completed)}, true, nil
			}
			usedAgents[key] = struct{}{}
		}
		plans = append(plans, plan)
	}
	if hit := w.workflowGraphHardBudgetLimitHit(graph, control, len(plans)); hit.Hit {
		paused := w.pauseWorkflowGraphForHardBudget(graph, control, request, completed, hit, handler)
		return workflowGraphParallelOutcome{Result: &paused}, true, nil
	}
	w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, control, len(plans), handler)
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: fmt.Sprintf("running %d workflow branches concurrently from %s", len(plans), control.Name), AgentID: "workflow", Mode: "workflow", NeedsAction: true})
	}
	w.persistWorkflowState(graph.Name, "running", WorkflowStage(control.Name), request, summarizeWorkflow(completed), fmt.Sprintf("running %d workflow branches concurrently", len(plans)))
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
	handoffWarnings := workflowGraphParallelPatchArtifactWarnings(graph, successful)
	workflowGraphAnnotateParallelPatchArtifactWarnings(successful, handoffWarnings)
	w.emitWorkflowGraphParallelPatchArtifactWarningEvent(graph, control, joinIndex, handoffWarnings, handler)
	conflicts := workflowGraphParallelFileConflicts(successful)
	workflowGraphAnnotateParallelFileConflicts(successful, conflicts)
	w.emitWorkflowGraphParallelFileConflictEvent(graph, control, joinIndex, conflicts, handler)
	completed = append(completed, successful...)
	for _, branch := range results {
		if branch.Err == nil && !branch.Suspended && len(branch.Completed) > 0 {
			w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, branch.Stage, 0, handler)
		}
	}
	if suspended != nil {
		w.captureWorkflowGraphApprovalContext(graph, suspended.Index, request, suspended.Result.Output, suspended.Result.ResponseMessage, completed, suspended.Result.ToolResults, suspended.Prompt)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(suspended.Result.ToolResults), suspended.Stage.Agent)
		w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(suspended.Stage.Name), request, summarizeWorkflow(completed), approvalPrompt)
		return workflowGraphParallelOutcome{Result: &WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(suspended.Stage.Name)}}, true, nil
	}
	for _, stageResult := range successful {
		if stageResult.Result.Incomplete {
			completedBeforeBranches := append([]WorkflowStageResult(nil), completed[:len(completed)-len(successful)]...)
			for _, sibling := range successful {
				if normalizeWorkflowSkillName(string(sibling.Stage)) == normalizeWorkflowSkillName(string(stageResult.Stage)) {
					continue
				}
				completedBeforeBranches = append(completedBeforeBranches, sibling)
			}
			paused := w.pauseWorkflowForIncompleteStage(graph.Name, request, completedBeforeBranches, stageResult)
			return workflowGraphParallelOutcome{Result: &paused}, true, nil
		}
	}
	for i := range results {
		branch := results[i]
		if !branch.Blocked {
			continue
		}
		completedBeforeBranches := append([]WorkflowStageResult(nil), completed[:len(completed)-len(successful)]...)
		for _, sibling := range successful {
			if normalizeWorkflowSkillName(string(sibling.Stage)) == normalizeWorkflowSkillName(string(branch.Contract.StageResult.Stage)) {
				continue
			}
			completedBeforeBranches = append(completedBeforeBranches, sibling)
		}
		outcome, _, err := w.handleWorkflowGraphStageContractFailure(ctx, graph, branch.Index, request, completedBeforeBranches, branch.Contract.StageResult, handler)
		if err != nil {
			return workflowGraphParallelOutcome{}, true, err
		}
		return workflowGraphParallelOutcome{Result: &outcome}, true, nil
	}
	return workflowGraphParallelOutcome{Completed: completed, Queue: uniqueWorkflowGraphStageIndices(nextQueue)}, true, nil
}

func (w *WorkflowRunner) runWorkflowGraphConcurrentBranchPlan(ctx context.Context, graph workflowGraph, plan workflowGraphParallelBranchPlan, parent string, request string, completed []WorkflowStageResult, handler func(event schema.StreamEvent) error) workflowGraphParallelBranchResult {
	result := workflowGraphParallelBranchResult{Index: plan.StartIndex, Stage: graph.Stages[plan.StartIndex]}
	branchCompleted := append([]WorkflowStageResult(nil), completed...)
	baseLen := len(branchCompleted)
	for _, index := range plan.StageIndices {
		stage := graph.Stages[index]
		stage = w.workflowGraphStageWithSoftBudgetRoute(graph, stage, 0, handler)
		result.Index = index
		result.Stage = stage
		skill, err := w.graphStageSkill(stage)
		if err != nil {
			result.Err = err
			return result
		}
		inputs := resolveWorkflowGraphStageInputs(stage, request, branchCompleted)
		inputValues := resolveWorkflowGraphStageInputValues(stage, request, branchCompleted)
		prompt := w.buildWorkflowGraphStagePrompt(ctx, graph, stage, request, branchCompleted)
		if handler != nil {
			_ = handler(schema.StreamEvent{Type: schema.StreamEventTaskStage, Content: fmt.Sprintf("parallel branch stage %s started", stage.Name), AgentID: stage.Agent, Mode: "workflow", TaskStage: stage.Name, WorkflowName: graph.Name, WorkflowStatus: "running", NextStage: stage.Name, Reason: "parallel_branch", SourceRef: parent})
		}
		stageResult, attempts, err := w.runWorkflowGraphExecutableStage(ctx, graph, stage, prompt, skill, handler)
		result.Result = stageResult
		result.Inputs = inputs
		result.InputValues = inputValues
		result.Prompt = prompt
		result.Attempts = attempts
		if err != nil {
			errorTargets := w.workflowGraphNamedStageIndices(graph, stage.OnError, branchCompleted)
			if len(errorTargets) > 0 {
				failed := workflowGraphErrorStageResult(stage, err, inputs, inputValues, attempts)
				workflowGraphMarkParallelBranchResult(&failed, parent, graph.Stages[plan.StartIndex].Name)
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
		workflowGraphMarkParallelBranchResult(&completedStage, parent, graph.Stages[plan.StartIndex].Name)
		if stageResult.Incomplete {
			branchCompleted = append(branchCompleted, completedStage)
			result.Completed = append([]WorkflowStageResult(nil), branchCompleted[baseLen:]...)
			result.Result = stageResult
			result.Queue = nil
			return result
		}
		if contractFailure := workflowGraphStageContractFailure(stage, completedStage); contractFailure.Failed && contractFailure.Required {
			workflowGraphApplyContractFailureMetadata(&completedStage, contractFailure)
			workflowGraphMarkParallelBranchResult(&completedStage, parent, graph.Stages[plan.StartIndex].Name)
			branchCompleted = append(branchCompleted, completedStage)
			contractFailure.StageResult = completedStage
			result.Contract = contractFailure
			result.Blocked = true
			result.Completed = append([]WorkflowStageResult(nil), branchCompleted[baseLen:]...)
			result.Result = stageResult
			result.Queue = nil
			return result
		}
		w.applyWorkflowGraphFinalQualityHandoff(graph, stage, branchCompleted, &completedStage, handler)
		workflowGraphMarkParallelBranchResult(&completedStage, parent, graph.Stages[plan.StartIndex].Name)
		branchCompleted = append(branchCompleted, completedStage)
	}
	result.Completed = append([]WorkflowStageResult(nil), branchCompleted[baseLen:]...)
	result.Queue = []int{plan.JoinIndex}
	return result
}

func workflowGraphMarkParallelBranchResult(result *WorkflowStageResult, parent, owner string) {
	if result == nil {
		return
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	result.Metadata["parallel_branch"] = "true"
	result.Metadata["parallel_parent"] = parent
	if strings.TrimSpace(owner) != "" {
		result.Metadata["parallel_branch_owner"] = owner
	}
}

func workflowGraphParallelPatchArtifactWarnings(graph workflowGraph, stages []WorkflowStageResult) []workflowGraphParallelPatchArtifactWarning {
	if len(stages) == 0 {
		return nil
	}
	stageByName := make(map[string]workflowGraphStage, len(graph.Stages))
	for _, stage := range graph.Stages {
		if key := normalizeWorkflowSkillName(stage.Name); key != "" {
			stageByName[key] = stage
		}
	}
	warnings := make([]workflowGraphParallelPatchArtifactWarning, 0)
	for _, stageResult := range stages {
		if !strings.EqualFold(strings.TrimSpace(stageResult.Metadata["parallel_branch"]), "true") {
			continue
		}
		stage, ok := stageByName[normalizeWorkflowSkillName(string(stageResult.Stage))]
		if !ok {
			continue
		}
		warning, ok := workflowGraphParallelPatchArtifactWarningForStage(stage, stageResult)
		if !ok {
			continue
		}
		warnings = append(warnings, warning)
	}
	sort.Slice(warnings, func(i, j int) bool {
		return normalizeWorkflowSkillName(warnings[i].Stage) < normalizeWorkflowSkillName(warnings[j].Stage)
	})
	return warnings
}

func workflowGraphParallelPatchArtifactWarningForStage(stage workflowGraphStage, stageResult WorkflowStageResult) (workflowGraphParallelPatchArtifactWarning, bool) {
	tools := workflowGraphDirectWriteToolsForStage(stage, stageResult)
	if len(tools) == 0 {
		return workflowGraphParallelPatchArtifactWarning{}, false
	}
	reason := "parallel branch allows direct write tools; prefer patch/artifact-only worker output and a single merge/apply stage"
	if workflowGraphStageDeclaresPatchArtifactOnlyHandoff(stage) {
		reason = "parallel branch declares patch/artifact-only handoff but still allows direct write tools; remove direct write tools from the concurrent worker"
	}
	warning := workflowGraphParallelPatchArtifactWarning{
		Stage:            strings.TrimSpace(stage.Name),
		Owner:            workflowGraphParallelFileConflictOwner(stageResult),
		DirectWriteTools: tools,
		Reason:           reason,
	}
	warning.SourceRef = workflowGraphParallelPatchArtifactWarningSourceRef([]workflowGraphParallelPatchArtifactWarning{warning})
	return warning, true
}

func workflowGraphDirectWriteToolsForStage(stage workflowGraphStage, stageResult WorkflowStageResult) []string {
	seen := map[string]struct{}{}
	tools := make([]string, 0)
	add := func(tool string) {
		tool = strings.TrimSpace(tool)
		if tool == "" || !workflowGraphToolNameLooksDirectWrite(tool) {
			return
		}
		key := normalizeWorkflowSkillName(tool)
		if key == "" {
			key = strings.ToLower(tool)
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		tools = append(tools, tool)
	}
	for _, tool := range workflowGraphStageAllowedTools(stage) {
		add(tool)
	}
	for _, result := range stageResult.Output.ToolResults {
		add(result.ToolName)
	}
	for _, result := range stageResult.Result.ToolResults {
		add(result.ToolName)
	}
	sort.Strings(tools)
	return tools
}

func workflowGraphToolNameLooksDirectWrite(tool string) bool {
	tool = strings.TrimSpace(strings.ToLower(filepath.ToSlash(tool)))
	if tool == "" {
		return false
	}
	if index := strings.LastIndex(tool, "/"); index >= 0 && index < len(tool)-1 {
		tool = tool[index+1:]
	}
	tool = strings.ReplaceAll(tool, "-", "_")
	switch tool {
	case "write_file", "append_file", "create_file", "delete_file", "remove_file", "move_file", "rename_file", "edit_file", "patch_file", "apply_patch":
		return true
	}
	for _, safe := range []string{"read", "search", "list", "fetch", "snapshot", "probe", "info", "preview", "dry_run", "plan"} {
		if strings.Contains(tool, safe) {
			return false
		}
	}
	for _, marker := range []string{"write", "append", "create", "delete", "remove", "move", "rename", "edit", "patch", "apply"} {
		if strings.Contains(tool, marker) {
			return true
		}
	}
	return false
}

func workflowGraphStageDeclaresPatchArtifactOnlyHandoff(stage workflowGraphStage) bool {
	values := make([]string, 0, 8)
	for _, key := range []string{"parallel_handoff", "handoff_mode", "handoff_policy", "write_mode", "output_mode", "worker_contract", "output_contract", "token_policy", "purpose"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			values = append(values, value)
		}
	}
	text := strings.ToLower(strings.Join(values, " "))
	if text == "" {
		return false
	}
	for _, marker := range []string{"patch_artifact_only", "patch-artifact-only", "patch/artifact-only", "artifact-only", "artifact only", "patch only", "patch_artifact"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func workflowGraphAnnotateParallelPatchArtifactWarnings(stages []WorkflowStageResult, warnings []workflowGraphParallelPatchArtifactWarning) {
	if len(stages) == 0 || len(warnings) == 0 {
		return
	}
	warningByStage := make(map[string]workflowGraphParallelPatchArtifactWarning, len(warnings))
	for _, warning := range warnings {
		if key := normalizeWorkflowSkillName(warning.Stage); key != "" {
			warningByStage[key] = warning
		}
	}
	for i := range stages {
		warning, ok := warningByStage[normalizeWorkflowSkillName(string(stages[i].Stage))]
		if !ok {
			continue
		}
		if stages[i].Metadata == nil {
			stages[i].Metadata = map[string]string{}
		}
		tools := strings.Join(warning.DirectWriteTools, ", ")
		stages[i].Metadata["parallel_patch_artifact_required"] = "true"
		stages[i].Metadata["parallel_direct_write_tools"] = tools
		stages[i].Metadata["parallel_patch_artifact_reason"] = warning.Reason
		stages[i].Metadata["parallel_patch_artifact_source_ref"] = warning.SourceRef
		stages[i].Metadata["parallel_patch_artifact_contract_check"] = "parallel_patch_artifact_handoff"
		if strings.TrimSpace(stages[i].Metadata["contract_check"]) == "" {
			stages[i].Metadata["contract_check"] = "parallel_patch_artifact_handoff"
		}
		if strings.TrimSpace(stages[i].Metadata["reason"]) == "" {
			stages[i].Metadata["reason"] = warning.Reason
		}
		if strings.TrimSpace(stages[i].Metadata["source_ref"]) == "" {
			stages[i].Metadata["source_ref"] = warning.SourceRef
		}
		stages[i].Metadata["severity"] = "warning"
		workflowStageSetOutputValue(&stages[i].Output, "parallel_patch_artifact_required", true)
		workflowStageSetOutputValue(&stages[i].Output, "parallel_direct_write_tools", append([]string(nil), warning.DirectWriteTools...))
		workflowStageSetOutputValue(&stages[i].Output, "parallel_patch_artifact_reason", warning.Reason)
		workflowStageSetOutputValue(&stages[i].Output, "parallel_patch_artifact_source_ref", warning.SourceRef)
	}
}

func workflowGraphParallelPatchArtifactWarningSourceRef(warnings []workflowGraphParallelPatchArtifactWarning) string {
	parts := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		stage := strings.TrimSpace(warning.Stage)
		if stage == "" {
			stage = strings.TrimSpace(warning.Owner)
		}
		if stage == "" || len(warning.DirectWriteTools) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:params.tools=%s", stage, strings.Join(warning.DirectWriteTools, ",")))
	}
	return strings.Join(parts, "; ")
}

func (w *WorkflowRunner) emitWorkflowGraphParallelPatchArtifactWarningEvent(graph workflowGraph, control workflowGraphStage, joinIndex int, warnings []workflowGraphParallelPatchArtifactWarning, handler func(event schema.StreamEvent) error) {
	if handler == nil || len(warnings) == 0 {
		return
	}
	stageName := strings.TrimSpace(control.Name)
	if joinIndex >= 0 && joinIndex < len(graph.Stages) {
		stageName = strings.TrimSpace(graph.Stages[joinIndex].Name)
	}
	sourceRef := workflowGraphParallelPatchArtifactWarningSourceRef(warnings)
	content := "parallel branch direct-write handoff warning before join"
	if len(warnings) > 1 {
		content = "parallel branch direct-write handoff warnings before join"
	}
	if sourceRef != "" {
		content += ": " + sourceRef
	}
	_ = handler(schema.StreamEvent{
		Type:           schema.StreamEventStatus,
		TaskStage:      stageName,
		Content:        content,
		AgentID:        "workflow",
		Mode:           "workflow",
		WorkflowName:   graph.Name,
		WorkflowStatus: "running",
		NextStage:      stageName,
		Reason:         "parallel_patch_artifact_required",
		Severity:       "warning",
		ContractCheck:  "parallel_patch_artifact_handoff",
		SourceRef:      sourceRef,
	})
}

func workflowGraphParallelFileConflicts(stages []WorkflowStageResult) []workflowGraphParallelFileConflict {
	if len(stages) < 2 {
		return nil
	}
	type fileTouch struct {
		Path   string
		Owner  string
		Stages map[string]struct{}
	}
	byPath := make(map[string]map[string]*fileTouch)
	for _, stage := range stages {
		if !strings.EqualFold(strings.TrimSpace(stage.Metadata["parallel_branch"]), "true") {
			continue
		}
		owner := workflowGraphParallelFileConflictOwner(stage)
		if owner == "" {
			continue
		}
		stageName := strings.TrimSpace(string(stage.Stage))
		for _, path := range workflowGraphStageChangedFilePaths(stage) {
			key := strings.ToLower(path)
			if byPath[key] == nil {
				byPath[key] = make(map[string]*fileTouch)
			}
			ownerKey := normalizeWorkflowSkillName(owner)
			if ownerKey == "" {
				ownerKey = strings.ToLower(owner)
			}
			touch := byPath[key][ownerKey]
			if touch == nil {
				touch = &fileTouch{Path: path, Owner: owner, Stages: map[string]struct{}{}}
				byPath[key][ownerKey] = touch
			}
			if stageName != "" {
				touch.Stages[stageName] = struct{}{}
			}
		}
	}
	conflicts := make([]workflowGraphParallelFileConflict, 0)
	for _, owners := range byPath {
		if len(owners) < 2 {
			continue
		}
		conflict := workflowGraphParallelFileConflict{}
		stageSet := map[string]struct{}{}
		for _, touch := range owners {
			if conflict.Path == "" {
				conflict.Path = touch.Path
			}
			conflict.Owners = append(conflict.Owners, touch.Owner)
			for stage := range touch.Stages {
				stageSet[stage] = struct{}{}
			}
		}
		sort.Strings(conflict.Owners)
		for stage := range stageSet {
			conflict.Stages = append(conflict.Stages, stage)
		}
		sort.Strings(conflict.Stages)
		conflict.SourceRef = workflowGraphParallelFileConflictSourceRef([]workflowGraphParallelFileConflict{conflict})
		conflicts = append(conflicts, conflict)
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return strings.ToLower(conflicts[i].Path) < strings.ToLower(conflicts[j].Path)
	})
	return conflicts
}

func workflowGraphParallelFileConflictOwner(stage WorkflowStageResult) string {
	for _, value := range []string{
		stage.Metadata["parallel_branch_owner"],
		stage.Metadata["parallel_start"],
		stage.Metadata["parallel_owner"],
	} {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return strings.TrimSpace(string(stage.Stage))
}

func workflowGraphAnnotateParallelFileConflicts(stages []WorkflowStageResult, conflicts []workflowGraphParallelFileConflict) {
	if len(stages) == 0 || len(conflicts) == 0 {
		return
	}
	conflictByPath := make(map[string]workflowGraphParallelFileConflict, len(conflicts))
	for _, conflict := range conflicts {
		if path, ok := workflowGraphNormalizeChangedFilePath(conflict.Path); ok {
			conflictByPath[strings.ToLower(path)] = conflict
		}
	}
	for i := range stages {
		stagePaths := workflowGraphStageChangedFilePaths(stages[i])
		if len(stagePaths) == 0 {
			continue
		}
		owner := workflowGraphParallelFileConflictOwner(stages[i])
		matchedPaths := make([]string, 0)
		owners := map[string]struct{}{}
		sourceRefs := make([]string, 0)
		for _, path := range stagePaths {
			conflict, ok := conflictByPath[strings.ToLower(path)]
			if !ok || !workflowGraphParallelFileConflictIncludesOwner(conflict, owner) {
				continue
			}
			matchedPaths = append(matchedPaths, conflict.Path)
			sourceRefs = append(sourceRefs, conflict.SourceRef)
			for _, conflictOwner := range conflict.Owners {
				owners[conflictOwner] = struct{}{}
			}
		}
		if len(matchedPaths) == 0 {
			continue
		}
		matchedPaths = workflowGraphUniqueSortedStrings(matchedPaths)
		ownerList := make([]string, 0, len(owners))
		for owner := range owners {
			ownerList = append(ownerList, owner)
		}
		sort.Strings(ownerList)
		sourceRefs = workflowGraphUniqueSortedStrings(sourceRefs)
		if stages[i].Metadata == nil {
			stages[i].Metadata = map[string]string{}
		}
		stages[i].Metadata["parallel_file_conflict"] = "true"
		stages[i].Metadata["parallel_file_conflict_paths"] = strings.Join(matchedPaths, ", ")
		stages[i].Metadata["parallel_file_conflict_owners"] = strings.Join(ownerList, ", ")
		stages[i].Metadata["parallel_file_conflict_source_ref"] = strings.Join(sourceRefs, "; ")
		stages[i].Metadata["contract_check"] = "parallel_file_ownership"
		stages[i].Metadata["severity"] = "warning"
		workflowStageSetOutputValue(&stages[i].Output, "parallel_file_conflict", true)
		workflowStageSetOutputValue(&stages[i].Output, "parallel_file_conflict_paths", append([]string(nil), matchedPaths...))
		workflowStageSetOutputValue(&stages[i].Output, "parallel_file_conflict_owners", append([]string(nil), ownerList...))
		workflowStageSetOutputValue(&stages[i].Output, "parallel_file_conflict_source_ref", strings.Join(sourceRefs, "; "))
	}
}

func workflowGraphParallelFileConflictIncludesOwner(conflict workflowGraphParallelFileConflict, owner string) bool {
	ownerKey := normalizeWorkflowSkillName(owner)
	for _, candidate := range conflict.Owners {
		if normalizeWorkflowSkillName(candidate) == ownerKey {
			return true
		}
	}
	return false
}

func workflowGraphParallelFileConflictSourceRef(conflicts []workflowGraphParallelFileConflict) string {
	parts := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		if strings.TrimSpace(conflict.Path) == "" || len(conflict.Owners) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%s", conflict.Path, strings.Join(conflict.Owners, ",")))
	}
	return strings.Join(parts, "; ")
}

func (w *WorkflowRunner) emitWorkflowGraphParallelFileConflictEvent(graph workflowGraph, control workflowGraphStage, joinIndex int, conflicts []workflowGraphParallelFileConflict, handler func(event schema.StreamEvent) error) {
	if handler == nil || len(conflicts) == 0 {
		return
	}
	stageName := strings.TrimSpace(control.Name)
	if joinIndex >= 0 && joinIndex < len(graph.Stages) {
		stageName = strings.TrimSpace(graph.Stages[joinIndex].Name)
	}
	sourceRef := workflowGraphParallelFileConflictSourceRef(conflicts)
	content := "parallel file conflict before join"
	if len(conflicts) > 1 {
		content = "parallel file conflicts before join"
	}
	if sourceRef != "" {
		content += ": " + sourceRef
	}
	_ = handler(schema.StreamEvent{
		Type:           schema.StreamEventStatus,
		TaskStage:      stageName,
		Content:        content,
		AgentID:        "workflow",
		Mode:           "workflow",
		WorkflowName:   graph.Name,
		WorkflowStatus: "running",
		NextStage:      stageName,
		Reason:         "parallel_file_conflict",
		Severity:       "warning",
		ContractCheck:  "parallel_file_ownership",
		SourceRef:      sourceRef,
	})
}

func workflowGraphStageChangedFilePaths(stage WorkflowStageResult) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0)
	add := func(path string) {
		normalized, ok := workflowGraphNormalizeChangedFilePath(path)
		if !ok {
			return
		}
		key := strings.ToLower(normalized)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		paths = append(paths, normalized)
	}
	for _, value := range []any{
		stage.Output.Values["changed_files"],
		stage.Output.Values["files"],
		stage.Output.Values["paths"],
		stage.Output.Variables["changed_files"],
		stage.Output.Variables["files"],
		stage.Output.Variables["paths"],
	} {
		for _, path := range workflowGraphChangedFileListValue(value) {
			add(path)
		}
	}
	for _, change := range stage.Output.Changes {
		for _, path := range change.Files {
			add(path)
		}
	}
	for _, change := range stage.Result.Changes {
		for _, path := range change.Files {
			add(path)
		}
	}
	for _, finding := range stage.Output.Findings {
		for _, path := range finding.Files {
			add(path)
		}
	}
	for _, finding := range stage.Result.Findings {
		for _, path := range finding.Files {
			add(path)
		}
	}
	for _, artifact := range stage.Output.Artifacts {
		if workflowGraphArtifactLooksLikeChange(artifact) {
			add(artifact.Metadata["path"])
			add(artifact.Title)
		}
	}
	for _, result := range stage.Output.ToolResults {
		add(workflowGraphChangedFilePathFromToolResult(result))
	}
	for _, result := range stage.Result.ToolResults {
		add(workflowGraphChangedFilePathFromToolResult(result))
	}
	sort.Strings(paths)
	return paths
}

func workflowGraphChangedFileListValue(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return trimWorkflowGraphStringList(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, workflowGraphChangedFileListValue(item)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"changed_files", "files", "paths", "relative_path", "path", "file", "filename"} {
			if nested, ok := typed[key]; ok {
				return workflowGraphChangedFileListValue(nested)
			}
		}
		return nil
	case map[string]string:
		for _, key := range []string{"changed_files", "files", "paths", "relative_path", "path", "file", "filename"} {
			if nested := strings.TrimSpace(typed[key]); nested != "" {
				return workflowGraphChangedFileListValue(nested)
			}
		}
		return nil
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err == nil {
			if decodedText, ok := decoded.(string); ok {
				if decodedText = strings.TrimSpace(decodedText); decodedText != "" && decodedText != text {
					return workflowGraphChangedFileListValue(decodedText)
				}
			} else if items := workflowGraphChangedFileListValue(decoded); len(items) > 0 {
				return items
			}
		}
		parts := strings.FieldsFunc(text, func(r rune) bool {
			return r == '\n' || r == ';' || r == ','
		})
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.Trim(strings.TrimSpace(part), ` "'`)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	default:
		text := strings.TrimSpace(workflowGraphValueString(value))
		if text == "" {
			return nil
		}
		return []string{text}
	}
}

func workflowGraphNormalizeChangedFilePath(path string) (string, bool) {
	path = strings.Trim(strings.TrimSpace(path), ` "'`)
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "./")
	if path == "" || path == "." || path == "/" || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") {
		return "", false
	}
	return path, true
}

func workflowGraphUniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
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
	sort.Strings(out)
	return out
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
		if ok {
			for _, stageIndex := range plan.StageIndices {
				if issue, hasIssue := workflowGraphParallelPatchArtifactValidationIssue(graph.Stages[stageIndex]); hasIssue {
					branch.Issues = append(branch.Issues, issue)
				}
			}
		}
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

func workflowGraphParallelPatchArtifactValidationIssue(stage workflowGraphStage) (WorkflowGraphValidationIssue, bool) {
	tools := workflowGraphDirectWriteToolsForStage(stage, WorkflowStageResult{})
	if len(tools) == 0 {
		return WorkflowGraphValidationIssue{}, false
	}
	message := fmt.Sprintf("parallel branch stage %s allows direct write tools (%s); prefer patch/artifact-only output and a single merge/apply stage", stage.Name, strings.Join(tools, ", "))
	if workflowGraphStageDeclaresPatchArtifactOnlyHandoff(stage) {
		message = fmt.Sprintf("parallel branch stage %s declares patch/artifact-only handoff but still allows direct write tools (%s); remove direct write tools from the concurrent worker", stage.Name, strings.Join(tools, ", "))
	}
	return WorkflowGraphValidationIssue{Level: "warning", Stage: stage.Name, Field: "params.tools", Message: message}, true
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

func workflowGraphSequentialBranchExecutableStageEligible(stage workflowGraphStage) bool {
	return !isVisualOnlyWorkflowNode(stage) &&
		!isControlWorkflowNode(stage) &&
		!isRepeatWorkflowNode(stage) &&
		!isSubWorkflowNode(stage) &&
		!isJoinWorkflowNode(stage)
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

func (w *WorkflowRunner) workflowGraphQueueTargetsUntilJoin(graph workflowGraph, targets []int, completed []WorkflowStageResult) []int {
	if len(targets) == 0 {
		return nil
	}
	branchStages := make([]int, 0, len(targets))
	joins := make([]int, 0, 1)
	for _, target := range targets {
		if target < 0 || target >= len(graph.Stages) {
			continue
		}
		if plan, ok := w.workflowGraphStaticBranchPlan(graph, target, completed); ok {
			branchStages = append(branchStages, plan.StageIndices...)
			if plan.JoinIndex >= 0 {
				joins = append(joins, plan.JoinIndex)
			}
			continue
		}
		branchStages = append(branchStages, target)
	}
	out := uniqueWorkflowGraphStageIndices(branchStages)
	out = append(out, uniqueWorkflowGraphStageIndices(joins)...)
	return uniqueWorkflowGraphStageIndices(out)
}

func (w *WorkflowRunner) workflowGraphStaticBranchPlan(graph workflowGraph, index int, completed []WorkflowStageResult) (workflowGraphParallelBranchPlan, bool) {
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
		if !workflowGraphSequentialBranchExecutableStageEligible(stage) || strings.TrimSpace(stage.NextStrategy) != "" {
			return workflowGraphParallelBranchPlan{}, false
		}
		plan.StageIndices = append(plan.StageIndices, current)
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
		if workflowGraphParallelSelectionConfigured(stage) {
			selected := w.workflowGraphSelectedParallelStageIndices(graph, stage, targets, request, completed)
			if len(selected) == 0 {
				selected = w.workflowGraphParallelFallbackStageIndices(graph, stage, targets, completed)
			}
			if len(selected) == 0 {
				reason := fallbackWorkflowGraphValue(stage.Params["selection_error"], "parallel branch selection resolved no runnable stages")
				return workflowGraphControlDecision{
					Kind:   "parallel",
					Route:  "no_branches",
					Status: "blocked",
					Reason: reason,
					Halt:   true,
					Variables: workflowStageMetadata(map[string]string{
						"branch_count": "0",
						"branches":     "",
					}),
					ValueVariables: map[string]any{
						"branches": []string{},
					},
				}
			}
			targets = selected
		}
		originalTargets := append([]int(nil), targets...)
		softLimit := w.workflowGraphSoftBudgetParallelMaxBranches(stage, request, completed)
		if softLimit > 0 && len(targets) > softLimit {
			targets = limitWorkflowGraphStageIndices(targets, softLimit)
		}
		variables := workflowStageMetadata(map[string]string{
			"branch_count": strconv.Itoa(len(targets)),
			"branches":     workflowGraphStageNamesForIndices(graph, targets),
		})
		valueVariables := map[string]any{
			"branches": workflowGraphStageNamesForIndicesList(graph, targets),
		}
		if softLimit > 0 && len(originalTargets) > len(targets) {
			skipped := workflowGraphStageIndexDifference(originalTargets, targets)
			variables = mergeWorkflowStageMetadata(variables, workflowStageMetadata(map[string]string{
				"branch_budget_limited": "true",
				"branch_budget_limit":   strconv.Itoa(softLimit),
				"branch_original_count": strconv.Itoa(len(originalTargets)),
				"branch_original":       workflowGraphStageNamesForIndices(graph, originalTargets),
				"branch_skipped":        workflowGraphStageNamesForIndices(graph, skipped),
			}))
			valueVariables["branch_budget_limited"] = true
			valueVariables["branch_budget_limit"] = softLimit
			valueVariables["branch_original"] = workflowGraphStageNamesForIndicesList(graph, originalTargets)
			valueVariables["branch_skipped"] = workflowGraphStageNamesForIndicesList(graph, skipped)
		}
		return workflowGraphControlDecision{
			Kind:           "parallel",
			Route:          "fan_out",
			Value:          workflowGraphStageNamesForIndices(graph, targets),
			Targets:        targets,
			Target:         workflowGraphStageNamesForIndices(graph, targets),
			Status:         "completed",
			Variables:      variables,
			ValueVariables: valueVariables,
		}
	case "join", "merge", "barrier":
		required := workflowGraphJoinRequiredStageNames(graph, stage, completed)
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
			ValueVariables: map[string]any{
				"wait_for": append([]string(nil), required...),
			},
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
		prompt = w.buildWorkflowGraphStagePrompt(ctx, graph, iterationStage, pending.request, completed)
	}
	if err := w.ensureWorkflowAgent(iterationStage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	if err := w.runtime.SetActiveAgent(iterationStage.Agent); err != nil {
		return WorkflowResult{}, err
	}
	result, err := continueAgentRunWithModelSkillAndOptions(ctx, w.runtime, iterationStage.Agent, prompt, iterationStage.Model, &skill, workflowStageRunOptions{AllowedTools: workflowGraphStageAllowedTools(iterationStage)}, pending.call, pending.responseContent, pending.responseMessage, toolResult, handler)
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow stage %s: %w", iterationStage.Name, err)
	}
	if hasSuspendedToolResult(result.ToolResults) {
		w.captureWorkflowGraphRepeatApprovalContext(graph, context, iterationIndex, pending.request, result.Output, result.ResponseMessage, completed, result.ToolResults, prompt)
		approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), iterationStage.Agent)
		w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(iterationStage.Name), pending.request, summarizeWorkflow(completed), approvalPrompt)
		return WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(iterationStage.Name)}, nil
	}
	inputs := workflowGraphRepeatIterationInputs(iterationStage, context, iterationIndex, pending.request, completed)
	inputValues := workflowGraphRepeatIterationInputValues(iterationStage, context, iterationIndex, pending.request, completed)
	iterationResult := workflowGraphRepeatIterationResult(iterationStage, graph.Stages[context.ControlIndex], context, iterationIndex, result, inputs, inputValues, 1)
	w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, iterationStage, 0, handler)
	if result.Incomplete {
		return w.pauseWorkflowForIncompleteStage(graph.Name, pending.request, completed, iterationResult), nil
	}
	if outcome, handled, err := w.handleWorkflowGraphStageContractFailure(ctx, graph, context.BodyIndex, pending.request, completed, iterationResult, handler); handled || err != nil {
		return outcome, err
	}
	w.applyWorkflowGraphFinalQualityHandoff(graph, iterationStage, completed, &iterationResult, handler)
	completed = append(completed, iterationResult)
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
		if hit := w.workflowGraphHardBudgetLimitHit(graph, iterationStage, 1); hit.Hit {
			paused := w.pauseWorkflowGraphForHardBudget(graph, iterationStage, request, completed, hit, handler)
			return workflowGraphRepeatOutcome{Result: &paused}, nil
		}
		w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, iterationStage, 1, handler)
		iterationStage = w.workflowGraphStageWithSoftBudgetRoute(graph, iterationStage, 1, handler)
		inputs := workflowGraphRepeatIterationInputs(iterationStage, context, iteration, request, completed)
		inputValues := workflowGraphRepeatIterationInputValues(iterationStage, context, iteration, request, completed)
		prompt := w.buildWorkflowGraphStagePrompt(ctx, graph, iterationStage, request, completed)
		w.persistWorkflowState(graph.Name, "running", WorkflowStage(iterationStage.Name), request, summarizeWorkflow(completed), "")
		if err := w.runtime.SetActiveAgent(iterationStage.Agent); err != nil {
			return workflowGraphRepeatOutcome{}, err
		}
		result, attempts, err := w.runWorkflowGraphExecutableStage(ctx, graph, iterationStage, prompt, skill, handler)
		if err != nil {
			return workflowGraphRepeatOutcome{}, fmt.Errorf("workflow stage %s: %w", iterationStage.Name, err)
		}
		if hasSuspendedToolResult(result.ToolResults) {
			w.captureWorkflowGraphRepeatApprovalContext(graph, context, iteration, request, result.Output, result.ResponseMessage, completed, result.ToolResults, prompt)
			approvalPrompt := fmt.Sprintf("Workflow is waiting for approval of tool call %s by agent %s.", firstSuspendedToolCallID(result.ToolResults), iterationStage.Agent)
			w.persistWorkflowState(graph.Name, "awaiting_tool_approval", WorkflowStage(iterationStage.Name), request, summarizeWorkflow(completed), approvalPrompt)
			return workflowGraphRepeatOutcome{Result: &WorkflowResult{Name: graph.Name, Status: "awaiting_tool_approval", PendingApproval: true, ApprovalPrompt: approvalPrompt, CompletedStages: completed, NextStage: WorkflowStage(iterationStage.Name)}}, nil
		}
		iterationResult := workflowGraphRepeatIterationResult(iterationStage, control, context, iteration, result, inputs, inputValues, attempts)
		w.emitWorkflowGraphSoftBudgetWarning(ctx, graph, iterationStage, 0, handler)
		if result.Incomplete {
			paused := w.pauseWorkflowForIncompleteStage(graph.Name, request, completed, iterationResult)
			return workflowGraphRepeatOutcome{Result: &paused}, nil
		}
		if outcome, handled, err := w.handleWorkflowGraphStageContractFailure(ctx, graph, context.BodyIndex, request, completed, iterationResult, handler); handled || err != nil {
			if err != nil {
				return workflowGraphRepeatOutcome{}, err
			}
			return workflowGraphRepeatOutcome{Result: &outcome}, nil
		}
		w.applyWorkflowGraphFinalQualityHandoff(graph, iterationStage, completed, &iterationResult, handler)
		completed = append(completed, iterationResult)
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
	verifications := make([]schema.Verification, 0)
	for _, iteration := range iterations {
		if strings.TrimSpace(iteration.Output.RawOutput) != "" {
			outputs = append(outputs, iteration.Output.RawOutput)
		} else {
			outputs = append(outputs, iteration.Result.Output)
		}
		summaries = append(summaries, iteration.Output.Summary)
		verifications = append(verifications, iteration.Result.Verification...)
	}
	status := "completed"
	if !passed && workflowGraphRepeatKind(context.Kind) == "loop" {
		status = "warning"
		verifications = append(verifications, schema.Verification{
			Kind:   "loop:completion",
			Status: "failed",
			Detail: fmt.Sprintf("loop did not satisfy %s before max_iterations=%d", fallbackWorkflowGraphValue(context.Until, "completion condition"), context.MaxIterations),
		})
	} else if workflowGraphRepeatKind(context.Kind) == "loop" {
		verifications = append(verifications, schema.Verification{
			Kind:   "loop:completion",
			Status: "passed",
			Detail: fmt.Sprintf("loop satisfied %s after %d iteration(s)", fallbackWorkflowGraphValue(context.Until, "completion condition"), len(iterations)),
		})
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
		Status:    status,
		Variables: variables,
		ValueVariables: map[string]any{
			"body_stage":      graph.Stages[context.BodyIndex].Name,
			"iteration_count": len(iterations),
			"max_iterations":  context.MaxIterations,
			"items":           append([]string(nil), context.Items...),
			"item_count":      len(context.Items),
			"outputs":         append([]string(nil), outputs...),
			"summaries":       append([]string(nil), summaries...),
			"until":           context.Until,
			"passed":          passed,
		},
	}
	decision.Targets = nil
	decision.Target = workflowGraphStageNamesForIndices(graph, decision.Targets)
	stageResult := workflowGraphControlStageResult(stage, graph, decision)
	if len(verifications) > 0 {
		stageResult.Result.Verification = append(stageResult.Result.Verification, verifications...)
		stageResult.Output.Verification = append(stageResult.Output.Verification, verifications...)
	}
	applyWorkflowStageCanonicalOutputFields(&stageResult.Output)
	return stageResult
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

func (w *WorkflowRunner) captureWorkflowGraphRepeatApprovalContext(graph workflowGraph, context workflowGraphRepeatContext, iteration int, request, responseContent string, responseMessage schema.Message, completed []WorkflowStageResult, results []schema.ToolResult, stagePrompt string) {
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	body := graph.Stages[context.BodyIndex]
	iterationStage := workflowGraphRepeatIterationStage(body, graph.Stages[context.ControlIndex], context, iteration)
	context.NextIteration = iteration + 1
	_ = w.runtime.AnnotateWorkflowGraphRepeatPendingApproval(callID, graph.Name, WorkflowStage(iterationStage.Name), request, responseContent, responseMessage, completed, context.BodyIndex, stagePrompt, context)
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

func workflowGraphContinueStageIndex(graph workflowGraph, stageName string, incomplete WorkflowStageResult) int {
	if index := workflowGraphStageIndexByName(graph, stageName); index >= 0 {
		return index
	}
	if original := strings.TrimSpace(incomplete.Metadata["original_stage"]); original != "" {
		return workflowGraphStageIndexByName(graph, original)
	}
	return workflowGraphStageIndexByName(graph, graphStageOriginalName(stageName))
}

func workflowGraphContinuableExecutableStage(stage workflowGraphStage) bool {
	return !isVisualOnlyWorkflowNode(stage) &&
		!isControlWorkflowNode(stage) &&
		!isRepeatWorkflowNode(stage) &&
		!isSubWorkflowNode(stage) &&
		!isJoinWorkflowNode(stage)
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
	required := workflowGraphJoinRequiredStageNames(graph, stage, completed)
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

func workflowGraphJoinRequiredStageNames(graph workflowGraph, stage workflowGraphStage, completed []WorkflowStageResult) []string {
	for _, key := range []string{"wait_for_ref", "requires_ref", "required_ref", "branches_ref", "stages_ref"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			if resolved, ok := resolveWorkflowGraphReferenceValue(value, "", completed); ok {
				return workflowGraphStageNamesFromValue(resolved)
			}
			return workflowGraphCSVStageNames(resolveWorkflowGraphReference(value, "", completed))
		}
	}
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

func workflowGraphStageNamesFromValue(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return workflowGraphCSVStageNames(typed)
	case []string:
		return workflowGraphUniqueStageNames(typed)
	case []any:
		names := make([]string, 0, len(typed))
		for _, item := range typed {
			names = append(names, workflowGraphStageNamesFromValue(item)...)
		}
		return workflowGraphUniqueStageNames(names)
	case map[string]string:
		for _, key := range []string{"branches", "active_branches", "selected_branches", "run_branches", "wait_for", "stages"} {
			if value := strings.TrimSpace(typed[key]); value != "" {
				return workflowGraphCSVStageNames(value)
			}
		}
	case map[string]any:
		for _, key := range []string{"branches", "active_branches", "selected_branches", "run_branches", "wait_for", "stages"} {
			if value, ok := typed[key]; ok {
				return workflowGraphStageNamesFromValue(value)
			}
		}
	}
	return workflowGraphCSVStageNames(workflowGraphValueString(value))
}

func workflowGraphUniqueStageNames(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		name := strings.Trim(strings.TrimSpace(value), `"'`)
		if name == "" {
			continue
		}
		key := normalizeWorkflowSkillName(name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return names
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
	Name          string                            `json:"name"`
	Label         string                            `json:"label,omitempty"`
	LabelZH       string                            `json:"label_zh,omitempty"`
	Type          string                            `json:"type,omitempty"`
	Description   string                            `json:"description,omitempty"`
	DescriptionZH string                            `json:"description_zh,omitempty"`
	Placeholder   string                            `json:"placeholder,omitempty"`
	PlaceholderZH string                            `json:"placeholder_zh,omitempty"`
	Group         string                            `json:"group,omitempty"`
	GroupZH       string                            `json:"group_zh,omitempty"`
	Required      bool                              `json:"required,omitempty"`
	Default       any                               `json:"default,omitempty"`
	Options       []any                             `json:"options,omitempty"`
	Rows          int                               `json:"rows,omitempty"`
	Min           any                               `json:"min,omitempty"`
	Max           any                               `json:"max,omitempty"`
	Pattern       string                            `json:"pattern,omitempty"`
	Multiple      bool                              `json:"multiple,omitempty"`
	Advanced      bool                              `json:"advanced,omitempty"`
	Children      []workflowGraphInputFieldDocument `json:"children,omitempty"`
	Fields        []workflowGraphInputFieldDocument `json:"fields,omitempty"`
}

type workflowGraphInputFieldsDocument struct {
	Fields     []workflowGraphInputFieldDocument           `json:"fields,omitempty"`
	Required   []string                                    `json:"required,omitempty"`
	Properties map[string]workflowGraphInputSchemaProperty `json:"properties,omitempty"`
}

type workflowGraphInputSchemaProperty struct {
	Type          string                                      `json:"type,omitempty"`
	Title         string                                      `json:"title,omitempty"`
	TitleZH       string                                      `json:"title_zh,omitempty"`
	Label         string                                      `json:"label,omitempty"`
	LabelZH       string                                      `json:"label_zh,omitempty"`
	Description   string                                      `json:"description,omitempty"`
	DescriptionZH string                                      `json:"description_zh,omitempty"`
	Placeholder   string                                      `json:"placeholder,omitempty"`
	PlaceholderZH string                                      `json:"placeholder_zh,omitempty"`
	Default       any                                         `json:"default,omitempty"`
	Required      []string                                    `json:"required,omitempty"`
	Enum          []any                                       `json:"enum,omitempty"`
	Options       []any                                       `json:"options,omitempty"`
	Format        string                                      `json:"format,omitempty"`
	Pattern       string                                      `json:"pattern,omitempty"`
	Minimum       any                                         `json:"minimum,omitempty"`
	Maximum       any                                         `json:"maximum,omitempty"`
	MinLength     any                                         `json:"minLength,omitempty"`
	MaxLength     any                                         `json:"maxLength,omitempty"`
	Properties    map[string]workflowGraphInputSchemaProperty `json:"properties,omitempty"`
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
			Name:          name,
			Label:         doc.Label,
			LabelZH:       doc.LabelZH,
			Type:          doc.Type,
			Description:   doc.Description,
			DescriptionZH: doc.DescriptionZH,
			Placeholder:   doc.Placeholder,
			PlaceholderZH: doc.PlaceholderZH,
			Group:         fieldGroup,
			GroupZH:       doc.GroupZH,
			Required:      doc.Required,
			Default:       workflowGraphInputAnyString(doc.Default),
			Options:       workflowGraphInputOptionStrings(doc.Options),
			Rows:          doc.Rows,
			Min:           workflowGraphInputAnyString(doc.Min),
			Max:           workflowGraphInputAnyString(doc.Max),
			Pattern:       doc.Pattern,
			Multiple:      doc.Multiple,
			Advanced:      doc.Advanced,
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
		Name:          fullName,
		Label:         fallbackWorkflowGraphValue(prop.Label, prop.Title),
		LabelZH:       fallbackWorkflowGraphValue(prop.LabelZH, prop.TitleZH),
		Type:          fieldType,
		Description:   prop.Description,
		DescriptionZH: prop.DescriptionZH,
		Placeholder:   prop.Placeholder,
		PlaceholderZH: prop.PlaceholderZH,
		Group:         fieldGroup,
		Required:      workflowGraphInputFieldRequiredByName(fullName, required),
		Default:       workflowGraphInputAnyString(prop.Default),
		Options:       options,
		Min:           fallbackWorkflowGraphValue(workflowGraphInputAnyString(prop.Minimum), workflowGraphInputAnyString(prop.MinLength)),
		Max:           fallbackWorkflowGraphValue(workflowGraphInputAnyString(prop.Maximum), workflowGraphInputAnyString(prop.MaxLength)),
		Pattern:       prop.Pattern,
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
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
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

func workflowGraphInputOptionStrings(values []any) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text := strings.TrimSpace(workflowGraphInputAnyString(value))
		if text != "" {
			out = append(out, text)
		}
	}
	return out
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
	field.LabelZH = strings.TrimSpace(field.LabelZH)
	field.Type = workflowGraphInputFieldType(field.Type)
	if field.Type == "" {
		field.Type = "string"
	}
	field.Description = strings.TrimSpace(field.Description)
	field.DescriptionZH = strings.TrimSpace(field.DescriptionZH)
	field.Placeholder = strings.TrimSpace(field.Placeholder)
	field.PlaceholderZH = strings.TrimSpace(field.PlaceholderZH)
	field.Group = strings.TrimSpace(field.Group)
	field.GroupZH = strings.TrimSpace(field.GroupZH)
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

func workflowGraphContractFailTarget(stage workflowGraphStage) string {
	for _, key := range []string{"contract_fail_stage", "on_contract_fail_stage", "acceptance_fail_stage", "on_acceptance_fail_stage", "remediation_stage", "failure_stage"} {
		if target := strings.TrimSpace(stage.Params[key]); target != "" {
			return target
		}
	}
	for _, key := range []string{"contract_fail", "contract_failed", "acceptance_fail", "acceptance_failed", "fail", "failed", "block", "blocked", "deny", "denied", "false", "no"} {
		if target := strings.TrimSpace(stage.Routes[key]); target != "" {
			return target
		}
	}
	return ""
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

type workflowGraphContractFailure struct {
	Failed       bool
	Required     bool
	Stage        string
	Check        string
	Checks       []string
	SourceRef    string
	SourceRefs   []string
	Reason       string
	Status       string
	Route        string
	FailedCount  int
	WarningCount int
	UnknownCount int
	StageResult  WorkflowStageResult
}

type workflowGraphFinalQualityIssue struct {
	Stage     string
	Kind      string
	Check     string
	Status    string
	Reason    string
	SourceRef string
	Severity  string
}

type workflowGraphFinalQualityHandoff struct {
	Issues       []workflowGraphFinalQualityIssue
	FailedCount  int
	WarningCount int
	Reason       string
	SourceRef    string
	Severity     string
}

func (h workflowGraphFinalQualityHandoff) HasIssues() bool {
	return len(h.Issues) > 0
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
	case "artifact", "output", "report", "evidence", "verification", "acceptance", "test", "tests", "diff", "patch", "audit", "change_report", "requirements", "plan":
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

func workflowGraphContractFailPolicy(stage workflowGraphStage) string {
	return strings.ToLower(strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params,
		"on_contract_fail",
		"contract_failure",
		"contract_fail",
		"on_acceptance_fail",
		"acceptance_failure",
		"acceptance_fail",
	)))
}

func workflowGraphContractFailureRequired(stage workflowGraphStage, failure workflowGraphContractFailure) bool {
	if !failure.Failed {
		return false
	}
	policy := workflowGraphContractFailPolicy(stage)
	switch policy {
	case "ignore", "warn", "warning", "continue", "record":
		return false
	case "pause", "block", "blocked", "fail", "failed", "route", "retry", "escalate", "remediate", "remediation":
		return true
	}
	return workflowGraphQualityParamBool(stage, []string{
		"contract_required",
		"require_contract",
		"output_contract_required",
		"require_output_contract",
		"json_required",
		"require_json_output",
		"acceptance_required",
		"require_acceptance",
		"block_on_acceptance_failure",
		"block_on_contract_failure",
	}, false)
}

func workflowGraphStageContractFailure(stage workflowGraphStage, stageResult WorkflowStageResult) workflowGraphContractFailure {
	failure := workflowGraphContractFailure{
		Stage:       strings.TrimSpace(string(stageResult.Stage)),
		Check:       "acceptance_criteria",
		Status:      "failed",
		StageResult: stageResult,
	}
	if failure.Stage == "" {
		failure.Stage = stage.Name
	}
	details := make([]string, 0)
	addIssue := func(check, sourceRef, reason string) {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return
		}
		check = strings.TrimSpace(check)
		if check == "" {
			check = "output_contract"
		}
		sourceRef = strings.TrimSpace(sourceRef)
		failure.Failed = true
		failure.FailedCount++
		if failure.Check == "" || failure.Check == "acceptance_criteria" && check != "acceptance_criteria" && len(failure.Checks) == 0 {
			failure.Check = check
		}
		if !containsWorkflowGraphString(failure.Checks, check) {
			failure.Checks = append(failure.Checks, check)
		}
		if failure.SourceRef == "" {
			failure.SourceRef = sourceRef
		}
		if sourceRef != "" && !containsWorkflowGraphString(failure.SourceRefs, sourceRef) {
			failure.SourceRefs = append(failure.SourceRefs, sourceRef)
		}
		details = append(details, reason)
	}
	for _, item := range stageResult.Acceptance {
		status := workflowGraphQualityStatus(item.Status)
		switch {
		case workflowGraphQualityStatusFailed(status):
			addIssue("acceptance_criteria", firstWorkflowGraphValue(item.Ref, item.Name), workflowGraphAcceptanceFailureDetail(item))
		case status == "" || !workflowGraphQualityStatusPassed(status):
			if status == "warning" || status == "warn" {
				failure.WarningCount++
			} else {
				failure.UnknownCount++
			}
		}
	}
	if workflowGraphQualityParamBool(stage, []string{"require_acceptance", "acceptance_required"}, false) && len(stageResult.Acceptance) == 0 {
		addIssue("acceptance_criteria", "acceptance_criteria", fmt.Sprintf("%s requires acceptance criteria but none were recorded", fallbackWorkflowGraphValue(failure.Stage, "stage")))
	}
	if workflowGraphQualityParamBool(stage, []string{"require_verification", "verification_required"}, false) && !workflowGraphStageHasVerification(stageResult) {
		addIssue("verification_evidence", "result.verification", fmt.Sprintf("%s requires verification results but none were recorded", fallbackWorkflowGraphValue(failure.Stage, "stage")))
	}
	if workflowGraphQualityParamBool(stage, []string{"require_evidence", "evidence_required", "require_artifacts", "artifacts_required"}, false) && !workflowGraphStageHasEvidence(stageResult) {
		addIssue("declared_artifacts", "result.artifacts", fmt.Sprintf("%s requires evidence artifacts but none were recorded", fallbackWorkflowGraphValue(failure.Stage, "stage")))
	}
	for _, key := range workflowGraphMissingMappedOutputKeys(stage, stageResult) {
		addIssue("mapped_outputs", "outputs."+key, fmt.Sprintf("%s mapped output %s was not produced", fallbackWorkflowGraphValue(failure.Stage, "stage"), key))
	}
	for _, name := range workflowGraphMissingDeclaredArtifactNames(stage, stageResult) {
		addIssue("declared_artifacts", "artifacts."+name, fmt.Sprintf("%s declared artifact %s was not produced", fallbackWorkflowGraphValue(failure.Stage, "stage"), name))
	}
	if workflowGraphStageRequiresJSONOutput(stage) {
		decoded, ok := workflowStageJSONOutputObject(stageResult.Output.RawOutput)
		if !ok {
			addIssue("json_output", "result.output", fmt.Sprintf("%s output must be a JSON object for %s", fallbackWorkflowGraphValue(failure.Stage, "stage"), workflowGraphStageContractLabel(stage)))
		} else {
			for _, key := range workflowGraphStageRequiredJSONKeys(stage) {
				if _, ok := workflowStageOutputJSONValue(decoded, key); !ok {
					addIssue("json_output", "result.output."+key, fmt.Sprintf("%s JSON output missing required key %s", fallbackWorkflowGraphValue(failure.Stage, "stage"), key))
				}
			}
		}
	}
	for _, section := range workflowGraphStageRequiredSections(stage) {
		if !workflowGraphOutputContainsSection(stageResult.Output.RawOutput, section) {
			addIssue("required_sections", "result.output."+normalizeWorkflowSkillName(section), fmt.Sprintf("%s output missing required section %s", fallbackWorkflowGraphValue(failure.Stage, "stage"), section))
		}
	}
	if !failure.Failed {
		return failure
	}
	if len(failure.Checks) > 0 {
		failure.Check = strings.Join(failure.Checks, ",")
	}
	if len(details) > 0 {
		failure.Reason = strings.Join(details, "; ")
	} else {
		failure.Reason = fmt.Sprintf("%s acceptance criteria failed", fallbackWorkflowGraphValue(failure.Stage, "stage"))
	}
	failure.Required = workflowGraphContractFailureRequired(stage, failure)
	if target := workflowGraphContractFailTarget(stage); strings.TrimSpace(target) != "" {
		failure.Route = strings.TrimSpace(target)
		failure.Required = true
	}
	return failure
}

func containsWorkflowGraphString(values []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, existing := range values {
		if strings.EqualFold(strings.TrimSpace(existing), value) {
			return true
		}
	}
	return false
}

func workflowGraphStageHasVerification(stageResult WorkflowStageResult) bool {
	for _, item := range stageResult.Output.Verification {
		if strings.TrimSpace(item.Kind) != "" || strings.TrimSpace(item.Detail) != "" || strings.TrimSpace(item.Status) != "" {
			return true
		}
	}
	for _, item := range stageResult.Result.Verification {
		if strings.TrimSpace(item.Kind) != "" || strings.TrimSpace(item.Detail) != "" || strings.TrimSpace(item.Status) != "" {
			return true
		}
	}
	return false
}

func workflowGraphStageHasEvidence(stageResult WorkflowStageResult) bool {
	return len(stageResult.Output.Evidence) > 0 || len(stageResult.Output.Artifacts) > 0
}

func workflowGraphMissingMappedOutputKeys(stage workflowGraphStage, stageResult WorkflowStageResult) []string {
	if len(stage.Outputs) == 0 {
		return nil
	}
	missing := make([]string, 0)
	keys := make([]string, 0, len(stage.Outputs))
	for key := range stage.Outputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !workflowGraphOutputHasMappedKey(stageResult.Output, key, stage.Outputs[key]) {
			missing = append(missing, key)
		}
	}
	return missing
}

func workflowGraphOutputHasMappedKey(output WorkflowStageOutput, key, expr string) bool {
	if !workflowGraphOutputHasKey(output, key) {
		return false
	}
	value, valueOK := workflowGraphOutputValueByKey(output, key)
	if valueOK && workflowTruthyAny(value) {
		return true
	}
	return !workflowGraphUnresolvedOutputReference(expr, output.Variables[key]) &&
		!workflowGraphUnresolvedOutputReference(expr, output.Variables[normalizeWorkflowSkillName(key)])
}

func workflowGraphOutputValueByKey(output WorkflowStageOutput, key string) (any, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}
	if value, ok := output.Values[key]; ok {
		return value, true
	}
	normalized := normalizeWorkflowSkillName(key)
	if value, ok := output.Values[normalized]; ok {
		return value, true
	}
	if value, ok := workflowGraphNestedValue(output.Values, strings.Split(key, ".")); ok {
		return value, true
	}
	return nil, false
}

func workflowGraphUnresolvedOutputReference(expr, value string) bool {
	expr = strings.TrimSpace(expr)
	value = strings.TrimSpace(value)
	if expr == "" || value == "" || !strings.EqualFold(expr, value) {
		return false
	}
	lower := strings.ToLower(expr)
	return strings.HasPrefix(lower, "result.") ||
		strings.HasPrefix(lower, "stages.") ||
		strings.HasPrefix(lower, "workflow.")
}

func workflowGraphOutputHasKey(output WorkflowStageOutput, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if value := strings.TrimSpace(output.Variables[key]); value != "" {
		return true
	}
	normalized := normalizeWorkflowSkillName(key)
	if value := strings.TrimSpace(output.Variables[normalized]); value != "" {
		return true
	}
	if value, ok := output.Values[key]; ok {
		return workflowTruthyAny(value)
	}
	if value, ok := output.Values[normalized]; ok {
		return workflowTruthyAny(value)
	}
	if value, ok := workflowGraphNestedValue(output.Values, strings.Split(key, ".")); ok {
		return workflowTruthyAny(value)
	}
	if value, ok := workflowGraphNestedValue(output.Variables, strings.Split(key, ".")); ok {
		return workflowTruthyAny(value)
	}
	return false
}

func workflowGraphMissingDeclaredArtifactNames(stage workflowGraphStage, stageResult WorkflowStageResult) []string {
	if len(stage.Artifacts) == 0 {
		return nil
	}
	produced := make(map[string]struct{}, len(stageResult.Output.Artifacts))
	for _, artifact := range stageResult.Output.Artifacts {
		for _, value := range []string{artifact.Metadata["name"], artifact.ID, artifact.Title} {
			if key := normalizeWorkflowArtifactName(value); key != "" {
				produced[key] = struct{}{}
			}
		}
	}
	missing := make([]string, 0)
	for index, artifact := range stage.Artifacts {
		name := normalizeWorkflowArtifactName(artifact.Name)
		if name == "" {
			name = fmt.Sprintf("artifact-%d", index+1)
		}
		if _, ok := produced[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func workflowGraphStageRequiresJSONOutput(stage workflowGraphStage) bool {
	if workflowGraphStageWorkerContractEnabled(stage) || workflowGraphParamBool(stage, "json_output") || workflowGraphParamBool(stage, "require_json_output") || workflowGraphParamBool(stage, "json_required") {
		return true
	}
	contract := strings.ToLower(strings.TrimSpace(stage.Params["output_contract"]))
	return strings.Contains(contract, "return compact json") || strings.Contains(contract, "return json") || strings.Contains(contract, "json object")
}

func workflowGraphStageContractLabel(stage workflowGraphStage) string {
	return fallbackWorkflowGraphValue(firstNonEmptyWorkflowGraphParam(stage.Params, "worker_contract", "output_contract", "contract"), "output contract")
}

func workflowGraphStageRequiredJSONKeys(stage workflowGraphStage) []string {
	keys := make([]string, 0)
	if contract := strings.ToLower(strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params, "worker_contract", "contract"))); contract != "" {
		switch contract {
		case "engineering_v1":
			keys = append(keys, "summary", "changed_files", "evidence", "verification", "blockers", "next_actions")
		case "domain_slice_v1":
			keys = append(keys, "summary", "domain", "evidence", "blockers", "next_actions")
		}
	}
	if explicit := firstNonEmptyWorkflowGraphParam(stage.Params, "required_json_keys", "json_keys", "required_fields"); explicit != "" {
		keys = append(keys, splitWorkflowGraphList(explicit)...)
	}
	if parsed := workflowGraphJSONKeysFromOutputContract(stage.Params["output_contract"]); len(parsed) > 0 {
		keys = append(keys, parsed...)
	}
	return dedupeWorkflowGraphContractNames(keys)
}

func workflowGraphJSONKeysFromOutputContract(contract string) []string {
	contract = strings.TrimSpace(contract)
	lower := strings.ToLower(contract)
	index := strings.Index(lower, "json with ")
	if index < 0 {
		index = strings.Index(lower, "json object with ")
	}
	if index < 0 {
		return nil
	}
	segment := contract[index:]
	if dot := strings.Index(segment, "."); dot >= 0 {
		segment = segment[:dot]
	}
	if withIndex := strings.Index(strings.ToLower(segment), "with "); withIndex >= 0 {
		segment = segment[withIndex+len("with "):]
	}
	return workflowGraphContractNameList(segment)
}

func workflowGraphStageRequiredSections(stage workflowGraphStage) []string {
	sections := splitWorkflowGraphList(firstNonEmptyWorkflowGraphParam(stage.Params, "required_sections", "sections_required"))
	contract := strings.TrimSpace(stage.Params["output_contract"])
	lower := strings.ToLower(contract)
	switch {
	case strings.Contains(lower, "sections named "):
		index := strings.Index(lower, "sections named ")
		segment := contract[index+len("sections named "):]
		if dot := strings.Index(segment, "."); dot >= 0 {
			segment = segment[:dot]
		}
		sections = append(sections, workflowGraphContractNameList(segment)...)
	case strings.HasPrefix(lower, "emit "):
		segment := strings.TrimSpace(contract[len("emit "):])
		if dot := strings.Index(segment, "."); dot >= 0 {
			segment = segment[:dot]
		}
		if !strings.Contains(strings.ToLower(segment), "json") {
			sections = append(sections, workflowGraphContractNameList(segment)...)
		}
	}
	return dedupeWorkflowGraphContractNames(sections)
}

func workflowGraphContractNameList(value string) []string {
	value = strings.ReplaceAll(value, " and ", ",")
	value = strings.ReplaceAll(value, " or ", ",")
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, ".:;")
		part = strings.TrimPrefix(part, "the ")
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func dedupeWorkflowGraphContractNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := normalizeWorkflowSkillName(value)
		if key == "" {
			key = strings.ToLower(value)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func workflowGraphOutputContainsSection(output, section string) bool {
	output = strings.ToLower(strings.TrimSpace(output))
	section = strings.ToLower(strings.TrimSpace(section))
	if output == "" || section == "" {
		return false
	}
	normalizedSection := normalizeWorkflowSkillName(section)
	for _, candidate := range []string{
		section + ":",
		"## " + section,
		"### " + section,
		normalizedSection + ":",
	} {
		if strings.Contains(output, strings.ToLower(candidate)) {
			return true
		}
	}
	return strings.Contains(output, section)
}

func workflowGraphAcceptanceFailureDetail(item session.WorkflowRunAcceptanceSnapshot) string {
	label := strings.TrimSpace(firstWorkflowGraphValue(item.Name, item.Ref, "acceptance"))
	reason := strings.TrimSpace(firstWorkflowGraphValue(item.Reason, item.Description))
	if reason == "" {
		return label + " failed"
	}
	return fmt.Sprintf("%s failed: %s", label, reason)
}

func workflowGraphApplyContractFailureMetadata(stageResult *WorkflowStageResult, failure workflowGraphContractFailure) {
	if stageResult == nil || !failure.Failed {
		return
	}
	if stageResult.Metadata == nil {
		stageResult.Metadata = make(map[string]string)
	}
	stageResult.Status = "blocked"
	stageResult.Metadata["reason"] = failure.Reason
	stageResult.Metadata["severity"] = "error"
	stageResult.Metadata["contract_check"] = failure.Check
	stageResult.Metadata["source_ref"] = failure.SourceRef
	stageResult.Metadata["contract_failed"] = "true"
	stageResult.Metadata["acceptance_failed"] = strconv.Itoa(failure.FailedCount)
	if len(failure.Checks) > 0 {
		stageResult.Metadata["contract_checks"] = strings.Join(failure.Checks, ",")
	}
	if len(failure.SourceRefs) > 0 {
		stageResult.Metadata["source_refs"] = strings.Join(failure.SourceRefs, ",")
	}
	if failure.Route != "" {
		stageResult.Metadata["contract_route"] = failure.Route
	}
	stageResult.Result.Verification = append(stageResult.Result.Verification, schema.Verification{
		Kind:   "contract:" + fallbackWorkflowGraphValue(failure.Check, "acceptance"),
		Status: "failed",
		Detail: failure.Reason,
	})
	stageResult.Output.Verification = append(stageResult.Output.Verification, schema.Verification{
		Kind:   "contract:" + fallbackWorkflowGraphValue(failure.Check, "acceptance"),
		Status: "failed",
		Detail: failure.Reason,
	})
	applyWorkflowStageCanonicalOutputFields(&stageResult.Output)
}

func workflowGraphStageLooksFinal(stage workflowGraphStage) bool {
	if workflowGraphParamBool(stage, "final_report") ||
		workflowGraphParamBool(stage, "final") ||
		workflowGraphParamBool(stage, "delivery") ||
		workflowGraphParamBool(stage, "handoff") ||
		workflowGraphParamBool(stage, "summary") {
		return true
	}
	name := strings.ToLower(strings.TrimSpace(stage.Name))
	if name == "" {
		return false
	}
	return strings.Contains(name, "report") ||
		strings.Contains(name, "handoff") ||
		strings.Contains(name, "delivery") ||
		strings.Contains(name, "summary") ||
		strings.Contains(name, "最终") ||
		strings.Contains(name, "交付") ||
		strings.Contains(name, "总结")
}

func buildWorkflowGraphFinalQualityHandoff(completed []WorkflowStageResult) workflowGraphFinalQualityHandoff {
	handoff := workflowGraphFinalQualityHandoff{}
	if len(completed) == 0 {
		return handoff
	}
	for _, stage := range completed {
		handoff.addIssues(workflowGraphFinalQualityIssuesForStage(stage))
	}
	handoff.finalize()
	return handoff
}

func (h *workflowGraphFinalQualityHandoff) addIssues(issues []workflowGraphFinalQualityIssue) {
	for _, issue := range issues {
		if strings.TrimSpace(issue.Stage) == "" && strings.TrimSpace(issue.Check) == "" && strings.TrimSpace(issue.Reason) == "" {
			continue
		}
		if strings.TrimSpace(issue.Severity) == "" {
			if workflowGraphQualityStatusFailed(issue.Status) {
				issue.Severity = "error"
			} else {
				issue.Severity = "warning"
			}
		}
		h.Issues = append(h.Issues, issue)
		if issue.Severity == "error" || workflowGraphQualityStatusFailed(issue.Status) {
			h.FailedCount++
		} else {
			h.WarningCount++
		}
	}
}

func (h *workflowGraphFinalQualityHandoff) finalize() {
	if len(h.Issues) == 0 {
		return
	}
	h.Severity = "warning"
	if h.FailedCount > 0 {
		h.Severity = "error"
	}
	parts := make([]string, 0, len(h.Issues))
	sourceRefs := make([]string, 0, len(h.Issues))
	seenRefs := make(map[string]struct{}, len(h.Issues))
	for _, issue := range h.Issues {
		if len(parts) < 6 {
			parts = append(parts, workflowGraphFinalQualityIssueDetail(issue))
		}
		ref := workflowGraphFinalQualityIssueSourceRef(issue)
		if ref == "" {
			continue
		}
		key := normalizeWorkflowSkillName(ref)
		if _, ok := seenRefs[key]; ok {
			continue
		}
		seenRefs[key] = struct{}{}
		sourceRefs = append(sourceRefs, ref)
	}
	prefix := fmt.Sprintf("final report has %d unresolved quality issue(s)", len(h.Issues))
	if h.FailedCount > 0 || h.WarningCount > 0 {
		prefix = fmt.Sprintf("final report has %d unresolved quality issue(s): %d failed, %d warning", len(h.Issues), h.FailedCount, h.WarningCount)
	}
	if len(parts) > 0 {
		prefix += " - " + strings.Join(parts, "; ")
	}
	if len(h.Issues) > len(parts) {
		prefix += fmt.Sprintf("; +%d more", len(h.Issues)-len(parts))
	}
	h.Reason = prefix
	h.SourceRef = strings.Join(sourceRefs, "; ")
}

func workflowGraphFinalQualityIssuesForStage(stage WorkflowStageResult) []workflowGraphFinalQualityIssue {
	stageName := strings.TrimSpace(string(stage.Stage))
	issues := make([]workflowGraphFinalQualityIssue, 0)
	status := workflowGraphQualityStatus(stage.Status)
	if workflowGraphFinalQualityStageStatusIssue(status) {
		issues = append(issues, workflowGraphFinalQualityIssue{
			Stage:     stageName,
			Kind:      "stage_status",
			Check:     "stage.status",
			Status:    status,
			Reason:    fmt.Sprintf("%s status is %s", fallbackWorkflowGraphValue(stageName, "stage"), fallbackWorkflowGraphValue(stage.Status, "unknown")),
			SourceRef: stageName,
			Severity:  workflowGraphFinalQualitySeverityForStatus(status),
		})
	}
	if workflowTruthy(stage.Metadata["contract_failed"]) {
		issues = append(issues, workflowGraphFinalQualityIssue{
			Stage:     stageName,
			Kind:      "contract",
			Check:     fallbackWorkflowGraphValue(stage.Metadata["contract_check"], "contract"),
			Status:    fallbackWorkflowGraphValue(stage.Status, "failed"),
			Reason:    fallbackWorkflowGraphValue(stage.Metadata["reason"], fmt.Sprintf("%s contract failed", fallbackWorkflowGraphValue(stageName, "stage"))),
			SourceRef: fallbackWorkflowGraphValue(stage.Metadata["source_ref"], stageName),
			Severity:  fallbackWorkflowGraphValue(stage.Metadata["severity"], "error"),
		})
	}
	if workflowTruthy(stage.Metadata["quality_failed"]) {
		issues = append(issues, workflowGraphFinalQualityIssue{
			Stage:     stageName,
			Kind:      "quality_gate",
			Check:     fallbackWorkflowGraphValue(stage.Metadata["contract_check"], "quality_gate"),
			Status:    fallbackWorkflowGraphValue(stage.Status, "failed"),
			Reason:    fallbackWorkflowGraphValue(stage.Metadata["reason"], fmt.Sprintf("%s quality gate failed", fallbackWorkflowGraphValue(stageName, "stage"))),
			SourceRef: fallbackWorkflowGraphValue(stage.Metadata["source_ref"], stageName),
			Severity:  fallbackWorkflowGraphValue(stage.Metadata["severity"], "error"),
		})
	}
	for _, acceptance := range stage.Acceptance {
		status := workflowGraphQualityStatus(acceptance.Status)
		switch {
		case workflowGraphQualityStatusFailed(status):
			issues = append(issues, workflowGraphFinalQualityIssue{
				Stage:     stageName,
				Kind:      "acceptance",
				Check:     fallbackWorkflowGraphValue(acceptance.Name, "acceptance"),
				Status:    status,
				Reason:    workflowGraphAcceptanceFailureDetail(acceptance),
				SourceRef: workflowGraphFinalQualityAcceptanceSource(stageName, acceptance),
				Severity:  "error",
			})
		case status == "" || !workflowGraphQualityStatusPassed(status):
			issues = append(issues, workflowGraphFinalQualityIssue{
				Stage:     stageName,
				Kind:      "acceptance",
				Check:     fallbackWorkflowGraphValue(acceptance.Name, "acceptance"),
				Status:    fallbackWorkflowGraphValue(status, "unknown"),
				Reason:    workflowGraphFinalQualityUnknownDetail("acceptance", acceptance.Name, status),
				SourceRef: workflowGraphFinalQualityAcceptanceSource(stageName, acceptance),
				Severity:  "warning",
			})
		}
	}
	for _, verification := range stage.Result.Verification {
		status := workflowGraphQualityStatus(verification.Status)
		switch {
		case workflowGraphQualityStatusFailed(status):
			issues = append(issues, workflowGraphFinalQualityIssue{
				Stage:     stageName,
				Kind:      "verification",
				Check:     fallbackWorkflowGraphValue(verification.Kind, "verification"),
				Status:    status,
				Reason:    workflowGraphFinalQualityVerificationDetail(verification),
				SourceRef: workflowGraphFinalQualityVerificationSource(stageName, verification),
				Severity:  "error",
			})
		case status == "" || !workflowGraphQualityStatusPassed(status):
			issues = append(issues, workflowGraphFinalQualityIssue{
				Stage:     stageName,
				Kind:      "verification",
				Check:     fallbackWorkflowGraphValue(verification.Kind, "verification"),
				Status:    fallbackWorkflowGraphValue(status, "unknown"),
				Reason:    workflowGraphFinalQualityUnknownDetail("verification", verification.Kind, status),
				SourceRef: workflowGraphFinalQualityVerificationSource(stageName, verification),
				Severity:  "warning",
			})
		}
	}
	return workflowGraphDeduplicateFinalQualityIssues(issues)
}

func workflowGraphDeduplicateFinalQualityIssues(issues []workflowGraphFinalQualityIssue) []workflowGraphFinalQualityIssue {
	if len(issues) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(issues))
	out := make([]workflowGraphFinalQualityIssue, 0, len(issues))
	for _, issue := range issues {
		key := strings.Join([]string{
			normalizeWorkflowSkillName(issue.Stage),
			normalizeWorkflowSkillName(issue.Kind),
			normalizeWorkflowSkillName(issue.Check),
			normalizeWorkflowSkillName(issue.SourceRef),
			normalizeWorkflowSkillName(issue.Reason),
		}, "|")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, issue)
	}
	return out
}

func workflowGraphFinalQualityStageStatusIssue(status string) bool {
	switch workflowGraphQualityStatus(status) {
	case "failed", "fail", "failure", "error", "blocked", "denied", "invalid", "rejected", "cancelled", "canceled", "incomplete", "warning", "warn":
		return true
	default:
		return false
	}
}

func workflowGraphFinalQualitySeverityForStatus(status string) string {
	switch workflowGraphQualityStatus(status) {
	case "warning", "warn":
		return "warning"
	default:
		return "error"
	}
}

func workflowGraphFinalQualityAcceptanceSource(stageName string, acceptance session.WorkflowRunAcceptanceSnapshot) string {
	if ref := strings.TrimSpace(acceptance.Ref); ref != "" {
		return fmt.Sprintf("%s:%s", fallbackWorkflowGraphValue(stageName, "stage"), ref)
	}
	return stageName
}

func workflowGraphFinalQualityVerificationSource(stageName string, verification schema.Verification) string {
	if kind := strings.TrimSpace(verification.Kind); kind != "" {
		return fmt.Sprintf("%s:%s", fallbackWorkflowGraphValue(stageName, "stage"), kind)
	}
	return stageName
}

func workflowGraphFinalQualityVerificationDetail(verification schema.Verification) string {
	label := fallbackWorkflowGraphValue(verification.Kind, "verification")
	if detail := strings.TrimSpace(verification.Detail); detail != "" {
		return fmt.Sprintf("%s failed: %s", label, detail)
	}
	return label + " failed"
}

func workflowGraphFinalQualityUnknownDetail(kind, name, status string) string {
	label := fallbackWorkflowGraphValue(name, kind)
	status = fallbackWorkflowGraphValue(status, "empty")
	return fmt.Sprintf("%s %s has unresolved status %s", kind, label, status)
}

func workflowGraphFinalQualityIssueDetail(issue workflowGraphFinalQualityIssue) string {
	label := fallbackWorkflowGraphValue(issue.Check, issue.Kind)
	stage := fallbackWorkflowGraphValue(issue.Stage, "stage")
	detail := strings.TrimSpace(issue.Reason)
	if detail == "" {
		detail = fmt.Sprintf("%s %s is %s", issue.Kind, label, fallbackWorkflowGraphValue(issue.Status, "unresolved"))
	}
	return fmt.Sprintf("%s/%s: %s", stage, label, detail)
}

func workflowGraphFinalQualityIssueSourceRef(issue workflowGraphFinalQualityIssue) string {
	if ref := strings.TrimSpace(issue.SourceRef); ref != "" {
		return ref
	}
	if strings.TrimSpace(issue.Check) != "" {
		return fmt.Sprintf("%s:%s", fallbackWorkflowGraphValue(issue.Stage, "stage"), issue.Check)
	}
	return issue.Stage
}

func workflowGraphApplyFinalQualityHandoffMetadata(stageResult *WorkflowStageResult, handoff workflowGraphFinalQualityHandoff) {
	if stageResult == nil || !handoff.HasIssues() {
		return
	}
	if stageResult.Metadata == nil {
		stageResult.Metadata = make(map[string]string)
	}
	if strings.EqualFold(strings.TrimSpace(stageResult.Status), "completed") {
		stageResult.Status = "warning"
	}
	stageResult.Metadata["unresolved_quality"] = "true"
	stageResult.Metadata["quality_failed"] = "true"
	stageResult.Metadata["contract_check"] = "final_quality_handoff"
	stageResult.Metadata["source_ref"] = handoff.SourceRef
	stageResult.Metadata["reason"] = handoff.Reason
	stageResult.Metadata["severity"] = handoff.Severity
	stageResult.Metadata["unresolved_quality_count"] = strconv.Itoa(len(handoff.Issues))
	stageResult.Metadata["unresolved_quality_failed"] = strconv.Itoa(handoff.FailedCount)
	stageResult.Metadata["unresolved_quality_warnings"] = strconv.Itoa(handoff.WarningCount)
	verification := schema.Verification{
		Kind:   "final_quality_handoff",
		Status: "warning",
		Detail: handoff.Reason,
	}
	if handoff.FailedCount > 0 {
		verification.Status = "failed"
	}
	stageResult.Result.Verification = append(stageResult.Result.Verification, verification)
	stageResult.Output.Verification = append(stageResult.Output.Verification, verification)
	workflowStageSetOutputValue(&stageResult.Output, "unresolved_quality", true)
	workflowStageSetOutputValue(&stageResult.Output, "unresolved_quality_count", len(handoff.Issues))
	workflowStageSetOutputValue(&stageResult.Output, "unresolved_quality_failed", handoff.FailedCount)
	workflowStageSetOutputValue(&stageResult.Output, "unresolved_quality_warnings", handoff.WarningCount)
	workflowStageSetOutputValue(&stageResult.Output, "unresolved_quality_source_ref", handoff.SourceRef)
	applyWorkflowStageCanonicalOutputFields(&stageResult.Output)
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
		result := copyAgentResultForWorkflow(snapshot.Result)
		output := WorkflowStageOutput{
			Summary:      fallbackWorkflowGraphValue(snapshot.Summary, truncateSummary(result.Output)),
			RawOutput:    limitWorkflowGraphText(result.Output),
			Variables:    copyStringMap(snapshot.Outputs),
			Values:       copyWorkflowAnyMap(snapshot.OutputValues),
			Artifacts:    append([]session.WorkflowRunArtifact(nil), snapshot.Artifacts...),
			ToolResults:  append([]schema.ToolResult(nil), result.ToolResults...),
			Findings:     append([]schema.Finding(nil), result.Findings...),
			Changes:      append([]schema.Change(nil), result.Changes...),
			Verification: append([]schema.Verification(nil), result.Verification...),
		}
		applyWorkflowStageCanonicalOutputFields(&output)
		results = append(results, WorkflowStageResult{
			Stage:                       WorkflowStage(snapshot.Stage),
			Agent:                       snapshot.AgentID,
			NodeType:                    snapshot.NodeType,
			Skill:                       snapshot.Skill,
			Tool:                        snapshot.Tool,
			Status:                      snapshot.Status,
			Attempts:                    snapshot.Attempts,
			Input:                       copyStringMap(snapshot.Inputs),
			InputValues:                 copyWorkflowAnyMap(snapshot.InputValues),
			Metadata:                    copyStringMap(snapshot.Metadata),
			Result:                      result,
			Output:                      output,
			Acceptance:                  append([]session.WorkflowRunAcceptanceSnapshot(nil), snapshot.Acceptance...),
			BudgetScope:                 snapshot.BudgetScope,
			BudgetReason:                snapshot.BudgetReason,
			BudgetMetric:                snapshot.BudgetMetric,
			BudgetUsed:                  snapshot.BudgetUsed,
			BudgetSoftLimit:             snapshot.BudgetSoftLimit,
			BudgetHardLimit:             snapshot.BudgetHardLimit,
			BudgetRemaining:             snapshot.BudgetRemaining,
			BudgetPromptTokens:          snapshot.BudgetPromptTokens,
			BudgetEstimatedPromptTokens: snapshot.BudgetEstimatedPromptTokens,
			BudgetNetPromptTokens:       snapshot.BudgetNetPromptTokens,
			BudgetGrossPromptTokens:     snapshot.BudgetGrossPromptTokens,
			BudgetSavedTokens:           snapshot.BudgetSavedTokens,
			BudgetMemorySavedTokens:     snapshot.BudgetMemorySavedTokens,
			BudgetHistorySavedTokens:    snapshot.BudgetHistorySavedTokens,
			BudgetArtifactSavedTokens:   snapshot.BudgetArtifactSavedTokens,
			BudgetSkillSavedTokens:      snapshot.BudgetSkillSavedTokens,
			BudgetToolSchemaSavedTokens: snapshot.BudgetToolSchemaSavedTokens,
			BudgetReportedPromptTokens:  snapshot.BudgetReportedPromptTokens,
			BudgetOutputTokens:          snapshot.BudgetOutputTokens,
			BudgetCachedTokens:          snapshot.BudgetCachedTokens,
			BudgetTotalTokens:           snapshot.BudgetTotalTokens,
			BudgetLLMCalls:              snapshot.BudgetLLMCalls,
			BudgetContinuations:         snapshot.BudgetContinuations,
			BudgetEstimatedInputCost:    snapshot.BudgetEstimatedInputCost,
			BudgetEstimatedOutputCost:   snapshot.BudgetEstimatedOutputCost,
			BudgetEstimatedTotalCost:    snapshot.BudgetEstimatedTotalCost,
			BudgetCostCurrency:          snapshot.BudgetCostCurrency,
			BudgetPricingSource:         snapshot.BudgetPricingSource,
		})
	}
	return results
}

func workflowGraphFindIncompleteStageResult(stages []WorkflowStageResult, stageName string) (WorkflowStageResult, bool) {
	target := normalizeWorkflowSkillName(stageName)
	for i := len(stages) - 1; i >= 0; i-- {
		stage := stages[i]
		if normalizeWorkflowSkillName(string(stage.Stage)) != target {
			continue
		}
		if stage.Result.Incomplete || strings.EqualFold(strings.TrimSpace(stage.Status), "incomplete") || strings.EqualFold(strings.TrimSpace(stage.Metadata["incomplete"]), "true") {
			return stage, true
		}
	}
	return WorkflowStageResult{}, false
}

func workflowGraphRemoveStageResult(stages []WorkflowStageResult, stageName string) []WorkflowStageResult {
	if len(stages) == 0 {
		return nil
	}
	target := normalizeWorkflowSkillName(stageName)
	out := make([]WorkflowStageResult, 0, len(stages))
	for _, stage := range stages {
		if normalizeWorkflowSkillName(string(stage.Stage)) == target {
			continue
		}
		out = append(out, stage)
	}
	return out
}

func workflowGraphContinueOutputPrompt(base string, incomplete WorkflowStageResult) string {
	partial := strings.TrimSpace(incomplete.Result.Output)
	if partial == "" {
		partial = strings.TrimSpace(incomplete.Output.RawOutput)
	}
	reason := strings.TrimSpace(incomplete.Result.IncompleteReason)
	if reason == "" {
		reason = strings.TrimSpace(incomplete.Metadata["incomplete_reason"])
	}
	stopReason := strings.TrimSpace(incomplete.Result.StopReason)
	if stopReason == "" {
		stopReason = strings.TrimSpace(incomplete.Metadata["stop_reason"])
	}
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(base))
	builder.WriteString("\n\nContinuation instructions:\n")
	builder.WriteString("- The previous run of this exact stage was incomplete. Continue the stage output; do not restart the task or discard prior work.\n")
	builder.WriteString("- Use the partial output below as already produced context, complete missing sections, evidence, and final decisions required by this stage.\n")
	builder.WriteString("- If the partial output contains JSON or another structured format, return one coherent completed artifact, not only a fragment.\n")
	if reason != "" {
		builder.WriteString("- Incomplete reason: ")
		builder.WriteString(reason)
		builder.WriteString("\n")
	}
	if stopReason != "" {
		builder.WriteString("- Previous stop reason: ")
		builder.WriteString(stopReason)
		builder.WriteString("\n")
	}
	if partial != "" {
		builder.WriteString("\nPartial output from previous attempt:\n")
		builder.WriteString(limitWorkflowGraphText(partial))
	}
	return builder.String()
}

func workflowGraphModelEscalationPrompt(base string, failed WorkflowStageResult) string {
	reason := strings.TrimSpace(firstWorkflowGraphValue(failed.Metadata["reason"], failed.BudgetReason))
	check := strings.TrimSpace(failed.Metadata["contract_check"])
	sourceRef := strings.TrimSpace(failed.Metadata["source_ref"])
	output := strings.TrimSpace(firstWorkflowGraphValue(failed.Result.Output, failed.Output.RawOutput))
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(base))
	builder.WriteString("\n\nModel escalation instructions:\n")
	builder.WriteString("- The previous answer for this same workflow stage failed a required output contract. Re-run the stage with the escalated model and produce a complete corrected answer before downstream work continues.\n")
	builder.WriteString("- Preserve the task intent, but fix the contract gap directly. Do not mark the stage complete unless the required structure, sections, artifacts, evidence, and acceptance requirements are satisfied.\n")
	if check != "" {
		builder.WriteString("- Contract check: ")
		builder.WriteString(check)
		builder.WriteString("\n")
	}
	if sourceRef != "" {
		builder.WriteString("- Source reference: ")
		builder.WriteString(sourceRef)
		builder.WriteString("\n")
	}
	if reason != "" {
		builder.WriteString("- Failure reason: ")
		builder.WriteString(reason)
		builder.WriteString("\n")
	}
	if output != "" {
		builder.WriteString("\nPrevious failed output:\n")
		builder.WriteString(limitWorkflowGraphText(output))
	}
	return builder.String()
}

func workflowGraphContinueOutputMetadata(incomplete WorkflowStageResult) map[string]string {
	metadata := map[string]string{
		"continued_from_incomplete": "true",
	}
	if stopReason := strings.TrimSpace(incomplete.Result.StopReason); stopReason != "" {
		metadata["previous_stop_reason"] = stopReason
	} else if stopReason := strings.TrimSpace(incomplete.Metadata["stop_reason"]); stopReason != "" {
		metadata["previous_stop_reason"] = stopReason
	}
	if count := incomplete.Result.ContinuationCount; count > 0 {
		metadata["previous_continuation_count"] = strconv.Itoa(count)
	} else if count := strings.TrimSpace(incomplete.Metadata["continuation_count"]); count != "" {
		metadata["previous_continuation_count"] = count
	}
	for _, key := range []string{"parallel_branch", "parallel_parent", "repeat", "repeat_kind", "repeat_control", "original_stage", "iteration_index", "iteration_number", "item"} {
		if value := strings.TrimSpace(incomplete.Metadata[key]); value != "" {
			metadata[key] = value
		}
	}
	return workflowStageMetadata(metadata)
}

func workflowGraphContinueOutputEventContent(incomplete WorkflowStageResult) string {
	stage := strings.TrimSpace(string(incomplete.Stage))
	if stage == "" {
		stage = "workflow stage"
	}
	reason := strings.TrimSpace(incomplete.Result.IncompleteReason)
	if reason == "" {
		reason = strings.TrimSpace(incomplete.Metadata["incomplete_reason"])
	}
	if reason == "" {
		return fmt.Sprintf("continuing incomplete output for %s", stage)
	}
	return fmt.Sprintf("continuing incomplete output for %s: %s", stage, reason)
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

func workflowGraphStageNamesForIndicesList(graph workflowGraph, indices []int) []string {
	if len(indices) == 0 {
		return nil
	}
	names := make([]string, 0, len(indices))
	for _, index := range indices {
		if index >= 0 && index < len(graph.Stages) {
			names = append(names, graph.Stages[index].Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func (w *WorkflowRunner) workflowGraphSelectedParallelStageIndices(graph workflowGraph, stage workflowGraphStage, candidates []int, request string, completed []WorkflowStageResult) []int {
	if len(candidates) == 0 {
		return nil
	}
	names := workflowGraphSelectedParallelStageNames(stage, request, completed)
	selected := filterExplicitWorkflowGraphStages(names, graph, candidates)
	if domainSelected := w.workflowGraphParallelDomainStageIndices(graph, stage, candidates, request, completed); len(domainSelected) > 0 {
		if len(selected) > 0 {
			if intersected := intersectWorkflowGraphStageIndices(selected, domainSelected); len(intersected) > 0 {
				selected = intersected
			} else {
				selected = domainSelected
			}
		} else {
			selected = domainSelected
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return limitWorkflowGraphStageIndices(selected, workflowGraphParallelMaxBranches(stage, request, completed))
}

func workflowGraphSelectedParallelStageNames(stage workflowGraphStage, request string, completed []WorkflowStageResult) []string {
	var names []string
	for _, key := range []string{"active_branches", "branches", "selected_branches", "run_branches"} {
		names = append(names, workflowGraphCSVStageNames(stage.Params[key])...)
	}
	for _, key := range []string{"active_branches_ref", "branches_ref", "selected_branches_ref", "run_branches_ref"} {
		ref := strings.TrimSpace(stage.Params[key])
		if ref == "" {
			continue
		}
		if value, ok := resolveWorkflowGraphReferenceValue(ref, request, completed); ok {
			names = append(names, workflowGraphStageNamesFromValue(value)...)
			continue
		}
		names = append(names, workflowGraphCSVStageNames(resolveWorkflowGraphReference(ref, request, completed))...)
	}
	return workflowGraphUniqueStageNames(names)
}

func workflowGraphParallelSelectionConfigured(stage workflowGraphStage) bool {
	for _, key := range []string{
		"active_branches", "branches", "selected_branches", "run_branches",
		"active_branches_ref", "branches_ref", "selected_branches_ref", "run_branches_ref",
		"domain_ref", "selected_domain_ref", "primary_domain_ref", "domain",
		"fallback_branch", "fallback_branches", "default_branch", "default_branches",
	} {
		if strings.TrimSpace(stage.Params[key]) != "" {
			return true
		}
	}
	return false
}

func (w *WorkflowRunner) workflowGraphParallelDomainStageIndices(graph workflowGraph, stage workflowGraphStage, candidates []int, request string, completed []WorkflowStageResult) []int {
	domain := workflowGraphParallelDomainValue(stage, request, completed)
	if domain == "" || strings.EqualFold(domain, "auto") || strings.EqualFold(domain, "multi") || strings.EqualFold(domain, "multiple") {
		return nil
	}
	return filterExplicitWorkflowGraphStages(workflowGraphParallelDomainStageNames(domain), graph, candidates)
}

func workflowGraphParallelDomainValue(stage workflowGraphStage, request string, completed []WorkflowStageResult) string {
	for _, key := range []string{"domain_ref", "selected_domain_ref", "primary_domain_ref"} {
		ref := strings.TrimSpace(stage.Params[key])
		if ref == "" {
			continue
		}
		if value, ok := resolveWorkflowGraphReferenceValue(ref, request, completed); ok {
			if text := strings.TrimSpace(workflowGraphValueString(value)); text != "" {
				return text
			}
		}
		if text := strings.TrimSpace(resolveWorkflowGraphReference(ref, request, completed)); text != "" && text != ref {
			return text
		}
	}
	return strings.TrimSpace(stage.Params["domain"])
}

func workflowGraphParallelDomainStageNames(domain string) []string {
	switch normalizeWorkflowSkillName(domain) {
	case "software", "software_engineering", "code", "coding", "implementation":
		return []string{"software-worker", "software-engineer", "software-engineering"}
	case "web_security", "web", "webapp", "web_application", "xss", "sqli", "sql_injection":
		return []string{"web-security-worker", "web-security-researcher", "web-security"}
	case "security", "security_research", "vulnerability", "vulnerability_research":
		return []string{"security-worker", "security-researcher", "security-research"}
	case "binary", "binary_analysis", "reverse", "reverse_engineering":
		return []string{"binary-worker", "binary-analyst", "binary-analysis"}
	case "documentation", "docs", "doc":
		return []string{"docs-worker", "documentation-specialist", "documentation"}
	case "operations", "ops", "network", "network_operations", "runbook":
		return []string{"ops-worker", "operations-specialist", "operations-runbook"}
	case "support", "customer_support", "customer":
		return []string{"support-worker", "support-specialist", "customer-support"}
	case "platform", "agent_framework", "framework", "extension", "extension_kit":
		return []string{"platform-worker", "framework-extension-architect", "agent-framework"}
	case "general":
		return []string{"general-worker", "general"}
	default:
		return []string{domain}
	}
}

func (w *WorkflowRunner) workflowGraphParallelFallbackStageIndices(graph workflowGraph, stage workflowGraphStage, candidates []int, completed []WorkflowStageResult) []int {
	var names []string
	for _, key := range []string{"fallback_branch", "fallback_branches", "default_branch", "default_branches"} {
		names = append(names, workflowGraphCSVStageNames(stage.Params[key])...)
	}
	if len(names) == 0 {
		return nil
	}
	return filterExplicitWorkflowGraphStages(names, graph, candidates)
}

func workflowGraphParallelMaxBranches(stage workflowGraphStage, request string, completed []WorkflowStageResult) int {
	for _, key := range []string{"max_parallel_branches_ref", "max_parallel_domains_ref", "max_branches_ref", "limit_ref"} {
		ref := strings.TrimSpace(stage.Params[key])
		if ref == "" {
			continue
		}
		if value, ok := resolveWorkflowGraphReferenceValue(ref, request, completed); ok {
			if max := workflowGraphPositiveInt(value); max > 0 {
				return max
			}
		}
		if max := workflowGraphPositiveInt(resolveWorkflowGraphReference(ref, request, completed)); max > 0 {
			return max
		}
	}
	for _, key := range []string{"max_parallel_branches", "max_parallel_domains", "max_branches", "limit"} {
		if max := workflowGraphPositiveInt(stage.Params[key]); max > 0 {
			return max
		}
	}
	return 0
}

func (w *WorkflowRunner) workflowGraphSoftBudgetParallelMaxBranches(stage workflowGraphStage, request string, completed []WorkflowStageResult) int {
	if w == nil || !w.workflowGraphSoftBudgetWarningEmitted("") {
		return 0
	}
	for _, key := range []string{"soft_budget_max_parallel_branches_ref", "soft_budget_max_branches_ref", "soft_budget.max_parallel_branches_ref", "budget.soft_max_parallel_branches_ref", "budget_soft_max_parallel_branches_ref"} {
		ref := strings.TrimSpace(stage.Params[key])
		if ref == "" {
			continue
		}
		if value, ok := resolveWorkflowGraphReferenceValue(ref, request, completed); ok {
			if max := workflowGraphPositiveInt(value); max > 0 {
				return max
			}
		}
		if max := workflowGraphPositiveInt(resolveWorkflowGraphReference(ref, request, completed)); max > 0 {
			return max
		}
	}
	for _, key := range []string{"soft_budget_max_parallel_branches", "soft_budget_max_branches", "soft_budget.max_parallel_branches", "budget.soft_max_parallel_branches", "budget_soft_max_parallel_branches"} {
		if max := workflowGraphPositiveInt(stage.Params[key]); max > 0 {
			return max
		}
	}
	return 0
}

func workflowGraphPositiveInt(value any) int {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return typed
		}
	case int64:
		if typed > 0 {
			return int(typed)
		}
	case float64:
		if typed > 0 {
			return int(typed)
		}
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil && parsed > 0 {
			return int(parsed)
		}
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(workflowGraphValueString(value)))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func workflowGraphStageIndexDifference(all, selected []int) []int {
	if len(all) == 0 {
		return nil
	}
	selectedSet := make(map[int]struct{}, len(selected))
	for _, index := range selected {
		selectedSet[index] = struct{}{}
	}
	out := make([]int, 0)
	for _, index := range all {
		if _, ok := selectedSet[index]; ok {
			continue
		}
		out = append(out, index)
	}
	return out
}

func limitWorkflowGraphStageIndices(indices []int, max int) []int {
	if max <= 0 || len(indices) <= max {
		return indices
	}
	return append([]int(nil), indices[:max]...)
}

func intersectWorkflowGraphStageIndices(left []int, right []int) []int {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	allowed := make(map[int]struct{}, len(right))
	for _, index := range right {
		allowed[index] = struct{}{}
	}
	out := make([]int, 0, len(left))
	for _, index := range left {
		if _, ok := allowed[index]; ok {
			out = append(out, index)
		}
	}
	return out
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

func (w *WorkflowRunner) buildWorkflowGraphStagePrompt(ctx context.Context, graph workflowGraph, stage workflowGraphStage, request string, completed []WorkflowStageResult) string {
	return buildWorkflowGraphStagePromptWithMemory(ctx, graph, stage, request, completed, w.workflowGraphMemoryStore())
}

func (w *WorkflowRunner) workflowGraphMemoryStore() *memory.Store {
	if w == nil || w.runtime == nil {
		return nil
	}
	return w.runtime.MemoryStore()
}

func buildWorkflowGraphStagePrompt(graph workflowGraph, stage workflowGraphStage, request string, completed []WorkflowStageResult) string {
	return buildWorkflowGraphStagePromptWithMemory(context.Background(), graph, stage, request, completed, nil)
}

func buildWorkflowGraphStagePromptWithMemory(ctx context.Context, graph workflowGraph, stage workflowGraphStage, request string, completed []WorkflowStageResult, store *memory.Store) string {
	var builder strings.Builder
	budget := workflowGraphStagePromptBudget(stage.Context)
	fmt.Fprintf(&builder, "Run workflow %q stage %q for the following request.\n\nOriginal request:\n%s\n", graph.Name, stage.Name, workflowGraphBudgetText(request, budget.RequestBytes, "original request truncated by request_max_tokens"))
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
		paramsText := workflowGraphStagePromptParameters(stage, budget.ParametersBytes)
		if paramsText != "" {
			builder.WriteString(paramsText)
		}
	}
	if inputs := resolveWorkflowGraphStageInputs(stage, request, completed); len(inputs) > 0 {
		builder.WriteString("\nMapped stage inputs:\n")
		keys := make([]string, 0, len(inputs))
		for key := range inputs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		inputBudget := budget.InputsBytes
		usedInputBytes := 0
		for _, key := range keys {
			linePrefix := fmt.Sprintf("- %s (%s): ", key, stage.Input[key])
			value := workflowGraphPromptValue(inputs[key])
			if inputBudget > 0 {
				remaining := inputBudget - usedInputBytes - len([]byte(linePrefix)) - 1
				if remaining <= 32 {
					fmt.Fprintf(&builder, "- %s (%s): [mapped inputs truncated by inputs_max_tokens]\n", key, stage.Input[key])
					break
				}
				value = workflowGraphBudgetText(value, remaining, "mapped input truncated by inputs_max_tokens")
			}
			line := linePrefix + value
			usedInputBytes += len([]byte(line)) + 1
			fmt.Fprintf(&builder, "%s\n", line)
		}
	}
	if len(completed) > 0 {
		if workflowGraphContextConfigured(stage.Context) {
			contextText := buildWorkflowGraphContractContext(ctx, store, stage, request, completed)
			if strings.TrimSpace(contextText) != "" {
				builder.WriteString(contextText)
			}
		} else {
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
	}
	if len(stage.Next) > 0 {
		fmt.Fprintf(&builder, "\nCandidate next stages: %s\n", strings.Join(stage.Next, ", "))
		if isWorkflowGraphSelectStrategy(stage.NextStrategy) {
			builder.WriteString("If you need a specific next stage, end with 'Next skill: <skill-or-stage-name>' or 'Next skills: <name>, <name>'.\n")
		}
	}
	builder.WriteString("\nUse the configured stage skill instructions. Produce a concise stage result.")
	builder.WriteString(workflowGraphStageWorkerContractPrompt(stage))
	builder.WriteString("\nReturn structured fields when applicable: summary, evidence, artifacts, decision, changed_files, verification, blockers, next_actions.")
	return workflowGraphBudgetPrompt(builder.String(), budget.PromptBytes)
}

type workflowGraphStagePromptBudgetSpec struct {
	PromptBytes     int
	RequestBytes    int
	InputsBytes     int
	ParametersBytes int
}

func workflowGraphStagePromptBudget(context workflowGraphStageContext) workflowGraphStagePromptBudgetSpec {
	return workflowGraphStagePromptBudgetSpec{
		PromptBytes:     workflowGraphTokenBudgetBytes(context.PromptMaxTokens),
		RequestBytes:    workflowGraphTokenBudgetBytes(context.RequestMaxTokens),
		InputsBytes:     workflowGraphTokenBudgetBytes(context.InputsMaxTokens),
		ParametersBytes: workflowGraphTokenBudgetBytes(context.ParametersMaxTokens),
	}
}

func workflowGraphTokenBudgetBytes(tokens int) int {
	if tokens <= 0 {
		return 0
	}
	return tokens * 4
}

func workflowGraphBudgetText(value string, maxBytes int, marker string) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len([]byte(value)) <= maxBytes {
		return value
	}
	suffix := " [" + strings.TrimSpace(marker) + "]"
	remaining := maxBytes - len([]byte(suffix))
	if remaining < 32 {
		remaining = maxBytes
		suffix = ""
	}
	return strings.TrimSpace(trimWorkflowGraphContextValue(value, remaining)) + suffix
}

func workflowGraphBudgetPrompt(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len([]byte(value)) <= maxBytes {
		return value
	}
	const tailMarker = "\nUse the configured stage skill instructions."
	index := strings.LastIndex(value, tailMarker)
	if index <= 0 {
		return workflowGraphBudgetText(value, maxBytes, "workflow stage prompt truncated by prompt_max_tokens")
	}
	tail := strings.TrimSpace(value[index:])
	marker := "\n[workflow stage prompt middle truncated by prompt_max_tokens]\n"
	tailBytes := len([]byte(tail))
	markerBytes := len([]byte(marker))
	if tailBytes+markerBytes+64 >= maxBytes {
		return workflowGraphBudgetText(value, maxBytes, "workflow stage prompt truncated by prompt_max_tokens")
	}
	prefixBudget := maxBytes - tailBytes - markerBytes
	prefix := trimWorkflowGraphContextValue(value[:index], prefixBudget)
	return strings.TrimSpace(prefix) + marker + tail
}

func workflowGraphPromptValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "\n") || len([]byte(value)) > 480 {
		return workflowGraphCompactRawReference(value)
	}
	return value
}

func workflowGraphStagePromptParameters(stage workflowGraphStage, maxBytes int) string {
	keys := workflowGraphStagePromptParamKeys(stage.Params)
	if len(keys) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Stage parameters:\n")
	for _, key := range keys {
		fmt.Fprintf(&builder, "- %s: %s\n", key, workflowGraphPromptValue(stage.Params[key]))
	}
	return workflowGraphBudgetText(builder.String(), maxBytes, "stage parameters truncated by parameters_max_tokens")
}

func workflowGraphStagePromptParamKeys(params map[string]string) []string {
	if len(params) == 0 {
		return nil
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		if workflowGraphStagePromptParamHidden(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		leftRank := workflowGraphStagePromptParamRank(keys[i])
		rightRank := workflowGraphStagePromptParamRank(keys[j])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return strings.ToLower(keys[i]) < strings.ToLower(keys[j])
	})
	return keys
}

func workflowGraphStagePromptParamRank(key string) int {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "purpose", "goal", "objective":
		return 0
	case "output_contract", "worker_contract", "contract", "output_format":
		return 1
	case "acceptance", "acceptance_bar", "quality_bar", "quality_policy":
		return 2
	case "token_policy", "context_policy", "handoff_policy":
		return 3
	case "edit_policy", "verification_policy", "validation_policy", "failure_policy", "risk_scope":
		return 4
	case "ownership", "tools", "allowed_tools", "tool":
		return 5
	default:
		return 10
	}
}

func workflowGraphStagePromptParamHidden(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "fields_json", "schema_json", "ui_schema", "template_json", "raw_schema", "large_context", "debug_prompt":
		return true
	default:
		return false
	}
}

func workflowGraphStageWorkerContractPrompt(stage workflowGraphStage) string {
	if !workflowGraphStageWorkerContractEnabled(stage) {
		return ""
	}
	return "\nWorker contract engineering_v1: return one compact JSON object with keys summary, changed_files, evidence, verification, blockers, next_actions. Keep values short; reference artifacts or file paths instead of copying full logs or diffs."
}

func workflowGraphStageWorkerContractEnabled(stage workflowGraphStage) bool {
	for _, key := range []string{"worker_contract", "output_format", "contract"} {
		value := strings.ToLower(strings.TrimSpace(stage.Params[key]))
		if value == "engineering_v1" || value == "worker_json" || value == "worker-json" || value == "json_worker" {
			return true
		}
	}
	return false
}

func workflowGraphContextConfigured(context workflowGraphStageContext) bool {
	return len(context.Include) > 0 ||
		len(context.Exclude) > 0 ||
		context.MaxTokens > 0 ||
		context.PromptMaxTokens > 0 ||
		context.RequestMaxTokens > 0 ||
		context.InputsMaxTokens > 0 ||
		context.ParametersMaxTokens > 0 ||
		context.Retrieval.Enabled ||
		strings.TrimSpace(context.Retrieval.Query) != ""
}

func workflowGraphContextMetadata(context workflowGraphStageContext) map[string]string {
	if !workflowGraphContextConfigured(context) {
		return nil
	}
	metadata := map[string]string{}
	if len(context.Include) > 0 {
		metadata["context.include"] = strings.Join(trimWorkflowGraphStringList(context.Include), ",")
	}
	if len(context.Exclude) > 0 {
		metadata["context.exclude"] = strings.Join(trimWorkflowGraphStringList(context.Exclude), ",")
	}
	if context.MaxTokens > 0 {
		metadata["context.max_tokens"] = strconv.Itoa(context.MaxTokens)
	}
	if context.PromptMaxTokens > 0 {
		metadata["context.prompt_max_tokens"] = strconv.Itoa(context.PromptMaxTokens)
	}
	if context.RequestMaxTokens > 0 {
		metadata["context.request_max_tokens"] = strconv.Itoa(context.RequestMaxTokens)
	}
	if context.InputsMaxTokens > 0 {
		metadata["context.inputs_max_tokens"] = strconv.Itoa(context.InputsMaxTokens)
	}
	if context.ParametersMaxTokens > 0 {
		metadata["context.parameters_max_tokens"] = strconv.Itoa(context.ParametersMaxTokens)
	}
	if context.Retrieval.Enabled {
		metadata["context.retrieval.enabled"] = "true"
	}
	if query := strings.TrimSpace(context.Retrieval.Query); query != "" {
		metadata["context.retrieval.query"] = query
	}
	return metadata
}

func buildWorkflowGraphContractContext(ctx context.Context, store *memory.Store, stage workflowGraphStage, request string, completed []WorkflowStageResult) string {
	entries := workflowGraphContextEntries(ctx, store, stage, request, completed)
	if len(entries) == 0 {
		return "\nSelected context:\n- No prior context selected by this stage contract.\n"
	}
	var builder strings.Builder
	builder.WriteString("\nSelected context:\n")
	for _, entry := range entries {
		if strings.TrimSpace(entry.Value) == "" {
			continue
		}
		fmt.Fprintf(&builder, "- %s: %s\n", entry.Ref, truncateSummary(entry.Value))
	}
	return builder.String()
}

type workflowGraphContextEntry struct {
	Ref   string
	Value string
}

func workflowGraphContextEntries(ctx context.Context, store *memory.Store, stage workflowGraphStage, request string, completed []WorkflowStageResult) []workflowGraphContextEntry {
	include := trimWorkflowGraphStringList(stage.Context.Include)
	exclude := trimWorkflowGraphStringList(stage.Context.Exclude)
	maxTokens := stage.Context.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1800
	}
	entries := make([]workflowGraphContextEntry, 0, len(include)+len(stage.Input))
	if len(include) == 0 {
		for _, prior := range completed {
			entries = append(entries, workflowGraphPriorSummaryContextEntry(prior))
		}
	} else {
		for _, ref := range include {
			entries = append(entries, workflowGraphContextEntriesForRef(ctx, store, ref, request, completed)...)
		}
	}
	entries = append(entries, workflowGraphContextEntriesFromStageInputs(stage, request, completed)...)
	entries = append(entries, workflowGraphRetrievalContextEntries(ctx, store, stage, request, completed)...)
	entries = dedupeWorkflowGraphContextEntries(entries)
	entries = filterWorkflowGraphContextEntries(entries, exclude)
	return limitWorkflowGraphContextEntries(entries, maxTokens)
}

func workflowGraphContextEntriesFromStageInputs(stage workflowGraphStage, request string, completed []WorkflowStageResult) []workflowGraphContextEntry {
	if len(stage.Input) == 0 {
		return nil
	}
	keys := make([]string, 0, len(stage.Input))
	for key := range stage.Input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]workflowGraphContextEntry, 0, len(keys))
	for _, key := range keys {
		ref := strings.TrimSpace(stage.Input[key])
		value := resolveWorkflowGraphReference(ref, request, completed)
		if strings.TrimSpace(value) == "" {
			continue
		}
		entries = append(entries, workflowGraphContextEntry{
			Ref:   "input." + key + " (" + ref + ")",
			Value: limitWorkflowGraphText(value),
		})
	}
	return entries
}

func workflowGraphRetrievalContextEntries(ctx context.Context, store *memory.Store, stage workflowGraphStage, request string, completed []WorkflowStageResult) []workflowGraphContextEntry {
	if store == nil || !stage.Context.Retrieval.Enabled {
		return nil
	}
	query := strings.TrimSpace(stage.Context.Retrieval.Query)
	if query == "" {
		query = request
	} else {
		query = renderWorkflowGraphContextQuery(query, request, completed)
	}
	if strings.TrimSpace(query) == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	memoryContext, err := store.PromptContextFresh(ctx, query, "")
	if err != nil || len(memoryContext.Blocks) == 0 {
		return nil
	}
	entries := make([]workflowGraphContextEntry, 0, len(memoryContext.Blocks))
	for _, block := range memoryContext.Blocks {
		ref := strings.TrimSpace(block.Ref)
		if ref == "" {
			ref = strings.TrimSpace(block.Title)
		}
		if ref == "" {
			ref = strings.TrimSpace(block.Kind)
		}
		if ref == "" || strings.TrimSpace(block.Summary) == "" {
			continue
		}
		entries = append(entries, workflowGraphContextEntry{
			Ref:   "memory.search." + strings.TrimSpace(block.Kind) + "." + ref,
			Value: workflowGraphMemoryBlockContextValue(block),
		})
		if len(entries) >= 4 {
			break
		}
	}
	return entries
}

func renderWorkflowGraphContextQuery(query, request string, completed []WorkflowStageResult) string {
	replacements := map[string]string{
		"{{input.goal}}":       request,
		"{{input}}":            request,
		"{{request}}":          request,
		"{{workflow.input}}":   request,
		"{{workflow.request}}": request,
	}
	if len(completed) > 0 {
		previous := completed[len(completed)-1]
		replacements["{{previous.summary}}"] = workflowGraphPriorSummaryContextEntry(previous).Value
		replacements["{{previous.output}}"] = previous.Output.Summary
		replacements["{{previous.raw_output}}"] = previous.Output.RawOutput
		replacements["{{previous.decision}}"] = previous.Output.Decision
		replacements["{{previous.next_actions}}"] = workflowGraphValueString(previous.Output.NextActions)
	}
	rendered := query
	for placeholder, value := range replacements {
		rendered = strings.ReplaceAll(rendered, placeholder, value)
	}
	return rendered
}

func workflowGraphMemoryBlockContextValue(block memory.PromptBlock) string {
	parts := make([]string, 0, 7)
	if strings.TrimSpace(block.ContentMode) != "" {
		parts = append(parts, "content_mode="+strings.TrimSpace(block.ContentMode))
	}
	if strings.TrimSpace(block.Hash) != "" {
		parts = append(parts, "hash="+strings.TrimSpace(block.Hash))
	}
	if strings.TrimSpace(block.Language) != "" {
		parts = append(parts, "language="+strings.TrimSpace(block.Language))
	}
	if block.Size > 0 {
		parts = append(parts, "size="+strconv.FormatInt(block.Size, 10))
	}
	if strings.TrimSpace(block.Summary) != "" {
		parts = append(parts, "summary="+strings.TrimSpace(block.Summary))
	}
	return strings.Join(parts, "; ")
}

func workflowGraphProjectMemoryContextEntries(store *memory.Store, ref string) []workflowGraphContextEntry {
	if store == nil {
		return nil
	}
	project, err := store.Project()
	if err != nil || strings.TrimSpace(project.Summary) == "" {
		return nil
	}
	return []workflowGraphContextEntry{{Ref: ref, Value: project.Summary}}
}

func workflowGraphChangedFileContextEntries(ctx context.Context, store *memory.Store, ref string, completed []WorkflowStageResult) []workflowGraphContextEntry {
	paths := workflowGraphChangedFilePaths(completed)
	if len(paths) == 0 {
		return nil
	}
	if store == nil {
		return []workflowGraphContextEntry{{Ref: ref, Value: strings.Join(paths, ", ")}}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	index, _, err := store.EnsureFreshFileIndex(ctx)
	if err != nil {
		return []workflowGraphContextEntry{{Ref: ref, Value: strings.Join(paths, ", ")}}
	}
	files := workflowGraphFileSummariesForPaths(index, paths)
	if len(files) == 0 {
		return []workflowGraphContextEntry{{Ref: ref, Value: strings.Join(paths, ", ")}}
	}
	entries := make([]workflowGraphContextEntry, 0, len(files))
	for _, file := range files {
		entries = append(entries, workflowGraphContextEntry{
			Ref:   ref + "." + file.Path,
			Value: workflowGraphFileSummaryContextValue(file),
		})
	}
	return entries
}

func workflowGraphChangedFilePaths(completed []WorkflowStageResult) []string {
	seen := map[string]struct{}{}
	paths := make([]string, 0)
	add := func(path string) {
		path, ok := workflowGraphNormalizeChangedFilePath(path)
		if !ok {
			return
		}
		key := strings.ToLower(path)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		paths = append(paths, path)
	}
	for _, stage := range completed {
		for _, path := range workflowGraphStageChangedFilePaths(stage) {
			add(path)
		}
	}
	sort.Strings(paths)
	return paths
}

func workflowGraphArtifactLooksLikeChange(artifact session.WorkflowRunArtifact) bool {
	kind := normalizeWorkflowArtifactKind(artifact.Kind)
	switch kind {
	case "diff", "patch", "change", "file_change", "write":
		return true
	default:
		return strings.TrimSpace(artifact.Metadata["path"]) != ""
	}
}

func workflowGraphChangedFilePathFromToolResult(result schema.ToolResult) string {
	if strings.TrimSpace(result.Content) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		return ""
	}
	for _, key := range []string{"relative_path", "path", "file", "filename"} {
		if value := strings.TrimSpace(workflowGraphValueString(payload[key])); value != "" {
			return value
		}
	}
	return ""
}

func workflowGraphFileSummariesForPaths(index memory.FileIndex, paths []string) []memory.FileSummary {
	if len(index.Files) == 0 || len(paths) == 0 {
		return nil
	}
	targets := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		path = strings.TrimPrefix(path, "./")
		if path != "" {
			targets[strings.ToLower(path)] = struct{}{}
		}
	}
	out := make([]memory.FileSummary, 0, len(targets))
	for _, file := range index.Files {
		if _, ok := targets[strings.ToLower(filepath.ToSlash(strings.TrimSpace(file.Path)))]; ok {
			out = append(out, file)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path)
	})
	return out
}

func workflowGraphFileSummaryContextValue(file memory.FileSummary) string {
	parts := make([]string, 0, 6)
	parts = append(parts, "content_mode=summary")
	if strings.TrimSpace(file.Hash) != "" {
		parts = append(parts, "hash="+strings.TrimSpace(file.Hash))
	}
	if strings.TrimSpace(file.Language) != "" {
		parts = append(parts, "language="+strings.TrimSpace(file.Language))
	}
	if file.Size > 0 {
		parts = append(parts, "size="+strconv.FormatInt(file.Size, 10))
	}
	if strings.TrimSpace(file.Summary) != "" {
		parts = append(parts, "summary="+strings.TrimSpace(file.Summary))
	}
	if len(file.Symbols) > 0 {
		parts = append(parts, "symbols="+strings.Join(file.Symbols, ", "))
	}
	return strings.Join(parts, "; ")
}

func workflowGraphContextEntriesForRef(ctx context.Context, store *memory.Store, ref, request string, completed []WorkflowStageResult) []workflowGraphContextEntry {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	switch strings.ToLower(ref) {
	case "workflow.input", "workflow.request", "input", "request":
		return []workflowGraphContextEntry{{Ref: ref, Value: request}}
	case "memory.project":
		return workflowGraphProjectMemoryContextEntries(store, ref)
	case "files.changed":
		return workflowGraphChangedFileContextEntries(ctx, store, ref, completed)
	case "previous", "previous.summary":
		if len(completed) == 0 {
			return nil
		}
		return []workflowGraphContextEntry{workflowGraphPriorSummaryContextEntry(completed[len(completed)-1])}
	case "previous.raw_output", "previous.output":
		if len(completed) == 0 {
			return nil
		}
		prior := completed[len(completed)-1]
		return []workflowGraphContextEntry{{Ref: ref, Value: firstWorkflowGraphContextValue(prior.Output.RawOutput, prior.Result.Output)}}
	case "previous.artifacts":
		if len(completed) == 0 {
			return nil
		}
		return workflowGraphArtifactContextEntries(ref, completed[len(completed)-1].Output.Artifacts)
	case "previous.evidence":
		if len(completed) == 0 {
			return nil
		}
		return workflowGraphEvidenceContextEntries(ref, completed[len(completed)-1].Output.Evidence)
	case "previous.decision":
		if len(completed) == 0 {
			return nil
		}
		return workflowGraphScalarContextEntry(ref, completed[len(completed)-1].Output.Decision)
	case "previous.next_actions":
		if len(completed) == 0 {
			return nil
		}
		return workflowGraphAnyContextEntry(ref, completed[len(completed)-1].Output.NextActions)
	}
	if artifactEntries := workflowGraphArtifactObjectContextEntries(ref); len(artifactEntries) > 0 {
		return artifactEntries
	}
	if strings.HasPrefix(strings.ToLower(ref), "previous.artifacts.") {
		if len(completed) == 0 {
			return nil
		}
		name := strings.TrimSpace(ref[len("previous.artifacts."):])
		return workflowGraphNamedArtifactContextEntries(ref, name, completed[len(completed)-1].Output.Artifacts)
	}
	if strings.HasPrefix(strings.ToLower(ref), "previous.") {
		if len(completed) == 0 {
			return nil
		}
		priorRef := "stages." + string(completed[len(completed)-1].Stage) + "." + strings.TrimPrefix(ref, "previous.")
		return workflowGraphContextEntriesForRef(ctx, store, priorRef, request, completed)
	}
	if strings.HasPrefix(strings.ToLower(ref), "stages.") {
		return workflowGraphStageContextEntriesForRef(ref, completed)
	}
	value := resolveWorkflowGraphReference(ref, request, completed)
	if strings.TrimSpace(value) == "" || value == ref {
		return nil
	}
	return []workflowGraphContextEntry{{Ref: ref, Value: value}}
}

func workflowGraphArtifactObjectContextEntries(ref string) []workflowGraphContextEntry {
	displayRef := strings.TrimSpace(ref)
	value := strings.TrimPrefix(displayRef, "artifact:")
	if !strings.HasPrefix(value, "sha256:") {
		return nil
	}
	hash := strings.TrimPrefix(value, "sha256:")
	if len(hash) != 64 {
		return nil
	}
	for _, r := range hash {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return nil
		}
	}
	return []workflowGraphContextEntry{{
		Ref:   displayRef,
		Value: "content_mode=artifact_ref; artifact_ref=" + strings.ToLower(value) + "; full content is stored externally and should be opened only when exact evidence is needed",
	}}
}

func workflowGraphStageContextEntriesForRef(ref string, completed []WorkflowStageResult) []workflowGraphContextEntry {
	parts := strings.Split(ref, ".")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "stages") {
		return nil
	}
	stage, ok := workflowGraphCompletedStageByName(completed, parts[1])
	if !ok {
		return nil
	}
	if len(parts) == 3 && strings.EqualFold(parts[2], "outputs") {
		return workflowGraphOutputVariableContextEntries(ref, stage)
	}
	if len(parts) >= 4 && strings.EqualFold(parts[2], "artifacts") {
		name := strings.Join(parts[3:], ".")
		if name == "" {
			return workflowGraphArtifactContextEntries(ref, stage.Output.Artifacts)
		}
		return workflowGraphNamedArtifactContextEntries(ref, name, stage.Output.Artifacts)
	}
	if len(parts) >= 4 && strings.EqualFold(parts[2], "evidence") {
		name := strings.Join(parts[3:], ".")
		if name == "" {
			return workflowGraphEvidenceContextEntries(ref, stage.Output.Evidence)
		}
		return workflowGraphNamedArtifactContextEntries(ref, name, stage.Output.Evidence)
	}
	value := resolveWorkflowStageReference(ref, completed)
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []workflowGraphContextEntry{{Ref: ref, Value: value}}
}

func workflowGraphScalarContextEntry(ref, value string) []workflowGraphContextEntry {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []workflowGraphContextEntry{{Ref: ref, Value: value}}
}

func workflowGraphAnyContextEntry(ref string, value any) []workflowGraphContextEntry {
	text := workflowGraphValueString(value)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []workflowGraphContextEntry{{Ref: ref, Value: text}}
}

func workflowGraphPriorSummaryContextEntry(stage WorkflowStageResult) workflowGraphContextEntry {
	summary := stage.Output.Summary
	if strings.TrimSpace(summary) == "" {
		summary = stage.Result.Output
	}
	return workflowGraphContextEntry{
		Ref:   fmt.Sprintf("stages.%s.outputs.summary", stage.Stage),
		Value: summary,
	}
}

func workflowGraphOutputVariableContextEntries(ref string, stage WorkflowStageResult) []workflowGraphContextEntry {
	if len(stage.Output.Variables) == 0 && len(stage.Output.Values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(stage.Output.Variables)+len(stage.Output.Values))
	seen := map[string]struct{}{}
	for key := range stage.Output.Variables {
		keys = append(keys, key)
		seen[key] = struct{}{}
	}
	for key := range stage.Output.Values {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	entries := make([]workflowGraphContextEntry, 0, len(keys))
	for _, key := range keys {
		value := stage.Output.Variables[key]
		if strings.TrimSpace(value) == "" {
			value = workflowGraphValueString(stage.Output.Values[key])
		}
		if strings.TrimSpace(value) == "" {
			continue
		}
		entries = append(entries, workflowGraphContextEntry{Ref: ref + "." + key, Value: value})
	}
	return entries
}

func workflowGraphArtifactContextEntries(ref string, artifacts []session.WorkflowRunArtifact) []workflowGraphContextEntry {
	if len(artifacts) == 0 {
		return nil
	}
	entries := make([]workflowGraphContextEntry, 0, len(artifacts))
	for _, artifact := range artifacts {
		value := workflowGraphArtifactContextValue(artifact)
		if strings.TrimSpace(value) == "" {
			continue
		}
		entries = append(entries, workflowGraphContextEntry{
			Ref:   ref + "." + workflowGraphArtifactContextName(artifact),
			Value: value,
		})
	}
	return entries
}

func workflowGraphEvidenceContextEntries(ref string, artifacts []session.WorkflowRunArtifact) []workflowGraphContextEntry {
	if len(artifacts) == 0 {
		return nil
	}
	entries := make([]workflowGraphContextEntry, 0, len(artifacts))
	for _, artifact := range artifacts {
		if !workflowGraphArtifactLooksLikeEvidence(artifact) {
			continue
		}
		value := workflowGraphArtifactContextValue(artifact)
		if strings.TrimSpace(value) == "" {
			continue
		}
		entries = append(entries, workflowGraphContextEntry{
			Ref:   ref + "." + workflowGraphArtifactContextName(artifact),
			Value: value,
		})
	}
	return entries
}

func workflowGraphNamedArtifactContextEntries(ref, name string, artifacts []session.WorkflowRunArtifact) []workflowGraphContextEntry {
	name = normalizeWorkflowArtifactName(name)
	if name == "" || len(artifacts) == 0 {
		return nil
	}
	for _, artifact := range artifacts {
		if normalizeWorkflowArtifactName(artifact.Metadata["name"]) != name &&
			normalizeWorkflowArtifactName(artifact.ID) != name &&
			normalizeWorkflowArtifactName(artifact.Title) != name &&
			normalizeWorkflowArtifactName(artifact.Kind) != name {
			continue
		}
		value := workflowGraphArtifactContextValue(artifact)
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return []workflowGraphContextEntry{{Ref: ref, Value: value}}
	}
	return nil
}

func workflowGraphArtifactContextValue(artifact session.WorkflowRunArtifact) string {
	parts := make([]string, 0, 5)
	if strings.TrimSpace(artifact.Title) != "" {
		parts = append(parts, "title="+strings.TrimSpace(artifact.Title))
	}
	if strings.TrimSpace(artifact.Kind) != "" {
		parts = append(parts, "kind="+strings.TrimSpace(artifact.Kind))
	}
	if strings.TrimSpace(artifact.Summary) != "" {
		parts = append(parts, "summary="+strings.TrimSpace(artifact.Summary))
	}
	if strings.TrimSpace(artifact.ID) != "" {
		parts = append(parts, "artifact_ref="+strings.TrimSpace(artifact.ID))
	}
	if len(artifact.Metadata) > 0 {
		if ref := strings.TrimSpace(artifact.Metadata["ref"]); ref != "" {
			parts = append(parts, "source_ref="+ref)
		}
	}
	return strings.Join(parts, "; ")
}

func workflowGraphArtifactContextName(artifact session.WorkflowRunArtifact) string {
	for _, value := range []string{artifact.Metadata["name"], artifact.ID, artifact.Title, artifact.Kind} {
		if name := normalizeWorkflowArtifactName(value); name != "" {
			return name
		}
	}
	return "artifact"
}

func workflowGraphArtifactLooksLikeEvidence(artifact session.WorkflowRunArtifact) bool {
	kind := normalizeWorkflowArtifactKind(artifact.Kind)
	switch kind {
	case "evidence", "report", "finding", "verification", "acceptance", "audit", "structured", "output":
		return true
	default:
		return strings.EqualFold(strings.TrimSpace(artifact.Metadata["evidence_category"]), "evidence")
	}
}

func workflowGraphCompletedStageByName(completed []WorkflowStageResult, name string) (WorkflowStageResult, bool) {
	return workflowGraphLatestStageResultByName(completed, name)
}

func workflowGraphLatestStageResultByName(completed []WorkflowStageResult, name string) (WorkflowStageResult, bool) {
	target := normalizeWorkflowSkillName(name)
	for i := len(completed) - 1; i >= 0; i-- {
		stage := completed[i]
		if normalizeWorkflowSkillName(string(stage.Stage)) == target {
			return stage, true
		}
		if normalizeWorkflowSkillName(stage.Metadata["original_stage"]) == target {
			return stage, true
		}
	}
	return WorkflowStageResult{}, false
}

func workflowGraphRunHasContractFailure(run session.WorkflowRunSnapshot, stageName string, stageResult WorkflowStageResult) bool {
	if strings.TrimSpace(stageResult.Metadata["contract_check"]) != "" ||
		strings.TrimSpace(stageResult.Metadata["contract_checks"]) != "" ||
		workflowTruthy(stageResult.Metadata["contract_failed"]) {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(stageResult.Status))
	if status == "contract_failed" || status == "blocked" {
		if strings.TrimSpace(stageResult.Metadata["reason"]) != "" || strings.TrimSpace(stageResult.Metadata["source_ref"]) != "" {
			return true
		}
	}
	target := normalizeWorkflowSkillName(stageName)
	for _, event := range run.Events {
		if normalizeWorkflowSkillName(event.Stage) != target && normalizeWorkflowSkillName(event.TaskStage) != target {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(event.Reason), "contract_validation_failed") ||
			strings.EqualFold(strings.TrimSpace(event.Type), "contract_validation_failed") ||
			strings.TrimSpace(event.ContractCheck) != "" {
			return true
		}
	}
	return false
}

func dedupeWorkflowGraphContextEntries(entries []workflowGraphContextEntry) []workflowGraphContextEntry {
	if len(entries) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]workflowGraphContextEntry, 0, len(entries))
	for _, entry := range entries {
		entry.Ref = strings.TrimSpace(entry.Ref)
		entry.Value = strings.TrimSpace(entry.Value)
		if entry.Ref == "" || entry.Value == "" {
			continue
		}
		key := strings.ToLower(entry.Ref)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, entry)
	}
	return out
}

func filterWorkflowGraphContextEntries(entries []workflowGraphContextEntry, exclude []string) []workflowGraphContextEntry {
	if len(entries) == 0 || len(exclude) == 0 {
		return entries
	}
	out := make([]workflowGraphContextEntry, 0, len(entries))
	for _, entry := range entries {
		if workflowGraphContextRefExcluded(entry.Ref, exclude) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func workflowGraphContextRefExcluded(ref string, exclude []string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	for _, pattern := range exclude {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		switch pattern {
		case "raw_tool_logs", "tool_logs":
			if strings.Contains(ref, "tool_results") {
				return true
			}
		case "large_file_contents":
			if strings.Contains(ref, "raw_output") || strings.Contains(ref, "result.output") {
				return true
			}
		}
		if ref == pattern || strings.HasPrefix(ref, pattern+".") || strings.Contains(ref, pattern) {
			return true
		}
	}
	return false
}

func limitWorkflowGraphContextEntries(entries []workflowGraphContextEntry, maxTokens int) []workflowGraphContextEntry {
	if len(entries) == 0 {
		return nil
	}
	if maxTokens <= 0 {
		return entries
	}
	maxBytes := maxTokens * 4
	used := 0
	out := make([]workflowGraphContextEntry, 0, len(entries))
	for _, entry := range entries {
		entryBytes := len([]byte(entry.Ref)) + len([]byte(entry.Value)) + 4
		if used+entryBytes <= maxBytes {
			out = append(out, entry)
			used += entryBytes
			continue
		}
		remaining := maxBytes - used - len([]byte(entry.Ref)) - 12
		if remaining <= 32 {
			break
		}
		entry.Value = trimWorkflowGraphContextValue(entry.Value, remaining) + " [context truncated by max_tokens]"
		out = append(out, entry)
		break
	}
	return out
}

func trimWorkflowGraphContextValue(value string, maxBytes int) string {
	if maxBytes <= 0 || len([]byte(value)) <= maxBytes {
		return value
	}
	trimmed := value[:maxBytes]
	for len(trimmed) > 0 && !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return strings.TrimRight(trimmed, "\r\n\t ")
}

func trimWorkflowGraphStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstWorkflowGraphContextValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isWorkflowGraphSelectStrategy(strategy string) bool {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "select", "best", "conditional", "planner_select", "planner-select", "dynamic", "graph":
		return true
	default:
		return false
	}
}

func (w *WorkflowRunner) runWorkflowGraphExecutableStage(ctx context.Context, graph workflowGraph, stage workflowGraphStage, prompt string, skill schema.Skill, handler func(event schema.StreamEvent) error) (schema.AgentResult, int, error) {
	attempts := stage.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			reason := ""
			if lastErr != nil {
				reason = lastErr.Error()
			}
			w.emitWorkflowGraphStageRetryEvent(graph, stage, attempt, reason, handler)
		}
		result, err := runSkillStageWithModelAndOptions(ctx, w.runtime, stage.Agent, prompt, skill, stage.Model, workflowStageRunOptions{AllowedTools: workflowGraphStageAllowedTools(stage)}, handler)
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
	metadata = mergeWorkflowStageMetadata(metadata, workflowGraphStageModelMetadata(stage.Model, result))
	metadata = mergeWorkflowStageMetadata(metadata, workflowGraphStageSoftBudgetRouteMetadata(stage))
	metadata = mergeWorkflowStageMetadata(metadata, workflowGraphStageModelEscalationMetadata(stage))
	metadata = mergeWorkflowStageMetadata(metadata, workflowGraphStageToolMetadata(stage))
	metadata = mergeWorkflowStageMetadata(metadata, workflowGraphStageExecutionMetadata(stage, workflowGraphStageExecutionReadiness(stage, false)))
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
	if contextMetadata := workflowGraphContextMetadata(stage.Context); len(contextMetadata) > 0 {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		for key, value := range contextMetadata {
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
	if result.Incomplete {
		stageResult.Status = "incomplete"
		if stageResult.Metadata == nil {
			stageResult.Metadata = map[string]string{}
		}
		stageResult.Metadata["incomplete"] = "true"
		stageResult.Metadata["incomplete_reason"] = result.IncompleteReason
		if strings.TrimSpace(result.StopReason) != "" {
			stageResult.Metadata["stop_reason"] = result.StopReason
		}
		if result.ContinuationCount > 0 {
			stageResult.Metadata["continuation_count"] = strconv.Itoa(result.ContinuationCount)
		}
	}
	stageResult.Acceptance = evaluateWorkflowGraphAcceptanceCriteria(stage, stageResult)
	if len(stageResult.Acceptance) > 0 {
		verifications := workflowGraphAcceptanceVerifications(stageResult.Acceptance)
		stageResult.Result.Verification = append(stageResult.Result.Verification, verifications...)
		stageResult.Output.Verification = append(stageResult.Output.Verification, verifications...)
	}
	stageResult.Output.Artifacts = workflowGraphDeclaredStageArtifacts(stage, stageResult.Output, stageResult.Result)
	stageResult.Output.Evidence = workflowStageEvidenceArtifacts(stageResult.Output.Artifacts)
	applyWorkflowStageCanonicalOutputFields(&stageResult.Output)
	return stageResult
}

func workflowGraphStageAllowedTools(stage workflowGraphStage) []string {
	values := make([]string, 0, 4)
	if tool := strings.TrimSpace(stage.Tool); tool != "" {
		values = append(values, tool)
	}
	for _, key := range []string{"tools", "allowed_tools", "tool"} {
		values = append(values, splitWorkflowGraphList(stage.Params[key])...)
	}
	return dedupeToolNames(values)
}

func workflowGraphStageToolMetadata(stage workflowGraphStage) map[string]string {
	tools := workflowGraphStageAllowedTools(stage)
	if len(tools) == 0 {
		return nil
	}
	return map[string]string{
		"tool.allowed":      strings.Join(tools, ","),
		"tool.stage_scoped": "true",
	}
}

func workflowGraphStageExecutionMetadata(stage workflowGraphStage, readiness workflowGraphExecutionReadiness) map[string]string {
	if readiness.Mode == "" {
		readiness = workflowGraphStageExecutionReadiness(stage, false)
	}
	values := map[string]string{
		"execution.mode":                      readiness.Mode,
		"execution.ready":                     strconv.FormatBool(readiness.Ready),
		"execution.requires_approval":         strconv.FormatBool(workflowGraphExecutionRequiresApproval(stage)),
		"execution.requires_authorized_scope": strconv.FormatBool(workflowGraphExecutionRequiresAuthorizedScope(stage)),
		"execution.requires_rollback":         strconv.FormatBool(workflowGraphExecutionRequiresRollback(stage)),
		"execution.requires_credential_ref":   strconv.FormatBool(workflowGraphExecutionRequiresCredentialRef(stage)),
		"execution.requires_allowlist":        strconv.FormatBool(workflowGraphExecutionRequiresAllowlist(stage)),
	}
	if readiness.RiskLevel != "" {
		values["execution.risk_level"] = readiness.RiskLevel
	}
	if readiness.Boundary != "" {
		values["execution.boundary"] = readiness.Boundary
	}
	if len(readiness.Missing) > 0 {
		values["execution.missing"] = strings.Join(readiness.Missing, ",")
	}
	if readiness.Reason != "" {
		values["execution.reason"] = readiness.Reason
	}
	if readiness.SourceRef != "" {
		values["execution.source_ref"] = readiness.SourceRef
	}
	if len(readiness.AllowLiveTools) > 0 {
		values["execution.allow_live_tools"] = strings.Join(readiness.AllowLiveTools, ",")
	}
	if required := workflowGraphExecutionRequiredParams(stage); len(required) > 0 {
		values["execution.required_params"] = strings.Join(required, ",")
	}
	return workflowStageMetadata(values)
}

func workflowGraphExecutionMode(stage workflowGraphStage) string {
	mode := strings.TrimSpace(stage.Execution.Mode)
	if mode == "" {
		mode = firstNonEmptyWorkflowGraphParam(stage.Params,
			"execution_mode",
			"execution.mode",
			"mode.execution",
			"live_mode",
		)
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "plan", "planned", "planning_only":
		return "planning"
	case "dry-run", "dryrun", "preview", "simulate", "simulation":
		return "dry_run"
	case "manual_review", "operator":
		return "manual"
	case "off", "none":
		return "disabled"
	default:
		return mode
	}
}

func workflowGraphExecutionModeValid(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "planning", "dry_run", "live", "manual", "disabled":
		return true
	default:
		return false
	}
}

func workflowGraphExecutionModeRequiresLiveReadiness(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "live":
		return true
	default:
		return false
	}
}

func workflowGraphExecutionRiskLevel(stage workflowGraphStage) string {
	risk := strings.TrimSpace(stage.Execution.RiskLevel)
	if risk == "" {
		risk = firstNonEmptyWorkflowGraphParam(stage.Params,
			"execution_risk_level",
			"execution.risk_level",
			"risk_level",
			"risk",
		)
	}
	risk = strings.ToLower(strings.TrimSpace(risk))
	switch risk {
	case "med":
		return "medium"
	case "crit":
		return "critical"
	default:
		return risk
	}
}

func workflowGraphExecutionRiskLevelValid(risk string) bool {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "", "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func workflowGraphExecutionBoundary(stage workflowGraphStage) string {
	return firstWorkflowGraphValue(
		stage.Execution.Boundary,
		firstNonEmptyWorkflowGraphParam(stage.Params,
			"execution_boundary",
			"execution.boundary",
			"boundary",
			"target_boundary",
		),
	)
}

func workflowGraphExecutionRequiresApproval(stage workflowGraphStage) bool {
	return workflowGraphExecutionModeRequiresLiveReadiness(workflowGraphExecutionMode(stage)) || stage.Execution.RequiresApproval || workflowGraphExecutionParamBool(stage, "requires_approval", "require_approval", "execution_requires_approval", "execution.requires_approval")
}

func workflowGraphExecutionRequiresAuthorizedScope(stage workflowGraphStage) bool {
	return workflowGraphExecutionModeRequiresLiveReadiness(workflowGraphExecutionMode(stage)) || stage.Execution.RequiresAuthorizedScope || workflowGraphExecutionParamBool(stage, "requires_authorized_scope", "require_authorized_scope", "execution_requires_authorized_scope", "execution.requires_authorized_scope")
}

func workflowGraphExecutionRequiresRollback(stage workflowGraphStage) bool {
	return workflowGraphExecutionModeRequiresLiveReadiness(workflowGraphExecutionMode(stage)) || stage.Execution.RequiresRollback || workflowGraphExecutionParamBool(stage, "requires_rollback", "require_rollback", "execution_requires_rollback", "execution.requires_rollback")
}

func workflowGraphExecutionRequiresCredentialRef(stage workflowGraphStage) bool {
	return workflowGraphExecutionModeRequiresLiveReadiness(workflowGraphExecutionMode(stage)) || stage.Execution.RequiresCredentialRef || workflowGraphExecutionParamBool(stage, "requires_credential_ref", "require_credential_ref", "execution_requires_credential_ref", "execution.requires_credential_ref")
}

func workflowGraphExecutionRequiresAllowlist(stage workflowGraphStage) bool {
	return workflowGraphExecutionModeRequiresLiveReadiness(workflowGraphExecutionMode(stage)) || stage.Execution.RequiresAllowlist || workflowGraphExecutionParamBool(stage, "requires_allowlist", "require_allowlist", "execution_requires_allowlist", "execution.requires_allowlist")
}

func workflowGraphExecutionParamBool(stage workflowGraphStage, keys ...string) bool {
	value := strings.ToLower(strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params, keys...)))
	switch value {
	case "1", "true", "yes", "y", "on", "required", "require":
		return true
	default:
		return false
	}
}

func workflowGraphExecutionParamTruthy(stage workflowGraphStage, keys ...string) bool {
	value := strings.ToLower(strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params, keys...)))
	switch value {
	case "1", "true", "yes", "y", "on", "approved", "authorized", "ready", "set", "present":
		return true
	default:
		return false
	}
}

func workflowGraphExecutionApprovalSatisfied(stage workflowGraphStage, approved bool) bool {
	_ = approved
	if stage.Approval {
		return true
	}
	return strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params,
		"approval_ref",
		"approved_by",
		"change_ticket",
		"change_request",
		"operator_approval",
		"execution_approval_ref",
		"execution.approval_ref",
	)) != "" || workflowGraphExecutionParamTruthy(stage, "approval_granted", "approved", "execution_approved")
}

func workflowGraphExecutionAuthorizedScopeSatisfied(stage workflowGraphStage) bool {
	return strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params,
		"authorized_scope",
		"authorization_scope",
		"scope_ref",
		"authorized_targets",
		"target_scope",
		"execution_authorized_scope",
		"execution.authorized_scope",
	)) != "" || workflowGraphExecutionParamTruthy(stage, "scope_authorized", "authorized", "execution_authorized")
}

func workflowGraphExecutionRollbackSatisfied(stage workflowGraphStage) bool {
	return strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params,
		"rollback_plan",
		"rollback_ref",
		"rollback_artifact",
		"backout_plan",
		"revert_plan",
		"execution_rollback_plan",
		"execution.rollback_plan",
	)) != "" || workflowGraphExecutionParamTruthy(stage, "rollback_ready", "rollback_available")
}

func workflowGraphExecutionCredentialRefSatisfied(stage workflowGraphStage) bool {
	return strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params,
		"credential_ref",
		"credentials_ref",
		"secret_ref",
		"auth_ref",
		"execution_credential_ref",
		"execution.credential_ref",
	)) != ""
}

func workflowGraphExecutionAllowlistSatisfied(stage workflowGraphStage) bool {
	if strings.TrimSpace(firstNonEmptyWorkflowGraphParam(stage.Params,
		"allowlist_ref",
		"allowed_hosts",
		"allowed_targets",
		"allowed_devices",
		"allowed_commands",
		"command_allowlist",
		"target_allowlist",
		"execution_allowlist_ref",
		"execution.allowlist_ref",
	)) != "" {
		return true
	}
	return workflowGraphExecutionParamTruthy(stage, "allowlist_confirmed", "allowlist_ready")
}

func workflowGraphExecutionRequiredParams(stage workflowGraphStage) []string {
	values := append([]string(nil), stage.Execution.RequiredParams...)
	values = append(values, splitWorkflowGraphList(firstNonEmptyWorkflowGraphParam(stage.Params,
		"execution_required_params",
		"execution.required_params",
		"required_params",
	))...)
	return dedupeWorkflowGraphContractNames(values)
}

func workflowGraphExecutionMissingRequiredParams(stage workflowGraphStage) []string {
	required := workflowGraphExecutionRequiredParams(stage)
	if len(required) == 0 {
		return nil
	}
	missing := make([]string, 0, len(required))
	for _, key := range required {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if strings.TrimSpace(stage.Params[key]) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func workflowGraphExecutionAllowLiveTools(stage workflowGraphStage) []string {
	values := append([]string(nil), stage.Execution.AllowLiveTools...)
	values = append(values, splitWorkflowGraphList(firstNonEmptyWorkflowGraphParam(stage.Params,
		"allow_live_tools",
		"execution_allow_live_tools",
		"execution.allow_live_tools",
		"live_tools",
	))...)
	return dedupeToolNames(values)
}

func workflowGraphExecutionMissingLiveTools(stage workflowGraphStage, allowed []string) []string {
	tools := workflowGraphStageAllowedTools(stage)
	if len(tools) == 0 {
		return nil
	}
	missing := make([]string, 0, len(tools))
	for _, tool := range tools {
		found := false
		for _, candidate := range allowed {
			if toolNameEquivalent(tool, candidate) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, tool)
		}
	}
	return missing
}

func workflowGraphExecutionMissingSourceRef(missing []string) string {
	if len(missing) == 0 {
		return "stage.execution"
	}
	refs := make([]string, 0, len(missing))
	for _, item := range missing {
		item = strings.TrimSpace(item)
		switch {
		case item == "approval":
			refs = append(refs, "stage.approval|params.approval_ref")
		case item == "authorized_scope":
			refs = append(refs, "params.authorized_scope")
		case item == "rollback":
			refs = append(refs, "params.rollback_plan")
		case item == "credential_ref":
			refs = append(refs, "params.credential_ref")
		case item == "allowlist":
			refs = append(refs, "params.allowed_hosts|params.allowed_commands")
		case strings.HasPrefix(item, "params."):
			refs = append(refs, item)
		case strings.HasPrefix(item, "allow_live_tools:"):
			refs = append(refs, "stage.execution.allow_live_tools")
		default:
			refs = append(refs, "stage.execution."+item)
		}
	}
	return strings.Join(refs, "; ")
}

func splitWorkflowGraphList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func mergeWorkflowStageMetadata(base map[string]string, extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return base
	}
	if base == nil {
		base = make(map[string]string, len(extra))
	}
	for key, value := range extra {
		if strings.TrimSpace(value) != "" {
			base[key] = value
		}
	}
	if len(base) == 0 {
		return nil
	}
	return base
}

func workflowGraphStageModelMetadata(model workflowGraphStageModel, result schema.AgentResult) map[string]string {
	if !workflowGraphStageModelConfigured(model) {
		return nil
	}
	values := map[string]string{
		"model.override": "true",
	}
	if provider := strings.TrimSpace(model.Provider); provider != "" {
		values["model.provider"] = provider
	}
	if name := strings.TrimSpace(result.Model); name != "" {
		values["model.model"] = name
	} else if name := strings.TrimSpace(model.Model); name != "" {
		values["model.model"] = name
	}
	if model.MaxTokens > 0 {
		values["model.max_tokens"] = strconv.Itoa(model.MaxTokens)
	}
	if model.Temperature != nil {
		values["model.temperature"] = strconv.FormatFloat(*model.Temperature, 'f', -1, 64)
	}
	return workflowStageMetadata(values)
}

func workflowGraphStageSoftBudgetRouteMetadata(stage workflowGraphStage) map[string]string {
	if !stage.SoftBudgetRoute {
		return nil
	}
	values := map[string]string{
		"model.soft_budget_route": "true",
		"budget.reason":           "budget_soft_limit_hit",
	}
	if ref := strings.TrimSpace(stage.SoftBudgetRouteRef); ref != "" {
		values["model.soft_budget_route_ref"] = ref
	}
	return workflowStageMetadata(values)
}

func workflowGraphStageModelEscalationMetadata(stage workflowGraphStage) map[string]string {
	if !stage.ModelEscalation {
		return nil
	}
	values := map[string]string{
		"model.escalated": "true",
		"model.route":     "model_escalated",
		"reason":          fallbackWorkflowGraphValue(stage.ModelEscalationWhy, "model_escalated"),
	}
	if ref := strings.TrimSpace(stage.ModelEscalationRef); ref != "" {
		values["model.escalation_ref"] = ref
		values["source_ref"] = ref
	}
	return workflowStageMetadata(values)
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
	applyWorkflowStageCanonicalOutputFields(&output)
	applyWorkflowGraphControlStageDeclaredOutputs(stage, &output)
	status := fallbackWorkflowGraphValue(decision.Status, "completed")
	stageResult := WorkflowStageResult{
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
	stageResult.Metadata = mergeWorkflowStageMetadata(stageResult.Metadata, workflowGraphStageExecutionMetadata(stage, workflowGraphStageExecutionReadiness(stage, false)))
	workflowGraphApplyQualityGateFailureMetadata(&stageResult, decision)
	stageResult.Output.Artifacts = workflowGraphDeclaredControlArtifacts(stage, stageResult.Output, stageResult.Result)
	stageResult.Output.Evidence = workflowStageEvidenceArtifacts(stageResult.Output.Artifacts)
	applyWorkflowStageCanonicalOutputFields(&stageResult.Output)
	stageResult.Acceptance = evaluateWorkflowGraphAcceptanceCriteria(stage, stageResult)
	if len(stageResult.Acceptance) > 0 {
		verifications := workflowGraphAcceptanceVerifications(stageResult.Acceptance)
		stageResult.Result.Verification = append(stageResult.Result.Verification, verifications...)
		stageResult.Output.Verification = append(stageResult.Output.Verification, verifications...)
	}
	applyWorkflowStageCanonicalOutputFields(&stageResult.Output)
	stageResult.Result.Verification = append([]schema.Verification(nil), stageResult.Output.Verification...)
	return stageResult
}

func applyWorkflowGraphControlStageDeclaredOutputs(stage workflowGraphStage, output *WorkflowStageOutput) {
	if output == nil || len(stage.Outputs) == 0 {
		return
	}
	keys := make([]string, 0, len(stage.Outputs))
	for key := range stage.Outputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, ok := workflowGraphControlStageOutputReference(stage.Outputs[key], *output)
		if !ok {
			continue
		}
		workflowStageSetOutputValue(output, key, value)
	}
}

func workflowGraphControlStageOutputReference(expr string, output WorkflowStageOutput) (any, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, false
	}
	if literal, ok := unquoteWorkflowLiteral(expr); ok {
		return literal, true
	}
	lower := strings.ToLower(expr)
	switch lower {
	case "result.output", "result.raw_output", "output", "raw_output":
		return fallbackWorkflowGraphValue(output.Variables["value"], output.RawOutput), true
	case "result.summary", "summary":
		return output.Summary, strings.TrimSpace(output.Summary) != ""
	case "result.variables", "outputs.variables":
		return copyStringMap(output.Variables), len(output.Variables) > 0
	case "result.values", "outputs.values":
		return copyWorkflowAnyMap(output.Values), len(output.Values) > 0
	}
	for _, prefix := range []string{"result.", "outputs.", "output."} {
		if strings.HasPrefix(lower, prefix) {
			rawKey := strings.TrimSpace(expr[len(prefix):])
			key := normalizeWorkflowSkillName(rawKey)
			if value, ok := output.Values[key]; ok {
				return copyWorkflowAnyValue(value), true
			}
			if value, ok := output.Values[rawKey]; ok {
				return copyWorkflowAnyValue(value), true
			}
			if value, ok := workflowGraphNestedValue(output.Values, strings.Split(rawKey, ".")); ok {
				return copyWorkflowAnyValue(value), true
			}
			if value, ok := workflowGraphNestedValue(output.Values, strings.Split(key, ".")); ok {
				return copyWorkflowAnyValue(value), true
			}
			if value := strings.TrimSpace(output.Variables[key]); value != "" {
				return value, true
			}
			if value := strings.TrimSpace(output.Variables[rawKey]); value != "" {
				return value, true
			}
			if value, ok := workflowGraphNestedValue(output.Variables, strings.Split(rawKey, ".")); ok {
				return copyWorkflowAnyValue(value), true
			}
			if value, ok := workflowGraphNestedValue(output.Variables, strings.Split(key, ".")); ok {
				return copyWorkflowAnyValue(value), true
			}
			return nil, false
		}
	}
	return nil, false
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
	case []session.WorkflowRunArtifact:
		return append([]session.WorkflowRunArtifact(nil), typed...)
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
	applyWorkflowStageCanonicalOutputFields(&output)
	return output
}

func applyWorkflowStageCanonicalOutputFields(output *WorkflowStageOutput) {
	if output == nil {
		return
	}
	applyWorkflowStageJSONOutputFields(output)
	if strings.TrimSpace(output.Summary) != "" {
		workflowStageSetOutputValue(output, "summary", output.Summary)
	}
	output.Evidence = workflowStageEvidenceArtifacts(output.Artifacts)
	output.Decision = workflowStageCanonicalDecision(output)
	if strings.TrimSpace(output.Decision) != "" {
		workflowStageSetOutputValue(output, "decision", output.Decision)
	}
	output.NextActions = workflowStageCanonicalNextActions(output)
	if len(output.NextActions) > 0 {
		workflowStageSetOutputValue(output, "next_actions", append([]string(nil), output.NextActions...))
	}
}

func applyWorkflowStageJSONOutputFields(output *WorkflowStageOutput) {
	if output == nil || strings.TrimSpace(output.RawOutput) == "" {
		return
	}
	decoded, ok := workflowStageJSONOutputObject(output.RawOutput)
	if !ok {
		return
	}
	for _, key := range []string{"summary", "evidence", "artifacts", "decision", "changed_files", "verification", "blockers", "next_actions"} {
		value, exists := workflowStageOutputJSONValue(decoded, key)
		if !exists {
			continue
		}
		workflowStageSetOutputValue(output, key, value)
	}
	if strings.TrimSpace(output.Summary) == "" {
		if value, ok := workflowStageOutputJSONValue(decoded, "summary"); ok {
			output.Summary = truncateSummary(workflowGraphValueString(value))
		}
	}
}

func workflowStageJSONOutputObject(value string) (map[string]any, bool) {
	for _, candidate := range workflowTeamRoleJSONCandidates(value) {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(candidate), &decoded); err == nil && len(decoded) > 0 {
			return decoded, true
		}
	}
	return nil, false
}

func workflowStageOutputJSONValue(decoded map[string]any, key string) (any, bool) {
	if len(decoded) == 0 {
		return nil, false
	}
	if value, ok := decoded[key]; ok {
		return value, true
	}
	normalized := normalizeWorkflowSkillName(key)
	for candidate, value := range decoded {
		if normalizeWorkflowSkillName(candidate) == normalized {
			return value, true
		}
	}
	return nil, false
}

func workflowStageSetOutputValue(output *WorkflowStageOutput, key string, value any) {
	if output.Values == nil {
		output.Values = map[string]any{}
	}
	if _, ok := output.Values[key]; !ok {
		output.Values[key] = copyWorkflowAnyValue(value)
	}
	if output.Variables == nil {
		output.Variables = map[string]string{}
	}
	if strings.TrimSpace(output.Variables[key]) == "" {
		output.Variables[key] = limitWorkflowGraphText(workflowGraphValueString(value))
	}
}

func workflowStageEvidenceArtifacts(artifacts []session.WorkflowRunArtifact) []session.WorkflowRunArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]session.WorkflowRunArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if workflowGraphArtifactLooksLikeEvidence(artifact) {
			out = append(out, artifact)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func workflowStageCanonicalDecision(output *WorkflowStageOutput) string {
	for _, key := range []string{"decision", "route", "target", "passed", "quality_status", "status"} {
		if value := strings.TrimSpace(output.Variables[key]); value != "" {
			return value
		}
		if value, ok := output.Values[key]; ok {
			if text := strings.TrimSpace(workflowGraphValueString(value)); text != "" {
				return text
			}
		}
	}
	for _, section := range output.ToolResults {
		if section.IsError || section.Denied {
			return "failed"
		}
	}
	return ""
}

func workflowStageCanonicalNextActions(output *WorkflowStageOutput) []string {
	for _, key := range []string{"next_actions", "next_action", "next_steps", "actions"} {
		if value, ok := output.Values[key]; ok {
			if actions := workflowStageNextActionList(value); len(actions) > 0 {
				return actions
			}
		}
		if value := output.Variables[key]; strings.TrimSpace(value) != "" {
			if actions := workflowStageNextActionList(value); len(actions) > 0 {
				return actions
			}
		}
	}
	return nil
}

func workflowStageNextActionList(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return trimWorkflowGraphStringList(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(workflowGraphValueString(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		var array []any
		if json.Unmarshal([]byte(text), &array) == nil {
			return workflowStageNextActionList(array)
		}
		parts := strings.FieldsFunc(text, func(r rune) bool {
			return r == '\n' || r == ';'
		})
		if len(parts) <= 1 && strings.Contains(text, ",") {
			parts = strings.Split(text, ",")
		}
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.Trim(strings.TrimSpace(part), "-* ")
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	default:
		text := strings.TrimSpace(workflowGraphValueString(value))
		if text == "" {
			return nil
		}
		return []string{text}
	}
}

func workflowGraphDeclaredStageArtifacts(stage workflowGraphStage, output WorkflowStageOutput, result schema.AgentResult) []session.WorkflowRunArtifact {
	if len(stage.Artifacts) == 0 {
		return nil
	}
	artifacts := make([]session.WorkflowRunArtifact, 0, len(stage.Artifacts))
	stageName := strings.TrimSpace(stage.Name)
	for index, declared := range stage.Artifacts {
		artifact := workflowGraphDeclaredStageArtifact(stageName, index+1, declared, stage, output, result)
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

func workflowGraphDeclaredControlArtifacts(stage workflowGraphStage, output WorkflowStageOutput, result schema.AgentResult) []session.WorkflowRunArtifact {
	return workflowGraphDeclaredStageArtifacts(stage, output, result)
}

func workflowGraphDeclaredStageArtifact(stageName string, index int, declared workflowGraphArtifact, stage workflowGraphStage, output WorkflowStageOutput, result schema.AgentResult) session.WorkflowRunArtifact {
	kind := normalizeWorkflowArtifactKind(declared.Kind)
	if kind == "" {
		kind = "artifact"
	}
	name := normalizeWorkflowArtifactName(declared.Name)
	if name == "" {
		name = fmt.Sprintf("%s-%d", kind, index)
	}
	content := workflowGraphDeclaredStageArtifactValue(declared.Content, stage, output, result)
	if strings.TrimSpace(content) == "" {
		content = workflowGraphDeclaredStageArtifactValue(declared.Ref, stage, output, result)
	}
	summary := workflowGraphDeclaredStageArtifactValue(declared.Summary, stage, output, result)
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

func workflowGraphDeclaredStageArtifactValue(expr string, stage workflowGraphStage, output WorkflowStageOutput, result schema.AgentResult) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if literal, ok := unquoteWorkflowLiteral(expr); ok {
		return literal
	}
	if value, ok := workflowGraphControlStageOutputReference(expr, output); ok {
		return workflowGraphValueString(value)
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
	if strings.HasPrefix(strings.ToLower(expr), "result.") {
		key := strings.TrimSpace(expr[len("result."):])
		if value, ok := workflowGraphResultJSONValue(result.Output, key); ok {
			return workflowGraphValueString(value)
		}
		switch strings.ToLower(key) {
		case "tool_results":
			return workflowGraphJSON(result.ToolResults)
		case "findings":
			return workflowGraphJSON(result.Findings)
		case "changes":
			return workflowGraphJSON(result.Changes)
		case "verification":
			return workflowGraphJSON(result.Verification)
		case "structured":
			return workflowGraphJSON(result.Structured)
		case "agent_id":
			return result.AgentID
		case "mode":
			return result.Mode
		}
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
	if strings.HasPrefix(strings.ToLower(expr), "result.") {
		key := strings.TrimSpace(expr[len("result."):])
		if value, ok := workflowGraphResultJSONValue(result.Output, key); ok {
			return copyWorkflowAnyValue(value), true
		}
		switch strings.ToLower(key) {
		case "tool_results":
			return append([]schema.ToolResult(nil), result.ToolResults...), true
		case "findings":
			return append([]schema.Finding(nil), result.Findings...), true
		case "changes":
			return append([]schema.Change(nil), result.Changes...), true
		case "verification":
			return append([]schema.Verification(nil), result.Verification...), true
		case "structured":
			return append([]schema.StructuredSection(nil), result.Structured...), true
		case "agent_id":
			return result.AgentID, true
		case "mode":
			return result.Mode, true
		}
	}
	if strings.HasPrefix(strings.ToLower(expr), "params.") {
		key := strings.TrimSpace(expr[len("params."):])
		return stage.Params[key], true
	}
	return nil, false
}

func workflowGraphResultJSONValue(output, key string) (any, bool) {
	if strings.TrimSpace(output) == "" || strings.TrimSpace(key) == "" {
		return nil, false
	}
	decoded, ok := workflowStageJSONOutputObject(output)
	if !ok {
		return nil, false
	}
	if value, ok := workflowStageOutputJSONValue(decoded, key); ok {
		return value, true
	}
	return workflowGraphNestedValue(decoded, strings.Split(key, "."))
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
		return workflowGraphCompactRawReference(completed[len(completed)-1].Output.RawOutput)
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
		return workflowGraphCompactRawReference(completed[len(completed)-1].Output.RawOutput), true
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
		if strings.HasPrefix(strings.ToLower(key), "artifacts.") {
			name := strings.TrimSpace(key[len("artifacts."):])
			return workflowGraphWorkflowRunArtifactsValue(workflowGraphNamedArtifacts(stage.Output.Artifacts, name)), true
		}
		if strings.HasPrefix(strings.ToLower(key), "evidence.") {
			name := strings.TrimSpace(key[len("evidence."):])
			return workflowGraphWorkflowRunArtifactsValue(workflowGraphNamedArtifacts(stage.Output.Evidence, name)), true
		}
		switch strings.ToLower(key) {
		case "summary":
			return stage.Output.Summary, true
		case "raw_output", "output":
			return workflowGraphCompactRawReference(stage.Output.RawOutput), true
		case "tool_results":
			return append([]schema.ToolResult(nil), stage.Output.ToolResults...), true
		case "findings":
			return append([]schema.Finding(nil), stage.Output.Findings...), true
		case "changes":
			return append([]schema.Change(nil), stage.Output.Changes...), true
		case "verification":
			if value, ok := stage.Output.Values[key]; ok {
				return copyWorkflowAnyValue(value), true
			}
			if value, ok := stage.Output.Variables[key]; ok {
				return value, true
			}
			return append([]schema.Verification(nil), stage.Output.Verification...), true
		case "artifacts":
			return append([]session.WorkflowRunArtifact(nil), stage.Output.Artifacts...), true
		case "evidence":
			return append([]session.WorkflowRunArtifact(nil), stage.Output.Evidence...), true
		case "decision":
			return stage.Output.Decision, true
		case "next_actions":
			return append([]string(nil), stage.Output.NextActions...), true
		case "changed_files", "blockers":
			if value, ok := stage.Output.Values[key]; ok {
				return copyWorkflowAnyValue(value), true
			}
			if value, ok := stage.Output.Variables[key]; ok {
				return value, true
			}
			return nil, false
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
			return workflowGraphCompactRawReference(stage.Result.Output), true
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

func workflowGraphNamedArtifacts(artifacts []session.WorkflowRunArtifact, name string) []session.WorkflowRunArtifact {
	name = normalizeWorkflowArtifactName(name)
	if name == "" {
		return nil
	}
	out := make([]session.WorkflowRunArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if normalizeWorkflowArtifactName(artifact.Metadata["name"]) == name ||
			normalizeWorkflowArtifactName(artifact.ID) == name ||
			normalizeWorkflowArtifactName(artifact.Title) == name ||
			normalizeWorkflowArtifactName(artifact.Kind) == name {
			out = append(out, artifact)
		}
	}
	return out
}

func workflowGraphWorkflowRunArtifactsValue(artifacts []session.WorkflowRunArtifact) any {
	switch len(artifacts) {
	case 0:
		return nil
	case 1:
		return artifacts[0]
	default:
		return append([]session.WorkflowRunArtifact(nil), artifacts...)
	}
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
			return workflowGraphCompactRawReference(stage.Output.RawOutput)
		case "tool_results":
			return workflowGraphJSON(stage.Output.ToolResults)
		case "findings":
			return workflowGraphJSON(stage.Output.Findings)
		case "changes":
			return workflowGraphJSON(stage.Output.Changes)
		case "verification":
			if value, ok := stage.Output.Values[key]; ok {
				return workflowGraphValueString(value)
			}
			if value := stage.Output.Variables[key]; strings.TrimSpace(value) != "" {
				return value
			}
			return workflowGraphJSON(stage.Output.Verification)
		case "artifacts":
			return workflowGraphJSON(stage.Output.Artifacts)
		case "evidence":
			return workflowGraphJSON(stage.Output.Evidence)
		case "decision":
			return stage.Output.Decision
		case "next_actions":
			return workflowGraphJSON(stage.Output.NextActions)
		case "changed_files", "blockers":
			if value, ok := stage.Output.Values[key]; ok {
				return workflowGraphValueString(value)
			}
			return stage.Output.Variables[key]
		default:
			if strings.HasPrefix(strings.ToLower(key), "artifacts.") || strings.HasPrefix(strings.ToLower(key), "evidence.") {
				if value, ok := resolveWorkflowStageReferenceValue(expr, completed); ok {
					return workflowGraphValueString(value)
				}
			}
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
			return workflowGraphCompactRawReference(stage.Result.Output)
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

func workflowGraphCompactRawReference(value string) string {
	value = strings.TrimSpace(value)
	const limit = 2400
	if len([]byte(value)) <= limit {
		return value
	}
	trimmed := trimWorkflowGraphContextValue(value, limit)
	return strings.TrimSpace(trimmed) + " [raw output compacted by workflow context budget]"
}

func (w *WorkflowRunner) captureWorkflowGraphApprovalContext(graph workflowGraph, index int, request, responseContent string, responseMessage schema.Message, completed []WorkflowStageResult, results []schema.ToolResult, stagePrompt string) {
	callID := firstSuspendedToolCallID(results)
	if callID == "" {
		return
	}
	stage := graph.Stages[index]
	_ = w.runtime.AnnotateWorkflowGraphPendingApproval(callID, graph.Name, WorkflowStage(stage.Name), request, responseContent, responseMessage, completed, index, stagePrompt)
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

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"gopkg.in/yaml.v3"
)

// WorkflowGraphDocument is the persisted, API-facing workflow graph shape.
type WorkflowGraphDocument struct {
	Name        string                       `json:"name" yaml:"name"`
	Description string                       `json:"description,omitempty" yaml:"description,omitempty"`
	Stages      []WorkflowGraphStageDocument `json:"stages" yaml:"stages"`
}

// WorkflowGraphStageDocument is one persisted workflow stage.
type WorkflowGraphStageDocument struct {
	Name               string                                     `json:"name" yaml:"name"`
	NodeType           string                                     `json:"node_type,omitempty" yaml:"node_type,omitempty"`
	Agent              string                                     `json:"agent" yaml:"agent"`
	Skill              string                                     `json:"skill" yaml:"skill"`
	Tool               string                                     `json:"tool,omitempty" yaml:"tool,omitempty"`
	Params             map[string]string                          `json:"params,omitempty" yaml:"params,omitempty"`
	Input              map[string]string                          `json:"input,omitempty" yaml:"input,omitempty"`
	Outputs            map[string]string                          `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Artifacts          []WorkflowGraphArtifactDocument            `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	AcceptanceCriteria []WorkflowGraphAcceptanceCriterionDocument `json:"acceptance_criteria,omitempty" yaml:"acceptance_criteria,omitempty"`
	Policy             string                                     `json:"policy,omitempty" yaml:"policy,omitempty"`
	Condition          string                                     `json:"condition,omitempty" yaml:"condition,omitempty"`
	Routes             map[string]string                          `json:"routes,omitempty" yaml:"routes,omitempty"`
	SwitchOn           string                                     `json:"switch_on,omitempty" yaml:"switch_on,omitempty"`
	Cases              map[string]string                          `json:"cases,omitempty" yaml:"cases,omitempty"`
	Retry              WorkflowGraphRetry                         `json:"retry,omitempty" yaml:"retry,omitempty"`
	OnError            []string                                   `json:"on_error,omitempty" yaml:"on_error,omitempty"`
	Approval           bool                                       `json:"approval,omitempty" yaml:"approval,omitempty"`
	NextStrategy       string                                     `json:"next_strategy,omitempty" yaml:"next_strategy,omitempty"`
	Next               []string                                   `json:"next,omitempty" yaml:"next,omitempty"`
	Position           WorkflowGraphPosition                      `json:"position,omitempty" yaml:"position,omitempty"`
}

// WorkflowGraphRetry configures retry behavior for an executable stage.
type WorkflowGraphRetry struct {
	MaxAttempts int `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`
}

// WorkflowGraphArtifactDocument declares a replay artifact emitted from a stage result.
type WorkflowGraphArtifactDocument struct {
	Name     string            `json:"name,omitempty" yaml:"name,omitempty"`
	Kind     string            `json:"kind,omitempty" yaml:"kind,omitempty"`
	Title    string            `json:"title,omitempty" yaml:"title,omitempty"`
	Ref      string            `json:"ref,omitempty" yaml:"ref,omitempty"`
	Summary  string            `json:"summary,omitempty" yaml:"summary,omitempty"`
	Content  string            `json:"content,omitempty" yaml:"content,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// WorkflowGraphAcceptanceCriterionDocument declares a stage acceptance check.
type WorkflowGraphAcceptanceCriterionDocument struct {
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Ref         string `json:"ref,omitempty" yaml:"ref,omitempty"`
	Equals      string `json:"equals,omitempty" yaml:"equals,omitempty"`
	Contains    string `json:"contains,omitempty" yaml:"contains,omitempty"`
	Expected    string `json:"expected,omitempty" yaml:"expected,omitempty"`
	Exists      *bool  `json:"exists,omitempty" yaml:"exists,omitempty"`
}

// WorkflowGraphPosition stores visual editor node placement.
type WorkflowGraphPosition struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
}

// WorkflowGraphSummary describes a built-in or persisted workflow graph.
type WorkflowGraphSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	Path        string `json:"path,omitempty"`
	Stages      int    `json:"stages,omitempty"`
	Valid       bool   `json:"valid"`
	Error       string `json:"error,omitempty"`
}

// WorkflowExecutorOption describes a runnable workflow entry. Some entries are
// persisted graph workflows; legacy compatibility executors remain runnable but
// are not directly editable as workflow graphs.
type WorkflowExecutorOption struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Source          string `json:"source"`
	Path            string `json:"path,omitempty"`
	Stages          int    `json:"stages,omitempty"`
	Valid           bool   `json:"valid"`
	Error           string `json:"error,omitempty"`
	Editable        bool   `json:"editable"`
	Legacy          bool   `json:"legacy,omitempty"`
	Compatibility   bool   `json:"compatibility,omitempty"`
	Overridden      bool   `json:"overridden,omitempty"`
	OverridesLegacy bool   `json:"overrides_legacy,omitempty"`
	OverridePath    string `json:"override_path,omitempty"`
	Detail          string `json:"detail,omitempty"`
}

// WorkflowGraphValidationIssue is a structured graph validation diagnostic.
type WorkflowGraphValidationIssue struct {
	Level   string `json:"level"`
	Stage   string `json:"stage,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// WorkflowGraphValidationResult is returned by validation-only APIs.
type WorkflowGraphValidationResult struct {
	Valid    bool                              `json:"valid"`
	Name     string                            `json:"name,omitempty"`
	Stages   int                               `json:"stages,omitempty"`
	Issues   []WorkflowGraphValidationIssue    `json:"issues,omitempty"`
	Parallel []WorkflowGraphParallelValidation `json:"parallel,omitempty"`
}

// WorkflowGraphParallelValidation explains whether a parallel node can use
// goroutine-level concurrent execution and why a branch is not eligible.
type WorkflowGraphParallelValidation struct {
	Stage       string                                  `json:"stage"`
	Enabled     bool                                    `json:"enabled"`
	Eligible    bool                                    `json:"eligible"`
	BranchCount int                                     `json:"branch_count"`
	Join        string                                  `json:"join,omitempty"`
	Branches    []WorkflowGraphParallelBranchValidation `json:"branches,omitempty"`
	Issues      []WorkflowGraphValidationIssue          `json:"issues,omitempty"`
}

// WorkflowGraphParallelBranchValidation describes one branch from a parallel node.
type WorkflowGraphParallelBranchValidation struct {
	Start    string                         `json:"start"`
	Eligible bool                           `json:"eligible"`
	Stages   []string                       `json:"stages,omitempty"`
	Agents   []string                       `json:"agents,omitempty"`
	Join     string                         `json:"join,omitempty"`
	Reason   string                         `json:"reason,omitempty"`
	Issues   []WorkflowGraphValidationIssue `json:"issues,omitempty"`
}

// WorkflowExpressionValidationResult is returned by Studio expression/reference validation APIs.
type WorkflowExpressionValidationResult struct {
	Valid       bool                           `json:"valid"`
	Mode        string                         `json:"mode,omitempty"`
	Expression  string                         `json:"expression,omitempty"`
	ValueType   string                         `json:"value_type,omitempty"`
	References  []WorkflowExpressionReference  `json:"references,omitempty"`
	Suggestions []WorkflowExpressionSuggestion `json:"suggestions,omitempty"`
	Issues      []WorkflowGraphValidationIssue `json:"issues,omitempty"`
}

// WorkflowExpressionReference describes one reference found in a workflow expression.
type WorkflowExpressionReference struct {
	Expression string `json:"expression"`
	Valid      bool   `json:"valid"`
	Type       string `json:"type,omitempty"`
	Stage      string `json:"stage,omitempty"`
	Output     string `json:"output,omitempty"`
	Path       string `json:"path,omitempty"`
	Message    string `json:"message,omitempty"`
}

// WorkflowExpressionSuggestion is one reference completion candidate for Studio.
type WorkflowExpressionSuggestion struct {
	Reference   string `json:"reference"`
	Type        string `json:"type,omitempty"`
	Stage       string `json:"stage,omitempty"`
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`
}

// WorkflowOptionSet exposes available agents and skills for workflow editors.
type WorkflowOptionSet struct {
	Agents              []WorkflowAgentOption              `json:"agents"`
	Skills              []WorkflowSkillOption              `json:"skills"`
	Tools               []string                           `json:"tools"`
	WorkflowExecutors   []WorkflowExecutorOption           `json:"workflow_executors,omitempty"`
	NodeTypes           []WorkflowNodeTypeOption           `json:"node_types,omitempty"`
	PolicyRules         []WorkflowPolicyRuleOption         `json:"policy_rules,omitempty"`
	ExpressionFunctions []WorkflowExpressionFunctionOption `json:"expression_functions,omitempty"`
	Templates           []WorkflowTemplateSummary          `json:"templates,omitempty"`
	TeamTemplates       []TeamTemplateSummary              `json:"team_templates,omitempty"`
}

type WorkflowAgentOption struct {
	Name string `json:"name"`
	Mode string `json:"mode,omitempty"`
}

type WorkflowSkillOption struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	PreferredAgent string   `json:"preferred_agent,omitempty"`
	Mode           string   `json:"mode,omitempty"`
	NextSkills     []string `json:"next_skills,omitempty"`
}

func (w *WorkflowRunner) ListWorkflowGraphs() []WorkflowGraphSummary {
	summaries := []WorkflowGraphSummary{}
	root, err := w.workflowGraphRoot()
	if err != nil {
		return summaries
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return summaries
		}
		summaries = append(summaries, WorkflowGraphSummary{Name: "(custom workflows)", Source: "custom", Path: root, Valid: false, Error: err.Error()})
		return summaries
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path, err := w.workflowGraphPath(name)
		if err != nil {
			summaries = append(summaries, WorkflowGraphSummary{Name: name, Source: "custom", Valid: false, Error: err.Error()})
			continue
		}
		doc, err := w.LoadWorkflowGraphDocument(name)
		if err != nil {
			source := "custom"
			if _, builtin := w.registry[normalizePersistedWorkflowName(name)]; builtin {
				source = "default"
			}
			summaries = append(summaries, WorkflowGraphSummary{Name: name, Source: source, Path: path, Valid: false, Error: err.Error()})
			continue
		}
		source := "custom"
		if _, builtin := w.registry[normalizePersistedWorkflowName(name)]; builtin {
			source = "default"
		}
		summaries = append(summaries, WorkflowGraphSummary{Name: doc.Name, Description: doc.Description, Source: source, Path: path, Stages: len(doc.Stages), Valid: true})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if sourceRank(summaries[i].Source) != sourceRank(summaries[j].Source) {
			return sourceRank(summaries[i].Source) < sourceRank(summaries[j].Source)
		}
		return summaries[i].Name < summaries[j].Name
	})
	return summaries
}

// WorkflowExecutors returns every runnable workflow entry: persisted graph
// workflows plus built-in compatibility executors that can be overridden by a
// graph with the same name.
func (w *WorkflowRunner) WorkflowExecutors() []WorkflowExecutorOption {
	graphs := w.ListWorkflowGraphs()
	out := make([]WorkflowExecutorOption, 0, len(graphs)+len(w.registry))
	graphByName := make(map[string]WorkflowGraphSummary, len(graphs))
	legacyNames := legacyWorkflowExecutorNames()
	for _, graph := range graphs {
		name := normalizePersistedWorkflowName(graph.Name)
		overridesLegacy := legacyNames[name]
		graphByName[name] = graph
		overridePath := ""
		if overridesLegacy {
			overridePath = graph.Path
		}
		out = append(out, WorkflowExecutorOption{
			Name:            graph.Name,
			Description:     graph.Description,
			Source:          graph.Source,
			Path:            graph.Path,
			Stages:          graph.Stages,
			Valid:           graph.Valid,
			Error:           graph.Error,
			Editable:        true,
			OverridesLegacy: overridesLegacy,
			OverridePath:    overridePath,
			Detail:          workflowExecutorDetail(graph.Source, overridesLegacy, graph.Valid),
		})
	}
	for _, name := range legacyWorkflowExecutorNameList(w.registry) {
		graph, overridden := graphByName[name]
		if overridden && graph.Valid {
			continue
		}
		out = append(out, WorkflowExecutorOption{
			Name:          name,
			Description:   legacyWorkflowExecutorDescription(name),
			Source:        "legacy_executor",
			Valid:         !overridden,
			Error:         legacyWorkflowExecutorError(graph),
			Editable:      false,
			Legacy:        true,
			Compatibility: true,
			Overridden:    overridden,
			OverridePath:  graph.Path,
			Detail:        workflowExecutorDetail("legacy_executor", overridden, !overridden),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if workflowExecutorSourceRank(out[i]) != workflowExecutorSourceRank(out[j]) {
			return workflowExecutorSourceRank(out[i]) < workflowExecutorSourceRank(out[j])
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func legacyWorkflowExecutorNameList(registry map[string]workflowDefinition) []string {
	if len(registry) == 0 {
		registry = builtInWorkflowRegistry()
	}
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, normalizePersistedWorkflowName(name))
	}
	sort.Strings(names)
	return names
}

func legacyWorkflowExecutorNames() map[string]bool {
	out := make(map[string]bool)
	for _, name := range legacyWorkflowExecutorNameList(nil) {
		out[name] = true
	}
	return out
}

func legacyWorkflowExecutorDescription(name string) string {
	switch normalizePersistedWorkflowName(name) {
	case workflowNamePlanFixAudit:
		return "Compatibility executor for the original plan, fix, and audit workflow. A saved graph named plan-fix-audit overrides it."
	case workflowNameSkillChain:
		return "Compatibility executor that chains matched skills. A saved graph named skill-chain overrides it."
	default:
		return "Compatibility workflow executor. A saved graph with the same name overrides it."
	}
}

func legacyWorkflowExecutorError(graph WorkflowGraphSummary) string {
	if graph.Valid || strings.TrimSpace(graph.Name) == "" {
		return ""
	}
	return graph.Error
}

func workflowExecutorDetail(source string, overridden, valid bool) string {
	if overridden {
		if valid {
			return "legacy compatibility executor is overridden by a saved workflow graph"
		}
		return "legacy compatibility executor is hidden because a saved workflow graph with the same name exists but is invalid"
	}
	if source == "legacy_executor" {
		return "built-in compatibility executor; fork the matching workflow template or save a graph with the same name to customize it"
	}
	return "editable workflow graph"
}

func workflowExecutorSourceRank(item WorkflowExecutorOption) int {
	if item.Overridden {
		return 3
	}
	switch item.Source {
	case "default":
		return 0
	case "custom":
		return 1
	case "legacy_executor":
		return 2
	default:
		return 4
	}
}

func sourceRank(source string) int {
	switch source {
	case "default":
		return 0
	case "custom":
		return 1
	default:
		return 2
	}
}

func (w *WorkflowRunner) LoadWorkflowGraphDocument(name string) (WorkflowGraphDocument, error) {
	path, err := w.workflowGraphPath(name)
	if err != nil {
		return WorkflowGraphDocument{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowGraphDocument{}, err
	}
	var doc WorkflowGraphDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return WorkflowGraphDocument{}, fmt.Errorf("parse workflow graph: %w", err)
	}
	if err := w.validateWorkflowGraph(normalizePersistedWorkflowName(name), doc.toInternalGraph()); err != nil {
		return WorkflowGraphDocument{}, err
	}
	return doc, nil
}

func (w *WorkflowRunner) SaveWorkflowGraphDocument(name string, doc WorkflowGraphDocument) error {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return err
	}
	if strings.TrimSpace(doc.Name) == "" {
		doc.Name = name
	}
	if normalizeWorkflowSkillName(doc.Name) != normalizeWorkflowSkillName(name) {
		return fmt.Errorf("workflow graph name %q does not match %q", doc.Name, name)
	}
	if err := w.validateWorkflowGraph(name, doc.toInternalGraph()); err != nil {
		return err
	}
	path, err := w.workflowGraphPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ValidateWorkflowGraphDocument validates a graph document without saving it.
func (w *WorkflowRunner) ValidateWorkflowGraphDocument(name string, doc WorkflowGraphDocument) WorkflowGraphValidationResult {
	name = normalizePersistedWorkflowName(name)
	if name == "" {
		name = normalizePersistedWorkflowName(doc.Name)
	}
	graph := doc.toInternalGraph()
	result := WorkflowGraphValidationResult{Name: name, Stages: len(doc.Stages), Parallel: w.workflowGraphParallelValidations(graph)}
	issues := w.validateWorkflowGraphDocumentIssues(name, doc)
	result.Valid = !workflowGraphExpressionHasError(issues)
	result.Issues = issues
	return result
}

// ValidateWorkflowExpression validates a Studio-authored expression or reference against a graph document.
func (w *WorkflowRunner) ValidateWorkflowExpression(name string, doc WorkflowGraphDocument, expression, mode string) WorkflowExpressionValidationResult {
	return w.ValidateWorkflowExpressionWithRun(name, doc, session.WorkflowRunSnapshot{}, expression, mode)
}

// ValidateWorkflowExpressionWithRun validates a Studio-authored expression with optional run-output type hints.
func (w *WorkflowRunner) ValidateWorkflowExpressionWithRun(name string, doc WorkflowGraphDocument, run session.WorkflowRunSnapshot, expression, mode string) WorkflowExpressionValidationResult {
	mode = normalizeWorkflowExpressionMode(mode)
	expression = strings.TrimSpace(expression)
	outputs := workflowGraphExpressionOutputIndex(w, name, doc, run)
	result := WorkflowExpressionValidationResult{
		Mode:        mode,
		Expression:  expression,
		Suggestions: workflowGraphReferenceSuggestionsFromOutputs(outputs),
	}
	if expression == "" {
		result.Issues = append(result.Issues, WorkflowGraphValidationIssue{Level: "error", Field: "expression", Message: "workflow expression is required"})
		return result
	}
	graphValidation := w.ValidateWorkflowGraphDocument(name, doc)
	for _, issue := range graphValidation.Issues {
		if issue.Level == "error" {
			result.Issues = append(result.Issues, WorkflowGraphValidationIssue{Level: "warning", Stage: issue.Stage, Field: issue.Field, Message: "graph validation issue may affect expression validation: " + issue.Message})
		}
	}
	refs, syntaxIssues := workflowGraphExpressionReferences(expression, mode)
	result.Issues = append(result.Issues, syntaxIssues...)
	if len(refs) == 0 {
		if mode == "reference" || workflowGraphLooksReference(expression) {
			refs = []string{expression}
		}
	}
	for _, ref := range refs {
		validation := workflowGraphValidateReference(ref, outputs)
		result.References = append(result.References, validation)
		if !validation.Valid {
			result.Issues = append(result.Issues, WorkflowGraphValidationIssue{Level: "error", Field: "expression", Message: validation.Message})
		}
	}
	switch mode {
	case "condition", "policy", "expression":
		result.ValueType = "boolean"
	case "switch", "reference":
		if len(result.References) == 1 && result.References[0].Valid {
			result.ValueType = result.References[0].Type
		} else {
			result.ValueType = "unknown"
		}
	default:
		result.ValueType = "unknown"
	}
	result.Valid = !workflowGraphExpressionHasError(result.Issues)
	return result
}

// RenderWorkflowGraphDocument renders a graph document as yaml or json bytes.
func RenderWorkflowGraphDocument(doc WorkflowGraphDocument, format string) ([]byte, string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "yaml", "yml":
		data, err := yaml.Marshal(doc)
		return data, "application/yaml; charset=utf-8", err
	case "json":
		data, err := json.MarshalIndent(doc, "", "  ")
		return append(data, '\n'), "application/json", err
	default:
		return nil, "", fmt.Errorf("unsupported workflow graph export format: %s", format)
	}
}

func (w *WorkflowRunner) DeleteWorkflowGraphDocument(name string) error {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return err
	}
	path, err := w.workflowGraphPath(name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (w *WorkflowRunner) WorkflowOptions() WorkflowOptionSet {
	options := WorkflowOptionSet{}
	if w == nil || w.runtime == nil {
		return options
	}
	for _, name := range w.runtime.AgentNames() {
		profile, ok := w.runtime.Profile(name)
		if !ok {
			continue
		}
		options.Agents = append(options.Agents, WorkflowAgentOption{Name: name, Mode: profile.Mode})
	}
	for _, skill := range w.runtime.SkillList() {
		options.Skills = append(options.Skills, WorkflowSkillOption{
			Name:           skill.Name,
			Description:    skill.Description,
			PreferredAgent: skill.PreferredAgent,
			Mode:           skill.Mode,
			NextSkills:     append([]string(nil), skill.NextSkills...),
		})
	}
	sort.Slice(options.Skills, func(i, j int) bool { return options.Skills[i].Name < options.Skills[j].Name })
	options.Tools = w.runtime.ToolNames()
	sort.Strings(options.Tools)
	options.WorkflowExecutors = w.WorkflowExecutors()
	options.NodeTypes = w.WorkflowNodeTypes()
	options.PolicyRules = w.WorkflowPolicyRules()
	options.ExpressionFunctions = w.WorkflowExpressionFunctions()
	options.Templates = w.WorkflowTemplates()
	options.TeamTemplates = w.TeamTemplates()
	return options
}

func (d WorkflowGraphDocument) toInternalGraph() workflowGraph {
	stages := make([]workflowGraphStage, 0, len(d.Stages))
	for _, stage := range d.Stages {
		stages = append(stages, workflowGraphStage{
			Name:               stage.Name,
			NodeType:           stage.NodeType,
			Agent:              stage.Agent,
			Skill:              stage.Skill,
			Tool:               stage.Tool,
			Params:             copyStringMap(stage.Params),
			Input:              copyStringMap(stage.Input),
			Outputs:            copyStringMap(stage.Outputs),
			Artifacts:          workflowGraphArtifactsToInternal(stage.Artifacts),
			AcceptanceCriteria: workflowGraphAcceptanceCriteriaToInternal(stage.AcceptanceCriteria),
			Policy:             stage.Policy,
			Condition:          stage.Condition,
			Routes:             copyStringMap(stage.Routes),
			SwitchOn:           stage.SwitchOn,
			Cases:              copyStringMap(stage.Cases),
			Retry:              workflowGraphRetry{MaxAttempts: stage.Retry.MaxAttempts},
			OnError:            append([]string(nil), stage.OnError...),
			Approval:           stage.Approval,
			NextStrategy:       stage.NextStrategy,
			Next:               append([]string(nil), stage.Next...),
		})
	}
	return workflowGraph{Name: d.Name, Description: d.Description, Stages: stages}
}

func workflowGraphAcceptanceCriteriaToInternal(criteria []WorkflowGraphAcceptanceCriterionDocument) []workflowGraphAcceptanceCriterion {
	if len(criteria) == 0 {
		return nil
	}
	out := make([]workflowGraphAcceptanceCriterion, 0, len(criteria))
	for _, criterion := range criteria {
		out = append(out, workflowGraphAcceptanceCriterion{
			Name:        criterion.Name,
			Description: criterion.Description,
			Ref:         criterion.Ref,
			Equals:      criterion.Equals,
			Contains:    criterion.Contains,
			Expected:    criterion.Expected,
			Exists:      criterion.Exists,
		})
	}
	return out
}

func workflowGraphArtifactsToInternal(artifacts []WorkflowGraphArtifactDocument) []workflowGraphArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	out := make([]workflowGraphArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, workflowGraphArtifact{
			Name:     artifact.Name,
			Kind:     artifact.Kind,
			Title:    artifact.Title,
			Ref:      artifact.Ref,
			Summary:  artifact.Summary,
			Content:  artifact.Content,
			Metadata: copyStringMap(artifact.Metadata),
		})
	}
	return out
}

func validateWorkflowGraphArtifactIssues(graphName, stageName string, artifacts []WorkflowGraphArtifactDocument) []WorkflowGraphValidationIssue {
	if len(artifacts) == 0 {
		return nil
	}
	issues := make([]WorkflowGraphValidationIssue, 0)
	seen := make(map[string]struct{}, len(artifacts))
	for i, artifact := range artifacts {
		label := fallbackWorkflowGraphValue(artifact.Name, fmt.Sprintf("#%d", i+1))
		name := normalizeWorkflowArtifactName(artifact.Name)
		if strings.TrimSpace(artifact.Name) != "" {
			if name == "" {
				issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stageName, Field: "artifacts.name", Message: fmt.Sprintf("workflow graph %s stage %s artifact %s has invalid name", graphName, stageName, label)})
			} else if _, ok := seen[name]; ok {
				issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stageName, Field: "artifacts.name", Message: fmt.Sprintf("workflow graph %s stage %s has duplicate artifact %q", graphName, stageName, artifact.Name)})
			}
			seen[name] = struct{}{}
		}
		if strings.TrimSpace(artifact.Ref) == "" && strings.TrimSpace(artifact.Content) == "" {
			issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stageName, Field: "artifacts.ref", Message: fmt.Sprintf("workflow graph %s stage %s artifact %s must declare ref or content", graphName, stageName, label)})
		}
		if strings.TrimSpace(artifact.Kind) != "" && normalizeWorkflowArtifactKind(artifact.Kind) == "" {
			issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stageName, Field: "artifacts.kind", Message: fmt.Sprintf("workflow graph %s stage %s artifact %s has invalid kind", graphName, stageName, label)})
		}
	}
	return issues
}

func (w *WorkflowRunner) validateWorkflowGraphDocumentIssues(name string, doc WorkflowGraphDocument) []WorkflowGraphValidationIssue {
	issues := make([]WorkflowGraphValidationIssue, 0)
	if err := validatePersistedWorkflowName(name); err != nil {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Field: "name", Message: err.Error()})
	}
	if strings.TrimSpace(doc.Name) == "" {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Field: "name", Message: "workflow graph missing name"})
	} else if name != "" && normalizeWorkflowSkillName(doc.Name) != normalizeWorkflowSkillName(name) {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Field: "name", Message: fmt.Sprintf("workflow graph name %q does not match %q", doc.Name, name)})
	}
	if len(doc.Stages) == 0 {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Field: "stages", Message: fmt.Sprintf("workflow graph %s has no stages", fallbackWorkflowGraphValue(doc.Name, name))})
		return issues
	}
	seen := make(map[string]struct{}, len(doc.Stages))
	stageNames := make(map[string]string, len(doc.Stages))
	for i, stage := range doc.Stages {
		stageLabel := fallbackWorkflowGraphValue(stage.Name, fmt.Sprintf("#%d", i))
		key := normalizeWorkflowSkillName(stage.Name)
		if strings.TrimSpace(stage.Name) == "" {
			issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stageLabel, Field: "name", Message: fmt.Sprintf("workflow graph %s stage %d missing name", fallbackWorkflowGraphValue(doc.Name, name), i)})
		} else if _, ok := seen[key]; ok {
			issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "name", Message: fmt.Sprintf("workflow graph %s has duplicate stage %q", fallbackWorkflowGraphValue(doc.Name, name), stage.Name)})
		}
		seen[key] = struct{}{}
		stageNames[key] = stage.Name
		internalStage := workflowGraphStage{
			Name:     stage.Name,
			NodeType: stage.NodeType,
			Agent:    stage.Agent,
			Skill:    stage.Skill,
			Params:   copyStringMap(stage.Params),
		}
		issues = append(issues, validateWorkflowGraphArtifactIssues(fallbackWorkflowGraphValue(doc.Name, name), stage.Name, stage.Artifacts)...)
		if isTeamWorkflowNode(internalStage) {
			teamName := workflowGraphTeamTemplateName(internalStage)
			if _, ok := w.TeamTemplate(teamName); !ok {
				issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "params.team", Message: fmt.Sprintf("workflow graph %s stage %s references unknown team template %s", fallbackWorkflowGraphValue(doc.Name, name), stage.Name, teamName)})
			}
			continue
		}
		if isRepeatWorkflowNode(internalStage) {
			if strings.TrimSpace(workflowGraphRepeatBodyName(internalStage)) == "" {
				issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "params.stage", Message: fmt.Sprintf("workflow graph %s stage %s missing repeat body params.stage/body", fallbackWorkflowGraphValue(doc.Name, name), stage.Name)})
			}
			continue
		}
		if isSubWorkflowNode(internalStage) {
			subWorkflow := workflowGraphSubWorkflowName(internalStage)
			if strings.TrimSpace(subWorkflow) == "" {
				issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "params.workflow", Message: fmt.Sprintf("workflow graph %s stage %s missing sub workflow params.workflow", fallbackWorkflowGraphValue(doc.Name, name), stage.Name)})
			} else if normalizePersistedWorkflowName(subWorkflow) == normalizePersistedWorkflowName(doc.Name) {
				issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "params.workflow", Message: fmt.Sprintf("workflow graph %s stage %s cannot call itself as a sub workflow", fallbackWorkflowGraphValue(doc.Name, name), stage.Name)})
			} else if err := validatePersistedWorkflowName(normalizePersistedWorkflowName(subWorkflow)); err != nil {
				issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "params.workflow", Message: err.Error()})
			}
			continue
		}
		if isVisualOnlyWorkflowNode(internalStage) || isControlWorkflowNode(internalStage) {
			continue
		}
		if strings.TrimSpace(stage.Agent) == "" {
			issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "agent", Message: fmt.Sprintf("workflow graph %s stage %s missing agent", fallbackWorkflowGraphValue(doc.Name, name), stage.Name)})
		}
		if strings.TrimSpace(stage.Skill) == "" {
			issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "skill", Message: fmt.Sprintf("workflow graph %s stage %s missing skill", fallbackWorkflowGraphValue(doc.Name, name), stage.Name)})
		}
		if stage.Retry.MaxAttempts < 0 {
			issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "retry.max_attempts", Message: fmt.Sprintf("workflow graph %s stage %s retry.max_attempts must not be negative", fallbackWorkflowGraphValue(doc.Name, name), stage.Name)})
		}
	}
	for _, stage := range doc.Stages {
		internalStage := workflowGraphStage{
			Name:     stage.Name,
			NodeType: stage.NodeType,
			Params:   copyStringMap(stage.Params),
			Next:     append([]string(nil), stage.Next...),
			OnError:  append([]string(nil), stage.OnError...),
			Routes:   copyStringMap(stage.Routes),
			Cases:    copyStringMap(stage.Cases),
		}
		if isRepeatWorkflowNode(internalStage) {
			bodyName := workflowGraphRepeatBodyName(internalStage)
			if strings.TrimSpace(bodyName) != "" {
				if _, ok := stageNames[normalizeWorkflowSkillName(bodyName)]; !ok {
					issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "params.stage", Message: fmt.Sprintf("workflow graph %s stage %s references unknown repeat body stage %q", fallbackWorkflowGraphValue(doc.Name, name), stage.Name, bodyName)})
				}
			}
		}
		for _, next := range workflowGraphStageNextReferences(internalStage) {
			if _, ok := stageNames[normalizeWorkflowSkillName(next)]; !ok {
				issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Stage: stage.Name, Field: "next", Message: fmt.Sprintf("workflow graph %s stage %s references unknown next stage %q", fallbackWorkflowGraphValue(doc.Name, name), stage.Name, next)})
			}
		}
	}
	issues = append(issues, workflowGraphQualityDesignWarnings(fallbackWorkflowGraphValue(doc.Name, name), doc)...)
	return issues
}

func workflowGraphQualityDesignWarnings(graphName string, doc WorkflowGraphDocument) []WorkflowGraphValidationIssue {
	if !workflowGraphDocumentLooksComplex(doc) {
		return nil
	}
	var hasAcceptance, hasQualityGate, hasVerifier, hasArtifacts, hasOutputs, hasApproval bool
	for _, stage := range doc.Stages {
		internalStage := workflowGraphStage{
			Name:     stage.Name,
			NodeType: stage.NodeType,
			Params:   copyStringMap(stage.Params),
			Approval: stage.Approval,
		}
		nodeType := normalizeWorkflowSkillName(stage.NodeType)
		if len(stage.AcceptanceCriteria) > 0 {
			hasAcceptance = true
		}
		if nodeType == "quality_gate" || nodeType == "quality_guard" || nodeType == "quality-guard" {
			hasQualityGate = true
		}
		if workflowGraphStageLooksLikeVerifier(stage) {
			hasVerifier = true
		}
		if len(stage.Artifacts) > 0 {
			hasArtifacts = true
		}
		if len(stage.Outputs) > 0 {
			hasOutputs = true
		}
		if stage.Approval || requiresWorkflowGraphCheckpointApproval(internalStage) || nodeType == "input_gate" || nodeType == "manual_input" {
			hasApproval = true
		}
	}
	issues := make([]WorkflowGraphValidationIssue, 0, 6)
	if !hasAcceptance {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "warning", Field: "acceptance_criteria", Message: fmt.Sprintf("complex workflow %s should declare acceptance criteria on key stages", graphName)})
	}
	if !hasQualityGate {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "warning", Field: "node_type", Message: fmt.Sprintf("complex workflow %s should include a quality_gate before final delivery or release", graphName)})
	}
	if !hasVerifier {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "warning", Field: "skill", Message: fmt.Sprintf("complex workflow %s should include a verifier, auditor, reviewer, or quality stage", graphName)})
	}
	if !hasArtifacts {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "warning", Field: "artifacts", Message: fmt.Sprintf("complex workflow %s should declare replay artifacts for important evidence", graphName)})
	}
	if !hasOutputs {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "warning", Field: "outputs", Message: fmt.Sprintf("complex workflow %s should declare output contracts for downstream data flow", graphName)})
	}
	if !hasApproval {
		issues = append(issues, WorkflowGraphValidationIssue{Level: "warning", Field: "approval", Message: fmt.Sprintf("complex workflow %s should include a checkpoint, approval, input gate, or explicit human review path for risky tasks", graphName)})
	}
	return issues
}

func workflowGraphDocumentLooksComplex(doc WorkflowGraphDocument) bool {
	executable := 0
	control := 0
	for _, stage := range doc.Stages {
		internalStage := workflowGraphStage{
			Name:     stage.Name,
			NodeType: stage.NodeType,
			Params:   copyStringMap(stage.Params),
		}
		switch {
		case isVisualOnlyWorkflowNode(internalStage):
			continue
		case isControlWorkflowNode(internalStage) || isRepeatWorkflowNode(internalStage) || isSubWorkflowNode(internalStage):
			control++
		default:
			executable++
		}
	}
	return executable >= 3 || (executable >= 2 && control > 0) || control >= 2 || len(doc.Stages) >= 5
}

func workflowGraphStageLooksLikeVerifier(stage WorkflowGraphStageDocument) bool {
	combined := strings.ToLower(strings.Join([]string{stage.Name, stage.NodeType, stage.Agent, stage.Skill}, " "))
	for _, token := range []string{"verify", "verifier", "audit", "auditor", "review", "reviewer", "quality", "test"} {
		if strings.Contains(combined, token) {
			return true
		}
	}
	return false
}

type workflowGraphReferenceOutput struct {
	Stage       string
	Name        string
	Type        string
	Source      string
	Description string
	Sample      any
}

func normalizeWorkflowExpressionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "reference", "ref":
		return "reference"
	case "condition":
		return "condition"
	case "policy", "policy_guard", "guard", "quality_gate", "quality_guard", "quality-guard":
		return "policy"
	case "switch", "router":
		return "switch"
	default:
		return "expression"
	}
}

func workflowGraphExpressionHasError(issues []WorkflowGraphValidationIssue) bool {
	for _, issue := range issues {
		if strings.EqualFold(issue.Level, "error") {
			return true
		}
	}
	return false
}

func workflowGraphExpressionReferences(expression, mode string) ([]string, []WorkflowGraphValidationIssue) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, nil
	}
	if normalizeWorkflowExpressionMode(mode) == "reference" {
		return workflowGraphUniqueReferenceList([]string{expression}), nil
	}
	refs, issues := workflowGraphExpressionReferencesFromValue(expression)
	return workflowGraphUniqueReferenceList(refs), issues
}

func workflowGraphExpressionReferencesFromValue(expression string) ([]string, []WorkflowGraphValidationIssue) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, nil
	}
	var refs []string
	var issues []WorkflowGraphValidationIssue
	if name, args, ok := workflowGraphFunctionCall(expression); ok {
		if issue, hasIssue := workflowGraphExpressionFunctionIssue(name, len(args)); hasIssue {
			issues = append(issues, issue)
		}
		for _, arg := range args {
			argRefs, argIssues := workflowGraphExpressionReferencesFromValue(arg)
			refs = append(refs, argRefs...)
			issues = append(issues, argIssues...)
		}
		return refs, issues
	}
	for _, op := range []string{">=", "<=", "!=", "==", ">", "<"} {
		left, right, ok := splitWorkflowComparison(expression, op)
		if !ok {
			continue
		}
		leftRefs, leftIssues := workflowGraphExpressionReferencesFromValue(left)
		rightRefs, rightIssues := workflowGraphExpressionReferencesFromValue(right)
		refs = append(refs, leftRefs...)
		refs = append(refs, rightRefs...)
		issues = append(issues, leftIssues...)
		issues = append(issues, rightIssues...)
		return refs, issues
	}
	if workflowGraphLooksFunctionExpression(expression) {
		name := strings.TrimSpace(expression[:strings.Index(expression, "(")])
		if name == "" {
			name = "workflow expression function"
		}
		issues = append(issues, WorkflowGraphValidationIssue{Level: "error", Field: "expression", Message: fmt.Sprintf("malformed workflow expression function %s", name)})
		return nil, issues
	}
	refs = appendWorkflowGraphReferenceIfNeeded(refs, expression)
	return refs, issues
}

func workflowGraphExpressionFunctionIssue(name string, count int) (WorkflowGraphValidationIssue, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	option, ok := workflowExpressionFunctionDefinition(name)
	if !ok {
		return WorkflowGraphValidationIssue{Level: "error", Field: "expression", Message: fmt.Sprintf("unsupported workflow expression function %s", name)}, true
	}
	if count >= option.MinArgs && count <= option.MaxArgs {
		return WorkflowGraphValidationIssue{}, false
	}
	return WorkflowGraphValidationIssue{Level: "error", Field: "expression", Message: fmt.Sprintf("%s() expects %s", name, workflowExpressionFunctionExpectedArgs(option))}, true
}

func workflowGraphLooksFunctionExpression(expression string) bool {
	expression = strings.TrimSpace(expression)
	open := strings.Index(expression, "(")
	return open > 0 && strings.HasSuffix(expression, ")")
}

func appendWorkflowGraphReferenceIfNeeded(refs []string, value string) []string {
	value = strings.TrimSpace(value)
	if workflowGraphLooksReference(value) {
		return append(refs, value)
	}
	return refs
}

func workflowGraphUniqueReferenceList(refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		key := strings.ToLower(ref)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func workflowGraphLooksReference(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if _, ok := workflowGraphLiteralValue(value); ok {
		return false
	}
	lower := strings.ToLower(value)
	return lower == "request" ||
		lower == "input" ||
		strings.HasPrefix(lower, "workflow.") ||
		strings.HasPrefix(lower, "previous.") ||
		strings.HasPrefix(lower, "stages.")
}

func workflowGraphDocumentOutputIndex(doc WorkflowGraphDocument) map[string]map[string]workflowGraphReferenceOutput {
	index := make(map[string]map[string]workflowGraphReferenceOutput, len(doc.Stages))
	for _, stage := range doc.Stages {
		stageKey := normalizeWorkflowSkillName(stage.Name)
		if stageKey == "" {
			continue
		}
		outputs := make(map[string]workflowGraphReferenceOutput)
		addOutput := func(name, typ, description string) {
			name = strings.TrimSpace(name)
			if name == "" {
				return
			}
			if typ == "" {
				typ = "unknown"
			}
			outputs[name] = workflowGraphReferenceOutput{Stage: stage.Name, Name: name, Type: typ, Source: "stage_output", Description: description}
		}
		addOutput("summary", "string", "bounded stage summary")
		addOutput("raw_output", "string", "raw stage output")
		addOutput("output", "string", "raw stage output")
		addOutput("tool_results", "array", "tool observations")
		addOutput("findings", "array", "structured findings")
		addOutput("changes", "array", "structured changes")
		addOutput("verification", "array", "verification records")
		internalStage := workflowGraphStage{
			Name:     stage.Name,
			NodeType: stage.NodeType,
			Params:   copyStringMap(stage.Params),
			Input:    copyStringMap(stage.Input),
			Outputs:  copyStringMap(stage.Outputs),
		}
		if isControlWorkflowNode(internalStage) {
			addOutput("kind", "string", "control node kind")
			addOutput("route", "string", "selected route")
			addOutput("value", "string", "decision value")
			addOutput("target", "string", "selected target stage")
			addOutput("passed", "boolean", "condition or policy result")
			addOutput("status", "string", "control status")
			addOutput("reason", "string", "block or approval reason")
		}
		switch normalizeWorkflowSkillName(stage.NodeType) {
		case "input_gate", "manual_input":
			for _, field := range workflowGraphInputGateFields(internalStage) {
				addOutput(field.Name, workflowGraphInputFieldOutputType(field.Type, field.Multiple), "manual input field")
			}
			for key := range stage.Input {
				addOutput(key, "unknown", "mapped input")
			}
			for key := range stage.Params {
				if !isWorkflowGraphInputGateControlParam(key) {
					addOutput(key, "string", "input gate parameter")
				}
			}
		case "parallel", "fan_out", "fork":
			addOutput("branch_count", "number", "parallel branch count")
		case "join", "merge", "barrier":
			addOutput("wait_for", "array", "required joined stages")
			addOutput("completed_count", "number", "completed branch count")
		case "for_each", "foreach", "map", "loop", "until", "while":
			addOutput("iteration_count", "number", "iteration count")
			addOutput("items", "array", "iteration items")
			addOutput("outputs", "array", "iteration outputs")
			addOutput("summaries", "array", "iteration summaries")
		case "team", "agent_team", "team_template":
			addOutput("team", "string", "team template name")
			addOutput("roles", "array", "team roles")
			addOutput("handoffs", "array", "team handoffs")
			addOutput("blackboard", "array", "team blackboard templates")
			addOutput("output_contract", "object", "team output contract")
		}
		for name, ref := range stage.Outputs {
			addOutput(name, workflowGraphResultReferenceType(ref), "declared stage output")
		}
		index[stageKey] = outputs
	}
	return index
}

func workflowGraphMergedOutputIndex(doc WorkflowGraphDocument, run session.WorkflowRunSnapshot) map[string]map[string]workflowGraphReferenceOutput {
	outputs := workflowGraphDocumentOutputIndex(doc)
	return workflowGraphMergeRunOutputIndex(outputs, run)
}

func workflowGraphExpressionOutputIndex(w *WorkflowRunner, name string, doc WorkflowGraphDocument, run session.WorkflowRunSnapshot) map[string]map[string]workflowGraphReferenceOutput {
	outputs := workflowGraphDocumentOutputIndex(doc)
	if strings.TrimSpace(name) == "" {
		name = doc.Name
	}
	if w != nil && w.runtime != nil {
		if catalog, ok := w.runtime.WorkflowSchema(name); ok {
			outputs = workflowGraphMergeSchemaOutputIndex(outputs, catalog)
		}
	}
	return workflowGraphMergeRunOutputIndex(outputs, run)
}

func workflowGraphMergeRunOutputIndex(outputs map[string]map[string]workflowGraphReferenceOutput, run session.WorkflowRunSnapshot) map[string]map[string]workflowGraphReferenceOutput {
	runOutputs := workflowGraphRunOutputIndex(run)
	return workflowGraphMergeOutputIndex(outputs, runOutputs)
}

func workflowGraphMergeSchemaOutputIndex(outputs map[string]map[string]workflowGraphReferenceOutput, catalog session.WorkflowSchemaSnapshot) map[string]map[string]workflowGraphReferenceOutput {
	return workflowGraphMergeOutputIndex(outputs, workflowGraphSchemaOutputIndex(catalog))
}

func workflowGraphMergeOutputIndex(outputs map[string]map[string]workflowGraphReferenceOutput, observedOutputs map[string]map[string]workflowGraphReferenceOutput) map[string]map[string]workflowGraphReferenceOutput {
	for stageKey, stageOutputs := range observedOutputs {
		if outputs[stageKey] == nil {
			outputs[stageKey] = make(map[string]workflowGraphReferenceOutput, len(stageOutputs))
		}
		for name, observed := range stageOutputs {
			existing := outputs[stageKey][name]
			if strings.TrimSpace(existing.Stage) == "" {
				outputs[stageKey][name] = observed
				continue
			}
			if observed.Type != "" && observed.Type != "unknown" {
				existing.Type = observed.Type
			}
			if strings.TrimSpace(observed.Source) != "" {
				existing.Source = observed.Source
			}
			if strings.TrimSpace(observed.Description) != "" {
				existing.Description = observed.Description
			}
			if observed.Sample != nil {
				existing.Sample = observed.Sample
			}
			outputs[stageKey][name] = existing
		}
	}
	return outputs
}

func workflowGraphRunOutputIndex(run session.WorkflowRunSnapshot) map[string]map[string]workflowGraphReferenceOutput {
	if len(run.CompletedStages) == 0 {
		return nil
	}
	index := make(map[string]map[string]workflowGraphReferenceOutput)
	for _, stage := range run.CompletedStages {
		stageName := strings.TrimSpace(stage.Stage)
		stageKey := normalizeWorkflowSkillName(stageName)
		if stageKey == "" {
			continue
		}
		outputs := index[stageKey]
		if outputs == nil {
			outputs = make(map[string]workflowGraphReferenceOutput)
			index[stageKey] = outputs
		}
		for name, value := range stage.OutputValues {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			outputs[name] = workflowGraphReferenceOutput{
				Stage:       stageName,
				Name:        name,
				Type:        workflowGraphAnyType(value),
				Source:      "run_output",
				Description: "observed typed run output",
				Sample:      value,
			}
		}
		for name, value := range stage.Outputs {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := outputs[name]; ok {
				continue
			}
			outputs[name] = workflowGraphReferenceOutput{
				Stage:       stageName,
				Name:        name,
				Type:        "string",
				Source:      "run_output",
				Description: "observed run output",
				Sample:      value,
			}
		}
	}
	return index
}

func workflowGraphSchemaOutputIndex(catalog session.WorkflowSchemaSnapshot) map[string]map[string]workflowGraphReferenceOutput {
	if len(catalog.Stages) == 0 {
		return nil
	}
	index := make(map[string]map[string]workflowGraphReferenceOutput, len(catalog.Stages))
	for _, stage := range catalog.Stages {
		stageName := strings.TrimSpace(stage.Stage)
		stageKey := normalizeWorkflowSkillName(stageName)
		if stageKey == "" || len(stage.Outputs) == 0 {
			continue
		}
		outputs := make(map[string]workflowGraphReferenceOutput, len(stage.Outputs))
		for name, value := range stage.Outputs {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			outputs[name] = workflowGraphReferenceOutput{
				Stage:       stageName,
				Name:        name,
				Type:        workflowGraphSchemaValueType(value),
				Source:      "schema_catalog",
				Description: "observed workflow schema",
				Sample:      workflowGraphSchemaSample(value),
			}
		}
		index[stageKey] = outputs
	}
	return index
}

func workflowGraphSchemaValueType(value session.WorkflowValueSchemaSnapshot) string {
	if strings.TrimSpace(value.Type) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value.Type)
}

func workflowGraphSchemaSample(value session.WorkflowValueSchemaSnapshot) any {
	switch workflowGraphSchemaValueType(value) {
	case "object":
		if len(value.Fields) == 0 {
			return map[string]any{}
		}
		out := make(map[string]any, len(value.Fields))
		keys := make([]string, 0, len(value.Fields))
		for key := range value.Fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out[key] = workflowGraphSchemaSample(value.Fields[key])
		}
		return out
	case "array":
		if value.Items == nil {
			return []any{}
		}
		return []any{workflowGraphSchemaSample(*value.Items)}
	case "number":
		return float64(0)
	case "boolean":
		return true
	case "null":
		return nil
	default:
		return ""
	}
}

func workflowGraphInputFieldOutputType(fieldType string, multiple bool) string {
	if multiple {
		return "array"
	}
	switch workflowGraphInputFieldType(fieldType) {
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "object":
		return "object"
	case "array":
		return "array"
	default:
		return "string"
	}
}

func workflowGraphResultReferenceType(ref string) string {
	ref = strings.TrimSpace(ref)
	if value, ok := workflowGraphLiteralValue(ref); ok {
		return workflowGraphAnyType(value)
	}
	switch strings.ToLower(ref) {
	case "result.tool_results", "tool_results", "result.findings", "findings", "result.changes", "changes", "result.verification", "verification", "result.structured", "structured":
		return "array"
	case "result.agent_id", "agent_id", "result.mode", "mode", "result.output", "result.raw_output", "output", "raw_output", "result.summary", "summary":
		return "string"
	default:
		if strings.HasPrefix(strings.ToLower(ref), "params.") {
			return "string"
		}
		return "unknown"
	}
}

func workflowGraphAnyType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int64, float64, float32, json.Number:
		return "number"
	case []any, []string:
		return "array"
	case map[string]any, map[string]string:
		return "object"
	default:
		return "string"
	}
}

func workflowGraphReferenceSuggestionsFromOutputs(outputs map[string]map[string]workflowGraphReferenceOutput) []WorkflowExpressionSuggestion {
	suggestions := []WorkflowExpressionSuggestion{
		{Reference: "workflow.input", Type: "string", Source: "workflow", Description: "Original workflow request"},
		{Reference: "previous.summary", Type: "string", Source: "previous", Description: "Previous stage summary"},
		{Reference: "previous.raw_output", Type: "string", Source: "previous", Description: "Previous stage raw output"},
	}
	stageKeys := make([]string, 0, len(outputs))
	for key := range outputs {
		stageKeys = append(stageKeys, key)
	}
	sort.Strings(stageKeys)
	for _, stageKey := range stageKeys {
		stageOutputs := outputs[stageKey]
		names := make([]string, 0, len(stageOutputs))
		for name := range stageOutputs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			output := stageOutputs[name]
			source := output.Source
			if source == "" {
				source = "stage_output"
			}
			base := fmt.Sprintf("stages.%s.outputs.%s", output.Stage, name)
			suggestions = append(suggestions, WorkflowExpressionSuggestion{
				Reference:   base,
				Type:        output.Type,
				Stage:       output.Stage,
				Source:      source,
				Description: output.Description,
			})
			suggestions = workflowGraphAppendNestedReferenceSuggestions(suggestions, base, output.Stage, source, output.Sample, 0)
		}
	}
	return suggestions
}

func workflowGraphAppendNestedReferenceSuggestions(suggestions []WorkflowExpressionSuggestion, base, stageName, source string, sample any, depth int) []WorkflowExpressionSuggestion {
	if sample == nil || depth >= 3 || len(suggestions) >= 200 {
		return suggestions
	}
	switch value := sample.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			if strings.TrimSpace(key) != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			ref := base + "." + key
			child := value[key]
			suggestions = append(suggestions, WorkflowExpressionSuggestion{
				Reference:   ref,
				Type:        workflowGraphAnyType(child),
				Stage:       stageName,
				Source:      source,
				Description: "observed nested run output",
			})
			suggestions = workflowGraphAppendNestedReferenceSuggestions(suggestions, ref, stageName, source, child, depth+1)
			if len(suggestions) >= 200 {
				return suggestions
			}
		}
	case map[string]string:
		keys := make([]string, 0, len(value))
		for key := range value {
			if strings.TrimSpace(key) != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			suggestions = append(suggestions, WorkflowExpressionSuggestion{
				Reference:   base + "." + key,
				Type:        "string",
				Stage:       stageName,
				Source:      source,
				Description: "observed nested run output",
			})
			if len(suggestions) >= 200 {
				return suggestions
			}
		}
	case []any:
		if len(value) > 0 {
			ref := base + ".0"
			suggestions = append(suggestions, WorkflowExpressionSuggestion{
				Reference:   ref,
				Type:        workflowGraphAnyType(value[0]),
				Stage:       stageName,
				Source:      source,
				Description: "observed array item",
			})
			suggestions = workflowGraphAppendNestedReferenceSuggestions(suggestions, ref, stageName, source, value[0], depth+1)
		}
	case []string:
		if len(value) > 0 {
			suggestions = append(suggestions, WorkflowExpressionSuggestion{
				Reference:   base + ".0",
				Type:        "string",
				Stage:       stageName,
				Source:      source,
				Description: "observed array item",
			})
		}
	}
	return suggestions
}

func workflowGraphValidateReference(ref string, outputs map[string]map[string]workflowGraphReferenceOutput) WorkflowExpressionReference {
	ref = strings.TrimSpace(ref)
	validation := WorkflowExpressionReference{Expression: ref}
	lower := strings.ToLower(ref)
	switch {
	case lower == "request" || lower == "input" || lower == "workflow.input" || lower == "workflow.request":
		validation.Valid = true
		validation.Type = "string"
		return validation
	case lower == "previous.summary" || lower == "previous.output" || lower == "previous.raw_output":
		validation.Valid = true
		validation.Type = "string"
		return validation
	case strings.HasPrefix(lower, "stages."):
		return workflowGraphValidateStageReference(ref, outputs)
	default:
		validation.Valid = false
		validation.Message = fmt.Sprintf("unsupported workflow reference %q", ref)
		return validation
	}
}

func workflowGraphValidateStageReference(ref string, outputs map[string]map[string]workflowGraphReferenceOutput) WorkflowExpressionReference {
	validation := WorkflowExpressionReference{Expression: ref}
	parts := strings.Split(ref, ".")
	if len(parts) < 3 {
		validation.Message = fmt.Sprintf("workflow reference %q must include stage and result/output path", ref)
		return validation
	}
	stageName := parts[1]
	stageOutputs, ok := outputs[normalizeWorkflowSkillName(stageName)]
	if !ok {
		validation.Stage = stageName
		validation.Message = fmt.Sprintf("workflow reference %q uses unknown stage %q", ref, stageName)
		return validation
	}
	validation.Stage = stageName
	section := strings.ToLower(parts[2])
	if section == "result" {
		if len(parts) < 4 {
			validation.Valid = true
			validation.Type = "object"
			return validation
		}
		key := strings.Join(parts[3:], ".")
		validation.Output = key
		validation.Type = workflowGraphResultReferenceType("result." + key)
		validation.Valid = true
		if validation.Type == "unknown" {
			validation.Message = fmt.Sprintf("workflow reference %q uses dynamic result path", ref)
		}
		return validation
	}
	if section != "outputs" {
		validation.Message = fmt.Sprintf("workflow reference %q must use outputs or result", ref)
		return validation
	}
	if len(parts) < 4 {
		validation.Valid = true
		validation.Type = "object"
		return validation
	}
	root := parts[3]
	output, ok := stageOutputs[root]
	if !ok {
		validation.Output = root
		validation.Message = fmt.Sprintf("workflow reference %q uses unknown output %q on stage %q", ref, root, stageName)
		return validation
	}
	validation.Output = root
	validation.Path = strings.Join(parts[4:], ".")
	validation.Type, validation.Valid, validation.Message = workflowGraphValidateNestedReferenceType(ref, output, parts[4:])
	return validation
}

func workflowGraphValidateNestedReferenceType(ref string, output workflowGraphReferenceOutput, path []string) (string, bool, string) {
	if typ, valid, message, ok := workflowGraphSamplePathType(ref, output.Sample, path); ok {
		return typ, valid, message
	}
	rootType := strings.TrimSpace(output.Type)
	if rootType == "" {
		rootType = "unknown"
	}
	if len(path) == 0 {
		return rootType, true, ""
	}
	switch rootType {
	case "object", "unknown":
		return "unknown", true, ""
	case "array":
		index := strings.Trim(strings.TrimSpace(path[0]), "[]")
		if _, err := strconv.Atoi(index); err != nil {
			return "unknown", false, fmt.Sprintf("workflow reference %q indexes an array with non-numeric segment %q", ref, path[0])
		}
		return "unknown", true, ""
	default:
		return rootType, false, fmt.Sprintf("workflow reference %q tries to access nested path on %s output", ref, rootType)
	}
}

func workflowGraphSamplePathType(ref string, sample any, path []string) (string, bool, string, bool) {
	if sample == nil {
		return "", false, "", false
	}
	if len(path) == 0 {
		return workflowGraphAnyType(sample), true, "", true
	}
	switch value := sample.(type) {
	case map[string]any:
		key := strings.TrimSpace(path[0])
		child, ok := value[key]
		if !ok {
			return "unknown", false, fmt.Sprintf("workflow reference %q uses missing object key %q", ref, key), true
		}
		return workflowGraphSamplePathType(ref, child, path[1:])
	case map[string]string:
		key := strings.TrimSpace(path[0])
		child, ok := value[key]
		if !ok {
			return "unknown", false, fmt.Sprintf("workflow reference %q uses missing object key %q", ref, key), true
		}
		return workflowGraphSamplePathType(ref, child, path[1:])
	case []any:
		indexToken := strings.Trim(strings.TrimSpace(path[0]), "[]")
		index, err := strconv.Atoi(indexToken)
		if err != nil {
			return "unknown", false, fmt.Sprintf("workflow reference %q indexes an array with non-numeric segment %q", ref, path[0]), true
		}
		if index < 0 || index >= len(value) {
			return "unknown", false, fmt.Sprintf("workflow reference %q indexes an array outside observed bounds", ref), true
		}
		return workflowGraphSamplePathType(ref, value[index], path[1:])
	case []string:
		indexToken := strings.Trim(strings.TrimSpace(path[0]), "[]")
		index, err := strconv.Atoi(indexToken)
		if err != nil {
			return "unknown", false, fmt.Sprintf("workflow reference %q indexes an array with non-numeric segment %q", ref, path[0]), true
		}
		if index < 0 || index >= len(value) {
			return "unknown", false, fmt.Sprintf("workflow reference %q indexes an array outside observed bounds", ref), true
		}
		return workflowGraphSamplePathType(ref, value[index], path[1:])
	default:
		typ := workflowGraphAnyType(sample)
		return typ, false, fmt.Sprintf("workflow reference %q tries to access nested path on %s output", ref, typ), true
	}
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (w *WorkflowRunner) workflowGraphRoot() (string, error) {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return "", fmt.Errorf("workflow runtime not configured")
	}
	return filepath.Join(w.runtime.RuntimeHome(), "workflows"), nil
}

func (w *WorkflowRunner) workflowGraphPath(name string) (string, error) {
	name = normalizePersistedWorkflowName(name)
	if err := validatePersistedWorkflowName(name); err != nil {
		return "", err
	}
	root, err := w.workflowGraphRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(root, name, "workflow.yaml"))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workflow path escapes runtime workflows directory")
	}
	return path, nil
}

func normalizePersistedWorkflowName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func validatePersistedWorkflowName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("invalid workflow name: %s", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("invalid workflow name %q: use lowercase letters, numbers, hyphen, or underscore", name)
		}
	}
	return nil
}

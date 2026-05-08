package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowTemplateSummary is a compact reusable workflow template row.
type WorkflowTemplateSummary struct {
	Kind                string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version             int      `json:"version,omitempty" yaml:"version,omitempty"`
	MinVersion          int      `json:"min_supported_version,omitempty" yaml:"min_supported_version,omitempty"`
	MigratedFromVersion int      `json:"migrated_from_version,omitempty" yaml:"migrated_from_version,omitempty"`
	Name                string   `json:"name" yaml:"name"`
	Title               string   `json:"title" yaml:"title"`
	Description         string   `json:"description,omitempty" yaml:"description,omitempty"`
	Category            string   `json:"category,omitempty" yaml:"category,omitempty"`
	Tags                []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Stages              int      `json:"stages" yaml:"stages"`
	Source              string   `json:"source,omitempty" yaml:"source,omitempty"`
	Path                string   `json:"path,omitempty" yaml:"path,omitempty"`
	Custom              bool     `json:"custom,omitempty" yaml:"custom,omitempty"`
}

// WorkflowTemplate is a reusable graph blueprint for Studio and scaffolds.
type WorkflowTemplate struct {
	WorkflowTemplateSummary
	Graph WorkflowGraphDocument `json:"graph" yaml:"graph"`
}

// WorkflowNodeTypeOption describes a supported workflow node type for editors.
type WorkflowNodeTypeOption struct {
	Kind         string                       `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version      int                          `json:"version,omitempty" yaml:"version,omitempty"`
	MinVersion   int                          `json:"min_supported_version,omitempty" yaml:"min_supported_version,omitempty"`
	Type         string                       `json:"type" yaml:"type"`
	Label        string                       `json:"label" yaml:"label"`
	Category     string                       `json:"category,omitempty" yaml:"category,omitempty"`
	Description  string                       `json:"description,omitempty" yaml:"description,omitempty"`
	Source       string                       `json:"source,omitempty" yaml:"source,omitempty"`
	Path         string                       `json:"path,omitempty" yaml:"path,omitempty"`
	Custom       bool                         `json:"custom,omitempty" yaml:"custom,omitempty"`
	Control      bool                         `json:"control,omitempty" yaml:"control,omitempty"`
	VisualOnly   bool                         `json:"visual_only,omitempty" yaml:"visual_only,omitempty"`
	DefaultStage WorkflowGraphStageDocument   `json:"default_stage,omitempty" yaml:"default_stage,omitempty"`
	Fields       []WorkflowNodeFieldOption    `json:"fields,omitempty" yaml:"fields,omitempty"`
	Outputs      []WorkflowNodeVariableOption `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Tags         []string                     `json:"tags,omitempty" yaml:"tags,omitempty"`
	Hints        []string                     `json:"hints,omitempty" yaml:"hints,omitempty"`
	Warnings     []string                     `json:"warnings,omitempty" yaml:"warnings,omitempty"`
	Examples     []WorkflowNodeExampleOption  `json:"examples,omitempty" yaml:"examples,omitempty"`
}

// WorkflowNodeFieldOption describes editable fields for a node type.
type WorkflowNodeFieldOption struct {
	Name        string   `json:"name" yaml:"name"`
	Label       string   `json:"label,omitempty" yaml:"label,omitempty"`
	Type        string   `json:"type,omitempty" yaml:"type,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Options     []string `json:"options,omitempty" yaml:"options,omitempty"`
}

// WorkflowNodeVariableOption describes common output variables for a node type.
type WorkflowNodeVariableOption struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// WorkflowNodeExampleOption describes one editor-facing node example.
type WorkflowNodeExampleOption struct {
	Title       string                     `json:"title,omitempty" yaml:"title,omitempty"`
	Description string                     `json:"description,omitempty" yaml:"description,omitempty"`
	Stage       WorkflowGraphStageDocument `json:"stage,omitempty" yaml:"stage,omitempty"`
	YAML        string                     `json:"yaml,omitempty" yaml:"yaml,omitempty"`
	Notes       []string                   `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// WorkflowPolicyRuleOption describes a built-in policy_guard rule.
type WorkflowPolicyRuleOption struct {
	Name        string                    `json:"name" yaml:"name"`
	Label       string                    `json:"label" yaml:"label"`
	Description string                    `json:"description,omitempty" yaml:"description,omitempty"`
	Source      string                    `json:"source,omitempty" yaml:"source,omitempty"`
	Operator    string                    `json:"operator,omitempty" yaml:"operator,omitempty"`
	Custom      bool                      `json:"custom,omitempty" yaml:"custom,omitempty"`
	Params      []WorkflowNodeFieldOption `json:"params,omitempty" yaml:"params,omitempty"`
}

// WorkflowTemplates returns built-in reusable workflow graph blueprints.
func (w *WorkflowRunner) WorkflowTemplates() []WorkflowTemplateSummary {
	templates := builtInWorkflowTemplates()
	if custom := w.customWorkflowTemplateMap(); len(custom) > 0 {
		for name, template := range custom {
			templates[name] = template
		}
	}
	out := make([]WorkflowTemplateSummary, 0, len(templates))
	for _, template := range templates {
		summary := template.WorkflowTemplateSummary
		summary.Stages = len(template.Graph.Stages)
		if summary.Source == "" {
			summary.Source = "built_in"
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// WorkflowTemplate loads one built-in template by name.
func (w *WorkflowRunner) WorkflowTemplate(name string) (WorkflowTemplate, bool) {
	if template, ok := w.customWorkflowTemplate(name); ok {
		template.WorkflowTemplateSummary.Stages = len(template.Graph.Stages)
		return template, true
	}
	templates := builtInWorkflowTemplates()
	template, ok := templates[normalizePersistedWorkflowName(name)]
	if !ok {
		return WorkflowTemplate{}, false
	}
	template.WorkflowTemplateSummary.Stages = len(template.Graph.Stages)
	if template.WorkflowTemplateSummary.Source == "" {
		template.WorkflowTemplateSummary.Source = "built_in"
	}
	return template, true
}

// WorkflowNodeTypes returns supported visual/executable/control nodes.
func (w *WorkflowRunner) WorkflowNodeTypes() []WorkflowNodeTypeOption {
	options := []WorkflowNodeTypeOption{
		{
			Type:        "start",
			Label:       "Start",
			Category:    "visual",
			Description: "Entry marker. It does not run a model stage.",
			VisualOnly:  true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "start",
				NodeType: "start",
			},
		},
		{
			Type:        "agent",
			Label:       "Agent",
			Category:    "execute",
			Description: "Run one configured agent with one skill prompt.",
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "agent",
				NodeType: "agent",
				Agent:    "planner",
				Skill:    "execution-plan",
				Outputs:  map[string]string{"summary": "result.summary", "raw_output": "result.output"},
			},
			Fields: commonExecutableNodeFields(),
			Outputs: []WorkflowNodeVariableOption{
				{Name: "summary", Description: "Bounded result summary"},
				{Name: "raw_output", Description: "Raw model output"},
				{Name: "findings", Description: "Structured findings when emitted"},
			},
		},
		{
			Type:        "skill",
			Label:       "Skill",
			Category:    "execute",
			Description: "Run a selected skill, optionally with mapped inputs and outputs.",
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "skill",
				NodeType: "skill",
				Agent:    "planner",
				Skill:    "execution-plan",
				Outputs:  map[string]string{"summary": "result.summary", "raw_output": "result.output"},
			},
			Fields:  commonExecutableNodeFields(),
			Outputs: executableWorkflowNodeOutputs(),
		},
		{
			Type:        "tool",
			Label:       "Tool",
			Category:    "execute",
			Description: "Tool-focused stage. Agent permissions and approval policy still apply.",
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "tool",
				NodeType: "tool",
				Agent:    "fixer",
				Skill:    "code-writing",
				Tool:     "file_tools/write_file",
				Approval: true,
				Outputs:  map[string]string{"summary": "result.summary", "tool_results": "result.tool_results"},
			},
			Fields: append(commonExecutableNodeFields(), WorkflowNodeFieldOption{
				Name:        "tool",
				Label:       "Tool",
				Type:        "tool",
				Description: "Preferred tool metadata for this stage.",
			}),
			Outputs: executableWorkflowNodeOutputs(),
		},
		{
			Type:        "team",
			Label:       "Team",
			Category:    "execute",
			Description: "Declare a reusable multi-agent team context. Set params.execute=true to expand the team template into executable role stages.",
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "team",
				NodeType: "team",
				Agent:    "planner",
				Params:   map[string]string{"team": "software-task-team"},
				Outputs:  map[string]string{"team": "result.structured"},
				Next:     []string{"next"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.team", Label: "Team template", Type: "team_template", Required: true, Description: "Built-in team template name."},
				{Name: "params.execute", Label: "Execute roles", Type: "bool", Description: "When true, runtime expands this team into role stages named <team>__<role> before continuing."},
				{Name: "agent", Label: "Entry agent", Type: "agent", Description: "Optional owner shown in collaboration records."},
				{Name: "input", Label: "Inputs", Type: "map", Description: "Optional workflow references to pass into the team context."},
				{Name: "outputs", Label: "Outputs", Type: "map", Description: "Optional output bindings, for example result.structured."},
				{Name: "next", Label: "Next stages", Type: "stage_list"},
			},
			Outputs: []WorkflowNodeVariableOption{
				{Name: "team", Description: "Team template name"},
				{Name: "title", Description: "Human-readable team title"},
				{Name: "entry_agent", Description: "Recommended entry agent"},
				{Name: "recommended_workflow", Description: "Recommended workflow template"},
				{Name: "roles", Description: "JSON role templates"},
				{Name: "handoffs", Description: "JSON handoff templates"},
				{Name: "blackboard", Description: "JSON shared blackboard templates"},
				{Name: "output_contract", Description: "JSON output contract"},
			},
		},
		{
			Type:        "condition",
			Label:       "Condition",
			Category:    "control",
			Description: "Route by a boolean expression such as contains(stage output, text).",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:      "condition",
				NodeType:  "condition",
				Condition: `contains(previous.raw_output, "risk")`,
				Routes:    map[string]string{"true": "yes", "false": "no"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "condition", Label: "Condition", Type: "expression", Required: true, Description: `Supports contains(ref, "text"), len(ref) > 0, comparisons, and truthy references.`},
				{Name: "routes.true", Label: "True route", Type: "stage", Description: "Stage name for a true condition."},
				{Name: "routes.false", Label: "False route", Type: "stage", Description: "Stage name for a false condition."},
			},
			Outputs: controlWorkflowNodeOutputs(),
		},
		{
			Type:        "switch",
			Label:       "Switch",
			Category:    "control",
			Description: "Route by a resolved value with named cases and a default fallback.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "switch",
				NodeType: "switch",
				SwitchOn: "previous.summary",
				Cases:    map[string]string{"default": "next"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "switch_on", Label: "Switch on", Type: "reference", Required: true, Description: "Reference such as stages.plan.outputs.route."},
				{Name: "cases", Label: "Cases", Type: "map", Description: "Case value to target stage. Use default, else, or * for fallback."},
			},
			Outputs: controlWorkflowNodeOutputs(),
		},
		{
			Type:        "policy_guard",
			Label:       "Policy Guard",
			Category:    "control",
			Description: "Allow, deny, or block a path based on a policy expression.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "guard",
				NodeType: "policy_guard",
				Policy:   `contains(previous.raw_output, "approved")`,
				Routes:   map[string]string{"allow": "next", "deny": "blocked"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.rule", Label: "Rule", Type: "policy_rule", Description: "Optional built-in policy rule. If empty, policy/condition expression is used."},
				{Name: "params.ref", Label: "Reference", Type: "reference", Description: "Reference consumed by named policy rules."},
				{Name: "params.needle", Label: "Needle", Type: "text", Description: "Text used by the contains rule."},
				{Name: "params.minimum", Label: "Minimum", Type: "text", Description: "Threshold used by min_count and risk_at_least."},
				{Name: "policy", Label: "Policy", Type: "expression", Required: true, Description: "Policy expression. Failing without a deny route blocks the workflow."},
				{Name: "routes.allow", Label: "Allow route", Type: "stage"},
				{Name: "routes.deny", Label: "Deny route", Type: "stage"},
				{Name: "params.reason", Label: "Reason", Type: "text", Description: "Operator-facing reason if blocked or denied."},
			},
			Outputs: controlWorkflowNodeOutputs(),
		},
		{
			Type:        "quality_gate",
			Label:       "Quality Gate",
			Category:    "control",
			Description: "Route or block based on prior acceptance criteria, verification results, artifact evidence, and tool errors.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "quality",
				NodeType: "quality_gate",
				Params: map[string]string{
					"require_acceptance":   "true",
					"require_verification": "true",
					"min_score":            "80",
				},
				Routes: map[string]string{"pass": "next", "fail": "revise"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.stage", Label: "Source stage", Type: "stage", Description: "Optional stage or comma-separated stages to evaluate. Empty evaluates all completed stages."},
				{Name: "params.require_acceptance", Label: "Require acceptance", Type: "bool", Description: "Fail when no acceptance criteria were recorded."},
				{Name: "params.require_verification", Label: "Require verification", Type: "bool", Description: "Fail when no verification results were recorded."},
				{Name: "params.require_evidence", Label: "Require evidence", Type: "bool", Description: "Fail when no declared evidence artifacts were recorded."},
				{Name: "params.min_score", Label: "Minimum score", Type: "number", Description: "Optional minimum quality score from 0 to 100."},
				{Name: "params.allow_unknown", Label: "Allow unknown statuses", Type: "bool", Description: "Allow verification or acceptance entries without a known pass/fail status."},
				{Name: "routes.pass", Label: "Pass route", Type: "stage"},
				{Name: "routes.warning", Label: "Warning route", Type: "stage"},
				{Name: "routes.fail", Label: "Fail route", Type: "stage"},
				{Name: "params.reason", Label: "Block reason", Type: "text", Description: "Operator-facing reason if the gate blocks without a fail route."},
			},
			Outputs: qualityWorkflowNodeOutputs(),
			Tags:    []string{"quality", "acceptance", "verification", "evidence"},
		},
		{
			Type:        "parallel",
			Label:       "Parallel",
			Category:    "control",
			Description: "Fan out to multiple branches. Branches run deterministically by default; set params.concurrent=true for eligible join-bound branches.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "parallel",
				NodeType: "parallel",
				Next:     []string{"branch-a", "branch-b"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "next", Label: "Branches", Type: "stage_list", Required: true, Description: "Branch stage names to schedule from this fan-out point."},
				{Name: "params.concurrent", Label: "Concurrent", Type: "bool", Description: "Opt in to goroutine-level branch execution when every direct branch flows into a join and does not require checkpoint approval."},
			},
			Outputs: controlWorkflowNodeOutputs(),
		},
		{
			Type:        "join",
			Label:       "Join",
			Category:    "control",
			Description: "Wait until required upstream branches complete, then continue.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "join",
				NodeType: "join",
				Next:     []string{"next"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.wait_for", Label: "Wait for", Type: "csv", Description: "Optional comma-separated stage names. If empty, direct non-visual predecessors are inferred."},
				{Name: "next", Label: "Next stages", Type: "stage_list"},
			},
			Outputs: controlWorkflowNodeOutputs(),
		},
		{
			Type:        "input_gate",
			Label:       "Input Gate",
			Category:    "control",
			Description: "Collect or publish variables. Manual gates pause until the operator submits values.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "collect",
				NodeType: "input_gate",
				Params:   map[string]string{"manual": "true", "fields": "target,severity"},
				Next:     []string{"next"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.manual", Label: "Manual", Type: "boolean", Description: "Pause this workflow until input is submitted."},
				{Name: "params.fields", Label: "Fields", Type: "csv", Description: "Comma-separated field names for frontend forms."},
				{Name: "params.prompt", Label: "Prompt", Type: "text", Description: "Operator-facing prompt."},
				{Name: "input", Label: "Mapped inputs", Type: "map", Description: "Optional references to publish as outputs."},
			},
			Outputs: []WorkflowNodeVariableOption{
				{Name: "route", Description: "Route hint derived from submitted or mapped values"},
				{Name: "value", Description: "Submitted values encoded as JSON"},
				{Name: "<field>", Description: "Each submitted or mapped field is available by name"},
			},
		},
		{
			Type:        "for_each",
			Label:       "For Each",
			Category:    "control",
			Description: "Run one executable body stage once for each item, then publish aggregate iteration outputs.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "for-each",
				NodeType: "for_each",
				Params:   map[string]string{"stage": "process-item", "items": "alpha,beta,gamma"},
				Next:     []string{"next"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.stage", Label: "Body stage", Type: "stage", Required: true, Description: "Executable stage to run for each item."},
				{Name: "params.items", Label: "Items", Type: "csv", Description: "Comma/newline separated items, or a JSON array."},
				{Name: "params.items_ref", Label: "Items reference", Type: "reference", Description: "Reference resolving to a JSON array or delimited text."},
				{Name: "next", Label: "After loop", Type: "stage_list", Description: "Stages to run after all iterations complete."},
			},
			Outputs: []WorkflowNodeVariableOption{
				{Name: "body_stage", Description: "Executable body stage name"},
				{Name: "iteration_count", Description: "Number of iterations completed"},
				{Name: "items", Description: "JSON array of input items"},
				{Name: "outputs", Description: "JSON array of iteration raw outputs"},
				{Name: "summaries", Description: "JSON array of iteration summaries"},
			},
		},
		{
			Type:        "loop",
			Label:       "Loop / Until",
			Category:    "control",
			Description: "Run one executable body stage repeatedly until an expression passes or the max-iteration guard is reached.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "loop",
				NodeType: "loop",
				Params:   map[string]string{"stage": "review", "max_iterations": "3", "until": `contains(previous.raw_output, "done")`},
				Next:     []string{"next"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.stage", Label: "Body stage", Type: "stage", Required: true, Description: "Executable stage to repeat."},
				{Name: "params.max_iterations", Label: "Max iterations", Type: "number", Description: "Bounded guard. Values above 20 are capped."},
				{Name: "params.until", Label: "Until", Type: "expression", Description: `Expression checked after each iteration, for example contains(previous.raw_output, "done").`},
				{Name: "next", Label: "After loop", Type: "stage_list", Description: "Stages to run after the loop finishes."},
			},
			Outputs: []WorkflowNodeVariableOption{
				{Name: "body_stage", Description: "Executable body stage name"},
				{Name: "iteration_count", Description: "Number of iterations completed"},
				{Name: "max_iterations", Description: "Configured bounded maximum"},
				{Name: "passed", Description: "Whether the until condition passed"},
				{Name: "outputs", Description: "JSON array of iteration raw outputs"},
				{Name: "summaries", Description: "JSON array of iteration summaries"},
			},
		},
		{
			Type:        "sub_workflow",
			Label:       "Sub Workflow",
			Category:    "control",
			Description: "Run another workflow as a nested reusable unit and publish its run id, status, and summary.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "sub-workflow",
				NodeType: "sub_workflow",
				Params:   map[string]string{"workflow": "plan-fix-audit", "request": "workflow.input"},
				Next:     []string{"next"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.workflow", Label: "Workflow", Type: "workflow", Required: true, Description: "Existing built-in or persisted workflow name."},
				{Name: "params.request", Label: "Request", Type: "reference", Description: "Reference or literal prompt passed to the nested workflow."},
				{Name: "input", Label: "Mapped inputs", Type: "map", Description: "Optional inputs encoded into the nested request when params.request is empty."},
				{Name: "next", Label: "Next stages", Type: "stage_list"},
			},
			Outputs: []WorkflowNodeVariableOption{
				{Name: "sub_workflow", Description: "Nested workflow name"},
				{Name: "sub_run_id", Description: "Nested workflow run id"},
				{Name: "status", Description: "Nested workflow status"},
				{Name: "summary", Description: "Nested workflow final summary"},
				{Name: "completed_stages", Description: "Nested completed stage count"},
			},
		},
		{
			Type:        "checkpoint",
			Label:       "Checkpoint",
			Category:    "control",
			Description: "Pause for operator approval when a run is not pre-approved.",
			Control:     true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "checkpoint",
				NodeType: "checkpoint",
				Params:   map[string]string{"prompt": "Approve this workflow checkpoint?"},
			},
			Fields: []WorkflowNodeFieldOption{
				{Name: "params.prompt", Label: "Prompt", Type: "text", Description: "Approval prompt shown to the operator."},
				{Name: "next", Label: "Next stages", Type: "stage_list"},
			},
			Outputs: controlWorkflowNodeOutputs(),
		},
		{
			Type:        "end",
			Label:       "End",
			Category:    "visual",
			Description: "Terminal marker. It does not run a model stage.",
			VisualOnly:  true,
			DefaultStage: WorkflowGraphStageDocument{
				Name:     "end",
				NodeType: "end",
			},
		},
	}
	return w.mergeWorkflowNodeMetadata(options)
}

const (
	workflowNodeMetadataResourceKind                = "goflow.workflow_node_metadata"
	workflowNodeMetadataResourceVersion             = 1
	workflowNodeMetadataResourceMinSupportedVersion = 1
)

func (w *WorkflowRunner) mergeWorkflowNodeMetadata(options []WorkflowNodeTypeOption) []WorkflowNodeTypeOption {
	if len(options) == 0 {
		return options
	}
	for i := range options {
		if options[i].Source == "" {
			options[i].Source = "built_in"
		}
	}
	metadata := w.customWorkflowNodeMetadata()
	if len(metadata) == 0 {
		return options
	}
	for i := range options {
		nodeType := normalizeWorkflowSkillName(options[i].Type)
		override, ok := metadata[nodeType]
		if !ok {
			continue
		}
		options[i] = mergeWorkflowNodeTypeOption(options[i], override)
	}
	return options
}

func (w *WorkflowRunner) customWorkflowNodeMetadata() map[string]WorkflowNodeTypeOption {
	roots := w.workflowNodeMetadataRoots()
	if len(roots) == 0 {
		return nil
	}
	out := make(map[string]WorkflowNodeTypeOption)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
				continue
			}
			path := filepath.Join(root, name)
			option, err := loadWorkflowNodeMetadata(path)
			if err != nil {
				continue
			}
			nodeType := normalizeWorkflowSkillName(option.Type)
			if nodeType == "" {
				continue
			}
			option.Type = nodeType
			option.Source = "custom_metadata"
			option.Path = path
			option.Custom = true
			out[nodeType] = option
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (w *WorkflowRunner) workflowNodeMetadataRoots() []string {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return nil
	}
	home := w.runtime.RuntimeHome()
	return []string{
		filepath.Join(home, "templates", "workflow_nodes"),
		filepath.Join(home, "metadata", "workflow_nodes"),
	}
}

func loadWorkflowNodeMetadata(path string) (WorkflowNodeTypeOption, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowNodeTypeOption{}, err
	}
	var option WorkflowNodeTypeOption
	if err := yaml.Unmarshal(data, &option); err != nil {
		return WorkflowNodeTypeOption{}, err
	}
	if err := validateWorkflowNodeMetadata(option); err != nil {
		return WorkflowNodeTypeOption{}, err
	}
	return option, nil
}

func validateWorkflowNodeMetadata(option WorkflowNodeTypeOption) error {
	if strings.TrimSpace(option.Kind) != "" && strings.TrimSpace(option.Kind) != workflowNodeMetadataResourceKind {
		return fmt.Errorf("unsupported workflow node metadata kind %q", option.Kind)
	}
	version := option.Version
	if version == 0 {
		version = workflowNodeMetadataResourceMinSupportedVersion
	}
	if version < workflowNodeMetadataResourceMinSupportedVersion {
		return fmt.Errorf("workflow node metadata version %d is no longer supported", version)
	}
	if version > workflowNodeMetadataResourceVersion {
		return fmt.Errorf("workflow node metadata version %d is newer than supported version %d", version, workflowNodeMetadataResourceVersion)
	}
	if option.MinVersion > workflowNodeMetadataResourceVersion {
		return fmt.Errorf("workflow node metadata requires reader version %d, supported version is %d", option.MinVersion, workflowNodeMetadataResourceVersion)
	}
	if strings.TrimSpace(option.Type) == "" {
		return fmt.Errorf("workflow node metadata type is required")
	}
	if err := validatePersistedWorkflowName(normalizePersistedWorkflowName(option.Type)); err != nil {
		return fmt.Errorf("invalid workflow node metadata type %q: %w", option.Type, err)
	}
	for i, field := range option.Fields {
		if strings.TrimSpace(field.Name) == "" {
			return fmt.Errorf("workflow node metadata %s field %d missing name", option.Type, i)
		}
	}
	for i, output := range option.Outputs {
		if strings.TrimSpace(output.Name) == "" {
			return fmt.Errorf("workflow node metadata %s output %d missing name", option.Type, i)
		}
	}
	return nil
}

func mergeWorkflowNodeTypeOption(base, override WorkflowNodeTypeOption) WorkflowNodeTypeOption {
	if strings.TrimSpace(override.Label) != "" {
		base.Label = override.Label
	}
	if strings.TrimSpace(override.Category) != "" {
		base.Category = override.Category
	}
	if strings.TrimSpace(override.Description) != "" {
		base.Description = override.Description
	}
	if workflowGraphStageDocumentHasContent(override.DefaultStage) {
		base.DefaultStage = override.DefaultStage
	}
	base.Fields = mergeWorkflowNodeFields(base.Fields, override.Fields)
	base.Outputs = mergeWorkflowNodeOutputs(base.Outputs, override.Outputs)
	base.Tags = workflowGraphUniqueStrings(append(base.Tags, override.Tags...))
	base.Hints = workflowGraphUniqueStrings(append(base.Hints, override.Hints...))
	base.Warnings = workflowGraphUniqueStrings(append(base.Warnings, override.Warnings...))
	if len(override.Examples) > 0 {
		base.Examples = append([]WorkflowNodeExampleOption(nil), override.Examples...)
	}
	base.Source = "built_in+custom_metadata"
	base.Path = override.Path
	base.Custom = true
	return base
}

func workflowGraphStageDocumentHasContent(stage WorkflowGraphStageDocument) bool {
	return strings.TrimSpace(stage.Name) != "" ||
		strings.TrimSpace(stage.NodeType) != "" ||
		strings.TrimSpace(stage.Agent) != "" ||
		strings.TrimSpace(stage.Skill) != "" ||
		strings.TrimSpace(stage.Tool) != "" ||
		len(stage.Params) > 0 ||
		len(stage.Input) > 0 ||
		len(stage.Outputs) > 0 ||
		len(stage.Artifacts) > 0 ||
		strings.TrimSpace(stage.Policy) != "" ||
		strings.TrimSpace(stage.Condition) != "" ||
		len(stage.Routes) > 0 ||
		strings.TrimSpace(stage.SwitchOn) != "" ||
		len(stage.Cases) > 0 ||
		stage.Retry.MaxAttempts != 0 ||
		len(stage.OnError) > 0 ||
		stage.Approval ||
		strings.TrimSpace(stage.NextStrategy) != "" ||
		len(stage.Next) > 0
}

func mergeWorkflowNodeFields(base, overrides []WorkflowNodeFieldOption) []WorkflowNodeFieldOption {
	if len(overrides) == 0 {
		return base
	}
	out := append([]WorkflowNodeFieldOption(nil), base...)
	positions := make(map[string]int, len(out))
	for i, field := range out {
		positions[normalizeWorkflowSkillName(field.Name)] = i
	}
	for _, override := range overrides {
		key := normalizeWorkflowSkillName(override.Name)
		if key == "" {
			continue
		}
		if index, ok := positions[key]; ok {
			out[index] = mergeWorkflowNodeField(out[index], override)
			continue
		}
		positions[key] = len(out)
		out = append(out, override)
	}
	return out
}

func mergeWorkflowNodeField(base, override WorkflowNodeFieldOption) WorkflowNodeFieldOption {
	if strings.TrimSpace(override.Label) != "" {
		base.Label = override.Label
	}
	if strings.TrimSpace(override.Type) != "" {
		base.Type = override.Type
	}
	if strings.TrimSpace(override.Description) != "" {
		base.Description = override.Description
	}
	if override.Required {
		base.Required = true
	}
	if len(override.Options) > 0 {
		base.Options = append([]string(nil), override.Options...)
	}
	return base
}

func mergeWorkflowNodeOutputs(base, overrides []WorkflowNodeVariableOption) []WorkflowNodeVariableOption {
	if len(overrides) == 0 {
		return base
	}
	out := append([]WorkflowNodeVariableOption(nil), base...)
	positions := make(map[string]int, len(out))
	for i, output := range out {
		positions[normalizeWorkflowSkillName(output.Name)] = i
	}
	for _, override := range overrides {
		key := normalizeWorkflowSkillName(override.Name)
		if key == "" {
			continue
		}
		if index, ok := positions[key]; ok {
			if strings.TrimSpace(override.Description) != "" {
				out[index].Description = override.Description
			}
			continue
		}
		positions[key] = len(out)
		out = append(out, override)
	}
	return out
}

// WorkflowPolicyRules returns built-in guard rules for visual editors.
func (w *WorkflowRunner) WorkflowPolicyRules() []WorkflowPolicyRuleOption {
	rules := []WorkflowPolicyRuleOption{
		{
			Name:        "expression",
			Label:       "Expression",
			Source:      "built_in",
			Operator:    "expression",
			Description: "Evaluate stage.policy or stage.condition with the baseline expression language.",
			Params: []WorkflowNodeFieldOption{
				{Name: "policy", Label: "Policy", Type: "expression", Required: true},
			},
		},
		{
			Name:        "ref_truthy",
			Label:       "Reference Is Present",
			Source:      "built_in",
			Operator:    "ref_truthy",
			Description: "Pass when params.ref resolves to a truthy non-empty value.",
			Params: []WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference", Required: true},
			},
		},
		{
			Name:        "contains",
			Label:       "Contains Text",
			Source:      "built_in",
			Operator:    "contains",
			Description: "Pass when params.ref contains params.needle, case-insensitively.",
			Params: []WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference", Required: true},
				{Name: "params.needle", Label: "Needle", Type: "text", Required: true},
			},
		},
		{
			Name:        "min_count",
			Label:       "Minimum Count",
			Source:      "built_in",
			Operator:    "min_count",
			Description: "Pass when len(params.ref) is greater than or equal to params.minimum.",
			Params: []WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference", Required: true},
				{Name: "params.minimum", Label: "Minimum", Type: "number", Required: true},
			},
		},
		{
			Name:        "risk_at_least",
			Label:       "Risk At Least",
			Source:      "built_in",
			Operator:    "risk_at_least",
			Description: "Pass when a referenced severity is at or above info, low, medium, high, or critical.",
			Params: []WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference", Required: true},
				{Name: "params.minimum", Label: "Minimum severity", Type: "select", Required: true, Options: []string{"info", "low", "medium", "high", "critical"}},
			},
		},
		{
			Name:        "team_approval_gate",
			Label:       "Team Approval Gate",
			Source:      "built_in",
			Operator:    "team_approval_gate",
			Description: "Pass when prior executable team role approval/rejection packets satisfy the configured quorum status.",
			Params: []WorkflowNodeFieldOption{
				{Name: "params.team", Label: "Team", Type: "text", Description: "Optional team name filter."},
				{Name: "params.status", Label: "Required status", Type: "select", Required: true, Options: []string{"passed", "pending", "blocked", "not_blocked"}},
				{Name: "params.approval_quorum", Label: "Approval quorum", Type: "number", Description: "Override required approvals when not inherited from the team node."},
				{Name: "params.approval_roles", Label: "Approval roles", Type: "text", Description: "Comma-separated role names counted by the gate."},
				{Name: "params.reject_blocks", Label: "Reject blocks", Type: "boolean", Description: "Treat counted rejections as a blocking gate status."},
			},
		},
	}
	return append(rules, w.CustomWorkflowPolicyRules()...)
}

func commonExecutableNodeFields() []WorkflowNodeFieldOption {
	return []WorkflowNodeFieldOption{
		{Name: "agent", Label: "Agent", Type: "agent", Required: true, Description: "Configured agent profile that owns this stage."},
		{Name: "skill", Label: "Skill", Type: "skill", Required: true, Description: "Skill instructions applied to the stage."},
		{Name: "input", Label: "Inputs", Type: "map", Description: "Input name to reference, for example stages.plan.outputs.summary."},
		{Name: "outputs", Label: "Outputs", Type: "map", Description: "Output name to result reference, for example result.output."},
		{Name: "artifacts", Label: "Artifacts", Type: "artifact_list", Description: "Declared replay artifacts such as reports, evidence, or diffs. Each item supports name, kind, title, ref, summary, and content."},
		{Name: "approval", Label: "Approval", Type: "boolean", Description: "Pause before entering this stage unless run is pre-approved."},
		{Name: "retry.max_attempts", Label: "Max attempts", Type: "number", Description: "Bounded retry count for this executable stage."},
		{Name: "on_error", Label: "On error", Type: "stage_list", Description: "Fallback stages when all retry attempts fail."},
	}
}

func executableWorkflowNodeOutputs() []WorkflowNodeVariableOption {
	return []WorkflowNodeVariableOption{
		{Name: "summary", Description: "Bounded result summary"},
		{Name: "raw_output", Description: "Raw model output"},
		{Name: "tool_results", Description: "Tool results as JSON"},
		{Name: "findings", Description: "Findings as JSON"},
		{Name: "changes", Description: "Changes as JSON"},
		{Name: "verification", Description: "Verification results as JSON"},
	}
}

func controlWorkflowNodeOutputs() []WorkflowNodeVariableOption {
	return []WorkflowNodeVariableOption{
		{Name: "kind", Description: "Control node kind"},
		{Name: "route", Description: "Selected route"},
		{Name: "target", Description: "Selected target stage"},
		{Name: "passed", Description: "Boolean decision when applicable"},
		{Name: "reason", Description: "Decision reason when present"},
	}
}

func qualityWorkflowNodeOutputs() []WorkflowNodeVariableOption {
	outputs := controlWorkflowNodeOutputs()
	outputs = append(outputs,
		WorkflowNodeVariableOption{Name: "quality_status", Description: "Quality outcome: passed, warning, or failed"},
		WorkflowNodeVariableOption{Name: "score", Description: "Quality score from 0 to 100"},
		WorkflowNodeVariableOption{Name: "acceptance_total", Description: "Acceptance checks evaluated"},
		WorkflowNodeVariableOption{Name: "acceptance_failed", Description: "Failed acceptance checks"},
		WorkflowNodeVariableOption{Name: "verification_total", Description: "Verification results evaluated"},
		WorkflowNodeVariableOption{Name: "verification_failed", Description: "Failed verification results"},
		WorkflowNodeVariableOption{Name: "evidence_artifacts", Description: "Evidence artifacts counted"},
		WorkflowNodeVariableOption{Name: "failures", Description: "Semicolon-separated failure reasons"},
	)
	return outputs
}

func builtInWorkflowTemplates() map[string]WorkflowTemplate {
	templates := []WorkflowTemplate{
		taskDecompositionWorkflowTemplate(),
		multiDomainIntakeRouterWorkflowTemplate(),
		agentFrameworkExtensionWorkflowTemplate(),
		planFixAuditWorkflowTemplate(),
		softwareQualityGateWorkflowTemplate(),
		webResearchRiskWorkflowTemplate(),
		securityEvidenceGateWorkflowTemplate(),
		parallelResearchReviewWorkflowTemplate(),
		binaryTriageWorkflowTemplate(),
		docsReviewWorkflowTemplate(),
		operationsRunbookWorkflowTemplate(),
		customerSupportTriageWorkflowTemplate(),
		humanInputSecurityWorkflowTemplate(),
		softwareTeamReviewGateWorkflowTemplate(),
	}
	out := make(map[string]WorkflowTemplate, len(templates))
	for _, template := range templates {
		name := normalizePersistedWorkflowName(template.Name)
		template.Name = name
		template.Graph.Name = name
		template.WorkflowTemplateSummary.Stages = len(template.Graph.Stages)
		out[name] = template
	}
	return out
}

func multiDomainIntakeRouterWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "multi-domain-intake-router",
			Title:       "Multi-Domain Intake Router",
			Category:    "starter",
			Description: "Collect task context, route it to a domain team, then synthesize a quality-gated handoff. Use this as the default Studio starter for mixed software, security, docs, ops, support, and platform requests.",
			Tags:        []string{"starter", "multi-domain", "team", "router", "quality_gate", "handoff"},
		},
		Graph: WorkflowGraphDocument{
			Description: "A practical entry workflow that links reusable team templates with a common intake, routing, synthesis, and quality gate.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"intake"}, Position: WorkflowGraphPosition{X: 60, Y: 330}},
				{
					Name:     "intake",
					NodeType: "input_gate",
					Params: map[string]string{
						"manual":      "true",
						"fields_json": `{"fields":[{"name":"domain","type":"select","label":"Task domain","required":true,"options":["software","web_security","security","binary","documentation","operations","support","platform","general"],"default":"general"},{"name":"goal","type":"textarea","label":"Goal","required":true},{"name":"target","type":"text","label":"Target file, URL, binary, system, or customer context"},{"name":"desired_output","type":"select","label":"Desired output","options":["implementation","review_report","runbook","customer_reply","extension_kit","plan"],"default":"plan"},{"name":"risk_tolerance","type":"select","label":"Risk tolerance","options":["low","medium","high"],"default":"medium"}]}`,
						"prompt":      "Confirm the domain, goal, target, desired output, and risk tolerance before routing to a domain team.",
					},
					Outputs: map[string]string{
						"domain":          "params.domain",
						"goal":            "params.goal",
						"target":          "params.target",
						"desired_output":  "params.desired_output",
						"risk_tolerance":  "params.risk_tolerance",
						"intake_summary":  "result.summary",
						"recommended_kit": "params.domain",
					},
					Next:     []string{"domain-router"},
					Position: WorkflowGraphPosition{X: 340, Y: 330},
				},
				{
					Name:     "domain-router",
					NodeType: "switch",
					SwitchOn: "stages.intake.outputs.domain",
					Cases: map[string]string{
						"software":      "software-team",
						"web_security":  "web-security-team",
						"security":      "security-team",
						"binary":        "binary-team",
						"documentation": "docs-team",
						"operations":    "ops-team",
						"support":       "support-team",
						"platform":      "platform-team",
						"general":       "general-plan",
						"default":       "general-plan",
					},
					Position: WorkflowGraphPosition{X: 640, Y: 330},
				},
				domainTeamRouterStage("software-team", "software-task-team", "software-review", 940, 40),
				domainTeamRouterStage("web-security-team", "web-research-team", "security-review", 940, 160),
				domainTeamRouterStage("security-team", "audit-security-team", "security-review", 940, 280),
				domainTeamRouterStage("binary-team", "binary-triage-team", "security-review", 940, 400),
				domainTeamRouterStage("docs-team", "documentation-team", "docs-review", 940, 520),
				domainTeamRouterStage("ops-team", "operations-runbook-team", "ops-review", 940, 640),
				domainTeamRouterStage("support-team", "customer-support-team", "support-review", 940, 760),
				domainTeamRouterStage("platform-team", "framework-extension-team", "extension-review", 940, 880),
				{
					Name:     "general-plan",
					NodeType: "agent",
					Agent:    "planner",
					Skill:    "execution-plan",
					Input: map[string]string{
						"domain":         "stages.intake.outputs.domain",
						"goal":           "stages.intake.outputs.goal",
						"target":         "stages.intake.outputs.target",
						"desired_output": "stages.intake.outputs.desired_output",
						"risk_tolerance": "stages.intake.outputs.risk_tolerance",
					},
					Params: map[string]string{
						"purpose":         "Plan a general task when no specialized domain team was selected.",
						"output_contract": "Emit sections named Summary, Recommended Agent, Recommended Skill, Required Tools, Suggested Workflow, Risks, Acceptance Criteria, and Next Steps.",
					},
					Outputs: map[string]string{
						"summary": "result.summary",
						"output":  "result.output",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "general-domain-plan", Kind: "plan", Ref: "result.output", Title: "General domain plan"},
					},
					Next:     []string{"synthesize"},
					Position: WorkflowGraphPosition{X: 940, Y: 1000},
				},
				{
					Name:     "synthesize",
					NodeType: "skill",
					Agent:    "chat",
					Skill:    "execution-plan",
					Input: map[string]string{
						"domain":         "stages.intake.outputs.domain",
						"goal":           "stages.intake.outputs.goal",
						"target":         "stages.intake.outputs.target",
						"desired_output": "stages.intake.outputs.desired_output",
						"branch_summary": "previous.summary",
					},
					Params: map[string]string{
						"purpose":         "Convert the selected domain team's output into a user-facing handoff and a reusable next-step plan.",
						"output_contract": "Emit sections named Domain Routed, Team Output Summary, Recommended Workflow Template, Required Agent/Skill/Tool Resources, Risks, Acceptance Criteria, and Final Handoff.",
						"quality_bar":     "The handoff must name the routed domain, the team/template used, what artifact was produced, and what the user should run next.",
					},
					Outputs: map[string]string{
						"handoff":              "result.output",
						"summary":              "result.summary",
						"recommended_workflow": "result.findings",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "multi-domain-handoff", Kind: "report", Ref: "result.output", Title: "Multi-domain handoff", Summary: "Routed domain output and recommended next workflow."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "has-domain", Ref: "result.output", Contains: "Domain Routed", Expected: "handoff names the routed domain"},
						{Name: "has-template", Ref: "result.output", Contains: "Recommended Workflow Template", Expected: "handoff recommends a concrete template"},
						{Name: "has-acceptance", Ref: "result.output", Contains: "Acceptance Criteria", Expected: "handoff includes acceptance criteria"},
					},
					Next:     []string{"quality"},
					Position: WorkflowGraphPosition{X: 1360, Y: 330},
				},
				{Name: "quality", NodeType: "quality_gate", Params: map[string]string{"require_acceptance": "true", "require_evidence": "true", "min_score": "75", "reason": "multi-domain handoff is missing domain routing evidence or acceptance criteria"}, Routes: map[string]string{"pass": "handoff", "warning": "operator-check", "fail": "revise"}, Position: WorkflowGraphPosition{X: 1680, Y: 330}},
				{Name: "operator-check", NodeType: "checkpoint", Params: map[string]string{"prompt": "Review the routed team output and final handoff before continuing."}, Next: []string{"handoff"}, Position: WorkflowGraphPosition{X: 1980, Y: 180}},
				{Name: "handoff", NodeType: "skill", Agent: "chat", Skill: "execution-plan", Input: map[string]string{"handoff": "stages.synthesize.outputs.handoff", "quality": "stages.quality.outputs.quality_status"}, Outputs: map[string]string{"final": "result.output"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "multi-domain-final", Kind: "report", Ref: "result.output", Title: "Final multi-domain response"}}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 2280, Y: 260}},
				{Name: "revise", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"failures": "stages.quality.outputs.failures", "handoff": "stages.synthesize.outputs.handoff"}, Outputs: map[string]string{"revision_plan": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1980, Y: 500}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 2580, Y: 330}},
			},
		},
	}
}

func domainTeamRouterStage(name, team, approvalPreset string, x, y int) WorkflowGraphStageDocument {
	params := map[string]string{
		"team":    team,
		"execute": "true",
	}
	if strings.TrimSpace(approvalPreset) != "" {
		params["approval_preset"] = approvalPreset
	}
	return WorkflowGraphStageDocument{
		Name:     name,
		NodeType: "team",
		Agent:    "planner",
		Params:   params,
		Input: map[string]string{
			"domain":         "stages.intake.outputs.domain",
			"goal":           "stages.intake.outputs.goal",
			"target":         "stages.intake.outputs.target",
			"desired_output": "stages.intake.outputs.desired_output",
			"risk_tolerance": "stages.intake.outputs.risk_tolerance",
		},
		Outputs: map[string]string{
			"team":    "result.structured",
			"summary": "result.summary",
		},
		Next:     []string{"synthesize"},
		Position: WorkflowGraphPosition{X: x, Y: y},
	}
}

func agentFrameworkExtensionWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "agent-framework-extension",
			Title:       "Agent Framework Extension",
			Category:    "platform",
			Description: "Design and materialize a linked Agent/Skill/Tool/Workflow/Team extension with quality gates and operator review.",
			Tags:        []string{"agent-framework", "extension", "kit", "workflow-template", "quality_gate"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Framework-extension workflow for building second-development resources as a coordinated kit instead of disconnected files.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"scope"}, Position: WorkflowGraphPosition{X: 60, Y: 260}},
				{
					Name:     "scope",
					NodeType: "input_gate",
					Params: map[string]string{
						"manual":      "true",
						"fields_json": `{"fields":[{"name":"domain","type":"text","label":"Domain or vertical","required":true},{"name":"goal","type":"textarea","label":"Extension goal","required":true},{"name":"resources","type":"multiselect","label":"Resources to create","options":["agent","skill","tool","workflow","workflow_template","team_template","policy_rule","kit"],"default":["agent","skill","workflow","kit"]},{"name":"risk_tolerance","type":"select","label":"Risk tolerance","options":["low","medium","high"],"default":"medium"}]}`,
						"prompt":      "Describe the vertical domain, extension goal, resources to create, and risk tolerance before generating a framework extension.",
					},
					Outputs: map[string]string{
						"domain":         "params.domain",
						"goal":           "params.goal",
						"resources":      "params.resources",
						"risk_tolerance": "params.risk_tolerance",
					},
					Next:     []string{"team"},
					Position: WorkflowGraphPosition{X: 330, Y: 250},
				},
				{
					Name:     "team",
					NodeType: "team",
					Agent:    "planner",
					Params: map[string]string{
						"team":    "framework-extension-team",
						"execute": "false",
					},
					Input: map[string]string{
						"domain":    "stages.scope.outputs.domain",
						"goal":      "stages.scope.outputs.goal",
						"resources": "stages.scope.outputs.resources",
					},
					Outputs: map[string]string{
						"team":  "result.structured",
						"roles": "result.roles",
					},
					Next:     []string{"design"},
					Position: WorkflowGraphPosition{X: 630, Y: 250},
				},
				{
					Name:     "design",
					NodeType: "agent",
					Agent:    "planner",
					Skill:    "execution-plan",
					Input: map[string]string{
						"domain":         "stages.scope.outputs.domain",
						"goal":           "stages.scope.outputs.goal",
						"resources":      "stages.scope.outputs.resources",
						"risk_tolerance": "stages.scope.outputs.risk_tolerance",
						"team":           "stages.team.outputs.team",
					},
					Params: map[string]string{
						"purpose":         "Design a linked GoFlow extension package that can be materialized as versionable resources.",
						"output_contract": "Emit sections named Extension Summary, Resource Map, Agent Contract, Skill Contract, Tool Boundary, Workflow Data Flow, Team Roles, Policy Gates, Files To Create, Restart/Reload Notes, and Acceptance Criteria.",
						"quality_bar":     "Every generated resource must have a name, storage path, owner, inputs, outputs, safety boundary, and validation path.",
					},
					Outputs: map[string]string{
						"design":       "result.output",
						"summary":      "result.summary",
						"resource_map": "result.findings",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "extension-design", Kind: "plan", Ref: "result.output", Title: "Extension design", Summary: "Linked Agent/Skill/Tool/Workflow/Team resource design."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "has-resource-map", Ref: "result.output", Contains: "Resource Map", Expected: "design lists linked resources"},
						{Name: "has-workflow-data-flow", Ref: "result.output", Contains: "Workflow Data Flow", Expected: "design explains output-to-input chaining"},
						{Name: "has-tool-boundary", Ref: "result.output", Contains: "Tool Boundary", Expected: "design declares tool permissions and sandbox boundary"},
						{Name: "has-acceptance", Ref: "result.output", Contains: "Acceptance Criteria", Expected: "design includes acceptance criteria"},
					},
					Next:     []string{"review-design"},
					Position: WorkflowGraphPosition{X: 950, Y: 250},
				},
				{
					Name:     "review-design",
					NodeType: "skill",
					Agent:    "auditor",
					Skill:    "code-audit",
					Input: map[string]string{
						"design": "stages.design.outputs.design",
						"scope":  "stages.scope.outputs.goal",
					},
					Params: map[string]string{
						"review_scope":    "Review the extension design for over-broad agents, missing skill/tool contracts, missing workflow data flow, missing validation, unsafe tool permissions, and restart/reload ambiguity.",
						"output_contract": "Emit sections named Review Decision, Missing Contracts, Safety Risks, Validation Gaps, Required Changes, and Recommendation. Use approved only when ready to materialize.",
					},
					Outputs: map[string]string{
						"review":         "result.output",
						"recommendation": "result.summary",
						"findings":       "result.findings",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "extension-design-review", Kind: "review", Ref: "result.output", Title: "Extension design review", Summary: "Safety and completeness review for the extension design."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "review-decision", Ref: "result.output", Contains: "Review Decision", Expected: "review declares readiness"},
						{Name: "safety-reviewed", Ref: "result.output", Contains: "Safety Risks", Expected: "review covers safety risks"},
						{Name: "validation-reviewed", Ref: "result.output", Contains: "Validation Gaps", Expected: "review covers validation gaps"},
					},
					Next:     []string{"quality"},
					Position: WorkflowGraphPosition{X: 1290, Y: 250},
				},
				{Name: "quality", NodeType: "quality_gate", Params: map[string]string{"require_acceptance": "true", "require_evidence": "true", "min_score": "80", "reason": "extension design is missing linked resource contracts, safety review, or validation evidence"}, Routes: map[string]string{"pass": "operator-check", "warning": "operator-check", "fail": "revise-design"}, Position: WorkflowGraphPosition{X: 1600, Y: 270}},
				{Name: "operator-check", NodeType: "checkpoint", Params: map[string]string{"prompt": "Review the extension design, safety review, and resource map before generating files."}, Next: []string{"materialize"}, Position: WorkflowGraphPosition{X: 1880, Y: 170}},
				{
					Name:     "materialize",
					NodeType: "agent",
					Agent:    "fixer",
					Skill:    "code-writing",
					Input: map[string]string{
						"design": "stages.design.outputs.design",
						"review": "stages.review-design.outputs.review",
					},
					Params: map[string]string{
						"purpose": "Create or update the requested GoFlow extension resources under their modular folders. Prefer scaffold commands or validated YAML formats where possible.",
					},
					Outputs: map[string]string{
						"changes": "result.changes",
						"summary": "result.summary",
						"output":  "result.output",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "extension-materialization", Kind: "change", Ref: "result.changes", Title: "Materialized extension resources"},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "changes-recorded", Ref: "result.output", Contains: "change", Expected: "materialization records created or changed resources"},
					},
					Approval: true,
					Retry:    WorkflowGraphRetry{MaxAttempts: 2},
					Next:     []string{"final-review"},
					Position: WorkflowGraphPosition{X: 2180, Y: 170},
				},
				{
					Name:     "final-review",
					NodeType: "skill",
					Agent:    "auditor",
					Skill:    "code-audit",
					Input: map[string]string{
						"design":  "stages.design.outputs.design",
						"changes": "stages.materialize.outputs.changes",
					},
					Params: map[string]string{
						"review_scope": "Check whether the generated resources match the design, remain workspace scoped, declare validation/restart guidance, and avoid unrelated edits.",
					},
					Outputs: map[string]string{
						"review": "result.output",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "extension-final-review", Kind: "review", Ref: "result.output", Title: "Final extension review"},
					},
					Next:     []string{"handoff"},
					Position: WorkflowGraphPosition{X: 2490, Y: 170},
				},
				{Name: "revise-design", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"failures": "stages.quality.outputs.failures", "review": "stages.review-design.outputs.review"}, Outputs: map[string]string{"revision_plan": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1880, Y: 390}},
				{Name: "handoff", NodeType: "skill", Agent: "chat", Skill: "execution-plan", Input: map[string]string{"changes": "stages.materialize.outputs.changes", "review": "stages.final-review.outputs.review"}, Outputs: map[string]string{"handoff": "result.output"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "extension-handoff", Kind: "report", Ref: "result.output", Title: "Extension handoff"}}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 2810, Y: 170}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 3120, Y: 260}},
			},
		},
	}
}

func taskDecompositionWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "task-decomposition-plan",
			Title:       "Task Decomposition Plan",
			Category:    "planning",
			Description: "Decompose a complex request into a reviewable workflow graph candidate with contracts, risks, artifacts, and acceptance criteria before execution.",
			Tags:        []string{"planning", "quality", "workflow-template", "acceptance", "risk"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Pre-flight planning workflow for complex tasks. It produces a normalized workflow graph candidate rather than directly changing project files.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"decompose"}, Position: WorkflowGraphPosition{X: 60, Y: 240}},
				{
					Name:     "decompose",
					NodeType: "agent",
					Agent:    "planner",
					Skill:    "execution-plan",
					Params: map[string]string{
						"purpose":         "Turn the original request into a normalized plan graph candidate before implementation.",
						"output_contract": "Emit sections named Plan Graph Candidate, Phases, Dependencies, Risk Labels, Required Workspace Access, Required Tools, Suggested Agents, Suggested Skills, Expected Artifacts, Acceptance Criteria, Checkpoints, and Failure Modes.",
						"quality_bar":     "Every executable node must have required inputs, expected outputs, artifacts, retry/failure guidance, and acceptance criteria.",
					},
					Outputs: map[string]string{
						"plan_graph_candidate": "result.output",
						"summary":              "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "plan-graph-candidate", Kind: "plan", Ref: "result.output", Title: "Plan graph candidate", Summary: "Normalized candidate workflow plan with phases, dependencies, contracts, risks, and artifacts."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "has-phases", Ref: "result.output", Contains: "Phases", Expected: "candidate lists the major phases"},
						{Name: "has-acceptance", Ref: "result.output", Contains: "Acceptance Criteria", Expected: "candidate declares acceptance criteria"},
						{Name: "has-artifacts", Ref: "result.output", Contains: "Expected Artifacts", Expected: "candidate declares expected artifacts"},
						{Name: "has-risks", Ref: "result.output", Contains: "Risk Labels", Expected: "candidate classifies task risk"},
					},
					Next:     []string{"review-plan"},
					Position: WorkflowGraphPosition{X: 340, Y: 220},
				},
				{
					Name:     "review-plan",
					NodeType: "skill",
					Agent:    "auditor",
					Skill:    "code-audit",
					Input: map[string]string{
						"candidate": "stages.decompose.outputs.plan_graph_candidate",
					},
					Params: map[string]string{
						"review_scope":    "Review the candidate for missing dependencies, unsafe tool access, weak acceptance criteria, missing evidence, missing human checkpoints, and unclear handoffs.",
						"output_contract": "Emit sections named Review Decision, Risks, Missing Evidence, Required Changes, and Recommendation. Include the word approved only if the candidate is ready to materialize.",
					},
					Outputs: map[string]string{
						"review":         "result.output",
						"findings":       "result.findings",
						"recommendation": "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "plan-review", Kind: "review", Ref: "result.output", Title: "Plan review", Summary: "Review of candidate graph quality, safety, evidence, and handoff readiness."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "review-decision", Ref: "result.output", Contains: "Review Decision", Expected: "review declares whether the candidate is ready"},
						{Name: "risk-reviewed", Ref: "result.output", Contains: "Risks", Expected: "review covers safety and quality risks"},
						{Name: "recommendation-present", Ref: "result.output", Contains: "Recommendation", Expected: "review gives an actionable recommendation"},
					},
					Next:     []string{"quality"},
					Position: WorkflowGraphPosition{X: 670, Y: 220},
				},
				{
					Name:     "quality",
					NodeType: "quality_gate",
					Params: map[string]string{
						"require_acceptance":   "true",
						"require_verification": "true",
						"require_evidence":     "true",
						"min_score":            "75",
						"reason":               "task decomposition candidate is missing required contracts, evidence, or review quality",
					},
					Routes:   map[string]string{"pass": "materialize-candidate", "warning": "operator-review", "fail": "clarify-scope"},
					Position: WorkflowGraphPosition{X: 990, Y: 250},
				},
				{
					Name:     "operator-review",
					NodeType: "checkpoint",
					Params: map[string]string{
						"prompt": "The decomposition passed with warnings. Review the candidate and plan-review artifacts before materializing the workflow draft.",
					},
					Next:     []string{"materialize-candidate"},
					Position: WorkflowGraphPosition{X: 1290, Y: 120},
				},
				{
					Name:     "materialize-candidate",
					NodeType: "skill",
					Agent:    "planner",
					Skill:    "execution-plan",
					Input: map[string]string{
						"candidate": "stages.decompose.outputs.plan_graph_candidate",
						"review":    "stages.review-plan.outputs.review",
						"quality":   "stages.quality.outputs.quality_status",
					},
					Params: map[string]string{
						"purpose":         "Produce a final workflow graph draft that can be copied into workflows/<name>/workflow.yaml or saved as a workflow template resource.",
						"output_contract": "Emit Final Workflow Draft, Node Contracts, Data Flow, Approval Gates, Evidence Plan, and Next Steps. Include a YAML skeleton with name, description, and stages.",
					},
					Outputs: map[string]string{
						"workflow_draft": "result.output",
						"summary":        "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "workflow-draft", Kind: "workflow_template", Ref: "result.output", Title: "Workflow draft", Summary: "Materialized workflow graph draft with node contracts and data-flow notes."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "draft-has-yaml", Ref: "result.output", Contains: "stages", Expected: "draft includes a workflow YAML skeleton"},
						{Name: "draft-has-data-flow", Ref: "result.output", Contains: "Data Flow", Expected: "draft explains how node outputs become downstream inputs"},
						{Name: "draft-has-approval-gates", Ref: "result.output", Contains: "Approval Gates", Expected: "draft names human or policy checkpoints"},
					},
					Next:     []string{"end"},
					Position: WorkflowGraphPosition{X: 1600, Y: 220},
				},
				{
					Name:     "clarify-scope",
					NodeType: "input_gate",
					Params: map[string]string{
						"manual":      "true",
						"fields_json": `{"fields":[{"name":"scope","type":"textarea","label":"Scope clarification","required":true},{"name":"risk_tolerance","type":"select","label":"Risk tolerance","options":["low","medium","high"],"default":"medium"},{"name":"required_artifacts","type":"textarea","label":"Required artifacts"}]}`,
						"prompt":      "The decomposition did not meet the quality bar. Provide missing scope, risk tolerance, and required artifacts before retrying.",
					},
					Outputs: map[string]string{
						"scope":              "params.scope",
						"risk_tolerance":     "params.risk_tolerance",
						"required_artifacts": "params.required_artifacts",
					},
					Next:     []string{"decompose"},
					Position: WorkflowGraphPosition{X: 1290, Y: 410},
				},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1900, Y: 240}},
			},
		},
	}
}

func planFixAuditWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "plan-fix-audit",
			Title:       "Plan -> Fix -> Audit",
			Category:    "software",
			Description: "Planner produces an implementation plan, fixer applies changes with approval, auditor reviews the result.",
			Tags:        []string{"coding", "audit", "approval"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Plan, implement, and audit a software change.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"plan"}, Position: WorkflowGraphPosition{X: 60, Y: 180}},
				{Name: "plan", NodeType: "agent", Agent: "planner", Skill: "execution-plan", Outputs: map[string]string{"plan": "result.output"}, Next: []string{"implement"}, Position: WorkflowGraphPosition{X: 300, Y: 160}},
				{Name: "implement", NodeType: "agent", Agent: "fixer", Skill: "code-writing", Input: map[string]string{"plan": "stages.plan.outputs.plan"}, Outputs: map[string]string{"changes": "result.changes", "summary": "result.summary"}, Approval: true, Retry: WorkflowGraphRetry{MaxAttempts: 2}, Next: []string{"audit"}, Position: WorkflowGraphPosition{X: 580, Y: 160}},
				{Name: "audit", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Input: map[string]string{"changes": "stages.implement.outputs.changes", "plan": "stages.plan.outputs.plan"}, Outputs: map[string]string{"audit": "result.output", "findings": "result.findings"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 860, Y: 160}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1120, Y: 180}},
			},
		},
	}
}

func softwareQualityGateWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "software-quality-gate",
			Title:       "Software Quality Gate",
			Category:    "software",
			Description: "Plan, implement, verify, audit, and route through an evidence-backed quality gate before handoff.",
			Tags:        []string{"coding", "tests", "audit", "quality_gate", "approval"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Complex software implementation workflow with explicit acceptance criteria, artifacts, verifier evidence, and quality-gate routing.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"plan"}, Position: WorkflowGraphPosition{X: 60, Y: 220}},
				{Name: "plan", NodeType: "agent", Agent: "planner", Skill: "execution-plan", Outputs: map[string]string{"plan": "result.output"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "implementation-plan", Kind: "report", Ref: "result.output", Title: "Implementation plan"}}, Next: []string{"implement"}, Position: WorkflowGraphPosition{X: 300, Y: 200}},
				{Name: "implement", NodeType: "agent", Agent: "fixer", Skill: "code-writing", Input: map[string]string{"plan": "stages.plan.outputs.plan"}, Outputs: map[string]string{"summary": "result.summary", "changes": "result.changes"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "change-summary", Kind: "change", Ref: "result.changes", Title: "Change summary"}}, AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{{Name: "changes-recorded", Ref: "result.output", Contains: "change", Expected: "implementation explains changed behavior"}}, Approval: true, Retry: WorkflowGraphRetry{MaxAttempts: 2}, Next: []string{"verify"}, Position: WorkflowGraphPosition{X: 580, Y: 200}},
				{Name: "verify", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Input: map[string]string{"changes": "stages.implement.outputs.changes", "plan": "stages.plan.outputs.plan"}, Outputs: map[string]string{"verification": "result.output"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "verification-report", Kind: "verification", Ref: "result.output", Title: "Verification report"}}, AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{{Name: "verification-evidence", Ref: "result.output", Contains: "test", Expected: "verification mentions tests or equivalent checks"}}, Next: []string{"audit"}, Position: WorkflowGraphPosition{X: 860, Y: 200}},
				{Name: "audit", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Input: map[string]string{"changes": "stages.implement.outputs.changes", "verification": "stages.verify.outputs.verification"}, Outputs: map[string]string{"audit": "result.output", "findings": "result.findings"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "audit-report", Kind: "report", Ref: "result.output", Title: "Audit report"}}, AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{{Name: "audit-risk-reviewed", Ref: "result.output", Contains: "risk", Expected: "audit mentions residual risk"}}, Next: []string{"quality"}, Position: WorkflowGraphPosition{X: 1140, Y: 200}},
				{Name: "quality", NodeType: "quality_gate", Params: map[string]string{"require_acceptance": "true", "require_verification": "true", "require_evidence": "true", "min_score": "80", "reason": "software quality evidence did not meet the release threshold"}, Routes: map[string]string{"pass": "handoff", "warning": "review", "fail": "revise"}, Position: WorkflowGraphPosition{X: 1420, Y: 220}},
				{Name: "handoff", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"quality": "stages.quality.outputs.quality_status", "audit": "stages.audit.outputs.audit"}, Outputs: map[string]string{"handoff": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1700, Y: 120}},
				{Name: "review", NodeType: "checkpoint", Params: map[string]string{"prompt": "Quality gate returned warnings. Review evidence before handoff."}, Next: []string{"handoff"}, Position: WorkflowGraphPosition{X: 1700, Y: 260}},
				{Name: "revise", NodeType: "skill", Agent: "fixer", Skill: "code-writing", Input: map[string]string{"failures": "stages.quality.outputs.failures", "audit": "stages.audit.outputs.audit"}, Outputs: map[string]string{"revision_plan": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1700, Y: 400}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 2020, Y: 220}},
			},
		},
	}
}

func webResearchRiskWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "web-research-risk",
			Title:       "Web Research -> Risk Audit",
			Category:    "security",
			Description: "Collect web evidence, audit it, branch on findings, and produce a risk report.",
			Tags:        []string{"web", "security", "branching"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Research a web target and route findings through a risk branch.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"collect"}, Position: WorkflowGraphPosition{X: 60, Y: 200}},
				{Name: "collect", NodeType: "agent", Agent: "auditor", Skill: "web-vulnerability-research", Outputs: map[string]string{"evidence": "result.output", "findings": "result.findings"}, Next: []string{"audit"}, Position: WorkflowGraphPosition{X: 300, Y: 170}},
				{Name: "audit", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Input: map[string]string{"evidence": "stages.collect.outputs.evidence"}, Outputs: map[string]string{"audit": "result.output", "findings": "result.findings"}, Next: []string{"has-risk"}, Position: WorkflowGraphPosition{X: 580, Y: 170}},
				{Name: "has-risk", NodeType: "condition", Condition: `contains(stages.audit.outputs.audit, "vulnerability")`, Routes: map[string]string{"true": "verify", "false": "report-clean"}, Position: WorkflowGraphPosition{X: 850, Y: 200}},
				{Name: "verify", NodeType: "checkpoint", Params: map[string]string{"prompt": "Review the evidence before running verification."}, Next: []string{"report-risk"}, Position: WorkflowGraphPosition{X: 1120, Y: 120}},
				{Name: "report-risk", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"audit": "stages.audit.outputs.audit"}, Outputs: map[string]string{"report": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1380, Y: 120}},
				{Name: "report-clean", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"audit": "stages.audit.outputs.audit"}, Outputs: map[string]string{"report": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1120, Y: 300}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1640, Y: 200}},
			},
		},
	}
}

func securityEvidenceGateWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "security-audit-evidence-gate",
			Title:       "Security Audit Evidence Gate",
			Category:    "security",
			Description: "Collect target scope, gather evidence, audit findings, and route through a quality gate before reporting.",
			Tags:        []string{"security", "evidence", "quality_gate", "manual-input"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Security audit workflow with operator scope, evidence artifacts, acceptance criteria, quality gate, and remediation branch.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"scope"}, Position: WorkflowGraphPosition{X: 60, Y: 220}},
				{Name: "scope", NodeType: "input_gate", Params: map[string]string{"manual": "true", "fields_json": `{"fields":[{"name":"target","type":"url","label":"Target URL","required":true},{"name":"scope","type":"textarea","label":"Scope","required":true},{"name":"depth","type":"integer","default":"1","min":"1","max":"3"}]}`, "prompt": "Provide the authorized target, scope, and depth before security analysis."}, Outputs: map[string]string{"target": "params.target", "scope": "params.scope", "depth": "params.depth"}, Next: []string{"collect"}, Position: WorkflowGraphPosition{X: 320, Y: 210}},
				{Name: "collect", NodeType: "skill", Agent: "auditor", Skill: "web-vulnerability-research", Input: map[string]string{"target": "stages.scope.outputs.target", "scope": "stages.scope.outputs.scope"}, Outputs: map[string]string{"evidence": "result.output", "findings": "result.findings"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "web-evidence", Kind: "evidence", Ref: "result.output", Title: "Collected web evidence"}}, AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{{Name: "target-covered", Ref: "result.output", Contains: "http", Expected: "evidence references fetched target material"}}, Next: []string{"audit"}, Position: WorkflowGraphPosition{X: 620, Y: 190}},
				{Name: "audit", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Input: map[string]string{"evidence": "stages.collect.outputs.evidence"}, Outputs: map[string]string{"audit": "result.output", "findings": "result.findings"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "audit-findings", Kind: "finding", Ref: "result.findings", Title: "Audit findings"}, {Name: "audit-report", Kind: "report", Ref: "result.output", Title: "Audit report"}}, AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{{Name: "risk-classified", Ref: "result.output", Contains: "risk", Expected: "audit classifies risk and impact"}}, Next: []string{"quality"}, Position: WorkflowGraphPosition{X: 920, Y: 190}},
				{Name: "quality", NodeType: "quality_gate", Params: map[string]string{"require_acceptance": "true", "require_evidence": "true", "min_score": "75", "reason": "security evidence is insufficient for a final report"}, Routes: map[string]string{"pass": "report", "warning": "review", "fail": "remediate"}, Position: WorkflowGraphPosition{X: 1220, Y: 220}},
				{Name: "review", NodeType: "checkpoint", Params: map[string]string{"prompt": "Review warning-level security evidence before producing the report."}, Next: []string{"report"}, Position: WorkflowGraphPosition{X: 1510, Y: 110}},
				{Name: "report", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"audit": "stages.audit.outputs.audit", "quality": "stages.quality.outputs.quality_status"}, Outputs: map[string]string{"report": "result.output"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "final-security-report", Kind: "report", Ref: "result.output", Title: "Final security report"}}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1510, Y: 240}},
				{Name: "remediate", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"failures": "stages.quality.outputs.failures", "audit": "stages.audit.outputs.audit"}, Outputs: map[string]string{"remediation_plan": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1510, Y: 390}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1810, Y: 240}},
			},
		},
	}
}

func parallelResearchReviewWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "parallel-research-review",
			Title:       "Parallel Research Review",
			Category:    "research",
			Description: "Fan out into independent research and audit branches, join their outputs, and write a consolidated report.",
			Tags:        []string{"parallel", "join", "research"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Parallel-style research and review workflow with a join barrier.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"split"}, Position: WorkflowGraphPosition{X: 60, Y: 220}},
				{Name: "split", NodeType: "parallel", Next: []string{"research", "audit"}, Position: WorkflowGraphPosition{X: 300, Y: 220}},
				{Name: "research", NodeType: "agent", Agent: "planner", Skill: "execution-plan", Outputs: map[string]string{"research": "result.output"}, Next: []string{"join"}, Position: WorkflowGraphPosition{X: 580, Y: 120}},
				{Name: "audit", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Outputs: map[string]string{"audit": "result.output", "findings": "result.findings"}, Next: []string{"join"}, Position: WorkflowGraphPosition{X: 580, Y: 320}},
				{Name: "join", NodeType: "join", Next: []string{"report"}, Position: WorkflowGraphPosition{X: 900, Y: 220}},
				{Name: "report", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"research": "stages.research.outputs.research", "audit": "stages.audit.outputs.audit"}, Outputs: map[string]string{"report": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1180, Y: 220}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1460, Y: 220}},
			},
		},
	}
}

func binaryTriageWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "binary-triage",
			Title:       "Binary Triage",
			Category:    "security",
			Description: "Plan binary analysis, collect triage evidence, classify risk, and report next steps.",
			Tags:        []string{"binary", "reverse", "security"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Static binary triage workflow for defensive vulnerability research.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"plan"}, Position: WorkflowGraphPosition{X: 60, Y: 200}},
				{Name: "plan", NodeType: "agent", Agent: "planner", Skill: "execution-plan", Outputs: map[string]string{"plan": "result.output"}, Next: []string{"triage"}, Position: WorkflowGraphPosition{X: 300, Y: 170}},
				{Name: "triage", NodeType: "skill", Agent: "auditor", Skill: "binary-vulnerability-research", Input: map[string]string{"plan": "stages.plan.outputs.plan"}, Outputs: map[string]string{"evidence": "result.output", "findings": "result.findings"}, Next: []string{"risk"}, Position: WorkflowGraphPosition{X: 580, Y: 170}},
				{Name: "risk", NodeType: "switch", SwitchOn: "stages.triage.outputs.findings", Cases: map[string]string{"default": "report", "critical": "checkpoint"}, Position: WorkflowGraphPosition{X: 850, Y: 200}},
				{Name: "checkpoint", NodeType: "checkpoint", Params: map[string]string{"prompt": "Critical binary-risk path requires operator review before deeper analysis."}, Next: []string{"deep-review"}, Position: WorkflowGraphPosition{X: 1120, Y: 120}},
				{Name: "deep-review", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Input: map[string]string{"evidence": "stages.triage.outputs.evidence"}, Outputs: map[string]string{"review": "result.output"}, Next: []string{"report"}, Position: WorkflowGraphPosition{X: 1380, Y: 120}},
				{Name: "report", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"evidence": "stages.triage.outputs.evidence"}, Outputs: map[string]string{"report": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1380, Y: 300}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1660, Y: 220}},
			},
		},
	}
}

func docsReviewWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "docs-review-publish",
			Title:       "Docs Review -> Publish",
			Category:    "documentation",
			Description: "Plan documentation, draft changes, review quality, and checkpoint before publish.",
			Tags:        []string{"docs", "review", "approval"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Documentation generation and review workflow.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"plan"}, Position: WorkflowGraphPosition{X: 60, Y: 180}},
				{Name: "plan", NodeType: "agent", Agent: "planner", Skill: "execution-plan", Outputs: map[string]string{"outline": "result.output"}, Next: []string{"draft"}, Position: WorkflowGraphPosition{X: 300, Y: 160}},
				{Name: "draft", NodeType: "agent", Agent: "fixer", Skill: "code-writing", Input: map[string]string{"outline": "stages.plan.outputs.outline"}, Outputs: map[string]string{"draft": "result.output", "changes": "result.changes"}, Approval: true, Next: []string{"review"}, Position: WorkflowGraphPosition{X: 580, Y: 160}},
				{Name: "review", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Input: map[string]string{"draft": "stages.draft.outputs.draft"}, Outputs: map[string]string{"review": "result.output"}, Next: []string{"publish-gate"}, Position: WorkflowGraphPosition{X: 860, Y: 160}},
				{Name: "publish-gate", NodeType: "checkpoint", Params: map[string]string{"prompt": "Approve documentation publish or handoff?"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1130, Y: 160}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1390, Y: 180}},
			},
		},
	}
}

func operationsRunbookWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "operations-runbook",
			Title:       "Operations Runbook",
			Category:    "operations",
			Description: "Plan an operational change, draft a runbook, review rollback coverage, and pause before operator handoff.",
			Tags:        []string{"operations", "runbook", "rollback", "quality_gate", "approval"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Operations runbook workflow with explicit prechecks, rollback evidence, risk review, quality routing, and operator approval.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"plan"}, Position: WorkflowGraphPosition{X: 60, Y: 240}},
				{
					Name:     "plan",
					NodeType: "agent",
					Agent:    "planner",
					Skill:    "execution-plan",
					Params: map[string]string{
						"purpose":         "Create a runbook plan before drafting operational instructions.",
						"output_contract": "Emit sections named Goal, Preconditions, Prechecks, Execution Steps, Rollback Plan, Success Signals, Risks, and Approvals.",
					},
					Outputs: map[string]string{
						"runbook_plan": "result.output",
						"summary":      "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "runbook-plan", Kind: "plan", Ref: "result.output", Title: "Runbook plan", Summary: "Operational plan with prechecks, rollback, success signals, and approvals."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "has-prechecks", Ref: "result.output", Contains: "Prechecks", Expected: "plan names prechecks before execution"},
						{Name: "has-rollback", Ref: "result.output", Contains: "Rollback", Expected: "plan includes rollback strategy"},
						{Name: "has-success-signals", Ref: "result.output", Contains: "Success", Expected: "plan includes success signals"},
					},
					Next:     []string{"draft"},
					Position: WorkflowGraphPosition{X: 330, Y: 220},
				},
				{
					Name:     "draft",
					NodeType: "agent",
					Agent:    "fixer",
					Skill:    "code-writing",
					Input: map[string]string{
						"plan": "stages.plan.outputs.runbook_plan",
					},
					Params: map[string]string{
						"purpose": "Draft or update the runbook document under the confirmed workspace. Keep commands explicit and reversible where possible.",
					},
					Outputs: map[string]string{
						"draft":   "result.output",
						"changes": "result.changes",
						"summary": "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "runbook-draft", Kind: "document", Ref: "result.output", Title: "Runbook draft", Summary: "Drafted runbook or change summary."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "draft-has-rollback", Ref: "result.output", Contains: "rollback", Expected: "draft preserves rollback guidance"},
						{Name: "draft-has-steps", Ref: "result.output", Contains: "step", Expected: "draft includes executable steps"},
					},
					Approval: true,
					Retry:    WorkflowGraphRetry{MaxAttempts: 2},
					Next:     []string{"risk-review"},
					Position: WorkflowGraphPosition{X: 620, Y: 220},
				},
				{
					Name:     "risk-review",
					NodeType: "skill",
					Agent:    "auditor",
					Skill:    "code-audit",
					Input: map[string]string{
						"plan":    "stages.plan.outputs.runbook_plan",
						"draft":   "stages.draft.outputs.draft",
						"changes": "stages.draft.outputs.changes",
					},
					Params: map[string]string{
						"review_scope":    "Review operational risk, destructive commands, rollback coverage, observability, and missing approvals.",
						"output_contract": "Emit sections named Risk Review, Blocking Issues, Rollback Coverage, Verification, and Recommendation.",
					},
					Outputs: map[string]string{
						"risk_review": "result.output",
						"findings":    "result.findings",
						"summary":     "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "runbook-risk-review", Kind: "review", Ref: "result.output", Title: "Runbook risk review", Summary: "Risk and rollback review for the operational runbook."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "risk-reviewed", Ref: "result.output", Contains: "Risk", Expected: "review covers operational risk"},
						{Name: "rollback-reviewed", Ref: "result.output", Contains: "Rollback", Expected: "review covers rollback readiness"},
					},
					Next:     []string{"quality"},
					Position: WorkflowGraphPosition{X: 920, Y: 220},
				},
				{Name: "quality", NodeType: "quality_gate", Params: map[string]string{"require_acceptance": "true", "require_evidence": "true", "min_score": "80", "reason": "runbook does not yet have enough evidence for operator handoff"}, Routes: map[string]string{"pass": "operator-check", "warning": "operator-check", "fail": "revise"}, Position: WorkflowGraphPosition{X: 1230, Y: 240}},
				{Name: "operator-check", NodeType: "checkpoint", Params: map[string]string{"prompt": "Review the runbook, rollback plan, and risk review before operator handoff."}, Next: []string{"handoff"}, Position: WorkflowGraphPosition{X: 1510, Y: 150}},
				{Name: "handoff", NodeType: "skill", Agent: "chat", Skill: "execution-plan", Input: map[string]string{"runbook": "stages.draft.outputs.draft", "risk_review": "stages.risk-review.outputs.risk_review", "quality": "stages.quality.outputs.quality_status"}, Outputs: map[string]string{"operator_handoff": "result.output"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "operator-handoff", Kind: "report", Ref: "result.output", Title: "Operator handoff"}}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1810, Y: 150}},
				{Name: "revise", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"failures": "stages.quality.outputs.failures", "review": "stages.risk-review.outputs.risk_review"}, Outputs: map[string]string{"revision_plan": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1510, Y: 370}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 2110, Y: 240}},
			},
		},
	}
}

func customerSupportTriageWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "customer-support-triage",
			Title:       "Customer Support Triage",
			Category:    "support",
			Description: "Classify a support request, gather required context, draft a response, and review customer-facing quality.",
			Tags:        []string{"support", "triage", "handoff", "input_gate", "quality_gate"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Support workflow with context clarification, response drafting, review, and quality-gated customer handoff.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"triage"}, Position: WorkflowGraphPosition{X: 60, Y: 240}},
				{
					Name:     "triage",
					NodeType: "agent",
					Agent:    "planner",
					Skill:    "execution-plan",
					Params: map[string]string{
						"purpose":         "Classify the customer request and identify missing context before drafting a response.",
						"output_contract": "Emit sections named Summary, Priority, Customer Impact, Known Facts, Missing Context, Suggested Response Plan, and Next Steps.",
					},
					Outputs: map[string]string{
						"triage":  "result.output",
						"summary": "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "support-triage", Kind: "triage", Ref: "result.output", Title: "Support triage", Summary: "Support issue classification and response plan."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "has-priority", Ref: "result.output", Contains: "Priority", Expected: "triage assigns priority"},
						{Name: "has-impact", Ref: "result.output", Contains: "Impact", Expected: "triage explains customer impact"},
						{Name: "has-next-steps", Ref: "result.output", Contains: "Next Steps", Expected: "triage includes actionable next steps"},
					},
					Next:     []string{"needs-context"},
					Position: WorkflowGraphPosition{X: 330, Y: 220},
				},
				{Name: "needs-context", NodeType: "condition", Condition: `contains(stages.triage.outputs.triage, "Missing Context")`, Routes: map[string]string{"true": "collect-context", "false": "draft-response"}, Position: WorkflowGraphPosition{X: 630, Y: 240}},
				{
					Name:     "collect-context",
					NodeType: "input_gate",
					Params: map[string]string{
						"manual":      "true",
						"fields_json": `{"fields":[{"name":"customer_context","type":"textarea","label":"Customer context","required":true},{"name":"account_tier","type":"select","label":"Account tier","options":["unknown","free","pro","enterprise"],"default":"unknown"},{"name":"known_workaround","type":"textarea","label":"Known workaround"}]}`,
						"prompt":      "Provide missing customer context before drafting the support response.",
					},
					Outputs: map[string]string{
						"customer_context": "params.customer_context",
						"account_tier":     "params.account_tier",
						"known_workaround": "params.known_workaround",
					},
					Next:     []string{"draft-response"},
					Position: WorkflowGraphPosition{X: 930, Y: 130},
				},
				{
					Name:     "draft-response",
					NodeType: "skill",
					Agent:    "chat",
					Skill:    "execution-plan",
					Input: map[string]string{
						"triage":           "stages.triage.outputs.triage",
						"customer_context": "stages.collect-context.outputs.customer_context",
						"known_workaround": "stages.collect-context.outputs.known_workaround",
					},
					Params: map[string]string{
						"purpose":         "Draft a concise customer-facing response and an internal next-action checklist.",
						"output_contract": "Emit sections named Customer Reply, Internal Notes, Follow-up Questions, and Next Actions.",
					},
					Outputs: map[string]string{
						"response": "result.output",
						"summary":  "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "customer-response-draft", Kind: "response", Ref: "result.output", Title: "Customer response draft", Summary: "Draft response and internal follow-up notes."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "has-customer-reply", Ref: "result.output", Contains: "Customer Reply", Expected: "draft contains a customer-facing reply"},
						{Name: "has-next-actions", Ref: "result.output", Contains: "Next Actions", Expected: "draft contains internal next actions"},
					},
					Next:     []string{"review"},
					Position: WorkflowGraphPosition{X: 1230, Y: 220},
				},
				{
					Name:     "review",
					NodeType: "skill",
					Agent:    "auditor",
					Skill:    "code-audit",
					Input: map[string]string{
						"triage":   "stages.triage.outputs.triage",
						"response": "stages.draft-response.outputs.response",
					},
					Params: map[string]string{
						"review_scope":    "Review clarity, unsupported claims, missing caveats, privacy-sensitive details, and escalation needs.",
						"output_contract": "Emit sections named Review Decision, Missing Information, Risk, and Recommendation. Use approved only when ready to send.",
					},
					Outputs: map[string]string{
						"review":         "result.output",
						"recommendation": "result.summary",
					},
					Artifacts: []WorkflowGraphArtifactDocument{
						{Name: "support-response-review", Kind: "review", Ref: "result.output", Title: "Support response review", Summary: "Review of response accuracy and customer-readiness."},
					},
					AcceptanceCriteria: []WorkflowGraphAcceptanceCriterionDocument{
						{Name: "review-decision", Ref: "result.output", Contains: "Review Decision", Expected: "review declares readiness"},
						{Name: "risk-reviewed", Ref: "result.output", Contains: "Risk", Expected: "review covers customer-facing risk"},
					},
					Next:     []string{"quality"},
					Position: WorkflowGraphPosition{X: 1530, Y: 220},
				},
				{Name: "quality", NodeType: "quality_gate", Params: map[string]string{"require_acceptance": "true", "require_evidence": "true", "min_score": "75", "reason": "support response is missing required triage, review, or response evidence"}, Routes: map[string]string{"pass": "handoff", "warning": "manager-check", "fail": "revise"}, Position: WorkflowGraphPosition{X: 1830, Y: 240}},
				{Name: "manager-check", NodeType: "checkpoint", Params: map[string]string{"prompt": "Review warning-level support response before handoff."}, Next: []string{"handoff"}, Position: WorkflowGraphPosition{X: 2110, Y: 130}},
				{Name: "handoff", NodeType: "skill", Agent: "chat", Skill: "execution-plan", Input: map[string]string{"response": "stages.draft-response.outputs.response", "review": "stages.review.outputs.review"}, Outputs: map[string]string{"final_response": "result.output"}, Artifacts: []WorkflowGraphArtifactDocument{{Name: "support-handoff", Kind: "report", Ref: "result.output", Title: "Support handoff"}}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 2400, Y: 130}},
				{Name: "revise", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"failures": "stages.quality.outputs.failures", "review": "stages.review.outputs.review"}, Outputs: map[string]string{"revision_plan": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 2110, Y: 370}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 2690, Y: 240}},
			},
		},
	}
}

func humanInputSecurityWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "human-input-security-review",
			Title:       "Human Input Security Review",
			Category:    "security",
			Description: "Collect operator scope, run targeted research, branch by risk, and report.",
			Tags:        []string{"manual-input", "security", "operator"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Operator-guided security review workflow using a manual input gate.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"collect-scope"}, Position: WorkflowGraphPosition{X: 60, Y: 200}},
				{Name: "collect-scope", NodeType: "input_gate", Params: map[string]string{"manual": "true", "fields": "target,scope,severity", "prompt": "Provide target, scope, and expected severity before analysis."}, Outputs: map[string]string{"target": "params.target", "scope": "params.scope"}, Next: []string{"research"}, Position: WorkflowGraphPosition{X: 330, Y: 190}},
				{Name: "research", NodeType: "skill", Agent: "auditor", Skill: "web-vulnerability-research", Input: map[string]string{"target": "stages.collect-scope.outputs.target", "scope": "stages.collect-scope.outputs.scope"}, Outputs: map[string]string{"evidence": "result.output", "findings": "result.findings"}, Next: []string{"risk-check"}, Position: WorkflowGraphPosition{X: 620, Y: 170}},
				{Name: "risk-check", NodeType: "condition", Condition: `len(stages.research.outputs.findings) > 2`, Routes: map[string]string{"true": "approval", "false": "report"}, Position: WorkflowGraphPosition{X: 910, Y: 200}},
				{Name: "approval", NodeType: "checkpoint", Params: map[string]string{"prompt": "Findings look substantial. Approve deeper verification?"}, Next: []string{"verify"}, Position: WorkflowGraphPosition{X: 1190, Y: 120}},
				{Name: "verify", NodeType: "skill", Agent: "auditor", Skill: "code-audit", Input: map[string]string{"evidence": "stages.research.outputs.evidence"}, Outputs: map[string]string{"verification": "result.output"}, Next: []string{"report"}, Position: WorkflowGraphPosition{X: 1460, Y: 120}},
				{Name: "report", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{"evidence": "stages.research.outputs.evidence"}, Outputs: map[string]string{"report": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 1460, Y: 300}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1720, Y: 220}},
			},
		},
	}
}

func softwareTeamReviewGateWorkflowTemplate() WorkflowTemplate {
	return WorkflowTemplate{
		WorkflowTemplateSummary: WorkflowTemplateSummary{
			Name:        "software-team-review-gate",
			Title:       "Software Team Review Gate",
			Category:    "software",
			Description: "Run the reusable software task team, evaluate its approval quorum, and branch to handoff or revision.",
			Tags:        []string{"team", "approval", "policy_guard", "review"},
		},
		Graph: WorkflowGraphDocument{
			Description: "Executable team workflow with an observable and routable review quorum.",
			Stages: []WorkflowGraphStageDocument{
				{Name: "start", NodeType: "start", Next: []string{"team"}, Position: WorkflowGraphPosition{X: 60, Y: 220}},
				{Name: "team", NodeType: "team", Agent: "planner", Params: map[string]string{
					"team":            "software-task-team",
					"execute":         "true",
					"approval_preset": "software-review",
				}, Next: []string{"review-gate"}, Position: WorkflowGraphPosition{X: 320, Y: 200}},
				{Name: "review-gate", NodeType: "policy_guard", Params: map[string]string{
					"rule":   "team_approval_gate",
					"team":   "software-task-team",
					"status": "passed",
					"reason": "team review quorum did not pass",
				}, Routes: map[string]string{"allow": "handoff", "deny": "revise"}, Position: WorkflowGraphPosition{X: 640, Y: 220}},
				{Name: "handoff", NodeType: "skill", Agent: "chat", Skill: "execution-plan", Input: map[string]string{
					"final_handoff": "stages.team__reporter.outputs.summary",
					"gate_status":   "stages.review-gate.outputs.value",
					"approvers":     "stages.review-gate.outputs.approvers",
				}, Outputs: map[string]string{"handoff": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 980, Y: 140}},
				{Name: "revise", NodeType: "skill", Agent: "planner", Skill: "execution-plan", Input: map[string]string{
					"team_summary":   "stages.team__reporter.outputs.summary",
					"gate_status":    "stages.review-gate.outputs.value",
					"gate_rejectors": "stages.review-gate.outputs.rejectors",
				}, Outputs: map[string]string{"revision_plan": "result.output"}, Next: []string{"end"}, Position: WorkflowGraphPosition{X: 980, Y: 320}},
				{Name: "end", NodeType: "end", Position: WorkflowGraphPosition{X: 1280, Y: 220}},
			},
		},
	}
}

// RenderWorkflowTemplateYAML renders a built-in workflow template with a new workflow name.
func RenderWorkflowTemplateYAML(name, templateName string) (string, error) {
	template, ok := (&WorkflowRunner{}).WorkflowTemplate(templateName)
	if !ok {
		return "", fmt.Errorf("unknown workflow template: %s", templateName)
	}
	doc := template.Graph
	doc.Name = name
	data, err := workflowGraphDocumentYAML(doc)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func workflowGraphDocumentYAML(doc WorkflowGraphDocument) ([]byte, error) {
	if strings.TrimSpace(doc.Name) == "" {
		return nil, fmt.Errorf("workflow document missing name")
	}
	return yaml.Marshal(doc)
}

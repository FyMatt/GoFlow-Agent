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
	NodeTypes           []string `json:"node_types,omitempty" yaml:"node_types,omitempty"`
	Agents              []string `json:"agents,omitempty" yaml:"agents,omitempty"`
	Skills              []string `json:"skills,omitempty" yaml:"skills,omitempty"`
	Tools               []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	TeamTemplates       []string `json:"team_templates,omitempty" yaml:"team_templates,omitempty"`
	PolicyRules         []string `json:"policy_rules,omitempty" yaml:"policy_rules,omitempty"`
	HasControlFlow      bool     `json:"has_control_flow,omitempty" yaml:"has_control_flow,omitempty"`
	HasDataFlow         bool     `json:"has_data_flow,omitempty" yaml:"has_data_flow,omitempty"`
	HasApproval         bool     `json:"has_approval,omitempty" yaml:"has_approval,omitempty"`
	HasQualityGate      bool     `json:"has_quality_gate,omitempty" yaml:"has_quality_gate,omitempty"`
}

// WorkflowTemplate is a reusable graph blueprint for Studio and scaffolds.
type WorkflowTemplate struct {
	WorkflowTemplateSummary `yaml:",inline"`
	Graph                   WorkflowGraphDocument `json:"graph" yaml:"graph"`
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
	Placeholder string   `json:"placeholder,omitempty" yaml:"placeholder,omitempty"`
	Default     string   `json:"default,omitempty" yaml:"default,omitempty"`
	Required    bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Options     []string `json:"options,omitempty" yaml:"options,omitempty"`
	Examples    []string `json:"examples,omitempty" yaml:"examples,omitempty"`
	Hints       []string `json:"hints,omitempty" yaml:"hints,omitempty"`
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
		summary := workflowTemplateSummaryWithGraph(template)
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
		return workflowTemplateWithGraphSummary(template), true
	}
	templates := builtInWorkflowTemplates()
	template, ok := templates[normalizePersistedWorkflowName(name)]
	if !ok {
		return WorkflowTemplate{}, false
	}
	if template.WorkflowTemplateSummary.Source == "" {
		template.WorkflowTemplateSummary.Source = "built_in"
	}
	return workflowTemplateWithGraphSummary(template), true
}

func workflowTemplateWithGraphSummary(template WorkflowTemplate) WorkflowTemplate {
	template.WorkflowTemplateSummary = workflowTemplateSummaryWithGraph(template)
	return template
}

func workflowTemplateSummaryWithGraph(template WorkflowTemplate) WorkflowTemplateSummary {
	summary := template.WorkflowTemplateSummary
	summary.Stages = len(template.Graph.Stages)
	metadata := summarizeWorkflowTemplateGraph(template.Graph)
	summary.NodeTypes = metadata.NodeTypes
	summary.Agents = metadata.Agents
	summary.Skills = metadata.Skills
	summary.Tools = metadata.Tools
	summary.TeamTemplates = metadata.TeamTemplates
	summary.PolicyRules = metadata.PolicyRules
	summary.HasControlFlow = metadata.HasControlFlow
	summary.HasDataFlow = metadata.HasDataFlow
	summary.HasApproval = metadata.HasApproval
	summary.HasQualityGate = metadata.HasQualityGate
	return summary
}

type workflowTemplateGraphSummary struct {
	NodeTypes      []string
	Agents         []string
	Skills         []string
	Tools          []string
	TeamTemplates  []string
	PolicyRules    []string
	HasControlFlow bool
	HasDataFlow    bool
	HasApproval    bool
	HasQualityGate bool
}

func summarizeWorkflowTemplateGraph(graph WorkflowGraphDocument) workflowTemplateGraphSummary {
	metadata := workflowTemplateGraphSummary{}
	for _, stage := range graph.Stages {
		nodeType := normalizeWorkflowSkillName(stage.NodeType)
		if nodeType == "" {
			switch {
			case strings.TrimSpace(stage.Tool) != "":
				nodeType = "tool"
			case strings.TrimSpace(stage.Skill) != "":
				nodeType = "skill"
			case strings.TrimSpace(stage.Agent) != "":
				nodeType = "agent"
			}
		}
		metadata.NodeTypes = append(metadata.NodeTypes, nodeType)
		metadata.Agents = append(metadata.Agents, strings.TrimSpace(stage.Agent))
		metadata.Skills = append(metadata.Skills, strings.TrimSpace(stage.Skill))
		metadata.Tools = append(metadata.Tools, strings.TrimSpace(stage.Tool))
		if team := workflowTemplateStageTeamTemplate(stage); team != "" {
			metadata.TeamTemplates = append(metadata.TeamTemplates, team)
		}
		if rule := workflowTemplateStagePolicyRule(stage); rule != "" {
			metadata.PolicyRules = append(metadata.PolicyRules, rule)
		}
		if workflowTemplateStageHasControlFlow(stage) {
			metadata.HasControlFlow = true
		}
		if workflowTemplateStageHasDataFlow(stage) {
			metadata.HasDataFlow = true
		}
		if stage.Approval || nodeType == "checkpoint" || nodeType == "manual_approval" || nodeType == "approval_gate" {
			metadata.HasApproval = true
		}
		if workflowTemplateStageHasQualityGate(stage) {
			metadata.HasQualityGate = true
		}
	}
	metadata.NodeTypes = workflowGraphUniqueStrings(metadata.NodeTypes)
	metadata.Agents = workflowGraphUniqueStrings(metadata.Agents)
	metadata.Skills = workflowGraphUniqueStrings(metadata.Skills)
	metadata.Tools = workflowGraphUniqueStrings(metadata.Tools)
	metadata.TeamTemplates = workflowGraphUniqueStrings(metadata.TeamTemplates)
	metadata.PolicyRules = workflowGraphUniqueStrings(metadata.PolicyRules)
	return metadata
}

func workflowTemplateStageTeamTemplate(stage WorkflowGraphStageDocument) string {
	nodeType := normalizeWorkflowSkillName(stage.NodeType)
	if nodeType != "team" && nodeType != "agent_team" && nodeType != "team_template" {
		return ""
	}
	for _, key := range []string{"team", "team_template", "template"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	return ""
}

func workflowTemplateStagePolicyRule(stage WorkflowGraphStageDocument) string {
	nodeType := normalizeWorkflowSkillName(stage.NodeType)
	if nodeType != "policy_guard" && nodeType != "guard" && nodeType != "quality_gate" && nodeType != "quality_guard" && nodeType != "quality-guard" {
		return ""
	}
	for _, key := range []string{"rule", "policy_rule", "preset"} {
		if value := strings.TrimSpace(stage.Params[key]); value != "" {
			return value
		}
	}
	return ""
}

func workflowTemplateStageHasControlFlow(stage WorkflowGraphStageDocument) bool {
	internalStage := workflowGraphStage{NodeType: stage.NodeType}
	if isControlWorkflowNode(internalStage) {
		return true
	}
	return strings.TrimSpace(stage.Condition) != "" ||
		strings.TrimSpace(stage.Policy) != "" ||
		strings.TrimSpace(stage.SwitchOn) != "" ||
		strings.TrimSpace(stage.NextStrategy) != "" ||
		len(stage.Routes) > 0 ||
		len(stage.Cases) > 0 ||
		len(stage.Next) > 1 ||
		len(stage.OnError) > 0
}

func workflowTemplateStageHasDataFlow(stage WorkflowGraphStageDocument) bool {
	return len(stage.Input) > 0 ||
		len(stage.Outputs) > 0 ||
		len(stage.Artifacts) > 0 ||
		len(stage.AcceptanceCriteria) > 0
}

func workflowTemplateStageHasQualityGate(stage WorkflowGraphStageDocument) bool {
	nodeType := normalizeWorkflowSkillName(stage.NodeType)
	return nodeType == "quality_gate" ||
		nodeType == "quality-guard" ||
		nodeType == "quality_guard" ||
		len(stage.AcceptanceCriteria) > 0 ||
		len(stage.Artifacts) > 0
}

// WorkflowNodeTypes returns supported visual/executable/control nodes.
func (w *WorkflowRunner) WorkflowNodeTypes() []WorkflowNodeTypeOption {
	options := builtInWorkflowNodeTypes()
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
	if len(override.Hints) > 0 {
		base.Hints = workflowGraphUniqueStrings(override.Hints)
	}
	if len(override.Warnings) > 0 {
		base.Warnings = workflowGraphUniqueStrings(override.Warnings)
	}
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
	if strings.TrimSpace(override.Placeholder) != "" {
		base.Placeholder = override.Placeholder
	}
	if strings.TrimSpace(override.Default) != "" {
		base.Default = override.Default
	}
	if override.Required {
		base.Required = true
	}
	if len(override.Options) > 0 {
		base.Options = append([]string(nil), override.Options...)
	}
	if len(override.Examples) > 0 {
		base.Examples = append([]string(nil), override.Examples...)
	}
	base.Hints = workflowGraphUniqueStrings(append(base.Hints, override.Hints...))
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
	rules := builtInWorkflowPolicyRules()
	return append(rules, w.CustomWorkflowPolicyRules()...)
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

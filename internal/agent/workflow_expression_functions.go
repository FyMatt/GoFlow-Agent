package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowExpressionFunctionOption describes one expression helper available to workflow editors.
type WorkflowExpressionFunctionOption struct {
	Kind        string                               `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version     int                                  `json:"version,omitempty" yaml:"version,omitempty"`
	MinVersion  int                                  `json:"min_supported_version,omitempty" yaml:"min_supported_version,omitempty"`
	Name        string                               `json:"name" yaml:"name"`
	Label       string                               `json:"label,omitempty" yaml:"label,omitempty"`
	Category    string                               `json:"category,omitempty" yaml:"category,omitempty"`
	Description string                               `json:"description,omitempty" yaml:"description,omitempty"`
	Source      string                               `json:"source,omitempty" yaml:"source,omitempty"`
	Path        string                               `json:"path,omitempty" yaml:"path,omitempty"`
	Custom      bool                                 `json:"custom,omitempty" yaml:"custom,omitempty"`
	Signature   string                               `json:"signature,omitempty" yaml:"signature,omitempty"`
	InsertText  string                               `json:"insert_text,omitempty" yaml:"insert_text,omitempty"`
	ReturnType  string                               `json:"return_type,omitempty" yaml:"return_type,omitempty"`
	MinArgs     int                                  `json:"min_args" yaml:"min_args"`
	MaxArgs     int                                  `json:"max_args" yaml:"max_args"`
	Modes       []string                             `json:"modes,omitempty" yaml:"modes,omitempty"`
	NodeTypes   []string                             `json:"node_types,omitempty" yaml:"node_types,omitempty"`
	Args        []WorkflowExpressionFunctionArgument `json:"args,omitempty" yaml:"args,omitempty"`
	Examples    []string                             `json:"examples,omitempty" yaml:"examples,omitempty"`
	Hints       []string                             `json:"hints,omitempty" yaml:"hints,omitempty"`
	Warnings    []string                             `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

// WorkflowExpressionFunctionArgument describes one function argument for Studio forms/completion.
type WorkflowExpressionFunctionArgument struct {
	Name        string   `json:"name" yaml:"name"`
	Label       string   `json:"label,omitempty" yaml:"label,omitempty"`
	Type        string   `json:"type,omitempty" yaml:"type,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Accepts     []string `json:"accepts,omitempty" yaml:"accepts,omitempty"`
}

// WorkflowExpressionFunctions returns supported expression helpers for visual editors.
func (w *WorkflowRunner) WorkflowExpressionFunctions() []WorkflowExpressionFunctionOption {
	return w.mergeWorkflowExpressionFunctionMetadata(workflowExpressionFunctionOptions())
}

const (
	workflowExpressionMetadataResourceKind                = "goflow.workflow_expression_function"
	workflowExpressionMetadataResourceVersion             = 1
	workflowExpressionMetadataResourceMinSupportedVersion = 1
)

func (w *WorkflowRunner) mergeWorkflowExpressionFunctionMetadata(options []WorkflowExpressionFunctionOption) []WorkflowExpressionFunctionOption {
	if len(options) == 0 {
		return options
	}
	for i := range options {
		if options[i].Source == "" {
			options[i].Source = "built_in"
		}
	}
	metadata := w.customWorkflowExpressionFunctionMetadata()
	if len(metadata) == 0 {
		return options
	}
	for i := range options {
		name := normalizeWorkflowSkillName(options[i].Name)
		override, ok := metadata[name]
		if !ok {
			continue
		}
		options[i] = mergeWorkflowExpressionFunctionOption(options[i], override)
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Category != options[j].Category {
			return options[i].Category < options[j].Category
		}
		return options[i].Name < options[j].Name
	})
	return options
}

func (w *WorkflowRunner) customWorkflowExpressionFunctionMetadata() map[string]WorkflowExpressionFunctionOption {
	roots := w.workflowExpressionFunctionMetadataRoots()
	if len(roots) == 0 {
		return nil
	}
	out := make(map[string]WorkflowExpressionFunctionOption)
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
			option, err := loadWorkflowExpressionFunctionMetadata(path)
			if err != nil {
				continue
			}
			key := normalizeWorkflowSkillName(option.Name)
			if key == "" {
				continue
			}
			option.Name = key
			option.Source = "custom_metadata"
			option.Path = path
			option.Custom = true
			out[key] = option
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (w *WorkflowRunner) workflowExpressionFunctionMetadataRoots() []string {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return nil
	}
	home := w.runtime.RuntimeHome()
	return []string{
		filepath.Join(home, "templates", "expression_helpers"),
		filepath.Join(home, "templates", "workflow_expressions"),
		filepath.Join(home, "metadata", "expression_helpers"),
		filepath.Join(home, "metadata", "workflow_expressions"),
	}
}

func loadWorkflowExpressionFunctionMetadata(path string) (WorkflowExpressionFunctionOption, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowExpressionFunctionOption{}, err
	}
	var option WorkflowExpressionFunctionOption
	if err := yaml.Unmarshal(data, &option); err != nil {
		return WorkflowExpressionFunctionOption{}, err
	}
	if err := validateWorkflowExpressionFunctionMetadata(option); err != nil {
		return WorkflowExpressionFunctionOption{}, err
	}
	return option, nil
}

func validateWorkflowExpressionFunctionMetadata(option WorkflowExpressionFunctionOption) error {
	if strings.TrimSpace(option.Kind) != "" && strings.TrimSpace(option.Kind) != workflowExpressionMetadataResourceKind {
		return fmt.Errorf("unsupported workflow expression function metadata kind %q", option.Kind)
	}
	version := option.Version
	if version == 0 {
		version = workflowExpressionMetadataResourceMinSupportedVersion
	}
	if version < workflowExpressionMetadataResourceMinSupportedVersion {
		return fmt.Errorf("workflow expression function metadata version %d is no longer supported", version)
	}
	if version > workflowExpressionMetadataResourceVersion {
		return fmt.Errorf("workflow expression function metadata version %d is newer than supported version %d", version, workflowExpressionMetadataResourceVersion)
	}
	if option.MinVersion > workflowExpressionMetadataResourceVersion {
		return fmt.Errorf("workflow expression function metadata requires reader version %d, supported version is %d", option.MinVersion, workflowExpressionMetadataResourceVersion)
	}
	if strings.TrimSpace(option.Name) == "" {
		return fmt.Errorf("workflow expression function metadata name is required")
	}
	for i, arg := range option.Args {
		if strings.TrimSpace(arg.Name) == "" {
			return fmt.Errorf("workflow expression function metadata %s arg %d missing name", option.Name, i)
		}
	}
	return nil
}

func mergeWorkflowExpressionFunctionOption(base, override WorkflowExpressionFunctionOption) WorkflowExpressionFunctionOption {
	if strings.TrimSpace(override.Label) != "" {
		base.Label = override.Label
	}
	if strings.TrimSpace(override.Category) != "" {
		base.Category = override.Category
	}
	if strings.TrimSpace(override.Description) != "" {
		base.Description = override.Description
	}
	if strings.TrimSpace(override.Signature) != "" {
		base.Signature = override.Signature
	}
	if strings.TrimSpace(override.InsertText) != "" {
		base.InsertText = override.InsertText
	}
	if strings.TrimSpace(override.ReturnType) != "" {
		base.ReturnType = override.ReturnType
	}
	if override.MinArgs > 0 {
		base.MinArgs = override.MinArgs
	}
	if override.MaxArgs > 0 {
		base.MaxArgs = override.MaxArgs
	}
	if len(override.Modes) > 0 {
		base.Modes = workflowGraphUniqueStrings(override.Modes)
	}
	if len(override.NodeTypes) > 0 {
		base.NodeTypes = workflowGraphUniqueStrings(override.NodeTypes)
	}
	base.Args = mergeWorkflowExpressionFunctionArgs(base.Args, override.Args)
	if len(override.Examples) > 0 {
		base.Examples = append([]string(nil), override.Examples...)
	}
	base.Hints = workflowGraphUniqueStrings(append(base.Hints, override.Hints...))
	base.Warnings = workflowGraphUniqueStrings(append(base.Warnings, override.Warnings...))
	base.Source = "built_in+custom_metadata"
	base.Path = override.Path
	base.Custom = true
	return base
}

func mergeWorkflowExpressionFunctionArgs(base, overrides []WorkflowExpressionFunctionArgument) []WorkflowExpressionFunctionArgument {
	if len(overrides) == 0 {
		return base
	}
	out := append([]WorkflowExpressionFunctionArgument(nil), base...)
	positions := make(map[string]int, len(out))
	for i, arg := range out {
		positions[normalizeWorkflowSkillName(arg.Name)] = i
	}
	for _, override := range overrides {
		key := normalizeWorkflowSkillName(override.Name)
		if key == "" {
			continue
		}
		if index, ok := positions[key]; ok {
			out[index] = mergeWorkflowExpressionFunctionArg(out[index], override)
			continue
		}
		positions[key] = len(out)
		out = append(out, override)
	}
	return out
}

func mergeWorkflowExpressionFunctionArg(base, override WorkflowExpressionFunctionArgument) WorkflowExpressionFunctionArgument {
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
	if len(override.Accepts) > 0 {
		base.Accepts = append([]string(nil), override.Accepts...)
	}
	return base
}

func workflowExpressionFunctionOptions() []WorkflowExpressionFunctionOption {
	options := []WorkflowExpressionFunctionOption{
		{
			Name:        "contains",
			Label:       "Contains",
			Category:    "collection",
			Description: "Pass when a string, array, or object contains the provided text or value.",
			Signature:   `contains(collection, needle)`,
			InsertText:  `contains(${reference}, "${needle}")`,
			ReturnType:  "boolean",
			MinArgs:     2,
			MaxArgs:     2,
			Modes:       workflowExpressionFunctionModes(),
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args: []WorkflowExpressionFunctionArgument{
				{Name: "collection", Label: "Collection", Type: "reference", Required: true, Accepts: []string{"string", "array", "object"}},
				{Name: "needle", Label: "Needle", Type: "value", Required: true, Accepts: []string{"string", "number", "boolean"}},
			},
			Examples: []string{
				`contains(stages.collect.outputs.tags, "api")`,
				`contains(previous.raw_output, "vulnerability")`,
			},
		},
		{
			Name:        "in",
			Label:       "In Collection",
			Category:    "collection",
			Description: "Pass when a value exactly matches an item in an array, object values, or object keys.",
			Signature:   `in(value, collection)`,
			InsertText:  `in("${value}", ${reference})`,
			ReturnType:  "boolean",
			MinArgs:     2,
			MaxArgs:     2,
			Modes:       workflowExpressionFunctionModes(),
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args: []WorkflowExpressionFunctionArgument{
				{Name: "value", Label: "Value", Type: "value", Required: true, Accepts: []string{"string", "number", "boolean"}},
				{Name: "collection", Label: "Collection", Type: "reference", Required: true, Accepts: []string{"array", "object", "string"}},
			},
			Examples: []string{
				`in("api", stages.collect.outputs.tags)`,
				`in("risk", stages.audit.outputs.finding)`,
			},
		},
		{
			Name:        "matches",
			Label:       "Regex Match",
			Category:    "string",
			Description: "Pass when the value matches a Go regular expression.",
			Signature:   `matches(value, pattern)`,
			InsertText:  `matches(${reference}, "${pattern}")`,
			ReturnType:  "boolean",
			MinArgs:     2,
			MaxArgs:     2,
			Modes:       workflowExpressionFunctionModes(),
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args: []WorkflowExpressionFunctionArgument{
				{Name: "value", Label: "Value", Type: "reference", Required: true, Accepts: []string{"string", "number", "boolean"}},
				{Name: "pattern", Label: "Pattern", Type: "regex", Required: true, Accepts: []string{"string"}},
			},
			Examples: []string{
				`matches(stages.collect.outputs.payload.risk, "high|critical")`,
				`matches(previous.summary, "(?i)done")`,
			},
		},
		{
			Name:        "any",
			Label:       "Any",
			Category:    "collection",
			Description: "Pass when any collection item is truthy, or any item equals/contains the optional needle.",
			Signature:   `any(collection[, needle])`,
			InsertText:  `any(${reference}, "${needle}")`,
			ReturnType:  "boolean",
			MinArgs:     1,
			MaxArgs:     2,
			Modes:       workflowExpressionFunctionModes(),
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args: []WorkflowExpressionFunctionArgument{
				{Name: "collection", Label: "Collection", Type: "reference", Required: true, Accepts: []string{"array", "object", "string"}},
				{Name: "needle", Label: "Needle", Type: "value", Accepts: []string{"string", "number", "boolean"}},
			},
			Examples: []string{
				`any(stages.collect.outputs.tags, "auth")`,
				`any(stages.audit.outputs.findings)`,
			},
		},
		{
			Name:        "all",
			Label:       "All",
			Category:    "collection",
			Description: "Pass when all collection items are truthy, or all items equal/contain the optional needle.",
			Signature:   `all(collection[, needle])`,
			InsertText:  `all(${reference})`,
			ReturnType:  "boolean",
			MinArgs:     1,
			MaxArgs:     2,
			Modes:       workflowExpressionFunctionModes(),
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args: []WorkflowExpressionFunctionArgument{
				{Name: "collection", Label: "Collection", Type: "reference", Required: true, Accepts: []string{"array", "object", "string"}},
				{Name: "needle", Label: "Needle", Type: "value", Accepts: []string{"string", "number", "boolean"}},
			},
			Examples: []string{
				`all(stages.collect.outputs.tags)`,
				`all(stages.review.outputs.approvals, "approved")`,
			},
		},
		{
			Name:        "exists",
			Label:       "Exists",
			Category:    "reference",
			Description: "Pass when a reference resolves to an existing non-null value.",
			Signature:   `exists(reference)`,
			InsertText:  `exists(${reference})`,
			ReturnType:  "boolean",
			MinArgs:     1,
			MaxArgs:     1,
			Modes:       workflowExpressionFunctionModes(),
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args:        []WorkflowExpressionFunctionArgument{{Name: "reference", Label: "Reference", Type: "reference", Required: true}},
			Examples:    []string{`exists(stages.audit.outputs.findings)`},
		},
		{
			Name:        "has",
			Label:       "Has Truthy Value",
			Category:    "reference",
			Description: "Pass when a value is truthy after resolving references and literals.",
			Signature:   `has(value)`,
			InsertText:  `has(${reference})`,
			ReturnType:  "boolean",
			MinArgs:     1,
			MaxArgs:     1,
			Modes:       workflowExpressionFunctionModes(),
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args:        []WorkflowExpressionFunctionArgument{{Name: "value", Label: "Value", Type: "reference", Required: true}},
			Examples:    []string{`has(stages.audit.outputs.summary)`},
		},
		{
			Name:        "len",
			Label:       "Length",
			Category:    "collection",
			Description: "Return the length of a string, array, object, or JSON-like value.",
			Signature:   `len(value)`,
			InsertText:  `len(${reference})`,
			ReturnType:  "number",
			MinArgs:     1,
			MaxArgs:     1,
			Modes:       []string{"condition", "policy", "switch", "expression"},
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args:        []WorkflowExpressionFunctionArgument{{Name: "value", Label: "Value", Type: "reference", Required: true, Accepts: []string{"string", "array", "object"}}},
			Examples:    []string{`len(stages.collect.outputs.tags) >= 2`},
		},
		{
			Name:        "risk_rank",
			Label:       "Risk Rank",
			Category:    "risk",
			Description: "Return a numeric severity rank: info=1, low=2, medium=3, high=4, critical=5.",
			Signature:   `risk_rank(value)`,
			InsertText:  `risk_rank(${reference})`,
			ReturnType:  "number",
			MinArgs:     1,
			MaxArgs:     1,
			Modes:       []string{"condition", "policy", "switch", "expression"},
			NodeTypes:   workflowExpressionFunctionNodeTypes(),
			Args:        []WorkflowExpressionFunctionArgument{{Name: "value", Label: "Value", Type: "reference", Required: true, Accepts: []string{"string", "number"}}},
			Examples: []string{
				`risk_rank(stages.audit.outputs.risk) >= 4`,
				`risk_rank(stages.audit.outputs.payload.severity) >= risk_rank("high")`,
			},
		},
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Category != options[j].Category {
			return options[i].Category < options[j].Category
		}
		return options[i].Name < options[j].Name
	})
	return options
}

func workflowExpressionFunctionDefinition(name string) (WorkflowExpressionFunctionOption, bool) {
	for _, option := range workflowExpressionFunctionOptions() {
		if option.Name == name {
			return option, true
		}
	}
	return WorkflowExpressionFunctionOption{}, false
}

func workflowExpressionFunctionModes() []string {
	return []string{"condition", "policy", "expression"}
}

func workflowExpressionFunctionNodeTypes() []string {
	return []string{"condition", "policy_guard", "quality_gate", "switch", "router"}
}

func workflowExpressionFunctionExpectedArgs(option WorkflowExpressionFunctionOption) string {
	if option.MinArgs == option.MaxArgs {
		switch option.MinArgs {
		case 1:
			return "one argument"
		case 2:
			return "two arguments"
		default:
			return "exactly " + workflowExpressionFunctionArgCount(option.MinArgs) + " arguments"
		}
	}
	if option.MinArgs == 1 && option.MaxArgs == 2 {
		return "one or two arguments"
	}
	return workflowExpressionFunctionArgCount(option.MinArgs) + " to " + workflowExpressionFunctionArgCount(option.MaxArgs) + " arguments"
}

func workflowExpressionFunctionArgCount(count int) string {
	switch count {
	case 0:
		return "zero"
	case 1:
		return "one"
	case 2:
		return "two"
	case 3:
		return "three"
	default:
		return fmt.Sprintf("%d", count)
	}
}

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	workflowPolicyRuleResourceKind                = "goflow.workflow_policy_rule"
	workflowPolicyRuleResourceVersion             = 2
	workflowPolicyRuleResourceMinSupportedVersion = 1
)

// WorkflowPolicyRuleDefinition is a persisted, declarative policy_guard rule.
type WorkflowPolicyRuleDefinition struct {
	Kind                string                    `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version             int                       `json:"version,omitempty" yaml:"version,omitempty"`
	MinVersion          int                       `json:"min_supported_version,omitempty" yaml:"min_supported_version,omitempty"`
	MigratedFromVersion int                       `json:"migrated_from_version,omitempty" yaml:"migrated_from_version,omitempty"`
	Name                string                    `json:"name" yaml:"name"`
	Label               string                    `json:"label,omitempty" yaml:"label,omitempty"`
	Description         string                    `json:"description,omitempty" yaml:"description,omitempty"`
	Operator            string                    `json:"operator,omitempty" yaml:"operator,omitempty"`
	Expression          string                    `json:"expression,omitempty" yaml:"expression,omitempty"`
	Reason              string                    `json:"reason,omitempty" yaml:"reason,omitempty"`
	Defaults            map[string]string         `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Params              []WorkflowNodeFieldOption `json:"params,omitempty" yaml:"params,omitempty"`
}

// NormalizeWorkflowPolicyRuleDefinition normalizes persisted rule fields before validation or saving.
func NormalizeWorkflowPolicyRuleDefinition(definition WorkflowPolicyRuleDefinition) WorkflowPolicyRuleDefinition {
	definition.Name = normalizePersistedWorkflowName(definition.Name)
	definition.Operator = normalizeWorkflowPolicyOperator(definition.Operator)
	if definition.Operator == "" {
		definition.Operator = "expression"
	}
	return definition
}

// ValidateWorkflowPolicyRuleDefinition validates a declarative policy_guard rule.
func ValidateWorkflowPolicyRuleDefinition(definition WorkflowPolicyRuleDefinition) error {
	return validateWorkflowPolicyRuleDefinition(NormalizeWorkflowPolicyRuleDefinition(definition))
}

// MigrateWorkflowPolicyRuleDefinition returns the current persisted policy rule resource envelope.
func MigrateWorkflowPolicyRuleDefinition(definition WorkflowPolicyRuleDefinition) WorkflowPolicyRuleDefinition {
	migrated := definition.Version == 0
	definition = NormalizeWorkflowPolicyRuleDefinition(definition)
	return normalizeWorkflowPolicyRuleResourceVersion(definition, migrated)
}

func (w *WorkflowRunner) CustomWorkflowPolicyRules() []WorkflowPolicyRuleOption {
	definitions := w.customWorkflowPolicyRuleDefinitions()
	out := make([]WorkflowPolicyRuleOption, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, WorkflowPolicyRuleOption{
			Name:        definition.Name,
			Label:       fallbackWorkflowGraphValue(definition.Label, workflowGraphHumanLabel(definition.Name)),
			Description: definition.Description,
			Source:      "custom",
			Operator:    normalizeWorkflowPolicyOperator(definition.Operator),
			Custom:      true,
			Params:      append([]WorkflowNodeFieldOption(nil), definition.Params...),
		})
	}
	return out
}

func (w *WorkflowRunner) customWorkflowPolicyRuleDefinitions() []WorkflowPolicyRuleDefinition {
	root, err := w.workflowPolicyRuleRoot()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	definitions := make([]WorkflowPolicyRuleDefinition, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".yaml") && !strings.HasSuffix(strings.ToLower(name), ".yml") {
			continue
		}
		path := filepath.Join(root, name)
		definition, err := loadWorkflowPolicyRuleDefinition(path)
		if err != nil || validateWorkflowPolicyRuleDefinition(definition) != nil {
			continue
		}
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool {
		return normalizePersistedWorkflowName(definitions[i].Name) < normalizePersistedWorkflowName(definitions[j].Name)
	})
	return definitions
}

func (w *WorkflowRunner) loadWorkflowPolicyRule(name string) (WorkflowPolicyRuleDefinition, bool) {
	target := normalizeWorkflowSkillName(name)
	if target == "" {
		return WorkflowPolicyRuleDefinition{}, false
	}
	for _, definition := range w.customWorkflowPolicyRuleDefinitions() {
		if normalizeWorkflowSkillName(definition.Name) == target {
			return definition, true
		}
	}
	return WorkflowPolicyRuleDefinition{}, false
}

func (w *WorkflowRunner) workflowPolicyRuleRoot() (string, error) {
	if w == nil || w.runtime == nil || strings.TrimSpace(w.runtime.RuntimeHome()) == "" {
		return "", fmt.Errorf("workflow runtime not configured")
	}
	return filepath.Join(w.runtime.RuntimeHome(), "policies", "workflow_rules"), nil
}

func loadWorkflowPolicyRuleDefinition(path string) (WorkflowPolicyRuleDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowPolicyRuleDefinition{}, err
	}
	var definition WorkflowPolicyRuleDefinition
	if err := yaml.Unmarshal(data, &definition); err != nil {
		return WorkflowPolicyRuleDefinition{}, err
	}
	definition = NormalizeWorkflowPolicyRuleDefinition(definition)
	if err := validateWorkflowPolicyRuleDefinition(definition); err != nil {
		return WorkflowPolicyRuleDefinition{}, err
	}
	return MigrateWorkflowPolicyRuleDefinition(definition), nil
}

func validateWorkflowPolicyRuleDefinition(definition WorkflowPolicyRuleDefinition) error {
	if strings.TrimSpace(definition.Kind) != "" && strings.TrimSpace(definition.Kind) != workflowPolicyRuleResourceKind {
		return fmt.Errorf("unsupported workflow policy rule kind %q", definition.Kind)
	}
	version := definition.Version
	if version == 0 {
		version = workflowPolicyRuleResourceMinSupportedVersion
	}
	if version < workflowPolicyRuleResourceMinSupportedVersion {
		return fmt.Errorf("workflow policy rule version %d is no longer supported", version)
	}
	if version > workflowPolicyRuleResourceVersion {
		return fmt.Errorf("workflow policy rule version %d is newer than supported version %d", version, workflowPolicyRuleResourceVersion)
	}
	if definition.MinVersion > workflowPolicyRuleResourceVersion {
		return fmt.Errorf("workflow policy rule requires reader version %d, supported version is %d", definition.MinVersion, workflowPolicyRuleResourceVersion)
	}
	if err := validatePersistedWorkflowName(normalizePersistedWorkflowName(definition.Name)); err != nil {
		return err
	}
	operator := normalizeWorkflowPolicyOperator(definition.Operator)
	if operator == "" {
		operator = "expression"
	}
	if !isWorkflowPolicyBuiltinOperator(operator) {
		return fmt.Errorf("unsupported workflow policy rule operator: %s", definition.Operator)
	}
	if operator == "expression" && strings.TrimSpace(definition.Expression) == "" {
		return fmt.Errorf("workflow policy rule %s expression is required for expression operator", definition.Name)
	}
	return nil
}

func isWorkflowPolicyBuiltinOperator(operator string) bool {
	switch normalizeWorkflowPolicyOperator(operator) {
	case "expression", "ref_truthy", "contains", "min_count", "risk_at_least", "team_approval_gate":
		return true
	default:
		return false
	}
}

func normalizeWorkflowPolicyRuleResourceVersion(definition WorkflowPolicyRuleDefinition, migrated bool) WorkflowPolicyRuleDefinition {
	original := definition.Version
	if original == 0 {
		original = workflowPolicyRuleResourceMinSupportedVersion
	}
	definition.Kind = workflowPolicyRuleResourceKind
	definition.Version = workflowPolicyRuleResourceVersion
	definition.MinVersion = workflowPolicyRuleResourceMinSupportedVersion
	if (migrated || original != workflowPolicyRuleResourceVersion) && definition.MigratedFromVersion == 0 {
		definition.MigratedFromVersion = original
	}
	return definition
}

func normalizeWorkflowPolicyOperator(operator string) string {
	operator = normalizeWorkflowSkillName(operator)
	switch operator {
	case "truthy", "present", "exists":
		return "ref_truthy"
	case "minimum_count":
		return "min_count"
	case "risk", "severity_at_least":
		return "risk_at_least"
	case "approval_gate", "review_gate", "team_quorum", "quorum_gate":
		return "team_approval_gate"
	default:
		return operator
	}
}

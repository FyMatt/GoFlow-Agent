package agent

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed templates/policy_rules/*.yaml
var embeddedWorkflowPolicyRuleFS embed.FS

type workflowPolicyRuleFile struct {
	Rules []WorkflowPolicyRuleOption `yaml:"rules"`
}

var (
	builtInWorkflowPolicyRuleOnce  sync.Once
	builtInWorkflowPolicyRuleCache []WorkflowPolicyRuleOption
	builtInWorkflowPolicyRuleErr   error
)

func builtInWorkflowPolicyRules() []WorkflowPolicyRuleOption {
	rules, err := loadBuiltInWorkflowPolicyRules()
	if err != nil {
		panic(err)
	}
	return cloneWorkflowPolicyRuleOptions(rules)
}

func loadBuiltInWorkflowPolicyRules() ([]WorkflowPolicyRuleOption, error) {
	builtInWorkflowPolicyRuleOnce.Do(func() {
		entries, err := embeddedWorkflowPolicyRuleFS.ReadDir("templates/policy_rules")
		if err != nil {
			builtInWorkflowPolicyRuleErr = fmt.Errorf("read built-in workflow policy rules: %w", err)
			return
		}
		seen := make(map[string]struct{})
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
				continue
			}
			data, err := embeddedWorkflowPolicyRuleFS.ReadFile("templates/policy_rules/" + name)
			if err != nil {
				builtInWorkflowPolicyRuleErr = fmt.Errorf("read built-in workflow policy rule %s: %w", name, err)
				return
			}
			rules, err := parseWorkflowPolicyRuleOptionData(data)
			if err != nil {
				builtInWorkflowPolicyRuleErr = fmt.Errorf("parse built-in workflow policy rule %s: %w", name, err)
				return
			}
			for _, rule := range rules {
				rule.Name = normalizeWorkflowPolicyOperator(rule.Name)
				rule.Operator = normalizeWorkflowPolicyOperator(rule.Operator)
				if rule.Operator == "" {
					rule.Operator = rule.Name
				}
				if err := validateWorkflowPolicyRuleOption(rule); err != nil {
					builtInWorkflowPolicyRuleErr = fmt.Errorf("validate built-in workflow policy rule %s: %w", name, err)
					return
				}
				if _, exists := seen[rule.Name]; exists {
					builtInWorkflowPolicyRuleErr = fmt.Errorf("duplicate built-in workflow policy rule %q", rule.Name)
					return
				}
				seen[rule.Name] = struct{}{}
				builtInWorkflowPolicyRuleCache = append(builtInWorkflowPolicyRuleCache, rule)
			}
		}
		if len(builtInWorkflowPolicyRuleCache) == 0 {
			builtInWorkflowPolicyRuleErr = fmt.Errorf("built-in workflow policy rules are empty")
			return
		}
		sortWorkflowPolicyRuleOptions(builtInWorkflowPolicyRuleCache)
	})
	if builtInWorkflowPolicyRuleErr != nil {
		return nil, builtInWorkflowPolicyRuleErr
	}
	return builtInWorkflowPolicyRuleCache, nil
}

func parseWorkflowPolicyRuleOptionData(data []byte) ([]WorkflowPolicyRuleOption, error) {
	var file workflowPolicyRuleFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if len(file.Rules) > 0 {
		return file.Rules, nil
	}
	var rule WorkflowPolicyRuleOption
	if err := yaml.Unmarshal(data, &rule); err != nil {
		return nil, err
	}
	if strings.TrimSpace(rule.Name) == "" {
		return nil, fmt.Errorf("workflow policy rule file does not define name or rules")
	}
	return []WorkflowPolicyRuleOption{rule}, nil
}

func validateWorkflowPolicyRuleOption(rule WorkflowPolicyRuleOption) error {
	if strings.TrimSpace(rule.Name) == "" {
		return fmt.Errorf("workflow policy rule name is required")
	}
	if strings.TrimSpace(rule.Label) == "" {
		return fmt.Errorf("workflow policy rule %s label is required", rule.Name)
	}
	if !isWorkflowPolicyBuiltinOperator(rule.Operator) {
		return fmt.Errorf("unsupported workflow policy rule operator: %s", rule.Operator)
	}
	for i, field := range rule.Params {
		if strings.TrimSpace(field.Name) == "" {
			return fmt.Errorf("workflow policy rule %s param %d missing name", rule.Name, i)
		}
	}
	return nil
}

func sortWorkflowPolicyRuleOptions(rules []WorkflowPolicyRuleOption) {
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Operator != rules[j].Operator {
			return rules[i].Operator < rules[j].Operator
		}
		return rules[i].Name < rules[j].Name
	})
}

func cloneWorkflowPolicyRuleOptions(rules []WorkflowPolicyRuleOption) []WorkflowPolicyRuleOption {
	out := make([]WorkflowPolicyRuleOption, len(rules))
	for i, rule := range rules {
		out[i] = cloneWorkflowPolicyRuleOption(rule)
	}
	return out
}

func cloneWorkflowPolicyRuleOption(rule WorkflowPolicyRuleOption) WorkflowPolicyRuleOption {
	rule.Params = append([]WorkflowNodeFieldOption(nil), rule.Params...)
	for i := range rule.Params {
		rule.Params[i].Options = append([]string(nil), rule.Params[i].Options...)
		rule.Params[i].Examples = append([]string(nil), rule.Params[i].Examples...)
		rule.Params[i].Hints = append([]string(nil), rule.Params[i].Hints...)
	}
	return rule
}

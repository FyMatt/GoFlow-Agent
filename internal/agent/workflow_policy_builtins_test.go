package agent

import "testing"

func TestBuiltInWorkflowPolicyRulesLoadFromEmbeddedYAML(t *testing.T) {
	rules := builtInWorkflowPolicyRules()
	if len(rules) < 6 {
		t.Fatalf("expected built-in policy rule metadata, got %d", len(rules))
	}
	var risk *WorkflowPolicyRuleOption
	for i := range rules {
		if rules[i].Name == "risk_at_least" {
			risk = &rules[i]
			break
		}
	}
	if risk == nil {
		t.Fatal("expected risk_at_least policy rule metadata")
	}
	if risk.Operator != "risk_at_least" || risk.Label != "Risk At Least" || len(risk.Params) != 2 {
		t.Fatalf("unexpected risk_at_least metadata: %#v", risk)
	}
	if len(risk.Params[1].Options) != 5 {
		t.Fatalf("expected severity options, got %#v", risk.Params[1])
	}
}

func TestBuiltInWorkflowPolicyRulesAreCloned(t *testing.T) {
	first := builtInWorkflowPolicyRules()
	if len(first) == 0 {
		t.Fatal("expected policy rules")
	}
	first[0].Name = "mutated"
	first[0].Params[0].Name = "mutated"

	second := builtInWorkflowPolicyRules()
	if second[0].Name == "mutated" || second[0].Params[0].Name == "mutated" {
		t.Fatalf("expected cloned policy rules, got %#v", second[0])
	}
}

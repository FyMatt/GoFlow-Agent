package agent

import "testing"

func TestBuiltInWorkflowExpressionFunctionsLoadFromEmbeddedYAML(t *testing.T) {
	options := workflowExpressionFunctionOptions()
	if len(options) < 9 {
		t.Fatalf("expected built-in expression helpers, got %d", len(options))
	}
	risk, ok := workflowExpressionFunctionDefinition("risk_rank")
	if !ok {
		t.Fatal("expected risk_rank expression helper")
	}
	if risk.Source != "" {
		t.Fatalf("expected raw built-in helper without source before merge, got %#v", risk)
	}
	if risk.ReturnType != "number" || risk.MinArgs != 1 || risk.MaxArgs != 1 {
		t.Fatalf("unexpected risk_rank metadata: %#v", risk)
	}
	if len(risk.Examples) == 0 || len(risk.Args) != 1 || risk.Args[0].Name != "value" {
		t.Fatalf("expected examples and value arg, got %#v", risk)
	}
}

func TestBuiltInWorkflowExpressionFunctionsAreCloned(t *testing.T) {
	first := workflowExpressionFunctionOptions()
	if len(first) == 0 {
		t.Fatal("expected helpers")
	}
	first[0].Name = "mutated"
	first[0].Args[0].Name = "mutated"

	second := workflowExpressionFunctionOptions()
	if second[0].Name == "mutated" || second[0].Args[0].Name == "mutated" {
		t.Fatalf("expected cloned helpers, got %#v", second[0])
	}
}

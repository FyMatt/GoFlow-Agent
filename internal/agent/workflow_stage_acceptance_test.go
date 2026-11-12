package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func stageWithReportSections() workflowGraphStage {
	return workflowGraphStage{
		Name:     "recovery-verify",
		NodeType: "skill",
		Params:   map[string]string{"enforce_acceptance_sections": "true"},
		AcceptanceCriteria: []workflowGraphAcceptanceCriterion{
			{Name: "recovery-result", Ref: "result.output", Contains: "返修复测结论"},
			{Name: "blocker-result", Ref: "result.output", Contains: "阻断项验证"},
		},
	}
}

// A report stage that omits its declared sections must be retried in place,
// instead of passing and failing a later quality gate with no recovery left.
func TestStageCompletionRequiresDeclaredReportSections(t *testing.T) {
	stage := stageWithReportSections()
	result := schema.AgentResult{Output: "summary: 返修复测通过\nevidence:\n  - go test 通过"}
	err := (&WorkflowRunner{}).workflowGraphStageCompletionError(workflowGraph{Name: "security-audit-evidence-gate"}, stage, result)
	if err == nil || !strings.Contains(err.Error(), "did not satisfy acceptance criteria") {
		t.Fatalf("a report missing its declared sections must be rejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "返修复测结论") || !strings.Contains(err.Error(), "阻断项验证") {
		t.Fatalf("the error must name the missing sections, got %v", err)
	}
	if !workflowGraphStageErrorIsRetryable(context.Background(), err) {
		t.Fatal("a missing report section must be retryable in the same stage")
	}
}

func TestStageCompletionAcceptsDeclaredReportSections(t *testing.T) {
	stage := stageWithReportSections()
	result := schema.AgentResult{Output: "返修复测结论：通过\n\n阻断项验证：四个阻断项均已复测"}
	if err := (&WorkflowRunner{}).workflowGraphStageCompletionError(workflowGraph{Name: "security-audit-evidence-gate"}, stage, result); err != nil {
		t.Fatalf("a report with its declared sections must pass: %v", err)
	}
}

// Stages that do not opt in keep their previous behaviour: acceptance criteria
// stay routing signals rather than a hard formatting gate.
func TestStageCompletionIgnoresSectionsWithoutOptIn(t *testing.T) {
	stage := stageWithReportSections()
	delete(stage.Params, "enforce_acceptance_sections")
	result := schema.AgentResult{Output: "summary: 返修复测通过"}
	if err := (&WorkflowRunner{}).workflowGraphStageCompletionError(workflowGraph{Name: "security-audit-evidence-gate"}, stage, result); err != nil {
		t.Fatalf("un-opted stages must not enforce report sections: %v", err)
	}
}

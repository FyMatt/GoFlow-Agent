package agent

import (
	"strings"
	"testing"
)

func TestWorkflowMappedInputBudgetKeepsLaterVerificationEvidence(t *testing.T) {
	stage := workflowGraphStage{
		Name: "audit",
		Input: map[string]string{
			"plan":         "stages.plan.outputs.report",
			"verification": "stages.verify.outputs.report",
		},
		Context: workflowGraphStageContext{InputsMaxTokens: 120},
	}
	completed := []WorkflowStageResult{
		{Stage: "plan", Output: WorkflowStageOutput{Variables: map[string]string{"report": strings.Repeat("Detailed implementation plan. ", 500)}}},
		{Stage: "verify", Output: WorkflowStageOutput{Variables: map[string]string{"report": "ACTUAL_VERIFICATION: go test ./... exited 0; go vet ./... exited 0."}}},
	}
	prompt := buildWorkflowGraphStagePrompt(workflowGraph{Name: "delivery"}, stage, "Review the actual change and verification", completed)
	lines := strings.Split(strings.Split(prompt, "Mapped stage inputs:\n")[1], "\n")
	mapped := strings.Join(lines[:2], "\n")
	if !strings.Contains(mapped, "ACTUAL_VERIFICATION") {
		t.Fatal("long alphabetically earlier input displaced the verification evidence")
	}
	if len(strings.TrimSpace(mapped)) > workflowGraphTokenBudgetBytes(stage.Context.InputsMaxTokens) {
		t.Fatalf("mapped inputs exceeded their configured budget: %d bytes", len(mapped))
	}
}
